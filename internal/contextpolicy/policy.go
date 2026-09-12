package contextpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// TierPolicy holds the tier-extended enforcement fields for a namespace policy.
// Zero values mean "not enforced" for all fields except AllowedOps:
//   - empty AllowedOps = all ops permitted (backward compat)
//   - MaxBytesPerKey = 0 → unlimited
//   - MaxRevisions = 0 → unlimited
//   - Retention = "" → no expiry enforcement
type TierPolicy struct {
	Tier               string     `json:"tier,omitempty"`
	Retention          string     `json:"retention,omitempty"`
	MaxRevisions       int        `json:"max_revisions,omitempty"`
	MaxBytesPerKey     int        `json:"max_bytes_per_key,omitempty"`
	AllowedOps         []string   `json:"allowed_ops,omitempty"`
	RequiredSchemaKeys []string   `json:"required_schema_keys,omitempty"`
	Redaction          *Redaction `json:"redaction,omitempty"`
}

// Redaction controls tombstone behavior on delete.
type Redaction struct {
	Allowed           bool `json:"allowed"`
	TombstoneOnDelete bool `json:"tombstone_on_delete"`
}

// HasOp reports whether the policy permits op.
// An empty AllowedOps slice means all ops are permitted (backward compatibility).
func (p TierPolicy) HasOp(op string) bool {
	if len(p.AllowedOps) == 0 {
		return true
	}
	for _, s := range p.AllowedOps {
		if s == op {
			return true
		}
	}
	return false
}

// ParseTierPolicy extracts TierPolicy fields from a raw policy map.
func ParseTierPolicy(policy map[string]any) TierPolicy {
	if policy == nil {
		return TierPolicy{}
	}
	// Round-trip through JSON for reliable type conversion.
	b, err := json.Marshal(policy)
	if err != nil {
		return TierPolicy{}
	}
	var tp TierPolicy
	_ = json.Unmarshal(b, &tp)
	return tp
}

// NamespaceOwner stores ownership metadata.
type NamespaceOwner struct {
	OwnerType string // user|app
	OwnerID   string
	Policy    map[string]any
}

// Engine manages namespace ownership and write authorization rules.
type Engine struct {
	mu     sync.RWMutex
	owners map[string]NamespaceOwner
}

// New returns an empty policy engine.
func New() *Engine {
	return &Engine{owners: map[string]NamespaceOwner{}}
}

// RegisterNamespace upserts owner metadata.
//
// owner_type "system" is accepted for sentinel registrations of namespaces
// that don't fit the user|app tier shape (single-segment, missing owner_id,
// or non-tier prefixes). System-owned entries are not consulted for
// access enforcement — CanWrite / CanPromote treat them like unregistered
// namespaces and fall through to the prefix-based default rules.
func (e *Engine) RegisterNamespace(namespace, ownerType, ownerID string, policy map[string]any) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return errors.New("namespace required")
	}
	if ownerType != "user" && ownerType != "app" && ownerType != "system" {
		return errors.New("owner_type must be user|app|system")
	}
	if strings.TrimSpace(ownerID) == "" {
		return errors.New("owner_id required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.owners[ns] = NamespaceOwner{OwnerType: ownerType, OwnerID: ownerID, Policy: policy}
	return nil
}

// GetNamespace returns namespace metadata when registered.
func (e *Engine) GetNamespace(namespace string) (NamespaceOwner, bool) {
	ns := strings.TrimSpace(namespace)
	e.mu.RLock()
	defer e.mu.RUnlock()
	owner, ok := e.owners[ns]
	return owner, ok
}

// CanWrite enforces namespace write rules.
func (e *Engine) CanWrite(clientID, actor, namespace string) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return errors.New("namespace required")
	}
	if strings.TrimSpace(actor) == "" {
		return errors.New("actor required")
	}

	// The registry is the authority on ownership, and it is consulted FIRST.
	//
	// It used to be consulted last, behind two string-prefix rules that read an
	// owner out of the path: `user/` meant "protected, actor=user only", and
	// `app/{id}/` meant "writable only by app:{id}", with {id} taken from the
	// second segment. Under the scope-type-rooted grammar
	// (CW-20260912-0078) the first segment names a SCOPE TYPE, one of six, and
	// a scope type is not an owner — `project/tether` has an owner, and it is
	// not "tether". Deriving one from the path is the claim this grammar
	// removes.
	e.mu.RLock()
	owner, registered := e.owners[ns]
	e.mu.RUnlock()
	// owner_type "system" is a SENTINEL registration for namespaces that never
	// fit the user|app tier shape, and RegisterNamespace documents that it is
	// not consulted for enforcement. Treating it as registered here would let a
	// sentinel row silently disable the scope fence below — so it falls
	// through, exactly as it did when the fence was a prefix rule.
	if registered && owner.OwnerType != "system" {
		switch owner.OwnerType {
		case "user":
			if actor != "user" {
				return fmt.Errorf("namespace %q writable only by user", ns)
			}
		case "app":
			if actor != "app:"+owner.OwnerID || clientID != owner.OwnerID {
				return fmt.Errorf("namespace %q writable only by app:%s", ns, owner.OwnerID)
			}
		}
		return nil
	}

	// UNREGISTERED. These two fences stay, and CW-20260912-0078 deliberately
	// did NOT remove them despite moving ownership to the registry in every
	// other respect.
	//
	// The reason is that registration is a SIDE EFFECT OF WRITING, so the
	// first write to any namespace arrives unregistered and reaches the
	// fallthrough below. That makes these the only rules protecting a
	// namespace that does not exist yet:
	//
	//   - user scope: without it, any actor could create a namespace in the
	//     one tier the ADR exists to keep agents out of.
	//   - app scope: without it, app `editor` may write `app/other/session`.
	//     That is cross-app isolation, not bookkeeping — TestPolicyDeniedWrite,
	//     TestPolicyAndDeterminismEndToEnd and the golden API error contract
	//     all assert the 403, on namespaces no registry row covers.
	//
	// So what this grammar change actually removes here is the ad-hoc STRING
	// PREFIX form, not the rules: both now read the scope off the shared
	// vocabulary, so `user/` and `app/` are scope types rather than two magic
	// strings, and the other four scopes are visibly unfenced rather than
	// accidentally omitted.
	//
	// Replacing inference with declared ownership is N4's job and needs
	// declared registration to exist first. Until then an unregistered
	// namespace cannot be refused outright without breaking every first write,
	// and these are a fence rather than a model.
	if scope, id, ok := splitScopeHead(ns); ok {
		switch scope {
		case "user":
			if actor != "user" {
				return fmt.Errorf("writes to protected namespace %q require actor=user", ns)
			}
		case "app":
			if id == "" {
				return fmt.Errorf("invalid app namespace %q", ns)
			}
			if clientID != id || actor != "app:"+id {
				return fmt.Errorf("namespace %q writable only by app:%s", ns, id)
			}
		}
	}
	return nil
}

// splitScopeHead returns the scope keyword and its id segment, and whether the
// namespace begins with a known scope type.
//
// It duplicates no vocabulary: scopeTypes is the list, stated once below. The
// parser in internal/memory owns the full grammar, but contextpolicy cannot
// import it — internal/memory imports contextpolicy's peer packages and the
// cycle is real — so what crosses the boundary is the scope vocabulary alone,
// pinned by TestScopeVocabularyMatchesTheParser.
func splitScopeHead(ns string) (scope, id string, ok bool) {
	parts := strings.Split(ns, "/")
	if len(parts) == 0 {
		return "", "", false
	}
	if !scopeTypes[parts[0]] {
		return "", "", false
	}
	if parts[0] == "system" {
		return parts[0], "", true
	}
	if len(parts) < 2 || parts[1] == "" {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

// scopeTypes is the closed scope vocabulary, mirroring memory.scopeKeywords.
var scopeTypes = map[string]bool{
	"user": true, "project": true, "app": true,
	"org": true, "session": true, "system": true,
}

// CanPromote checks promotion constraints into protected user namespace.
func (e *Engine) CanPromote(actor, toNamespace string) error {
	if actor != "user" {
		return errors.New("promote requires actor=user")
	}
	if !strings.HasPrefix(strings.TrimSpace(toNamespace), "user/") {
		return errors.New("promotion target must be in user/*")
	}
	return nil
}

// ValidatePayload enforces namespace schema policy when configured.
// Supported policy keys:
// - required_keys: [string]
func (e *Engine) ValidatePayload(namespace string, payload json.RawMessage) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return errors.New("namespace required")
	}
	e.mu.RLock()
	owner, ok := e.owners[ns]
	e.mu.RUnlock()
	if !ok || owner.Policy == nil {
		return nil
	}

	required := requiredKeys(owner.Policy)
	if len(required) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return errors.New("schema validation failed: payload must be a JSON object")
	}
	var missing []string
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("schema validation failed: missing required keys: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateTierPolicy checks tier enforcement fields for the given op.
// op must be one of: "write", "promote.request", "promote.approve", "promote.apply",
// "repair", "namespace.register".
// payloadLen is the byte length of the payload (used for max_bytes_per_key check).
// payload is only examined for required_schema_keys when op == "write".
func (e *Engine) ValidateTierPolicy(namespace, op string, payloadLen int, payload json.RawMessage) error {
	ns := strings.TrimSpace(namespace)
	e.mu.RLock()
	owner, ok := e.owners[ns]
	e.mu.RUnlock()
	if !ok {
		return nil
	}

	tp := ParseTierPolicy(owner.Policy)

	if !tp.HasOp(op) {
		return &PolicyViolation{
			Field:  "allowed_ops",
			Detail: fmt.Sprintf("%s not permitted in namespace %q", op, ns),
		}
	}

	if op == "write" {
		if tp.MaxBytesPerKey > 0 && payloadLen > tp.MaxBytesPerKey {
			return &PolicyViolation{
				Field:  "max_bytes_per_key",
				Detail: fmt.Sprintf("payload size %d exceeds limit %d for namespace %q", payloadLen, tp.MaxBytesPerKey, ns),
			}
		}
		if len(tp.RequiredSchemaKeys) > 0 && len(payload) > 0 {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(payload, &obj); err != nil {
				return &PolicyViolation{Field: "required_schema_keys", Detail: "payload must be a JSON object for schema key validation"}
			}
			var missing []string
			for _, k := range tp.RequiredSchemaKeys {
				if _, found := obj[k]; !found {
					missing = append(missing, k)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				return &PolicyViolation{
					Field:  "required_schema_keys",
					Detail: fmt.Sprintf("missing required keys: %s", strings.Join(missing, ", ")),
				}
			}
		}
	}
	return nil
}

// PolicyViolation is returned when a tier policy check fails.
type PolicyViolation struct {
	Field  string
	Detail string
}

func (v *PolicyViolation) Error() string {
	return fmt.Sprintf("policy_violation: %s: %s", v.Field, v.Detail)
}

func requiredKeys(policy map[string]any) []string {
	raw, ok := policy["required_keys"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

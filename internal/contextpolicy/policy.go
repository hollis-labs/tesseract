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

	if strings.HasPrefix(ns, "user/") {
		if actor != "user" {
			return fmt.Errorf("writes to protected namespace %q require actor=user", ns)
		}
		return nil
	}

	if strings.HasPrefix(ns, "app/") {
		parts := strings.Split(ns, "/")
		if len(parts) < 2 || parts[1] == "" {
			return fmt.Errorf("invalid app namespace %q", ns)
		}
		nsOwner := parts[1]
		if clientID != nsOwner || actor != "app:"+nsOwner {
			return fmt.Errorf("namespace %q writable only by app:%s", ns, nsOwner)
		}
	}

	e.mu.RLock()
	owner, ok := e.owners[ns]
	e.mu.RUnlock()
	if !ok {
		return nil
	}

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

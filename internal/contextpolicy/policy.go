package contextpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const MinimumWorkspaceRetentionIdle = 30 * 24 * time.Hour

// WorkspaceRetentionPolicy is the namespace-level retention override. A nil
// PurgeEnabled inherits the operator setting; false can only make it safer.
type WorkspaceRetentionPolicy struct {
	PurgeEnabled *bool
	MinimumIdle  time.Duration
}

// ParseWorkspaceRetentionPolicy strictly decodes the workspace_retention
// member without treating the older generic retention string as workspace TTL.
func ParseWorkspaceRetentionPolicy(policy map[string]any) (WorkspaceRetentionPolicy, bool, error) {
	raw, present := policy["workspace_retention"]
	if !present {
		return WorkspaceRetentionPolicy{}, false, nil
	}
	member, ok := raw.(map[string]any)
	if !ok {
		return WorkspaceRetentionPolicy{}, true, fmt.Errorf("workspace_retention must be an object")
	}
	for key := range member {
		if key != "purge_enabled" && key != "minimum_idle" {
			return WorkspaceRetentionPolicy{}, true, fmt.Errorf("unknown workspace_retention setting %q", key)
		}
	}
	var out WorkspaceRetentionPolicy
	if value, ok := member["purge_enabled"]; ok {
		enabled, valid := value.(bool)
		if !valid {
			return WorkspaceRetentionPolicy{}, true, fmt.Errorf("workspace_retention.purge_enabled must be boolean")
		}
		out.PurgeEnabled = &enabled
	}
	if value, ok := member["minimum_idle"]; ok {
		text, valid := value.(string)
		if !valid || strings.TrimSpace(text) == "" {
			return WorkspaceRetentionPolicy{}, true, fmt.Errorf("workspace_retention.minimum_idle must be a duration string")
		}
		duration, err := time.ParseDuration(text)
		if err != nil {
			return WorkspaceRetentionPolicy{}, true, fmt.Errorf("workspace_retention.minimum_idle: %w", err)
		}
		if duration < MinimumWorkspaceRetentionIdle {
			return WorkspaceRetentionPolicy{}, true, fmt.Errorf("workspace_retention.minimum_idle must be at least %s", MinimumWorkspaceRetentionIdle)
		}
		out.MinimumIdle = duration
	}
	return out, true, nil
}

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
// access enforcement — CanWrite treats them like unregistered
// namespaces and fall through to the prefix-based default rules.
func (e *Engine) RegisterNamespace(namespace, ownerType, ownerID string, policy map[string]any) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return errors.New("namespace required")
	}
	if !scopeTypes[ownerType] {
		return errors.New("owner_type must be user|project|app|org|session|system")
	}
	if strings.TrimSpace(ownerID) == "" {
		return errors.New("owner_id required")
	}
	if _, _, err := ParseWorkspaceRetentionPolicy(policy); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.owners[ns] = NamespaceOwner{OwnerType: ownerType, OwnerID: ownerID, Policy: policy}
	return nil
}

// GetNamespace returns namespace metadata when registered. It checks for an
// exact registration with declared policy first, then falls back to the scope
// head (e.g. project/{slug}), and finally falls back to exact inferred registration.
func (e *Engine) GetNamespace(namespace string) (NamespaceOwner, bool) {
	ns := strings.TrimSpace(namespace)
	e.mu.RLock()
	defer e.mu.RUnlock()
	if owner, ok := e.owners[ns]; ok {
		if isDeclaredPolicy(owner.Policy) {
			return owner, true
		}
	}
	if head := extractScopeHead(ns); head != "" && head != ns {
		if owner, ok := e.owners[head]; ok {
			return owner, true
		}
	}
	if owner, ok := e.owners[ns]; ok {
		return owner, true
	}
	return NamespaceOwner{}, false
}

func isDeclaredPolicy(policy map[string]any) bool {
	if policy == nil {
		return false
	}
	src, _ := policy["source"].(string)
	if src == "inferred" || src == "inferred-backfill" {
		return false
	}
	return len(policy) > 0
}

// ScopePolicyViolation is returned when a namespace write authorization fails.
type ScopePolicyViolation struct {
	Namespace string
	Scope     string
	Actor     string
	Message   string
}

func (v *ScopePolicyViolation) Error() string {
	return v.Message
}

// UserScopeWriteError returns a teaching error when a non-user actor attempts
// to write to a user-scoped namespace.
//
// Rejection follows the voice and shape of CW-20260912-0055: it answers what was
// attempted, what is probably right, and where to fetch the authoritative rule.
func UserScopeWriteError(namespace, actor string) error {
	head := ExtractScopeHead(namespace)
	if head == "" {
		head = namespace
	}
	msg := fmt.Sprintf("writes to protected namespace %q require actor=user (attempted scope %q with actor %q). "+
		"User scope is reserved for content the human author explicitly directs — almost never the right answer for an agent. "+
		"What to use instead: for agent-authored project content, use `project/{slug}` (e.g. project/<project_id>/memory/<type>); "+
		"for how-we-work guidelines or conventions, use `system` (e.g. system/memory/<type>). "+
		"Run `tesseract_skills namespaces` for canonical namespace patterns and authority rules.",
		namespace, head, actor)
	return &ScopePolicyViolation{
		Namespace: namespace,
		Scope:     head,
		Actor:     actor,
		Message:   msg,
	}
}

// CanWrite enforces namespace write rules.
//
// The honest limitation (CW-20260912-0082): actor is caller-asserted, so this
// gates honesty rather than authority — any field an agent can set, an agent
// can set wrongly. CW-20260912-0024 (proxy-stamped provenance) is the real fix
// and is not in scope here; a forgeable check plus a teaching error still
// prevents the accidental case, which produced the 1152 inferred namespaces in
// the first place.
func (e *Engine) CanWrite(clientID, actor, namespace string) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return errors.New("namespace required")
	}
	if strings.TrimSpace(actor) == "" {
		return errors.New("actor required")
	}

	// The registry is the authority on ownership, and it is consulted FIRST
	// via GetNamespace, which checks declared exact registration first, falls
	// back to scope head (e.g. project/{slug} or user/{id}), and finally falls
	// back to exact inferred registration.
	owner, registered := e.GetNamespace(ns)

	// owner_type "system" is a SENTINEL registration for namespaces that never
	// fit the user|app tier shape, and RegisterNamespace documents that it is
	// not consulted for enforcement. Treating it as registered here would let a
	// sentinel row silently disable the scope fence below — so it falls
	// through, exactly as it did when the fence was a prefix rule.
	if registered && owner.OwnerType != "system" {
		switch owner.OwnerType {
		case "user":
			if actor != "user" {
				return UserScopeWriteError(ns, actor)
			}
		case "app":
			if actor != "app:"+owner.OwnerID || clientID != owner.OwnerID {
				return fmt.Errorf("namespace %q writable only by app:%s", ns, owner.OwnerID)
			}
		}
		return nil
	}

	// UNREGISTERED fallback:
	// If the scope is user, non-user actors are refused with the teaching error.
	// If the scope is app, cross-app isolation is enforced.
	// Other scopes (project, org, session, system) are not fenced.
	if scope, id, ok := splitScopeHead(ns); ok {
		switch scope {
		case "user":
			if actor != "user" {
				return UserScopeWriteError(ns, actor)
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

// ExtractScopeHead returns the {scope}/{id} head for recognized scopes (user,
// project, app, org, session), or "system" for system. Returns "" for unknown
// shapes.
func ExtractScopeHead(namespace string) string {
	ns := strings.TrimSpace(namespace)
	parts := strings.Split(ns, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	if !scopeTypes[parts[0]] {
		return ""
	}
	if parts[0] == "system" {
		return "system"
	}
	if len(parts) >= 2 && parts[1] != "" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

func extractScopeHead(namespace string) string {
	return ExtractScopeHead(namespace)
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

// CanPromote was retired in CW-20260912-0082. It had zero callers across the
// codebase (verified by CW-20260911-0006). It belonged to the legacy
// context/records store promotion path (internal/contextapi/promote_handler.go),
// which is being retired separately under CW-20260909-0037, and is completely
// distinct from the shipped workspacepromotion package.

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

// Validate checks tier enforcement fields for the given op.
// op must be one of: "write", "promote.request", "promote.approve", "promote.apply",
// "repair", "namespace.register".
// payloadLen is the byte length of the payload (used for max_bytes_per_key check).
// payload is only examined for required_schema_keys when op == "write".
func (p TierPolicy) Validate(namespace, op string, payloadLen int, payload json.RawMessage) error {
	if !p.HasOp(op) {
		return &PolicyViolation{
			Field:  "allowed_ops",
			Detail: fmt.Sprintf("%s not permitted in namespace %q", op, namespace),
		}
	}

	if op == "write" {
		if p.MaxBytesPerKey > 0 && payloadLen > p.MaxBytesPerKey {
			return &PolicyViolation{
				Field:  "max_bytes_per_key",
				Detail: fmt.Sprintf("payload size %d exceeds limit %d for namespace %q", payloadLen, p.MaxBytesPerKey, namespace),
			}
		}
		if len(p.RequiredSchemaKeys) > 0 {
			if len(payload) == 0 || strings.TrimSpace(string(payload)) == "" || string(payload) == "null" {
				missing := make([]string, len(p.RequiredSchemaKeys))
				copy(missing, p.RequiredSchemaKeys)
				sort.Strings(missing)
				return &PolicyViolation{
					Field:  "required_schema_keys",
					Detail: fmt.Sprintf("missing required keys: %s", strings.Join(missing, ", ")),
				}
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(payload, &obj); err != nil {
				return &PolicyViolation{Field: "required_schema_keys", Detail: "payload must be a JSON object for schema key validation"}
			}
			var missing []string
			for _, k := range p.RequiredSchemaKeys {
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

// ValidateTierPolicy checks tier enforcement fields for the given op.
// op must be one of: "write", "promote.request", "promote.approve", "promote.apply",
// "repair", "namespace.register".
// payloadLen is the byte length of the payload (used for max_bytes_per_key check).
// payload is only examined for required_schema_keys when op == "write".
func (e *Engine) ValidateTierPolicy(namespace, op string, payloadLen int, payload json.RawMessage) error {
	owner, ok := e.GetNamespace(namespace)
	if !ok {
		return nil
	}
	tp := ParseTierPolicy(owner.Policy)
	return tp.Validate(namespace, op, payloadLen, payload)
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

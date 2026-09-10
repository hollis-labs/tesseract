package typeregistry

import (
	"fmt"
	"sort"
	"strings"
)

// The context-record surface of the registry.
//
// Everything here is scoped to VocabContextRecordType and named to say so.
// The promoted package serves three vocabularies, so a bare GetType or
// ValidateStatus would be asking "known to what?" and answering "whichever one
// the author happened to mean" — which is how a registry serving one store
// quietly keeps serving one store. These methods retire with the context store
// (CW-20260909-0037); the generic lookups in registry.go are what the other
// domains use and what a new domain gets.

// Status is a context record lifecycle state.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusReviewed   Status = "reviewed"
	StatusCanonical  Status = "canonical"
	StatusDeprecated Status = "deprecated"
)

// ValidStatuses is the canonical set of allowed status values.
var ValidStatuses = []Status{StatusDraft, StatusReviewed, StatusCanonical, StatusDeprecated}

// IsValidStatus reports whether s is a recognized status value.
func IsValidStatus(s string) bool {
	for _, v := range ValidStatuses {
		if string(v) == s {
			return true
		}
	}
	return false
}

// ValidTransitions defines the allowed status transitions.
var ValidTransitions = map[string][]string{
	"draft":      {"reviewed", "deprecated"},
	"reviewed":   {"canonical", "deprecated"},
	"canonical":  {"deprecated"},
	"deprecated": {}, // terminal state
}

// CanTransition checks whether the transition from oldStatus to newStatus is
// allowed by the lifecycle.
func CanTransition(oldStatus, newStatus string) bool {
	allowed, ok := ValidTransitions[oldStatus]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == newStatus {
			return true
		}
	}
	return false
}

// IsPromotable reports whether a status can be advanced forward.
func IsPromotable(status string) bool {
	switch status {
	case "draft", "reviewed":
		return true
	default:
		return false
	}
}

// NextPromotionStatus returns the next status in the promotion chain.
func NextPromotionStatus(current string) string {
	switch current {
	case "draft":
		return "reviewed"
	case "reviewed":
		return "canonical"
	default:
		return ""
	}
}

// ContextType returns the declaration for a context record type.
func (r *Registry) ContextType(typeID string) (Type, bool) {
	return r.Lookup(VocabContextRecordType, typeID)
}

// ListContextTypes returns every context record type, sorted by type_id.
func (r *Registry) ListContextTypes() []Type {
	return r.Types(VocabContextRecordType)
}

// GetView returns the view definition for the given view_id.
func (r *Registry) GetView(viewID string) (ViewDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.views[viewID]
	return v, ok
}

// ListViews returns all registered views sorted by view_id.
func (r *Registry) ListViews() []ViewDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ViewDef, 0, len(r.views))
	for _, v := range r.views {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ViewID < out[j].ViewID })
	return out
}

// IsKnownContextType reports whether typeID is accepted as a context record
// type — declared, or under the `custom/` prefix the vocabulary leaves open.
func (r *Registry) IsKnownContextType(typeID string) bool {
	return r.Allows(VocabContextRecordType, typeID)
}

// ValidateContextType validates that a record type is known. An empty type is
// valid: record_type is optional on the context surface.
func (r *Registry) ValidateContextType(typeID string) error {
	if typeID == "" {
		return nil
	}
	if !r.IsKnownContextType(typeID) {
		return fmt.Errorf("unknown context type: %q", typeID)
	}
	return nil
}

// ValidateContextStatus checks that status is valid for the record type.
func (r *Registry) ValidateContextStatus(typeID, status string) error {
	if status == "" {
		return nil // status is optional (defaults to draft)
	}
	if !IsValidStatus(status) {
		return fmt.Errorf("invalid status: %q (valid: draft, reviewed, canonical, deprecated)", status)
	}
	if typeID == "" {
		return nil
	}
	t, ok := r.ContextType(typeID)
	if !ok {
		return nil // custom/ types accept all valid statuses
	}
	if !t.HasAllowedStatus(status) {
		return fmt.Errorf("status %q not allowed for type %q (allowed: %s)",
			status, typeID, strings.Join(t.AllowedStatuses, ", "))
	}
	return nil
}

// ValidateContextRequiredFields checks that every required field for a
// declared record type is present as a top-level payload key. A custom/ type
// declares nothing, so there is nothing to require.
func (r *Registry) ValidateContextRequiredFields(typeID string, payload map[string]any) error {
	if typeID == "" || strings.HasPrefix(typeID, "custom/") {
		return nil
	}
	t, ok := r.ContextType(typeID)
	if !ok || len(t.RequiredFields) == 0 {
		return nil
	}
	var missing []string
	for _, f := range t.RequiredFields {
		v, exists := payload[f]
		if !exists {
			missing = append(missing, f)
			continue
		}
		// Treat empty strings as missing.
		if s, ok := v.(string); ok && s == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("type %q requires fields: %s", typeID, strings.Join(missing, ", "))
	}
	return nil
}

// ValidateContextTransition validates a status transition for a record type.
//
// It takes no actor. It used to: a type could declare
// "draft->reviewed:requires_human_approval" and this refused the move unless
// the caller passed actor=user. That knob left with promotion_rules — a type
// declaring who may move a record is workflow authority sitting in a type
// declaration, per [[tesseract_type_declaration_field_set]] — and it was
// already vacuous, since every caller defaults actor to "user" when the field
// is absent. Dropping the parameter rather than ignoring it is what keeps the
// signature honest about that.
func (r *Registry) ValidateContextTransition(typeID, oldStatus, newStatus string) error {
	if oldStatus == "" {
		oldStatus = "draft"
	}
	if newStatus == "" {
		return fmt.Errorf("target status is required")
	}
	if !IsValidStatus(newStatus) {
		return fmt.Errorf("invalid target status: %q", newStatus)
	}
	if err := r.ValidateContextStatus(typeID, newStatus); err != nil {
		return err
	}
	if !CanTransition(oldStatus, newStatus) {
		return fmt.Errorf("invalid transition: %s -> %s", oldStatus, newStatus)
	}
	return nil
}

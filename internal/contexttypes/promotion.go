package contexttypes

import (
	"fmt"
	"strings"
)

// ValidTransitions defines the allowed status transitions.
var ValidTransitions = map[string][]string{
	"draft":      {"reviewed", "deprecated"},
	"reviewed":   {"canonical", "deprecated"},
	"canonical":  {"deprecated"},
	"deprecated": {}, // terminal state
}

// CanTransition checks whether the transition from oldStatus to newStatus is valid.
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

// ValidateTransition validates a status transition, including type-specific rules.
func (r *Registry) ValidateTransition(typeID, oldStatus, newStatus, actor string) error {
	if oldStatus == "" {
		oldStatus = "draft"
	}
	if newStatus == "" {
		return fmt.Errorf("target status is required")
	}
	if !IsValidStatus(newStatus) {
		return fmt.Errorf("invalid target status: %q", newStatus)
	}

	// Check type allows the target status.
	if err := r.ValidateStatus(typeID, newStatus); err != nil {
		return err
	}

	// Check the transition is valid.
	if !CanTransition(oldStatus, newStatus) {
		return fmt.Errorf("invalid transition: %s -> %s", oldStatus, newStatus)
	}

	// Check type-specific promotion rules.
	if typeID != "" {
		ct, ok := r.GetType(typeID)
		if ok {
			for _, rule := range ct.PromotionRules {
				if err := checkPromotionRule(rule, oldStatus, newStatus, actor); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// RequiresHumanApproval reports whether the transition requires human approval.
func (r *Registry) RequiresHumanApproval(typeID, oldStatus, newStatus string) bool {
	if typeID == "" {
		return false
	}
	ct, ok := r.GetType(typeID)
	if !ok {
		return false
	}
	transKey := oldStatus + "->" + newStatus
	for _, rule := range ct.PromotionRules {
		parts := strings.SplitN(rule, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == transKey && parts[1] == "requires_human_approval" {
			return true
		}
	}
	return false
}

// checkPromotionRule validates a single promotion rule against the transition.
// Rule format: "from->to:requires_human_approval"
func checkPromotionRule(rule, oldStatus, newStatus, actor string) error {
	parts := strings.SplitN(rule, ":", 2)
	if len(parts) != 2 {
		return nil // skip malformed rules
	}
	transKey := parts[0]
	constraint := parts[1]

	actualKey := oldStatus + "->" + newStatus
	if transKey != actualKey {
		return nil // rule doesn't apply to this transition
	}

	switch constraint {
	case "requires_human_approval":
		if actor != "user" {
			return fmt.Errorf("transition %s requires human approval (actor=user), got actor=%q", transKey, actor)
		}
	}

	return nil
}

// IsPromotable reports whether a status can be promoted (advanced forward).
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

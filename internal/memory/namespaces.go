package memory

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Scope is the memory namespace scope (D10).
type Scope int

const (
	ScopeUnknown Scope = iota
	ScopeUser
	ScopeProject
	ScopeSession
)

// Namespace is a parsed memory namespace.
type Namespace struct {
	Scope     Scope
	UserID    string
	ProjectID string // only populated for ScopeProject
	SessionID string // only populated for ScopeSession
	Type      string // {type} segment — always populated for valid namespaces
}

// String returns the canonical string form of the namespace, matching the
// format accepted by ParseNamespace.
func (n Namespace) String() string {
	switch n.Scope {
	case ScopeUnknown:
		return ""
	case ScopeUser:
		return fmt.Sprintf("user/%s/memory/%s", n.UserID, n.Type)
	case ScopeProject:
		return fmt.Sprintf("user/%s/project/%s/memory/%s", n.UserID, n.ProjectID, n.Type)
	case ScopeSession:
		return fmt.Sprintf("user/%s/session/%s/memory/%s", n.UserID, n.SessionID, n.Type)
	}
	return ""
}

// Prefix returns the namespace string WITHOUT the {type} segment — i.e. the
// shared prefix that all typed sub-namespaces in this scope share. Useful for
// recall prefix-matching (CW-20260519-0030).
//
// Returns "" for ScopeUnknown.
func (n Namespace) Prefix() string {
	switch n.Scope {
	case ScopeUnknown:
		return ""
	case ScopeUser:
		return fmt.Sprintf("user/%s/memory", n.UserID)
	case ScopeProject:
		return fmt.Sprintf("user/%s/project/%s/memory", n.UserID, n.ProjectID)
	case ScopeSession:
		return fmt.Sprintf("user/%s/session/%s/memory", n.UserID, n.SessionID)
	}
	return ""
}

// ErrInvalidNamespace is returned when a namespace string cannot be parsed.
var ErrInvalidNamespace = errors.New("invalid memory namespace")

// idSegmentRE validates user_id / project_id / session_id segments.
// Accepts alphanumerics, hyphen, underscore, colon (for manual:<ulid>), dot.
// Keeps flexibility — project_id in particular is opaque per D10.
var idSegmentRE = regexp.MustCompile(`^[a-zA-Z0-9_\-:.]+$`)

// typeSegmentRE validates the {type} segment. Lowercase letters and underscore.
// Constrained to keep types stable and grep-able across the catalog.
var typeSegmentRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// DefaultTypeAllowlist is the config-driven allowlist of memory namespace
// {type} segment values. Adding a type = append one entry here.
//
// The list is locked by decision in CW-20260519-0029 (sprint SP-20260518-0012,
// "Memory namespace shallow + faceted"). `notes` is the catch-all default
// bucket for memories that don't carry a stronger type.
var DefaultTypeAllowlist = []string{
	"decisions",
	"feedback",
	"followups",
	"learnings",
	"limitations",
	"notes",
	"outcomes",
	"references",
}

// allowedTypes holds the package-level allowlist as a set for O(1) lookup.
// Initialized from DefaultTypeAllowlist; exposed via SetTypeAllowlist for
// future config-driven overrides and test scenarios.
var allowedTypes = makeTypeSet(DefaultTypeAllowlist)

func makeTypeSet(list []string) map[string]struct{} {
	m := make(map[string]struct{}, len(list))
	for _, t := range list {
		m[t] = struct{}{}
	}
	return m
}

// IsValidType reports whether t is in the current namespace type allowlist.
func IsValidType(t string) bool {
	_, ok := allowedTypes[t]
	return ok
}

// TypeAllowlist returns a sorted copy of the current type allowlist.
func TypeAllowlist() []string {
	out := make([]string, 0, len(allowedTypes))
	for t := range allowedTypes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// SetTypeAllowlist replaces the active allowlist. Returns a restore function
// that resets the prior allowlist; intended for tests and future config wiring.
func SetTypeAllowlist(list []string) (restore func()) {
	prev := allowedTypes
	allowedTypes = makeTypeSet(list)
	return func() { allowedTypes = prev }
}

// ParseNamespace parses a memory namespace string into its components.
// Accepts the three canonical fixed-depth shapes from CW-20260519-0029:
//
//	user/{user_id}/memory/{type}
//	user/{user_id}/project/{project_id}/memory/{type}
//	user/{user_id}/session/{session_id}/memory/{type}
//
// {type} is validated against the package allowlist (DefaultTypeAllowlist by
// default). Any other shape — including the legacy flat
// `user/{id}/memory` — returns a wrapped ErrInvalidNamespace.
func ParseNamespace(s string) (Namespace, error) {
	if s == "" {
		return Namespace{}, fmt.Errorf("%w: empty", ErrInvalidNamespace)
	}
	if strings.HasSuffix(s, "/") {
		return Namespace{}, fmt.Errorf("%w: trailing slash in %q", ErrInvalidNamespace, s)
	}
	parts := strings.Split(s, "/")
	// 4-seg user/{id}/memory/{type}; 6-seg user/{id}/{project|session}/{id}/memory/{type}.
	if len(parts) != 4 && len(parts) != 6 {
		return Namespace{}, fmt.Errorf("%w: wrong segment count in %q (want 4 or 6, got %d)",
			ErrInvalidNamespace, s, len(parts))
	}
	if parts[0] != "user" {
		return Namespace{}, fmt.Errorf("%w: must start with 'user/', got %q", ErrInvalidNamespace, parts[0])
	}
	if parts[1] == "" || !idSegmentRE.MatchString(parts[1]) {
		return Namespace{}, fmt.Errorf("%w: invalid user_id %q", ErrInvalidNamespace, parts[1])
	}

	ns := Namespace{UserID: parts[1]}
	var memorySeg, typeSeg string
	switch len(parts) {
	case 4:
		// user/{id}/memory/{type}
		memorySeg = parts[2]
		typeSeg = parts[3]
		ns.Scope = ScopeUser
	case 6:
		// user/{id}/{project|session}/{id}/memory/{type}
		mid := parts[2]
		id := parts[3]
		memorySeg = parts[4]
		typeSeg = parts[5]
		if id == "" || !idSegmentRE.MatchString(id) {
			return Namespace{}, fmt.Errorf("%w: invalid %s id %q", ErrInvalidNamespace, mid, id)
		}
		switch mid {
		case "project":
			ns.Scope = ScopeProject
			ns.ProjectID = id
		case "session":
			ns.Scope = ScopeSession
			ns.SessionID = id
		default:
			return Namespace{}, fmt.Errorf("%w: unknown scope %q (expected 'project' or 'session')",
				ErrInvalidNamespace, mid)
		}
	}

	if memorySeg != "memory" {
		return Namespace{}, fmt.Errorf("%w: penultimate segment must be 'memory', got %q in %q",
			ErrInvalidNamespace, memorySeg, s)
	}
	if typeSeg == "" {
		return Namespace{}, fmt.Errorf("%w: type segment is required in %q", ErrInvalidNamespace, s)
	}
	if !typeSegmentRE.MatchString(typeSeg) {
		return Namespace{}, fmt.Errorf("%w: invalid type segment %q (must be lowercase letters/digits/underscore, starting with a letter)",
			ErrInvalidNamespace, typeSeg)
	}
	if !IsValidType(typeSeg) {
		return Namespace{}, fmt.Errorf("%w: unknown type %q (allowed: %s)",
			ErrInvalidNamespace, typeSeg, strings.Join(TypeAllowlist(), ", "))
	}
	ns.Type = typeSeg
	return ns, nil
}

// ValidateNamespace is a convenience wrapper that returns only the error.
func ValidateNamespace(s string) error {
	_, err := ParseNamespace(s)
	return err
}

package memory

import (
	"errors"
	"fmt"
	"regexp"
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
}

// String returns the canonical string form of the namespace, matching the
// format accepted by ParseNamespace.
func (n Namespace) String() string {
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

// ParseNamespace parses a memory namespace string into its components.
// Accepts the three canonical forms from D10:
//
//	user/{user_id}/memory
//	user/{user_id}/project/{project_id}/memory
//	user/{user_id}/session/{session_id}/memory
//
// Any other shape returns a wrapped ErrInvalidNamespace.
func ParseNamespace(s string) (Namespace, error) {
	if s == "" {
		return Namespace{}, fmt.Errorf("%w: empty", ErrInvalidNamespace)
	}
	if strings.HasSuffix(s, "/") {
		return Namespace{}, fmt.Errorf("%w: trailing slash in %q", ErrInvalidNamespace, s)
	}
	parts := strings.Split(s, "/")
	// Shortest valid: user/{id}/memory = 3 parts.
	// Longest valid: user/{id}/project/{pid}/memory = 5 parts.
	if len(parts) < 3 || len(parts) > 5 {
		return Namespace{}, fmt.Errorf("%w: wrong segment count in %q", ErrInvalidNamespace, s)
	}
	if parts[0] != "user" {
		return Namespace{}, fmt.Errorf("%w: must start with 'user/', got %q", ErrInvalidNamespace, parts[0])
	}
	if parts[1] == "" || !idSegmentRE.MatchString(parts[1]) {
		return Namespace{}, fmt.Errorf("%w: invalid user_id %q", ErrInvalidNamespace, parts[1])
	}
	last := parts[len(parts)-1]
	if last != "memory" {
		return Namespace{}, fmt.Errorf("%w: must end with '/memory', got %q", ErrInvalidNamespace, last)
	}
	ns := Namespace{UserID: parts[1]}
	switch len(parts) {
	case 3:
		ns.Scope = ScopeUser
		return ns, nil
	case 5:
		mid := parts[2]
		id := parts[3]
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
			return Namespace{}, fmt.Errorf("%w: unknown scope %q", ErrInvalidNamespace, mid)
		}
		return ns, nil
	default:
		return Namespace{}, fmt.Errorf("%w: malformed %q", ErrInvalidNamespace, s)
	}
}

// ValidateNamespace is a convenience wrapper that returns only the error.
func ValidateNamespace(s string) error {
	_, err := ParseNamespace(s)
	return err
}

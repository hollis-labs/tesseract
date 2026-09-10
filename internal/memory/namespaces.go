package memory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
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

// The memory namespace {type} vocabulary — now a registry lookup.
//
// It used to be DefaultTypeAllowlist, a Go slice whose own comment opened
// "the config-driven allowlist of memory namespace {type} segment values."
// It was not config-driven: it was a literal, `SetTypeAllowlist` had no
// non-test caller, and `internal/config` had no key for it. That was
// CW-20260909-0027, and this is what makes the sentence true rather than
// deleting it — the values now live in the type registry under
// `memory.type`, seeded from Go defaults and revisable in types.yaml.
//
// The list is still locked by decision in CW-20260519-0029 (sprint
// SP-20260518-0012, "Memory namespace shallow + faceted"), and `notes` is
// still the deliberate catch-all for memories that carry no stronger type.
// Config-driven means an operator can revise it without a release; it does
// not mean the list is arbitrary. Revise it in the registry, not by widening
// the parser.

// IsValidType reports whether t is in the current namespace type vocabulary.
func IsValidType(t string) bool {
	return typeregistry.Default().Allows(typeregistry.VocabMemoryType, t)
}

// TypeAllowlist returns the current type vocabulary, sorted.
func TypeAllowlist() []string {
	return typeregistry.Default().Values(typeregistry.VocabMemoryType)
}

// SetTypeAllowlist replaces the active vocabulary and returns a restore
// function.
//
// Kept as a thin wrapper over typeregistry.Install rather than deleted: it is
// the shape the existing tests are written against, and a test that needs a
// narrower vocabulary should not have to build a whole registry to get one.
// Production wiring goes through the registry directly — cmd/tesseract reads
// types.yaml at boot.
func SetTypeAllowlist(list []string) (restore func()) {
	types := make([]typeregistry.Type, 0, len(list))
	for _, t := range list {
		types = append(types, typeregistry.Type{TypeID: t})
	}
	r := typeregistry.NewRegistry()
	if err := r.LoadVocabulary(typeregistry.Vocabulary{
		VocabularyID: typeregistry.VocabMemoryType,
		Closed:       true,
		Types:        types,
	}); err != nil {
		// Panic rather than swallow. The only way to get here is an invalid
		// entry — an empty string in the list — which is a programmer error in
		// the test that called this. Ignoring it would install a registry
		// carrying the DEFAULT vocabulary, so the override would silently not
		// apply and the test would pass for the wrong reason. That failure is
		// invisible in exactly the tests this helper exists to serve.
		panic(fmt.Sprintf("memory.SetTypeAllowlist(%q): %v", list, err))
	}
	return typeregistry.Install(r)
}

// ParseNamespace parses a memory namespace string into its components.
// Accepts the three canonical fixed-depth shapes from CW-20260519-0029:
//
//	user/{user_id}/memory/{type}
//	user/{user_id}/project/{project_id}/memory/{type}
//	user/{user_id}/session/{session_id}/memory/{type}
//
// {type} is validated against the `memory.type` vocabulary in the type
// registry. Any other shape — including the legacy flat `user/{id}/memory` —
// returns a wrapped ErrInvalidNamespace.
func ParseNamespace(s string) (Namespace, error) {
	return parseScopedNamespace(s, memoryNamespaceSegment, typeregistry.VocabMemoryType)
}

// ValidateNamespace is a convenience wrapper that returns only the error.
func ValidateNamespace(s string) error {
	_, err := ParseNamespace(s)
	return err
}

// ── The scoped shallow-faceted grammar ───────────────────────────────────────

// memoryNamespaceSegment and eventNamespaceSegment are the domain segments of
// the two grammars parseScopedNamespace serves.
const (
	memoryNamespaceSegment = "memory"
	eventNamespaceSegment  = "event"
)

// scopedNamespaceSegments is the closed set of domain segments that use the
// scoped shallow-faceted grammar: a fixed depth, an optional project/session
// scope, and a {type} segment from a closed vocabulary.
//
// Knowledge is deliberately NOT here. Its namespaces are deep-hierarchical
// with free depth ({user|app}/{id}/knowledge/...), so it has no {type} segment
// to validate and no fixed shape to parse — and treating any namespace ending
// in `/knowledge` as a prefix request (see scopedPrefix) would reinterpret
// namespaces that are legal exact knowledge namespaces today.
var scopedNamespaceSegments = []string{memoryNamespaceSegment, eventNamespaceSegment}

// ParseEventNamespace parses an event namespace string into its components.
//
// The grammar is memory's with `event` in the domain-segment position
// (CW-20260909-0035, settled with Chrispian 2026-09-10):
//
//	user/{user_id}/event/{type}
//	user/{user_id}/project/{project_id}/event/{type}
//	user/{user_id}/session/{session_id}/event/{type}
//
// Reusing memory's shape rather than inventing one is the decision, not an
// economy. An agent that knows where its memories live can guess where its
// reasoning log lives, the scope segments are already the partitions a log is
// read by — a session for an agent's reasoning, the user scope for a journal —
// and the prefix-match machinery in recall_namespace.go generalizes to it with
// one list entry rather than a second parser.
//
// {type} is validated against the `event.type` vocabulary in the type
// registry, which ships `journal` and `reasoning`. The segment names the
// STREAM rather than a taxonomy of what happened; see defaultEventTypes for
// why that is the axis that earns a path segment.
//
// What the grammar deliberately does NOT carry is time. Dates in the path
// (.../event/journal/2026/09) look like the obvious partition for a log and
// are a trap: created_at is already indexed and the log read filters on it, so
// date segments turn every time-range read into a multi-namespace query for
// no gain. Time is an attribute, not a partition.
func ParseEventNamespace(s string) (Namespace, error) {
	return parseScopedNamespace(s, eventNamespaceSegment, typeregistry.VocabEventType)
}

// EventTypeList renders the current event {type} vocabulary for a tool or
// error message, so a description cannot advertise a value the parser rejects.
func EventTypeList() string {
	return typeregistry.Default().List(typeregistry.VocabEventType)
}

// EventTypeAllowlist returns the current event {type} vocabulary, sorted.
func EventTypeAllowlist() []string {
	return typeregistry.Default().Values(typeregistry.VocabEventType)
}

// ValidateEventNamespace is a convenience wrapper that returns only the error.
func ValidateEventNamespace(s string) error {
	_, err := ParseEventNamespace(s)
	return err
}

// parseScopedNamespace parses the scoped shallow-faceted grammar for one
// domain segment, validating {type} against one registry vocabulary.
//
// It is parameterized rather than copied because the two grammars differ in
// exactly two tokens. A second hand-written parser would be two places for the
// segment-count rule, the id vocabulary and the scope keywords to drift, and
// the drift would be invisible: each parser would pass its own tests.
//
// The returned Namespace carries no record of WHICH segment it was parsed
// under, and String()/Prefix() render the memory form. That is a real
// limitation, held deliberately: nothing consumes a round-tripped event
// namespace today, and inventing a parallel type or a domain field on this one
// to serve no caller is how a struct acquires a zero value that renders the
// wrong domain. See ParseEventNamespace's callers — all of them validate.
func parseScopedNamespace(s, domainSeg, vocabID string) (Namespace, error) {
	if s == "" {
		return Namespace{}, fmt.Errorf("%w: empty", ErrInvalidNamespace)
	}
	if strings.HasSuffix(s, "/") {
		return Namespace{}, fmt.Errorf("%w: trailing slash in %q", ErrInvalidNamespace, s)
	}
	parts := strings.Split(s, "/")
	// 4-seg user/{id}/{domain}/{type}; 6-seg user/{id}/{project|session}/{id}/{domain}/{type}.
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
	var gotDomainSeg, typeSeg string
	switch len(parts) {
	case 4:
		// user/{id}/{domain}/{type}
		gotDomainSeg = parts[2]
		typeSeg = parts[3]
		ns.Scope = ScopeUser
	case 6:
		// user/{id}/{project|session}/{id}/{domain}/{type}
		mid := parts[2]
		id := parts[3]
		gotDomainSeg = parts[4]
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

	if gotDomainSeg != domainSeg {
		return Namespace{}, fmt.Errorf("%w: penultimate segment must be %q, got %q in %q",
			ErrInvalidNamespace, domainSeg, gotDomainSeg, s)
	}
	if typeSeg == "" {
		return Namespace{}, fmt.Errorf("%w: type segment is required in %q", ErrInvalidNamespace, s)
	}
	if !typeSegmentRE.MatchString(typeSeg) {
		return Namespace{}, fmt.Errorf("%w: invalid type segment %q (must be lowercase letters/digits/underscore, starting with a letter)",
			ErrInvalidNamespace, typeSeg)
	}
	if !typeregistry.Default().Allows(vocabID, typeSeg) {
		return Namespace{}, fmt.Errorf("%w: unknown type %q (allowed: %s)",
			ErrInvalidNamespace, typeSeg, strings.Join(typeregistry.Default().Values(vocabID), ", "))
	}
	ns.Type = typeSeg
	return ns, nil
}

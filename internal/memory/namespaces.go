package memory

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// Scope is the namespace scope type — the FIRST segment of a namespace under
// the scope-type-rooted grammar (ADR adr_namespace_architecture, accepted
// 2026-09-12; CW-20260912-0078).
//
// The first segment names the scope type and the second its id, so scope and
// ownership stop sharing one string. Ownership moves to the namespace
// registry, which already carries owner_type and owner_id; wiring those is N4.
// What this grammar removes is the PATH's claim to encode ownership.
type Scope int

const (
	ScopeUnknown Scope = iota
	ScopeUser
	ScopeProject
	ScopeApp
	ScopeOrg
	ScopeSession
	ScopeSystem
)

// scopeKeywords maps the first segment to its scope, and is the closed set of
// scope types. It is the one place the vocabulary is stated.
//
// `system` is a SINGLETON: it takes no id segment, so its head is one segment
// where every other scope's is two. That irregularity is deliberate and was
// chosen over minting a fake id like `system/system` — an irregular tier reads
// better than an id nobody means.
var scopeKeywords = map[string]Scope{
	"user":    ScopeUser,
	"project": ScopeProject,
	"app":     ScopeApp,
	"org":     ScopeOrg,
	"session": ScopeSession,
	"system":  ScopeSystem,
}

// String renders the scope keyword, so an error message cannot name a scope
// the parser does not accept.
func (s Scope) String() string {
	for keyword, scope := range scopeKeywords {
		if scope == s {
			return keyword
		}
	}
	return "unknown"
}

// ScopeList renders the scope vocabulary for a tool or error message, sorted.
func ScopeList() string {
	keywords := make([]string, 0, len(scopeKeywords))
	for keyword := range scopeKeywords {
		keywords = append(keywords, keyword)
	}
	sort.Strings(keywords)
	return strings.Join(keywords, ", ")
}

// Namespace is a parsed namespace.
type Namespace struct {
	Scope Scope

	// ScopeID is the id segment following the scope keyword — the user id,
	// project slug, app id, org slug or session id. Empty for ScopeSystem,
	// which is a singleton and has no id segment.
	ScopeID string

	// Type is the {type} segment, always populated for a valid memory or
	// event namespace. Empty for knowledge, which has free depth after its
	// head — see Tail.
	Type string

	// Tail is the free-depth remainder after a knowledge head, with no
	// leading slash. Empty for memory and event, whose grammar ends at {type}.
	Tail string

	// Legacy reports that this parsed from a PRE-MIGRATION shape
	// (user/{uid}/project/{pid}/memory/{type} and its session peer) rather
	// than from the scope-type-rooted grammar. See parseLegacyNestedHead for
	// why those still parse and when they stop.
	Legacy bool

	// LegacyUserID is the user id that a legacy nested shape carried ahead of
	// its real scope — `chrispian` in
	// user/chrispian/project/tesseract/memory/notes.
	//
	// It is retained rather than discarded because N6's migration needs it:
	// the path is losing its ownership claim, and the registry row that
	// replaces it has to be seeded with the owner the path used to assert.
	// Empty for every namespace parsed under the new grammar, INCLUDING user
	// scope — read ScopeID there.
	LegacyUserID string

	// domain is the domain segment this was parsed under: memory, event or
	// knowledge. Unexported with a Domain() accessor because nothing should
	// be able to construct a Namespace that claims a domain its parser never
	// checked — the previous struct carried no record of which grammar built
	// it, and String() rendered the memory form for an event namespace.
	domain string
}

// UserID reports the user id for a user-scoped namespace, and "" for every
// other scope.
//
// Kept as an accessor rather than a field so that there is exactly one place
// holding the id (ScopeID). The old struct had a field per scope, which meant
// a reader had to know which one was populated to know what they were looking
// at — and under six scopes that would have been six fields, four of them
// always empty.
func (n Namespace) UserID() string {
	if n.Scope == ScopeUser {
		return n.ScopeID
	}
	return n.LegacyUserID
}

// ProjectID reports the project slug for a project-scoped namespace, "" otherwise.
func (n Namespace) ProjectID() string {
	if n.Scope == ScopeProject {
		return n.ScopeID
	}
	return ""
}

// SessionID reports the session id for a session-scoped namespace, "" otherwise.
func (n Namespace) SessionID() string {
	if n.Scope == ScopeSession {
		return n.ScopeID
	}
	return ""
}

// Head returns the scope head — the segments before the domain segment, with
// no trailing slash. `project/tesseract`, or `system` for the singleton.
//
// This is the unit the ADR calls the fixed-depth head: the part of a namespace
// whose shape is known for every domain, including knowledge, whose tail is
// free. Returns "" for ScopeUnknown.
func (n Namespace) Head() string {
	switch n.Scope {
	case ScopeUnknown:
		return ""
	case ScopeSystem:
		return "system"
	default:
		return n.Scope.String() + "/" + n.ScopeID
	}
}

// Domain returns the domain segment this namespace was parsed under, which the
// parser records so a rendered namespace cannot name the wrong domain.
func (n Namespace) Domain() string { return n.domain }

// String returns the CANONICAL string form — always the scope-type-rooted
// grammar, even for a namespace parsed from a legacy nested shape.
//
// That asymmetry is the useful part rather than a wart: parsing a legacy
// namespace and rendering it back yields the namespace it should become, so
// N6's migration mapping is this function rather than a second table that has
// to agree with this parser. user/chrispian/project/tesseract/memory/notes
// renders as project/tesseract/memory/notes, and the user id it dropped is
// still on the struct as LegacyUserID for the registry row N6 writes.
func (n Namespace) String() string {
	head := n.Head()
	if head == "" || n.domain == "" {
		return ""
	}
	switch {
	case n.Tail != "":
		return head + "/" + n.domain + "/" + n.Tail
	case n.Type != "":
		return head + "/" + n.domain + "/" + n.Type
	default:
		return head + "/" + n.domain
	}
}

// Prefix returns the namespace WITHOUT its {type} segment — the shared prefix
// every typed sub-namespace in this scope and domain shares
// (CW-20260519-0030). For knowledge it returns the head plus domain, which is
// where the fixed depth ends and free depth begins.
//
// Returns "" for ScopeUnknown.
//
// NOTE this is the string a caller would sweep with, but it is NOT on its own
// a prefix REQUEST — see scopedPrefix, which grants bare-form inference only
// to the two grammars that already had it.
func (n Namespace) Prefix() string {
	head := n.Head()
	if head == "" || n.domain == "" {
		return ""
	}
	return head + "/" + n.domain
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

// TypeList renders the current memory {type} vocabulary for a tool or error
// message, so a description cannot advertise a value the parser rejects.
//
// memory_write's `namespace` description restated the eight types as a literal
// until CW-20260910-0067, and went on advertising `references` after the
// vocabulary dropped it. `knowledge_write`'s `kind` had been rendered from the
// vocabulary since the registry move for exactly this reason; this is the peer
// that was missed.
func TypeList() string {
	return typeregistry.Default().List(typeregistry.VocabMemoryType)
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
	memoryNamespaceSegment    = "memory"
	eventNamespaceSegment     = "event"
	knowledgeNamespaceSegment = "knowledge"
)

// scopedNamespaceSegments is the closed set of domain segments whose BARE form
// scopedPrefix infers as a prefix request.
//
// It is no longer "the domains with a fixed shape" — since CW-20260912-0078
// knowledge has a fixed-depth head too, and ParseKnowledgeNamespace parses it.
// What this list now means is narrower and only about recall: these are the
// two spellings where bare-form inference is GRANDFATHERED.
//
// Knowledge stays out, and the original reason survives the grammar change
// intact. Treating `{scope}/{id}/knowledge` as a prefix request would
// reinterpret namespaces that are legal exact knowledge namespaces today —
// `user/chrispian/knowledge` holds a canonical record right now. The head
// being parseable made that inference look safe; it is not, and scopedPrefix
// carries the measurement.
//
// Nothing should be added here. A new domain gets the explicit `/*` form,
// which works at every tier.
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
	ns, rest, err := parseHead(s, domainSeg)
	if err != nil {
		return Namespace{}, err
	}
	// memory and event end at {type}: exactly one segment after the domain.
	if len(rest) != 1 {
		return Namespace{}, fmt.Errorf("%w: %s namespace needs exactly one {type} segment after %q in %q, got %d",
			ErrInvalidNamespace, domainSeg, domainSeg, s, len(rest))
	}
	typeSeg := rest[0]
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

// ParseKnowledgeNamespace parses the knowledge grammar: a FIXED-DEPTH HEAD and
// FREE DEPTH after it.
//
//	{scope}/{id}/knowledge/{anything}/{...}
//	system/knowledge/{anything}/{...}
//
// This is the part of CW-20260912-0078 that is genuinely new. Knowledge was
// deliberately excluded from the scoped grammar because its namespaces have
// free depth: there is no {type} segment to validate and no fixed shape to
// parse. That reasoning was correct and is not overturned here — it is
// SCOPED. The head is fixed and therefore parseable; the tail keeps its free
// depth and is validated only for empty segments.
//
// At least one tail segment is required by the GRAMMAR, because a head with no
// tail names the scope rather than a place to put a record. It is NOT enforced
// as a write rule here — knowledgePolicy.ValidateNamespace still accepts a
// tail-less namespace, and `user/chrispian/knowledge` holds a canonical record
// today. Whether that shape should remain writable is N4's question. What
// matters for this slice is that nothing REINTERPRETS it; see scopedPrefix.
func ParseKnowledgeNamespace(s string) (Namespace, error) {
	ns, rest, err := parseHead(s, knowledgeNamespaceSegment)
	if err != nil {
		return Namespace{}, err
	}
	if len(rest) == 0 {
		return Namespace{}, fmt.Errorf("%w: knowledge namespace needs at least one segment after %q in %q",
			ErrInvalidNamespace, knowledgeNamespaceSegment, s)
	}
	for _, seg := range rest {
		if seg == "" {
			return Namespace{}, fmt.Errorf("%w: empty segment in %q", ErrInvalidNamespace, s)
		}
	}
	ns.Tail = strings.Join(rest, "/")
	return ns, nil
}

// ValidateKnowledgeNamespace is a convenience wrapper returning only the error.
func ValidateKnowledgeNamespace(s string) error {
	_, err := ParseKnowledgeNamespace(s)
	return err
}

// parseHead parses the scope head and the domain segment, returning the parsed
// Namespace and the remaining segments after the domain.
//
// It is the single place the grammar's SHAPE lives, so memory, event and
// knowledge cannot drift on what a scope head is. Three head forms are
// accepted:
//
//	{scope}/{id}/{domain}/...   the scope-type-rooted grammar (five scopes)
//	system/{domain}/...         the singleton, which has no id segment
//	user/{uid}/{project|session}/{id}/{domain}/...   LEGACY, see below
//
// WHY THE LEGACY FORM STILL PARSES. N1 makes the new grammar parseable and
// queryable; N6 moves the data. Those have to be separable, and a parser that
// accepted only the new shape would reject every namespace in the live store
// the moment it shipped — the store would be unreadable and unwritable with no
// migration yet able to run against it. So both parse during the transition,
// the legacy result is marked Legacy, and String() renders it in the new form
// so the migration mapping IS this parser.
//
// The legacy form comes OUT once N6 has migrated and been verified — not when
// N6 merges. That distinction is the same one the MCP gateway strip carries,
// and for the same reason.
//
// There is no ambiguity between the two: a legacy namespace always begins
// `user/`, and the segment counts differ (4 or 6 legacy against 3 or 4 new).
// The one shape both could claim — `user/{id}/{domain}/{type}` — means the
// same thing under each, because user scope is exactly what it always was.
func parseHead(s, domainSeg string) (Namespace, []string, error) {
	if s == "" {
		return Namespace{}, nil, fmt.Errorf("%w: empty", ErrInvalidNamespace)
	}
	if strings.HasSuffix(s, "/") {
		return Namespace{}, nil, fmt.Errorf("%w: trailing slash in %q", ErrInvalidNamespace, s)
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return Namespace{}, nil, fmt.Errorf("%w: too few segments in %q", ErrInvalidNamespace, s)
	}

	scope, known := scopeKeywords[parts[0]]
	if !known {
		return Namespace{}, nil, fmt.Errorf("%w: unknown scope %q in %q (expected one of: %s)",
			ErrInvalidNamespace, parts[0], s, ScopeList())
	}

	// system is the singleton: system/{domain}/... — no id segment.
	if scope == ScopeSystem {
		ns := Namespace{Scope: ScopeSystem, domain: domainSeg}
		return finishHead(ns, s, domainSeg, parts[1:])
	}

	if parts[1] == "" || !idSegmentRE.MatchString(parts[1]) {
		return Namespace{}, nil, fmt.Errorf("%w: invalid %s id %q in %q",
			ErrInvalidNamespace, parts[0], parts[1], s)
	}

	// The legacy nested shapes, which are the only ones that put a scope
	// keyword in the THIRD segment: user/{uid}/project/{pid}/{domain}/...
	if scope == ScopeUser && len(parts) >= 5 {
		if nested, isNested := scopeKeywords[parts[2]]; isNested && (nested == ScopeProject || nested == ScopeSession) {
			if parts[3] == "" || !idSegmentRE.MatchString(parts[3]) {
				return Namespace{}, nil, fmt.Errorf("%w: invalid %s id %q in %q",
					ErrInvalidNamespace, parts[2], parts[3], s)
			}
			ns := Namespace{
				Scope:        nested,
				ScopeID:      parts[3],
				Legacy:       true,
				LegacyUserID: parts[1],
				domain:       domainSeg,
			}
			return finishHead(ns, s, domainSeg, parts[4:])
		}
	}

	ns := Namespace{Scope: scope, ScopeID: parts[1], domain: domainSeg}
	return finishHead(ns, s, domainSeg, parts[2:])
}

// finishHead checks that the segment in the domain position is the domain we
// were asked for, and returns whatever follows it.
func finishHead(ns Namespace, s, domainSeg string, rest []string) (Namespace, []string, error) {
	if len(rest) == 0 {
		return Namespace{}, nil, fmt.Errorf("%w: missing %q segment in %q", ErrInvalidNamespace, domainSeg, s)
	}
	if rest[0] != domainSeg {
		return Namespace{}, nil, fmt.Errorf("%w: expected %q segment, got %q in %q",
			ErrInvalidNamespace, domainSeg, rest[0], s)
	}
	return ns, rest[1:], nil
}

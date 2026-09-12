// Package surfacefields declares how each fact a caller supplies is spelled and
// placed on every surface that accepts it.
//
// It is to request shape what tests/parity's surfaceCatalog is to existence:
// one table, imported by the surfaces rather than owned by any of them. The
// catalog asserts that an operation has a door on both MCP and HTTP; this
// asserts that the two doors take the same facts under names a caller can
// predict — and, where they do not, that somebody wrote the difference down.
//
// # Why this exists
//
// Every genuine MCP/HTTP divergence CW-20260912-0048 found was introduced
// either by adding a door or by adding a field across doors. The second is the
// one that keeps happening quietly: `payload_data` landed on all three write
// tools on 2026-09-12 and the hand-written translation table in
// internal/contextapi was not updated, so the one field that task was filed
// about became the one field the translation could not translate. The table was
// not wrong when it was written. It went stale, because nothing tied it to the
// thing it described.
//
// A row here is that tie. A divergence becomes a row someone wrote rather than
// a thing nobody noticed, and TestDoorsMatchTheSurfaces fails when a surface
// grows a field the table does not carry.
//
// # What this is not
//
// It is not a plan to make the surfaces identical. Several differences are
// correct and must survive: MCP arguments are flat by protocol while HTTP
// bodies nest, `tags` is a JSON-encoded string on one and an array on the
// other, and memory_promote says `actor_agent_id` where the write doors say
// `author_agent_id` because the author wrote the revision and the actor is
// promoting it. Uniform means predictable, not identical. Each of those is a
// row with a Why.
package surfacefields

import "strings"

// Door is one operation reachable on both surfaces — the granularity at which
// spellings actually differ.
//
// A door is narrower than a tool: tesseract_get serves three routes with one
// argument set, while the three write tools take three different ones. Naming
// the door after the (domain, operation) pair rather than after the tool keeps
// one row per thing a caller can get wrong.
type Door struct {
	// Name is the stable identifier used in test failures and in the registry
	// that maps an HTTP request struct to its door.
	Name string

	// MCPTool and HTTPMethod/HTTPPath must match a surfaceCatalog row. The
	// catalog remains the source of truth for whether the pairing exists; this
	// records what the pairing accepts.
	MCPTool    string
	HTTPMethod string
	HTTPPath   string

	// Derived reports whether both sides of this door can be read off the code
	// rather than declared here.
	//
	// True for a door whose HTTP side decodes into a struct: the fields are
	// reflectable, so TestDoorsMatchTheSurfaces can assert the table against
	// both surfaces and fail on a field either one grows.
	//
	// False for a door whose HTTP side reads query parameters by literal string
	// (every GET). Nothing reflects r.URL.Query().Get("namespace"), so the HTTP
	// column for those doors is a declaration, and the check earns it back by
	// asserting the door's behavior instead — see
	// TestReadDoorsAcceptTheSpellingsTheyDeclare.
	Derived bool

	Fields []Field
}

// Field declares one concept's spelling on each surface of one door.
type Field struct {
	// Concept is the canonical name for the fact, shared across doors. It is
	// what makes "knowledge's `data` is memory's `payload.data`" expressible:
	// the two rows carry different spellings under one concept.
	Concept string

	// MCP is the tool argument name. Empty means the concept is not accepted
	// over MCP, which is itself a claim the check verifies.
	MCP string

	// MCPAliases are spellings a caller plausibly reaches for that are not the
	// argument's real name — a mechanical flattening of the HTTP path, most
	// often. They feed the hint lookup and nothing else; the check does not
	// expect the tool to declare them.
	MCPAliases []string

	// HTTP is the dotted path into the request body, or the query parameter
	// name on a GET. Empty means the concept is not accepted over HTTP.
	HTTP string

	// Why is required when the two spellings differ by more than the flat/
	// nested projection and the difference is meant to stay. It is the sentence
	// a reader needs in order to leave the row alone.
	Why string

	// Pending names the task that will remove this divergence. It is the
	// alternative to Why for a difference nobody intends to keep.
	//
	// A Pending row is not an exemption that can rot. TestPendingRowsStillDiverge
	// fails when the divergence it names has gone, so landing the fix without
	// clearing the marker is caught by the same check that tolerates it.
	Pending string
}

// Diverges reports whether the two surfaces spell this concept differently
// beyond the projection MCP applies to every nested HTTP field.
//
// The projection is mechanical: an HTTP path joins with underscores to give the
// MCP argument, so payload.summary is payload_summary and pointer.scheme is
// pointer_scheme. A row that does not satisfy it is a difference somebody chose
// — or failed to notice — and needs Why or Pending.
//
// A concept absent from one surface is not a divergence in spelling; it is a
// capability difference, checked separately by TestOneSidedFieldsAreExplained.
func (f Field) Diverges() bool {
	if f.MCP == "" || f.HTTP == "" {
		return false
	}
	return f.MCP != strings.ReplaceAll(f.HTTP, ".", "_")
}

// OneSided reports whether exactly one surface accepts this concept.
func (f Field) OneSided() bool {
	return (f.MCP == "") != (f.HTTP == "")
}

// HTTPSpellingFor answers the question an unknown-field rejection asks: a
// caller sent an MCP argument name to an HTTP route, so what does this route
// call the same fact?
//
// It replaces a literal map, and it can express an answer that map could not.
// The old table was flat-to-nested only, so it had no way to say that
// /v1/knowledge/write takes `payload_data` as the flat `data` — it could only
// point at a parent object that door does not have. Here the answer is whatever
// the row says, in either direction and under any name.
func (d Door) HTTPSpellingFor(mcpName string) (string, bool) {
	for _, f := range d.Fields {
		if f.HTTP == "" {
			continue
		}
		if f.MCP == mcpName {
			return f.HTTP, true
		}
		for _, alias := range f.MCPAliases {
			if alias == mcpName {
				return f.HTTP, true
			}
		}
	}
	return "", false
}

// MCPSpellingFor is the reverse: what the MCP tool calls a fact an HTTP caller
// named. The MCP surface has no consumer for it yet — the undeclared-argument
// refusal that would carry it lands in CW-20260912-0055 — so this exists to
// keep the table's two directions symmetric rather than to serve a caller
// today, and the check asserts it round-trips.
func (d Door) MCPSpellingFor(httpPath string) (string, bool) {
	for _, f := range d.Fields {
		if f.MCP != "" && f.HTTP == httpPath {
			return f.MCP, true
		}
	}
	return "", false
}

// Field returns the row for a concept on this door.
func (d Door) Field(concept string) (Field, bool) {
	for _, f := range d.Fields {
		if f.Concept == concept {
			return f, true
		}
	}
	return Field{}, false
}

// DoorByName resolves a door by its identifier.
func DoorByName(name string) (Door, bool) {
	for _, d := range Doors {
		if d.Name == name {
			return d, true
		}
	}
	return Door{}, false
}

// DoorByMCPTool resolves the door a tool serves. It reports false for a tool
// that serves several doors, because "which argument set" has no single answer
// there — callers that need one must name the door.
func DoorByMCPTool(tool string) (Door, bool) {
	var found Door
	var seen int
	for _, d := range Doors {
		if d.MCPTool == tool {
			found = d
			seen++
		}
	}
	if seen != 1 {
		return Door{}, false
	}
	return found, true
}

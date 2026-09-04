// Tool-name drift guard.
//
// v0.8.0 shipped a skill body that told agents to call `vanta_skills` — a tool
// no adapter registers. Nothing checked shipped prose against the live tool
// surface, so it would have kept shipping. This file is that check.
//
// It introspects the same live surface the parity test uses
// (Adapter.RegisterAllTools + MCPServer.ListTools — no stdio server), extracts
// tool-name-shaped tokens from every shipped skill body and doc, and fails when
// a token that looks like a Tesseract tool does not resolve to a registered one.
//
// The durable value is on rename events, within a stated boundary. A tool name
// is two anchors — a domain segment and an operation segment — and the guard
// catches a stale reference as long as ONE of them survives the rename. Taking
// a live name as the worked example:
//
//	tesseract_recall → memory_recall      caught (operation segment survives)
//	tesseract_recall → tesseract_search   caught (domain segment survives)
//	tesseract_recall → memory_search      BLIND  (neither survives)
//
// A rename that changes both segments at once is not covered, and would ship
// stale references silently. If one is ever planned, land the doc updates in
// the same commit; this guard will not catch them for you. See looksLikeTool
// for why the rule is anchored this way rather than on a fixed list of domain
// prefixes.
package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// ── Scan scope ─────────────────────────────────────────────────────────

// scannedRoots are walked recursively for .md files. Depth is unbounded on
// purpose: a fixed set of globs ("docs/*/*.md") silently loses coverage the
// day someone adds a level, and a guard that quietly stops looking is worse
// than no guard.
var scannedRoots = []string{
	"internal/mcpadapter/skills",
	"docs",
}

// scannedFiles are individually named shipped docs outside any scanned root.
//
// CHANGELOG.md sits beside README.md at the repo root and is deliberately not
// listed. Release notes describe identifier changes, so a line naming both the
// old and the new ID is exactly what they are for. Scanning it would turn this
// guard red on exactly the commit that ships a rename — the moment it most
// needs to be usable.
var scannedFiles = []string{
	"README.md",
}

// retiredToolReferences is the file-scoped exception for migration material.
// A migration table cannot tell a consumer what to replace without naming the
// old tool, but those names must not become globally accepted vocabulary: that
// would hide a stale executable instruction in an unrelated current doc.
var retiredToolReferences = map[string]map[string]string{
	"docs/guides/tesseract-adoption-and-v0.9-migration.md": {
		"context_head":             "v0.8-to-v0.9 replacement table",
		"memory_get":               "v0.8-to-v0.9 replacement table",
		"knowledge_get":            "v0.8-to-v0.9 replacement table",
		"context_history":          "v0.8-to-v0.9 replacement table",
		"memory_history":           "v0.8-to-v0.9 replacement table",
		"knowledge_history":        "v0.8-to-v0.9 replacement table",
		"memory_get_revision":      "v0.8-to-v0.9 replacement table",
		"memory_deprecate":         "v0.8-to-v0.9 replacement table",
		"memory_recall":            "v0.8-to-v0.9 replacement table",
		"tesseract_lookup":         "v0.8-to-v0.9 replacement table",
		"views_evaluate":           "v0.8-to-v0.9 replacement table",
		"context_packet":           "v0.8-to-v0.9 replacement table",
		"context_broker_plan":      "v0.8-to-v0.9 replacement table",
		"context_broker_fetch":     "v0.8-to-v0.9 replacement table",
		"context_broker":           "v0.8-to-v0.9 replacement table",
		"context_bulk_ingest":      "v0.8-to-v0.9 replacement table",
		"context_chunked_ingest":   "v0.8-to-v0.9 replacement table",
		"context_status_promote":   "v0.8-to-v0.9 replacement table and non-equivalence warning",
		"context_status_deprecate": "v0.8-to-v0.9 replacement table",
		"context_types_list":       "v0.8-to-v0.9 replacement table",
		"context_views_list":       "v0.8-to-v0.9 replacement table",
		"context_namespaces_list":  "v0.8-to-v0.9 replacement table",
		"context_namespace_show":   "v0.8-to-v0.9 replacement table",
		"context_promote_request":  "v0.8-to-v0.9 replacement table",
		"context_promote_approve":  "v0.8-to-v0.9 replacement table",
		"context_promote_apply":    "v0.8-to-v0.9 replacement table",
		"context_audit":            "v0.8-to-v0.9 replacement table",
		"context_promote_list":     "v0.8-to-v0.9 replacement table",
		"context_session_snapshot": "v0.8-to-v0.9 replacement table",
	},
}

// ── The allowlist ──────────────────────────────────────────────────────
//
// Two buckets, because they mean different things to a reviewer and rot in
// different ways. Every entry carries why it is not a tool name.

// nonToolVocabulary lists snake_case identifiers that pass the tool-shaped
// heuristic but are not tools: request/response fields, audit event types,
// SQL columns, error codes. Adding an entry is a claim that the token names
// something other than an MCP tool — say what, so the next reader can check.
var nonToolVocabulary = map[string]string{
	"auto_embed":          "proposed namespace-policy flag, listed under \"## Future\" in docs/vector-search.md; not a tool and not yet a field anywhere in the code",
	"bulk_ingest":         "audit event_type emitted by context_ingest under mode=bulk",
	"chunked_ingest":      "audit event_type emitted by context_ingest under mode=chunked",
	"current_revision":    "memory_state SQL column (TEXT): revision_id of the memory's current head; surfaces as the state.current_revision field on a full recall result",
	"get_revision":        "a VERB, not a tool: the operation segment of tesseract_get_revision, listed on its own row in the generated tool-naming table of docs/MCP_TOOLS.md. Any multi-segment verb in that table lands here; single-word verbs carry no underscore and never reach this heuristic",
	"head_revision":       "API response field: id of the current revision",
	"max_tokens_estimate": "the packet-assembly token budget: a request field on context_pack (both shapes) and context_plan, an echoed field in context_plan's plan.budget, and a truncation_reason value on every assembly path",
	"memory_revisions":    "SQL table: every revision of every domain, keyed by revision_id and discriminated by a domain column; named in docs/MCP_TOOLS.md when explaining why `domain` has to be a filter rather than a hint",
	"memory_state":        "SQL table: one row per logical memory, holding current_revision, activation and access_count. It carries NO domain column, which is why (namespace, key) alone cannot identify a domain",
	"memory_id":           "memory_revisions and memory_state SQL column (TEXT): the stable id of the memory a revision belongs to; surfaces as revision.memory_id on every recall result",
	"memory_key":          "memory_write request field: the stable key of a keyed memory",
	"missing_head":        "API error code: namespace/key has no head revision",
	"rag_query":           "the operation segment of context_rag_query, quoted in the exemptions row of the generated tool-naming table in docs/MCP_TOOLS.md. Not a tool name on its own",
	"session_snapshot":    "audit event_type emitted by context_session_write",
	"source_revision":     "promotion API response field",
	"status_deprecate":    "audit event_type emitted by the HTTP route POST /v1/context/status/deprecate only; context_status_set with status=deprecated emits nothing",
	"status_promote":      "audit event_type emitted by context_status_set on the promotion path",
	"target_revision":     "promotion API response field",
	"tokens_estimate":     "context_pack manifest field under shape=packet",
	"typed_write":         "audit event_type emitted by context_typed_write",
}

// plannedTool is a forward reference: a doc that names a tool ID and says, in
// the same breath, that the tool does not exist yet.
//
// That is a third category, distinct from both a real tool name and a non-tool
// identifier. A stale reference points at something that used to exist and is
// a defect. A forward reference points at something that is scheduled and is
// not — the doc is not wrong, the code has simply not caught up.
//
// What keeps this bucket from becoming an overflow bin for anything
// inconvenient is that membership has to prove itself at both ends:
//
//   - Front end: Doc must resolve — the named file and line must exist and must
//     actually contain the token (TestPlannedToolsAreTracked). An entry cannot
//     claim a forward declaration that isn't there.
//   - Back end: the entry expires. When the tool registers, or the doc stops
//     naming it, TestToolNameAllowlistIsCurrent fails and the entry comes out.
//
// If a token is not a tool at all, it belongs in nonToolVocabulary. If a doc
// names a tool that should already exist, that is the defect this guard is for
// — fix the doc, do not add it here.
type plannedTool struct {
	Doc     string // "path:line" of the forward declaration, relative to repo root
	Tracked string // tracking ID for the work that registers the tool
	Why     string
}

var plannedTools = map[string]plannedTool{
	"context_consistency_repair": {
		Doc:     "docs/MCP_TOOLS.md:281",
		Tracked: "TASK-20260415-010",
		Why: "MCP peer of the HTTP-only /v1/context/consistency/repair. The doc names it " +
			"while stating it is batch 2; surfaceCatalog waives the same route as " +
			"\"batch 2 — MCP peer pending TASK-20260415-010\".",
	},
}

// trackingIDRE matches the tracking-ID formats this repo uses in waivers and
// tickets: TASK-YYYYMMDD-NNN, CW-YYYYMMDD-NNNN, SPR-YYYYMMDD-slug.
var trackingIDRE = regexp.MustCompile(`^(TASK|CW|SPR)-\d{8}-[A-Za-z0-9-]+$`)

// ── Token extraction ───────────────────────────────────────────────────

// toolTokenRE matches a lower-snake_case identifier with at least one
// underscore. Every registered tool name has one; single words like
// "activation" or "canonical" do not, which removes most of the prose noise
// before the tool-shaped heuristic even runs.
var toolTokenRE = regexp.MustCompile(`[a-z][a-z0-9]*(?:_[a-z0-9]+)+`)

// tokenHit is one occurrence of a candidate token.
type tokenHit struct {
	Token string
	File  string // repo-relative
	Line  int
}

// extractCandidates pulls tool-name-shaped tokens out of markdown text.
//
// Matching is not restricted to backticked spans. Measured at 0b482c4 —
// re-derive before relying on it — backtick-plus-headings and match-everywhere
// flagged the identical set of tokens, so restricting to code spans bought no
// reduction in noise while adding a way to be wrong. Headings have to be
// covered either way: internal/mcpadapter/skills/promotion.md heads a section
// "## memory_promote (the shortcut)", with the tool name bare.
//
// Two exclusions, both for tokens that are structurally something else. Both
// are deliberately narrow: an exclusion is a hole in the guard, so it must
// match the one construct it is for and nothing that merely resembles it.
//
//   - Followed by "_*": a wildcard family reference ("context_promote_*")
//     naming a group of tools rather than one. The check requires the
//     underscore. Excluding a bare trailing "*" would also swallow markdown
//     emphasis — "**memory_recall**" and "*vanta_skills*" close with the same
//     character — which is exactly how a stale name in a bolded definition
//     list ("- **memory_recall** — search memory.") would slip through. Every
//     wildcard family in this corpus carries the underscore, so requiring it
//     costs nothing.
//   - Followed by a known source-file extension: a filename
//     ("internal/mcpadapter/memory_tools.go"), which shares the domain segment
//     with the tools it registers and would otherwise flag on every mention.
//     Matched against a fixed extension list, not a length bound — ".limit"
//     and ".mode" are field paths on a tool, not filenames, and must stay
//     visible.
func extractCandidates(file, content string) []tokenHit {
	var hits []tokenHit
	for i, line := range strings.Split(content, "\n") {
		for _, loc := range toolTokenRE.FindAllStringIndex(line, -1) {
			rest := line[loc[1]:]
			if strings.HasPrefix(rest, "_*") {
				continue // wildcard family, e.g. context_promote_*
			}
			if fileExtRE.MatchString(rest) {
				continue // source filename, e.g. memory_tools.go
			}
			hits = append(hits, tokenHit{Token: line[loc[0]:loc[1]], File: file, Line: i + 1})
		}
	}
	return hits
}

// fileExtRE matches a known file extension immediately following a token. The
// alternation is a closed list rather than a character-count bound, so a
// sentence-ending period ("call memory_recall.") and a field path
// ("memory_recall.limit") are both left visible to the guard.
var fileExtRE = regexp.MustCompile(`^\.(go|md|json|ya?ml|sql|sh|toml|txt|html|css|jsx?|tsx?|py)\b`)

// ── The tool-shaped heuristic ──────────────────────────────────────────

// toolShape holds the first and last name segments of the live tool surface.
// Both sets are derived from the registry at run time, never hardcoded, so the
// heuristic re-anchors itself on whatever the tools are currently called.
type toolShape struct {
	registered map[string]struct{}
	firstSegs  map[string]struct{}
	lastSegs   map[string]struct{}
}

func newToolShape(registered map[string]struct{}) toolShape {
	ts := toolShape{
		registered: registered,
		firstSegs:  map[string]struct{}{},
		lastSegs:   map[string]struct{}{},
	}
	for name := range registered {
		segs := strings.Split(name, "_")
		ts.firstSegs[segs[0]] = struct{}{}
		ts.lastSegs[segs[len(segs)-1]] = struct{}{}
	}
	return ts
}

// looksLikeTool reports whether a token is close enough to the live naming
// convention that a reader could mistake it for a tool.
//
// The rule is: it shares a domain segment (first) or an operation segment
// (last) with some registered tool. Both halves are load-bearing —
//
//   - Prefix alone misses the motivating case. `vanta_skills` carries a domain
//     segment no tool uses, which is exactly what made it stale.
//   - Suffix alone misses `context_consistency_repair`, whose operation segment
//     is likewise absent from the surface.
//
// Either-match keeps both. Most snake_case tokens in the corpus are field
// names, error codes and audit event types; the shape rule cuts them down to
// the allowlist below, which is small enough to read. If that ratio ever stops
// holding, re-derive it rather than trusting this sentence — the counts move
// with every doc edit, which is why none are written here.
func (ts toolShape) looksLikeTool(token string) bool {
	segs := strings.Split(token, "_")
	if _, ok := ts.firstSegs[segs[0]]; ok {
		return true
	}
	_, ok := ts.lastSegs[segs[len(segs)-1]]
	return ok
}

// unresolved returns the hits that look like tools, are not registered, and are
// not allowlisted.
func (ts toolShape) unresolved(hits []tokenHit) []tokenHit {
	var out []tokenHit
	for _, h := range hits {
		if _, ok := ts.registered[h.Token]; ok {
			continue
		}
		if _, ok := nonToolVocabulary[h.Token]; ok {
			continue
		}
		if _, ok := plannedTools[h.Token]; ok {
			continue
		}
		if tokens := retiredToolReferences[h.File]; tokens != nil {
			if _, ok := tokens[h.Token]; ok {
				continue
			}
		}
		if ts.looksLikeTool(h.Token) {
			out = append(out, h)
		}
	}
	return out
}

// ── Tests ──────────────────────────────────────────────────────────────

// TestShippedProseNamesOnlyRegisteredTools is the guard. It fails CI when a
// shipped skill body or doc references a tool name that is not registered.
func TestShippedProseNamesOnlyRegisteredTools(t *testing.T) {
	ts := newToolShape(registeredToolNames(t))
	hits := scanShippedProse(t)
	if len(hits) == 0 {
		t.Fatal("scanned zero tokens across the shipped corpus — the scan globs or the repo root are wrong, not the docs")
	}

	bad := ts.unresolved(hits)
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].File != bad[j].File {
			return bad[i].File < bad[j].File
		}
		return bad[i].Line < bad[j].Line
	})
	for _, h := range bad {
		t.Errorf("%s:%d: %q looks like a Tesseract tool name but no adapter registers it.\n"+
			"    Fix the prose, register the tool, or — if it is not a tool at all — add it to "+
			"nonToolVocabulary in this file with a note saying what it is.",
			h.File, h.Line, h.Token)
	}
}

// TestToolDescriptionsNameOnlyRegisteredTools applies the same rule to the tool
// descriptions agents actually read at the call site. Descriptions cross-refer
// ("prefer `memory_recall` for search"), so they drift on a rename the same way
// prose does.
//
// Scope is the tool description only, not per-parameter descriptions. Parameter
// names are themselves snake_case request fields, so scanning the input schema
// would flag most of them and push the allowlist past the size where anyone
// reads it.
func TestToolDescriptionsNameOnlyRegisteredTools(t *testing.T) {
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("toolname-drift-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)
	ts := newToolShape(registeredToolNames(t))

	for name, tool := range srv.ListTools() {
		for _, h := range ts.unresolved(extractCandidates(name, tool.Tool.Description)) {
			t.Errorf("tool %q description references %q, which no adapter registers", name, h.Token)
		}
	}
}

// TestDriftGuardCatchesUnregisteredToolName proves the guard is not a no-op.
//
// A guard nobody has watched fail is indistinguishable from a guard whose
// scanner silently stopped matching. mustFlag pins the shapes that must go red
// — chiefly the historical defect (`vanta_skills`, shipped in v0.8.0, fixed in
// 8932553) and a wave-3 target name that does not exist yet. mustPass pins the
// exclusions, so tightening the rule cannot quietly start crying wolf.
func TestDriftGuardCatchesUnregisteredToolName(t *testing.T) {
	ts := newToolShape(registeredToolNames(t))

	mustFlag := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "historical vanta_skills reference",
			doc:  "**Reinforcement.** See `vanta_skills recall-and-ranking`.\n",
			want: "vanta_skills",
		},
		{
			// The complement of the vanta_skills case: a live domain segment
			// with an operation segment no tool has. Both halves of
			// looksLikeTool's either-match rule need a fixture, or tightening
			// one half could silently disable the other.
			name: "unregistered operation segment under a live domain segment",
			doc:  "Call `memory_search` to search memory.\n",
			want: "memory_search",
		},
		{
			name: "bare tool name in a heading",
			doc:  "## memory_recal (the shortcut)\n",
			want: "memory_recal",
		},
		{
			name: "sentence-ending period is not a file extension",
			doc:  "Reinforcement comes from vanta_skills. Recall does not count.\n",
			want: "vanta_skills",
		},
		{
			// Bold closes with the same character a wildcard family does.
			// "- **tool_name** — description" is idiomatic markdown for a
			// definition list, and this corpus writes bare bold snake_case in
			// prose — re-derive with a grep for '\*\*[a-z_]*_' under
			// internal/mcpadapter/skills before trusting that sentence.
			name: "bold emphasis does not read as a wildcard family",
			doc:  "- **vanta_skills** — search memory.\n",
			want: "vanta_skills",
		},
		{
			name: "italic emphasis does not read as a wildcard family",
			doc:  "See the *vanta_skills* skill.\n",
			want: "vanta_skills",
		},
		{
			name: "bold inside a heading",
			doc:  "## **vanta_skills** (the shortcut)\n",
			want: "vanta_skills",
		},
		{
			// ".limit" is a field path on a tool, not a file extension. The
			// old length-bounded rule exempted anything 1-5 chars after a dot.
			name: "short field path is not a file extension",
			doc:  "Set `vanta_skills.limit` to 10.\n",
			want: "vanta_skills",
		},
	}
	for _, tc := range mustFlag {
		t.Run(tc.name, func(t *testing.T) {
			bad := ts.unresolved(extractCandidates("fixture.md", tc.doc))
			if len(bad) != 1 || bad[0].Token != tc.want {
				t.Fatalf("guard did not flag %q; got %v", tc.want, bad)
			}
		})
	}

	mustPass := []struct {
		name string
		doc  string
	}{
		{"registered tool", "Call `tesseract_recall` to search memory.\n"},
		{"wildcard family", "Cross-ownership moves go through `context_promote_*`.\n"},
		{"allowlisted field name", "Required: `memory_key`.\n"},
		{"single-word prose", "Deliberate reads reinforce a memory's activation.\n"},
		{"source filename", "Registered in internal/mcpadapter/memory_tools.go.\n"},
		{"bold registered tool", "- **tesseract_recall** — search memory.\n"},
		{"wildcard family in bold", "Moves go through **context_promote_***.\n"},
	}
	for _, tc := range mustPass {
		t.Run(tc.name, func(t *testing.T) {
			if bad := ts.unresolved(extractCandidates("fixture.md", tc.doc)); len(bad) != 0 {
				t.Fatalf("guard false-positived on %q: %v", tc.doc, bad)
			}
		})
	}
}

// TestToolNameAllowlistIsCurrent keeps the allowlist from becoming a place
// where drift hides.
//
// An entry that no longer appears in the corpus is dead weight that would
// silently absorb a future real violation of the same name. An entry that has
// become a registered tool means the surface caught up with the doc — the most
// likely way that happens is the rename this guard exists for, so it must not
// pass quietly.
func TestToolNameAllowlistIsCurrent(t *testing.T) {
	registered := registeredToolNames(t)
	seen := map[string]struct{}{}
	seenByFile := map[string]map[string]struct{}{}
	for _, h := range scanShippedProse(t) {
		seen[h.Token] = struct{}{}
		if seenByFile[h.File] == nil {
			seenByFile[h.File] = map[string]struct{}{}
		}
		seenByFile[h.File][h.Token] = struct{}{}
	}

	entries := map[string]map[string]string{
		"nonToolVocabulary": nonToolVocabulary,
		"plannedTools":      {},
	}
	for token, p := range plannedTools {
		entries["plannedTools"][token] = p.Why
	}

	for _, label := range []string{"nonToolVocabulary", "plannedTools"} {
		for token, why := range entries[label] {
			if _, ok := registered[token]; ok {
				t.Errorf("%s entry %q is now a registered tool — drop it from the allowlist (was: %s)",
					label, token, why)
			}
			if _, ok := seen[token]; !ok {
				t.Errorf("%s entry %q no longer appears in any scanned doc or skill — remove it "+
					"so it cannot mask a future violation (was: %s)", label, token, why)
			}
		}
	}

	for token := range plannedTools {
		if _, ok := nonToolVocabulary[token]; ok {
			t.Errorf("%q is in both allowlist buckets — it is either a tool or it is not", token)
		}
	}

	for file, tokens := range retiredToolReferences {
		if len(tokens) == 0 {
			t.Errorf("retiredToolReferences[%q] is empty — remove the file entry", file)
		}
		for token, why := range tokens {
			if strings.TrimSpace(why) == "" {
				t.Errorf("retiredToolReferences[%q][%q] has no reason", file, token)
			}
			if _, ok := registered[token]; ok {
				t.Errorf("retiredToolReferences[%q][%q] is registered again — it is no longer a retired-only reference", file, token)
			}
			if _, ok := seenByFile[file][token]; !ok {
				t.Errorf("retiredToolReferences[%q][%q] no longer appears in that file — remove the stale exception", file, token)
			}
		}
	}
}

// TestPlannedToolsAreTracked closes the front end of the plannedTools bucket.
//
// The comment on plannedTool demands a tracking ID and a doc reference, but a
// note nobody validates is a note that drifts — a wrong justification reads
// exactly like a right one. This makes each entry prove its claim: the tracking
// ID has to be well-formed, and the cited file and line have to exist and to
// actually contain the token.
//
// The line-number check is deliberately brittle. If the doc is edited and the
// forward declaration moves, this fails and someone re-reads the doc, which is
// the correct outcome — the entry's whole justification is that a specific
// piece of prose says a specific thing.
func TestPlannedToolsAreTracked(t *testing.T) {
	root := repoRoot(t)

	for token, p := range plannedTools {
		if strings.TrimSpace(p.Why) == "" {
			t.Errorf("plannedTools[%q]: Why is empty — say why the doc names an unregistered tool", token)
		}
		if !trackingIDRE.MatchString(p.Tracked) {
			t.Errorf("plannedTools[%q]: Tracked = %q is not a recognized tracking ID (want TASK-/CW-/SPR-YYYYMMDD-...) "+
				"— an entry here must point at tracked work, not just assert it", token, p.Tracked)
			continue
		}

		path, lineNo, ok := strings.Cut(p.Doc, ":")
		if !ok {
			t.Errorf("plannedTools[%q]: Doc = %q is not \"path:line\"", token, p.Doc)
			continue
		}
		n, err := strconv.Atoi(lineNo)
		if err != nil || n < 1 {
			t.Errorf("plannedTools[%q]: Doc = %q has no valid line number", token, p.Doc)
			continue
		}
		// #nosec G304 -- path comes from plannedTools, a compile-time constant in this file.
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Errorf("plannedTools[%q]: Doc names %s, which cannot be read: %v", token, path, err)
			continue
		}
		lines := strings.Split(string(body), "\n")
		if n > len(lines) {
			t.Errorf("plannedTools[%q]: Doc points at %s:%d but the file has %d lines", token, path, n, len(lines))
			continue
		}
		if !strings.Contains(lines[n-1], token) {
			t.Errorf("plannedTools[%q]: Doc points at %s:%d, but that line does not mention %q.\n"+
				"    line reads: %s\n"+
				"    Re-read the doc: either the forward declaration moved (update Doc) or it is gone "+
				"(drop the entry, and the guard will flag any remaining reference).",
				token, path, n, token, strings.TrimSpace(lines[n-1]))
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────

// registeredToolNames introspects the live tool surface the same way
// TestMCPRegistrationMatchesCatalog does.
func registeredToolNames(t *testing.T) map[string]struct{} {
	t.Helper()
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("toolname-drift-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	names := map[string]struct{}{}
	for name := range srv.ListTools() {
		names[name] = struct{}{}
	}
	if len(names) == 0 {
		t.Fatal("registered zero tools — the adapter is not wired, so any clean result here is meaningless")
	}
	return names
}

// scanShippedProse walks scannedRoots for markdown, adds scannedFiles, and
// returns every candidate token found.
func scanShippedProse(t *testing.T) []tokenHit {
	t.Helper()
	root := repoRoot(t)

	var files []string
	for _, r := range scannedRoots {
		dir := filepath.Join(root, r)
		if err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") {
				files = append(files, p)
			}
			return nil
		}); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	for _, f := range scannedFiles {
		p := filepath.Join(root, f)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("scannedFiles entry %q does not exist (%v) — fix the list, do not let it scan nothing", f, err)
		}
		files = append(files, p)
	}
	if len(files) == 0 {
		t.Fatalf("scannedRoots + scannedFiles matched no files under %s", root)
	}
	sort.Strings(files)

	var hits []tokenHit
	for _, f := range files {
		// #nosec G304 -- f comes from filepath.Glob over scannedGlobs, which are
		// compile-time constants rooted at the module directory; no external input.
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = f
		}
		hits = append(hits, extractCandidates(rel, string(body))...)
	}
	return hits
}

// repoRoot resolves the module root from this package's working directory and
// verifies it, so a moved test file fails loudly instead of scanning nothing.
func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %q has no go.mod (%v) — this test's relative path is stale", root, err)
	}
	return root
}

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
// The durable value is on rename events: when a tool ID changes, every doc and
// skill that still names the old ID fails here instead of shipping.
package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// ── Scan scope ─────────────────────────────────────────────────────────

// scannedGlobs are the shipped surfaces whose prose must name only registered
// tools, relative to the repo root.
//
// CHANGELOG.md is deliberately absent. Release notes describe identifier
// changes, so they legitimately name superseded IDs ("the lookup tool is now
// `tesseract_lookup`"). Scanning it would turn this guard red on exactly the
// commit that ships a rename — the moment it most needs to be usable.
var scannedGlobs = []string{
	"internal/mcpadapter/skills/*.md",
	"docs/*.md",
	"docs/*/*.md",
	"docs/*/*/*.md",
	"README.md",
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
	"auto_embed":          "context_write request field (bool): embed on write",
	"bulk_ingest":         "audit event_type emitted by context_bulk_ingest",
	"chunked_ingest":      "audit event_type emitted by context_chunked_ingest",
	"head_revision":       "API response field: id of the current revision",
	"max_tokens_estimate": "context_packet budget field",
	"memory_key":          "memory_write request field: the stable key of a keyed memory",
	"missing_head":        "API error code: namespace/key has no head revision",
	"session_snapshot":    "audit event_type emitted by context_session_snapshot",
	"source_revision":     "promotion API response field",
	"status_deprecate":    "audit event_type emitted by context_status_deprecate",
	"status_promote":      "audit event_type emitted by context_status_promote",
	"target_revision":     "promotion API response field",
	"tokens_estimate":     "context_packet manifest field",
	"typed_write":         "audit event_type emitted by context_typed_write",
}

// plannedTools lists tool IDs a doc names before the tool exists. Each entry
// must carry the tracking ID for the work that registers it. When it lands,
// TestToolNameAllowlistIsCurrent fails and the entry comes out.
//
// This bucket is a pressure valve, not a parking lot: an entry here is a doc
// making a promise the code has not kept.
var plannedTools = map[string]string{
	"context_consistency_repair": "docs/MCP_TOOLS.md names the MCP peer of /v1/context/consistency/repair " +
		"as batch-2 work; surfaceCatalog waives that route as \"MCP peer pending TASK-20260415-010\"",
}

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
// Matching is not restricted to backticked spans. Measured against the tree at
// 0b482c4, backtick-plus-headings and match-everywhere flag the identical set
// of 15 distinct tokens, so restricting to code spans buys no reduction in
// noise while adding a way to be wrong. Headings have to be covered either way:
// internal/mcpadapter/skills/promotion.md heads a section
// "## memory_promote (the shortcut)", with the tool name bare.
//
// Two exclusions, both for tokens that are structurally something else:
//
//   - Followed by "*" or "_*": a wildcard family reference
//     ("context_promote_*") naming a group of tools rather than one.
//   - Followed by a file extension: a source filename
//     ("internal/mcpadapter/memory_tools.go"), which shares the domain segment
//     with the tools it registers and would otherwise flag on every mention.
func extractCandidates(file, content string) []tokenHit {
	var hits []tokenHit
	for i, line := range strings.Split(content, "\n") {
		for _, loc := range toolTokenRE.FindAllStringIndex(line, -1) {
			rest := line[loc[1]:]
			if strings.HasPrefix(rest, "*") || strings.HasPrefix(rest, "_*") {
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

// fileExtRE matches a file extension immediately following a token. Anchored
// and bounded so a sentence-ending period ("call memory_recall.") is not
// mistaken for one.
var fileExtRE = regexp.MustCompile(`^\.[a-z]{1,5}\b`)

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
// Either-match keeps both. On this tree it cuts the 145 distinct non-tool
// snake_case tokens in the scanned corpus down to the 15 allowlist entries
// below.
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
			name: "wave-3 target name referenced before the rename lands",
			doc:  "Call `tesseract_recall` to search memory.\n",
			want: "tesseract_recall",
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
		{"registered tool", "Call `memory_recall` to search memory.\n"},
		{"wildcard family", "Cross-ownership moves go through `context_promote_*`.\n"},
		{"allowlisted field name", "Required: `memory_key`.\n"},
		{"single-word prose", "Deliberate reads reinforce a memory's activation.\n"},
		{"source filename", "Registered in internal/mcpadapter/memory_tools.go.\n"},
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
	for _, h := range scanShippedProse(t) {
		seen[h.Token] = struct{}{}
	}

	for _, bucket := range []struct {
		label   string
		entries map[string]string
	}{
		{"nonToolVocabulary", nonToolVocabulary},
		{"plannedTools", plannedTools},
	} {
		for token, why := range bucket.entries {
			if _, ok := registered[token]; ok {
				t.Errorf("%s entry %q is now a registered tool — drop it from the allowlist (was: %s)",
					bucket.label, token, why)
			}
			if _, ok := seen[token]; !ok {
				t.Errorf("%s entry %q no longer appears in any scanned doc or skill — remove it "+
					"so it cannot mask a future violation (was: %s)", bucket.label, token, why)
			}
		}
	}

	if _, ok := nonToolVocabulary["context_consistency_repair"]; ok {
		t.Error("context_consistency_repair belongs in plannedTools, not nonToolVocabulary — it names a tool")
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

// scanShippedProse reads every file matched by scannedGlobs and returns all
// candidate tokens.
func scanShippedProse(t *testing.T) []tokenHit {
	t.Helper()
	root := repoRoot(t)

	var files []string
	for _, g := range scannedGlobs {
		matches, err := filepath.Glob(filepath.Join(root, g))
		if err != nil {
			t.Fatalf("glob %q: %v", g, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatalf("scannedGlobs matched no files under %s", root)
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

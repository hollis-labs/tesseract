package parity

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/hollis-labs/tesseract/internal/mcpadapter"
)

// Naming-vocabulary conformance for the MCP tool surface.
//
// The vocabulary itself lives in internal/mcpadapter/toolvocab.go as data. This
// file has two jobs, and they are different jobs:
//
//  1. Assert the live surface matches the table. Derived — nothing restated.
//  2. Assert THE TABLE IS RIGHT. Not derivable, by construction: a check that
//     reads the table to decide whether the table is correct agrees with itself
//     no matter what the table says. TestNamingVocabularyMatchesHandStatedNames
//     is the anchor, and every name in it is a literal written out here.
//
// The second job is the point of this file. CW-20260825-0007 shipped a
// consolidation whose test asserted only that a description CONTAINED the
// shared rule — so rewording the rule reworded the assertion with it, and a
// wrong boundary passed. Consolidating removes drift between copies; it does
// nothing about the single copy being wrong.

// ── 1. The surface matches the table ───────────────────────────────────

// TestEveryRegisteredToolMatchesVerbTable is the conformance check AC1 asks
// for: every registered name, checked against the vocabulary predicate rather
// than against a snapshot of today's names.
func TestEveryRegisteredToolMatchesVerbTable(t *testing.T) {
	names := sortedRegisteredToolNames(t)
	if len(names) == 0 {
		t.Fatal("registered zero tools — the adapter is not wired, so a clean result here would be meaningless")
	}
	for _, name := range names {
		if err := mcpadapter.ValidateToolName(name); err != nil {
			t.Errorf("%v", err)
		}
	}
}

// TestToolNameExemptionsAreCurrent keeps the exemption list honest at both
// ends. An exemption for a tool that no longer exists is dead weight; an
// exemption for a name that would pass the rule anyway is a claim of
// non-conformance that is not true, and it hides the fact that the name is fine.
func TestToolNameExemptionsAreCurrent(t *testing.T) {
	registered := registeredToolNames(t)
	for name, why := range mcpadapter.ToolNameExemptions {
		if _, ok := registered[name]; !ok {
			t.Errorf("ToolNameExemptions has %q, which no adapter registers — drop it (was: %s)", name, why)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("ToolNameExemptions[%q] has no reason — an exemption without one is indistinguishable from an oversight", name)
		}
		// CheckToolNameAgainstVocabulary is the same predicate without the
		// exemption short-circuit, so this asks "would it pass anyway?"
		// without restating the rule here.
		if err := mcpadapter.CheckToolNameAgainstVocabulary(name); err == nil {
			t.Errorf("ToolNameExemptions has %q, but that name matches the verb table on its own — remove the exemption (was: %s)", name, why)
		}
	}
}

// ── 2. The table is right ──────────────────────────────────────────────

// TestNamingVocabularyMatchesHandStatedNames is the anchor.
//
// Every name below is written out by hand. None is read from ToolVerbTable,
// ToolPrefixRule, ToolNameExemptions or the live registry, so editing any of
// those cannot edit this assertion along with it. Widening `get` to accept the
// `memory_` prefix, or admitting `head` as a verb, makes this fail — which is
// the whole reason it exists.
//
// mustAccept deliberately mixes names that are registered today with names that
// are NOT, so the list is a statement about the RULE rather than a snapshot of
// the surface.
//
// DO NOT let a bulk rename run over this file. The retired names in mustReject
// are the assertion; rewriting them to their replacements makes every case pass
// and checks nothing. That is not hypothetical — the CW-20260825-0012 rename
// script did exactly that on its first pass and the three names below had to be
// typed back in by hand.
func TestNamingVocabularyMatchesHandStatedNames(t *testing.T) {
	mustAccept := []struct {
		name string
		why  string
	}{
		// Live names.
		{"tesseract_get", "cross-domain fetch-one"},
		{"tesseract_get_revision", "two-segment verb; must beat the shorter `revision` suffix"},
		{"tesseract_history", "cross-domain revision history"},
		{"tesseract_recall", "cross-domain ranked retrieval"},
		{"tesseract_touch", "cross-domain reinforcement"},
		{"tesseract_deprecate", "cross-domain soft-remove"},
		{"memory_write", "per-domain write"},
		{"knowledge_write", "per-domain write"},
		{"context_write", "per-domain write"},
		{"memory_promote", "promote is scoped to memory and context"},
		{"context_typed_write", "subject `typed` between prefix and verb"},
		{"context_registry_list", "subject `registry`"},
		{"context_namespace_register", "subject `namespace`"},
		{"context_status_set", "subject `status`"},

		// Not registered. These are the half that makes the list a rule and
		// not a snapshot: a correct name for a tool that does not exist.
		{"knowledge_typed_write", "write is per-domain, so any domain may carry it with any subject"},
		{"context_ttl_set", "set with a subject the surface has never used"},
		{"context_pin_list", "list with a subject the surface has never used"},
		{"memory_session_promote", "promote with a subject, under an allowed prefix"},
	}

	for _, tc := range mustAccept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			if err := mcpadapter.ValidateToolName(tc.name); err != nil {
				t.Fatalf("%q should be accepted (%s), got: %v", tc.name, tc.why, err)
			}
		})
	}

	mustReject := []struct {
		name string
		why  string
	}{
		// Names this parent actually retired. Each one passing again would mean
		// the vocabulary had drifted back to what it was built to fix.
		{"views_evaluate", "no domain prefix at all — the CW-20260825-0011 rename target"},
		{"context_head", "`head` is not an operation — the pre-CW-20260825-0010 spelling of tesseract_get"},
		{"context_namespace_show", "`show` is not an operation; fetch-one is `get`"},
		{"tesseract_lookup", "`lookup` is not an operation; ranked retrieval is `recall`"},
		{"context_broker", "`broker` names a component, not an operation; the tool plans a fetch"},
		{"context_audit", "a bare noun with no operation segment"},
		{"context_session_snapshot", "`snapshot` was a one-off verb for what is a write"},

		// Prefix-rule violations: the verb exists, the prefix may not carry it.
		{"memory_get", "`get` is cross-domain, so it must be tesseract_get"},
		{"knowledge_get", "`get` is cross-domain"},
		{"memory_recall", "`recall` is cross-domain, so it must be tesseract_recall"},
		{"memory_history", "`history` is cross-domain"},
		{"tesseract_write", "`write` is per-domain; there is no cross-domain write"},
		{"knowledge_promote", "`promote` is scoped to context and memory only"},
		{"tesseract_list", "`list` is a context-domain registry op"},

		// Malformed.
		{"tesseract", "single segment — no verb"},
		{"contextwrite", "no separator, so there is no prefix and no verb"},
		{"plugin_write", "`plugin` is not one of the four prefixes"},
		{"Context_Write", "segments must be lower-case"},
	}

	for _, tc := range mustReject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			if err := mcpadapter.ValidateToolName(tc.name); err == nil {
				t.Fatalf("%q should be rejected (%s), but the vocabulary accepted it", tc.name, tc.why)
			}
		})
	}
}

// ── 3. The doc derives from the same definitions ───────────────────────

const (
	namingBeginMarker = "<!-- BEGIN GENERATED: tool-naming -->"
	namingEndMarker   = "<!-- END GENERATED: tool-naming -->"
)

// updateDocs regenerates the doc blocks this file guards instead of failing on
// them. Run: go test ./tests/parity/ -run TestDocNamingSection -update-docs
//
// This is what makes docs/MCP_TOOLS.md GENERATED rather than merely checked:
// the doc is this program's output, so there is one definition and two
// consumers rather than a Go structure and a hand-kept copy of it.
var updateDocs = flag.Bool("update-docs", false, "rewrite generated blocks in docs/ from their Go source")

// TestDocNamingSectionMatchesVerbTable asserts docs/MCP_TOOLS.md carries the
// rendering of the same structures the conformance check reads — AC1's "one
// definition, both consumers derive from it", made checkable.
func TestDocNamingSectionMatchesVerbTable(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "MCP_TOOLS.md")
	body := readMCPToolsDoc(t)

	start := strings.Index(body, namingBeginMarker)
	end := strings.Index(body, namingEndMarker)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("docs/MCP_TOOLS.md has no %s / %s block — the naming section is not generated from ToolVerbTable",
			namingBeginMarker, namingEndMarker)
	}

	got := strings.TrimSpace(body[start+len(namingBeginMarker) : end])
	want := strings.TrimSpace(mcpadapter.RenderVerbTableMarkdown())
	if got == want {
		return
	}

	if *updateDocs {
		rewritten := body[:start+len(namingBeginMarker)] + "\n" + want + "\n" + body[end:]
		if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
			t.Fatalf("rewrite %s: %v", path, err)
		}
		t.Logf("regenerated the tool-naming block in %s", path)
		return
	}

	t.Errorf("docs/MCP_TOOLS.md naming section is stale.\n--- doc ---\n%s\n--- ToolVerbTable renders ---\n%s\n"+
		"Regenerate with: go test ./tests/parity/ -run TestDocNamingSection -update-docs", got, want)
}

// docCatalogRowRE matches a catalog table row whose first cell is a backticked
// tool name: "| `context_write` | `write` | ... |".
var docCatalogRowRE = regexp.MustCompile("^\\|\\s*`([a-z][a-z0-9_]*)`\\s*\\|")

// TestDocListsExactlyRegisteredTools is AC2, with "consistent" replaced by a
// predicate: SET EQUALITY between the doc's tool catalog and the live registry.
//
// The CW-20260825-0005 drift guard covers one direction — shipped prose naming
// a tool nobody registers. This is the other: a tool registered and missing
// from the catalog, which that guard cannot see because there is no token for
// it to flag.
func TestDocListsExactlyRegisteredTools(t *testing.T) {
	documented := docCatalogNames(t)
	registered := registeredToolNames(t)

	if len(documented) == 0 {
		t.Fatal("extracted zero tool names from the doc's catalog section — the extraction is wrong, not the doc")
	}

	for name := range registered {
		if _, ok := documented[name]; !ok {
			t.Errorf("tool %q is registered but has no row in the docs/MCP_TOOLS.md catalog — add it", name)
		}
	}
	for name := range documented {
		if _, ok := registered[name]; !ok {
			t.Errorf("docs/MCP_TOOLS.md catalog has a row for %q, which no adapter registers — remove it or register the tool", name)
		}
	}
}

// docCatalogNames extracts the first-cell tool names from the tables under
// "## Tool catalog", stopping at the next level-2 heading.
//
// Scope is the catalog section only, on purpose. The doc names tools in
// playbooks and prose too, and set equality against every mention would fail on
// a legitimate cross-reference. The catalog is the part that claims to be the
// list.
func docCatalogNames(t *testing.T) map[string]struct{} {
	t.Helper()
	body := readMCPToolsDoc(t)

	const heading = "## Tool catalog"
	start := strings.Index(body, heading)
	if start < 0 {
		t.Fatalf("docs/MCP_TOOLS.md has no %q heading — this extraction is stale", heading)
	}
	rest := body[start+len(heading):]
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}

	out := map[string]struct{}{}
	for _, line := range strings.Split(rest, "\n") {
		if m := docCatalogRowRE.FindStringSubmatch(line); m != nil {
			out[m[1]] = struct{}{}
		}
	}
	return out
}

func readMCPToolsDoc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "docs", "MCP_TOOLS.md")
	// #nosec G304 -- path is built from a compile-time constant under the module root.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func sortedRegisteredToolNames(t *testing.T) []string {
	t.Helper()
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("toolvocab-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	var names []string
	for name := range srv.ListTools() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

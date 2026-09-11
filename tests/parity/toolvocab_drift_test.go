//go:build drift

// Doc-drift half of the tool-vocabulary suite — REPORT-ONLY (CW-20260911-0050).
//
// These three assert the CONTENT OF MUTABLE ARTIFACTS: a frozen digest over two
// hand-maintained literal lists, and two checks that docs/MCP_TOOLS.md still
// renders what the Go structures say. Per what-a-check-may-assert.md, a document
// is checked at a release gate rather than at every commit — its drift is not a
// build failure, and pinning it taxes exactly the edits you want cheap while
// firing at whoever improved the wording rather than whoever changed behaviour.
//
// The conformance half stayed blocking and is in toolvocab_test.go. The split is
// by what each function asserts, not by file: TestEveryRegisteredToolMatchesVerbTable
// and TestToolNameExemptionsAreCurrent are property-shaped, and
// TestNamingVocabularyMatchesHandStatedNames is a deliberate hand-typed anchor
// pinning a vocabulary somebody chose — the exemption the policy grants, where a
// literal IS the requirement rather than a record of today's debt.
//
// Run with: make drift-report

package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/mcpadapter"
)

// anchorDigest is a sha256 over the NAMES in anchorMustAccept and
// anchorMustReject, sorted, with the accept and reject halves tagged. The `why`
// strings are excluded on purpose: improving one is a legitimate edit and should
// not trip this.
//
// PROVENANCE — required, because a frozen value without one is a guess with a
// checksum: from the outside, an anchor someone verified and an anchor someone
// captured are indistinguishable, permanently.
//
//	All 52 names — 25 accept, 27 reject — were checked BY HAND, one at a time,
//	against ToolVerbTable and ToolPrefixRule as they stand on branch
//	wave3/0012-verb-vocabulary (CW-20260825-0012, branched from 2004638), on
//	2026-08-25, during the review pass that added the seven context-domain
//	verbs. For each accept the verb's Prefixes were read and confirmed to
//	contain the name's prefix; for each reject the specific reason — unknown
//	prefix, unknown verb, or verb-not-scoped-to-this-prefix — was identified
//	and is recorded in that entry's `why`. The digest was then taken from the
//	checked state. It was NOT captured from whatever the lists happened to hold.
//
// WHAT THIS EARNS, stated narrowly, because the obvious justification is the
// wrong one. anchorMustReject is ALREADY self-protecting against a rename this
// vocabulary motivates — the replacement conforms, so the reject case flips to a
// failure, measured above. The digest earns its place elsewhere:
//
//   - anchorMustAccept, where rewriting a name to another conforming name
//     passes silently because both sides of the assertion agree;
//   - any edit to either list that this vocabulary does not motivate — a
//     dropped case, a typo, a "tidy-up" that removes a duplicate-looking entry.
const anchorDigest = "f14bd323cdff2441e595dc753017b2a2115618ea67bc2ef50363da22052ddaca"

// TestAnchorListsAreFrozen fails when either literal list changes.
func TestAnchorListsAreFrozen(t *testing.T) {
	got := digestAnchorLists()
	if got != anchorDigest {
		t.Errorf("the anchor lists changed: digest %s, expected %s.\n"+
			"    If a bulk rename edited this file, REVERT THE LITERALS rather than updating this constant — "+
			"the retired names are the assertion, and a replacement name is one the vocabulary accepts.\n"+
			"    If you deliberately added or removed a case, check the new list by hand against ToolVerbTable, "+
			"then update both the constant and the provenance note above it.", got, anchorDigest)
	}
}

func digestAnchorLists() string {
	var lines []string
	for _, tc := range anchorMustAccept {
		lines = append(lines, "accept:"+tc.name)
	}
	for _, tc := range anchorMustReject {
		lines = append(lines, "reject:"+tc.name)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
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

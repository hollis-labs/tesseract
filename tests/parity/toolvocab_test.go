package parity

import (
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

// THE ANCHOR — anchorMustAccept and anchorMustReject.
//
// Every name below is written out by hand. None is read from ToolVerbTable,
// ToolPrefixRule, ToolNameExemptions or the live registry, so editing any of
// those cannot edit this assertion along with it. Widening `get` to accept the
// `memory_` prefix, or admitting `head` as a verb, makes this fail — which is
// the whole reason it exists.
//
// anchorMustAccept deliberately mixes names that are registered today with
// names that are NOT, so the list is a statement about the RULE rather than a
// snapshot of the surface.
//
// ── How this file behaves under a bulk rename ──────────────────────────
//
// anchorMustReject IS SELF-PROTECTING, and structurally so. A retired name's
// replacement is, by construction, a name the vocabulary ACCEPTS — that is why
// it was chosen as the replacement. So a rename that rewrites an entry there
// turns it into an assertion that a conforming name is rejected, which fails.
// Measured, not assumed: running
//
//	sed -i '' -e 's/context_broker/context_plan/g' \
//	          -e 's/context_audit/context_audit_list/g' \
//	          -e 's/context_session_snapshot/context_session_write/g' \
//	          tests/parity/toolvocab_test.go
//
// over this file produces three failures — reject/context_plan,
// reject/context_audit_list, reject/context_session_write — not a silent pass.
// (CW-20260825-0012's rename script did run over this file, and its author's
// first report claimed the opposite. It was a prediction, never executed. The
// reviewer ran it.)
//
// anchorMustAccept IS NOT PROTECTED that way. Rewriting a name there to another
// live or conforming name passes silently, because both sides of the assertion
// agree. Nor is either list protected against an edit this vocabulary does not
// motivate — a dropped case, a typo, a tidy-up. That is the gap
// TestAnchorListsAreFrozen closes, and both halves of it were measured: with
// one anchorMustAccept name rewritten to another conforming name, the anchor
// test PASSED and TestAnchorListsAreFrozen FAILED.

// anchorName is one hand-written case: a tool name and why the vocabulary must
// take the view of it that it does.
type anchorName struct {
	name string
	why  string
}

var anchorMustAccept = []anchorName{
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

	// The seven context-domain assembly and vector verbs. Without these
	// every one of them was unanchored: widening `pack` or `plan` to all
	// four prefixes left this test PASSING, and the only failure was the
	// doc-rendering check — whose own documented fix, -update-docs, would
	// then have written the wrong scoping into docs/MCP_TOOLS.md and made
	// the suite green. `plan` is the verb this ticket introduced.
	{"context_embed", "embed is context-domain"},
	{"context_estimate", "estimate is context-domain"},
	{"context_ingest", "ingest is context-domain"},
	{"context_pack", "pack is context-domain"},
	{"context_plan", "plan is context-domain — the verb CW-20260825-0012 introduced"},
	{"context_search", "search is context-domain"},
	{"context_view", "view is context-domain"},

	// Not registered. These are the half that makes the list a rule and
	// not a snapshot: a correct name for a tool that does not exist.
	{"knowledge_typed_write", "write is per-domain, so any domain may carry it with any subject"},
	{"context_ttl_set", "set with a subject the surface has never used"},
	{"context_pin_list", "list with a subject the surface has never used"},
	{"memory_session_promote", "promote with a subject, under an allowed prefix"},
}

var anchorMustReject = []anchorName{
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

	// The scoping half of the seven context-domain verbs. These are what
	// make widening any of them to another prefix fail HERE, rather than
	// only in the doc rendering.
	{"memory_embed", "`embed` is context-domain only"},
	{"knowledge_estimate", "`estimate` is context-domain only"},
	{"memory_ingest", "`ingest` is context-domain only"},
	{"tesseract_pack", "`pack` is context-domain only; there is no cross-domain pack"},
	{"tesseract_plan", "`plan` is context-domain only"},
	{"knowledge_search", "`search` is context-domain only"},
	{"tesseract_view", "`view` is context-domain only"},
	{"tesseract_register", "`register` is context-domain only"},
	{"memory_set", "`set` is context-domain only"},

	// Malformed.
	{"tesseract", "single segment — no verb"},
	{"contextwrite", "no separator, so there is no prefix and no verb"},
	{"plugin_write", "`plugin` is not one of the four prefixes"},
	{"Context_Write", "segments must be lower-case"},
}

func TestNamingVocabularyMatchesHandStatedNames(t *testing.T) {
	for _, tc := range anchorMustAccept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			if err := mcpadapter.ValidateToolName(tc.name); err != nil {
				t.Fatalf("%q should be accepted (%s), got: %v", tc.name, tc.why, err)
			}
		})
	}
	for _, tc := range anchorMustReject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			if err := mcpadapter.ValidateToolName(tc.name); err == nil {
				t.Fatalf("%q should be rejected (%s), but the vocabulary accepted it", tc.name, tc.why)
			}
		})
	}
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

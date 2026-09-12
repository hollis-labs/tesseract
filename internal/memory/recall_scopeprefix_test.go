package memory

import (
	"fmt"
	"strings"
	"testing"
)

// TestCrossProjectPrefix_TheCW20260909_0002Probe re-runs the probe that
// established the cross-project recall gap, against the new grammar.
//
// CW-20260909-0002 measured this on the live store and got:
//
//	user/chrispian/project              0 — not a cross-project prefix
//	user/chrispian/project/*/memory     0 — no mid-path wildcard
//	user/chrispian/project/<pid>/memory ✓
//
// The first two returned nothing because `project` was a MID-PATH segment
// under the owner-rooted grammar: nothing could match across projects without
// naming each one. Under the scope-type-rooted grammar `project` is the ROOT,
// so a prefix over it is expressible for the first time.
//
// The assertion is on the SQL, not on a row count. A count that happens to be
// right is how a prefix bug survives a green suite — a clause that matched
// nothing and a clause that matched everything can both return "the number I
// expected" against a small fixture.
func TestCrossProjectPrefix_TheCW20260909_0002Probe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		probe   string
		wantSQL string
		wantArg string
		why     string
	}{
		{
			name:    "every project, all domains",
			probe:   "project/*",
			wantSQL: "(r.namespace LIKE ?)",
			wantArg: "project/%",
			why:     "the cross-project sweep that returned 0 before: project is the root now",
		},
		{
			name:    "one project, all domains",
			probe:   "project/tesseract/*",
			wantSQL: "(r.namespace LIKE ?)",
			wantArg: "project/tesseract/%",
			why:     "one project's memory AND knowledge AND events, which had no spelling at all before",
		},
		{
			name:    "one project's memory, bare form",
			probe:   "project/tesseract/memory",
			wantSQL: "(r.namespace LIKE ?)",
			wantArg: "project/tesseract/memory/%",
			why:     "grandfathered bare-form inference still covers /memory",
		},
		{
			name:    "one project's knowledge needs the explicit form",
			probe:   "project/tesseract/knowledge/*",
			wantSQL: "(r.namespace LIKE ?)",
			wantArg: "project/tesseract/knowledge/%",
			why:     "knowledge gets no bare-form inference — see scopedPrefix",
		},
		{
			name:    "a fully typed namespace stays exact",
			probe:   "project/tesseract/memory/decisions",
			wantSQL: "(r.namespace = ?)",
			wantArg: "project/tesseract/memory/decisions",
			why:     "the leaf is a namespace, not a prefix",
		},
		{
			// The whole reason inference was not extended. This namespace
			// holds a canonical record; reading it as a prefix would return
			// the 74 namespaces beneath it instead of the one record in it.
			name:    "a bare knowledge head stays EXACT",
			probe:   "user/chrispian/knowledge",
			wantSQL: "(r.namespace = ?)",
			wantArg: "user/chrispian/knowledge",
			why:     "user/chrispian/knowledge holds a real record; inference would widen the read",
		},
		{
			// 64 records across 9 namespaces sit at exactly two segments.
			name:    "a bare scope head stays EXACT",
			probe:   "app/mentat",
			wantSQL: "(r.namespace = ?)",
			wantArg: "app/mentat",
			why:     "app/mentat holds 35 records; inference would make every one unreachable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := buildNamespaceClause([]string{tc.probe})
			if sql != tc.wantSQL {
				t.Errorf("SQL for %q = %q, want %q (%s)", tc.probe, sql, tc.wantSQL, tc.why)
			}
			if len(args) != 1 {
				t.Fatalf("args for %q = %v, want exactly one", tc.probe, args)
			}
			if got, _ := args[0].(string); got != tc.wantArg {
				t.Errorf("bind arg for %q = %q, want %q (%s)", tc.probe, got, tc.wantArg, tc.why)
			}
		})
	}
}

// TestScopePrefix_ExplicitWildcardWorksAtEveryTier states the rule an agent
// learns: `/*` sweeps, everywhere, at any depth.
//
// Before CW-20260912-0078 the wildcard was only recognized directly after a
// `/memory` or `/event` segment; anywhere else it was matched as a literal
// asterisk and quietly returned nothing.
func TestScopePrefix_ExplicitWildcardWorksAtEveryTier(t *testing.T) {
	for _, probe := range []string{
		"project/*",
		"project/tether/*",
		"project/tether/knowledge/*",
		"project/tether/knowledge/adr/2026/*",
		"user/chrispian/memory/*",
		"system/*",
		"org/hollis-labs/*",
		"session/s-1/event/*",
	} {
		t.Run(probe, func(t *testing.T) {
			pfx, ok := scopedPrefix(probe)
			if !ok {
				t.Fatalf("scopedPrefix(%q) did not recognize an explicit wildcard", probe)
			}
			if want := strings.TrimSuffix(probe, "/*"); pfx != want {
				t.Errorf("scopedPrefix(%q) = %q, want %q", probe, pfx, want)
			}
		})
	}
}

// TestScopePrefix_InferenceIsNotExtended is the negative half, and it is the
// one that must fail if someone "simplifies" scopedPrefix into a uniform rule.
//
// Each of these is a real shape in the live store. Reading any of them as a
// prefix silently widens a result set, which is the failure AGENTS.md cites as
// the reason a retired recall filter is refused rather than ignored.
func TestScopePrefix_InferenceIsNotExtended(t *testing.T) {
	for _, exact := range []string{
		"user/chrispian/knowledge",         // holds a canonical record
		"project/tether/knowledge",         // the knowledge head
		"system/knowledge",                 // the singleton's knowledge head
		"app/mentat",                       // 35 records
		"app/cortex",                       // 3 records
		"user/chrispian",                   // a bare scope head
		"project/tether",                   // a bare scope head
		"user/chrispian/knowledge/handoff", // an ordinary knowledge namespace
	} {
		t.Run(exact, func(t *testing.T) {
			if pfx, ok := scopedPrefix(exact); ok {
				t.Errorf("scopedPrefix(%q) inferred a prefix %q.\n"+
					"    Bare-form inference is grandfathered to /memory and /event ONLY. "+
					"Extending it reinterprets namespaces that hold records today.", exact, pfx)
			}
		})
	}
}

// TestScopePrefix_GrandfatheredInferenceStillClaimsUserMemory records a sharp
// edge this slice found and deliberately did NOT fix.
//
// `user/memory` is a real namespace holding 10 records. It ends in `/memory`,
// so the grandfathered bare-form rule reads it as a prefix — meaning those 10
// records are not reachable by an exact namespace recall.
//
// That is PRE-EXISTING, not a regression: the rule before CW-20260912-0078 was
// `strings.HasSuffix(ns, "/memory")` and claimed this namespace exactly the
// same way. Withdrawing inference from `/memory` would fix it and break every
// caller using the documented shorthand, which is a worse trade for one
// namespace.
//
// Under the new grammar the shape is also actively misleading: it parses as
// "user scope, id `memory`" — a user called `memory`. N6 should treat it as a
// RENAME rather than a move. Pinned here so the behavior is a known
// consequence rather than a surprise, and so that fixing it in N6 has a test
// that says what changed.
func TestScopePrefix_GrandfatheredInferenceStillClaimsUserMemory(t *testing.T) {
	pfx, ok := scopedPrefix("user/memory")
	if !ok {
		t.Fatal("user/memory is no longer read as a prefix — if that was deliberate, " +
			"N6's rename landed and this test should record the new behavior")
	}
	if pfx != "user/memory" {
		t.Errorf("prefix = %q, want %q", pfx, "user/memory")
	}
}

// TestBuildNamespaceClause_DepthAgainstTheRealRegistry re-measures the
// SQLITE_MAX_EXPR_DEPTH ceiling against a realistic worst case, as
// CW-20260912-0078 asks.
//
// The concern was that six scope types produce more prefix terms than two did.
// They do — but term COUNT is not what the ceiling is about. Exact matches
// collapse into one IN list regardless of count, and the prefix terms are OR'd
// as a balanced tree, so parse depth grows as log2(n) rather than n. Going
// from two scopes to six multiplies n; log2 absorbs it.
//
// The worst case for depth is therefore every namespace being a PREFIX (no IN
// collapse to hide behind), at the real registry's order of magnitude: 1,152
// registered namespaces, of which 1,013 match `*/memory` alone (measured
// 2026-09-12 via context_registry_list). This runs 2,000 to leave headroom.
func TestBuildNamespaceClause_DepthAgainstTheRealRegistry(t *testing.T) {
	const (
		count    = 2000
		maxDepth = 1000 // SQLITE_MAX_EXPR_DEPTH
	)

	namespaces := make([]string, 0, count)
	for i := 0; i < count; i++ {
		// Every one a prefix, so nothing collapses into the IN list.
		namespaces = append(namespaces, fmt.Sprintf("project/p%d/memory", i))
	}

	sql, args := buildNamespaceClause(namespaces)
	if len(args) != count {
		t.Fatalf("bind args = %d, want %d", len(args), count)
	}

	depth := maxParenDepth(sql)
	if depth >= maxDepth {
		t.Fatalf("expression depth %d at %d prefix terms is at or past SQLITE_MAX_EXPR_DEPTH (%d)",
			depth, count, maxDepth)
	}
	// log2(2000) ≈ 11, so anything near the term count means the balanced tree
	// stopped balancing and we are back to a flat OR chain.
	if depth > 32 {
		t.Errorf("expression depth %d at %d terms is far above log2(n) ≈ 11 — "+
			"the OR tree is no longer balanced", depth, count)
	}
	t.Logf("%d prefix terms → parse depth %d (ceiling %d)", count, depth, maxDepth)

	// A mixed load is the ordinary shape: exact namespaces collapse, so the
	// term count the tree sees is the prefix count plus one.
	mixed := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if i%2 == 0 {
			mixed = append(mixed, fmt.Sprintf("project/p%d/memory/notes", i))
		} else {
			mixed = append(mixed, fmt.Sprintf("project/p%d/memory", i))
		}
	}
	mixedSQL, _ := buildNamespaceClause(mixed)
	t.Logf("%d mixed terms → parse depth %d", count, maxParenDepth(mixedSQL))
}

// maxParenDepth returns the deepest parenthesis nesting in a SQL fragment,
// which is the quantity SQLITE_MAX_EXPR_DEPTH bounds.
func maxParenDepth(sql string) int {
	depth, deepest := 0, 0
	for _, r := range sql {
		switch r {
		case '(':
			depth++
			if depth > deepest {
				deepest = depth
			}
		case ')':
			depth--
		}
	}
	return deepest
}

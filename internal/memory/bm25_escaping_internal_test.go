package memory

// FTS5 MATCH escaping, proved rather than asserted.
//
// search_mode=lexical is a NEW entry point at which user input reaches the
// MATCH parser, so "the sanitizer already handles it" is a claim that has to be
// re-earned rather than inherited. These tests feed both query builders every
// character and keyword the MATCH grammar treats as syntax and check three
// things at once: the expression built, that SQLite accepts it, and that it
// answers the question the caller asked instead of a different one.
//
// The unsanitized control is the load-bearing half. A test that only ran the
// sanitized form would pass just as happily against a sanitizer that did
// nothing, on any input SQLite happens to tolerate.

import (
	"context"
	"strings"
	"testing"
)

// ftsMetacharacterQueries are the inputs a caller can type that the MATCH
// grammar would otherwise read as syntax.
var ftsMetacharacterQueries = []struct {
	name string
	// raw is what a caller types.
	raw string
	// terms is sanitizeBM25Query's output (hybrid: one phrase per token).
	terms string
	// phrase is sanitizeBM25Phrase's output (lexical: one phrase per
	// whitespace-separated run).
	phrase string
}{
	{"ticket id (hyphens are the NOT-ish operator and the acceptance case)",
		"CW-20260519-0032", `"CW" "20260519" "0032"`, `"CW 20260519 0032"`},
	{"double quote (closes a phrase)",
		`say "hello" now`, `"say" "hello" "now"`, `"say" "hello" "now"`},
	{"prefix star",
		"memor*", `"memor"`, `"memor"`},
	{"column filter colon",
		"payload_body:memory", `"payload_body" "memory"`, `"payload_body memory"`},
	{"initial-token caret",
		"^memory", `"memory"`, `"memory"`},
	{"parentheses (grouping)",
		"(memory OR recall)", `"memory" "OR" "recall"`, `"memory" "OR" "recall"`},
	{"bare AND",
		"AND memory", `"AND" "memory"`, `"AND" "memory"`},
	{"bare OR",
		"memory OR recall", `"memory" "OR" "recall"`, `"memory" "OR" "recall"`},
	{"bare NOT",
		"memory NOT recall", `"memory" "NOT" "recall"`, `"memory" "NOT" "recall"`},
	{"bare NEAR",
		"NEAR memory", `"NEAR" "memory"`, `"NEAR" "memory"`},
	{"NEAR with its parenthesis",
		"NEAR(memory recall, 3)", `"NEAR" "memory" "recall" "3"`, `"NEAR memory" "recall" "3"`},
	{"path-like symbol",
		"internal/memory/bm25.go", `"internal" "memory" "bm25" "go"`, `"internal memory bm25 go"`},
	{"symbol with underscores survives whole",
		"fetchBM25Candidates_v2", `"fetchBM25Candidates_v2"`, `"fetchBM25Candidates_v2"`},
	{"SQL-ish injection attempt",
		`x" OR 1=1 --`, `"x" "OR" "1" "1"`, `"x" "OR" "1 1"`},
	{"only punctuation",
		"--*^:()", "", ""},
	{"empty",
		"", "", ""},
}

// The two builders must produce exactly these expressions. Pinning the strings
// is what makes the SQLite half below a check on the grammar rather than on
// whatever the builders happened to emit that day.
func TestBM25Builders_EscapeEveryMatchMetacharacter(t *testing.T) {
	for _, tc := range ftsMetacharacterQueries {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeBM25Query(tc.raw); got != tc.terms {
				t.Errorf("sanitizeBM25Query(%q) = %q, want %q", tc.raw, got, tc.terms)
			}
			if got := sanitizeBM25Phrase(tc.raw); got != tc.phrase {
				t.Errorf("sanitizeBM25Phrase(%q) = %q, want %q", tc.raw, got, tc.phrase)
			}
			// Nothing either builder emits may contain a character the MATCH
			// grammar reads as syntax outside a phrase. Quotes are allowed
			// because they are the phrase delimiters; they must balance.
			for _, s := range []string{sanitizeBM25Query(tc.raw), sanitizeBM25Phrase(tc.raw)} {
				if n := strings.Count(s, `"`); n%2 != 0 {
					t.Errorf("unbalanced quotes in %q — a phrase is left open", s)
				}
				if i := strings.IndexAny(s, "*^:()-,.'"); i >= 0 {
					t.Errorf("metacharacter %q survived into %q at %d", s[i], s, i)
				}
			}
		})
	}
}

// Every builder output must be a legal MATCH expression, and the unsanitized
// form must be shown to be worse — otherwise this proves nothing about the
// sanitizer.
func TestBM25Builders_SQLiteAcceptsEveryEscapedForm(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, bm25SampleInput("esc.a",
		"ticket CW-20260519-0032 and internal/memory/bm25.go",
		"say \"hello\" now; memory recall payload_body fetchBM25Candidates_v2 NEAR")); err != nil {
		t.Fatalf("write: %v", err)
	}

	matchCount := func(expr string) (int, error) {
		var n int
		err := ms.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM memory_revisions_fts WHERE memory_revisions_fts MATCH ?`,
			expr).Scan(&n)
		return n, err
	}

	var rawErrors, rawWrongSemantics int
	for _, tc := range ftsMetacharacterQueries {
		t.Run(tc.name, func(t *testing.T) {
			for label, expr := range map[string]string{
				"terms":  tc.terms,
				"phrase": tc.phrase,
			} {
				if expr == "" {
					continue // fetchBM25Candidates short-circuits before MATCH
				}
				if _, err := matchCount(expr); err != nil {
					t.Errorf("%s form %q was rejected by SQLite: %v", label, expr, err)
				}
			}

			// The control. Handing the raw query straight to MATCH is what the
			// builders exist to prevent; count how often it errors outright or
			// silently answers a different question than the AND-of-tokens the
			// caller meant.
			if strings.TrimSpace(tc.raw) == "" {
				return
			}
			rawN, rawErr := matchCount(tc.raw)
			if rawErr != nil {
				rawErrors++
				return
			}
			if tc.terms == "" {
				return
			}
			safeN, err := matchCount(tc.terms)
			if err != nil {
				t.Fatalf("sanitized form %q errored: %v", tc.terms, err)
			}
			if rawN != safeN {
				rawWrongSemantics++
			}
		})
	}

	// If the raw form never misbehaved, this fixture cannot express the bug
	// and the test above is decoration.
	if rawErrors == 0 {
		t.Error("no raw query was rejected by SQLite — the fixture cannot show that escaping is load-bearing")
	}
	if rawWrongSemantics == 0 {
		t.Error("no raw query silently changed meaning — the fixture cannot show that operator neutralization matters")
	}
	t.Logf("unsanitized control: %d/%d queries were SQLite syntax errors, %d/%d silently matched a different row set",
		rawErrors, len(ftsMetacharacterQueries), rawWrongSemantics, len(ftsMetacharacterQueries))
}

// Per-token quoting must not change what the hybrid arm retrieves or how it is
// ordered, or this lane silently re-ranked every existing relevance recall.
//
// The bare form is spelled out here rather than referenced, because the point
// is to compare against the expression the builder USED to emit; once the
// builder quotes, nothing else in the repo can produce the control.
func TestSanitizeBM25Query_PerTokenQuotingIsRankingNeutral(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Enough overlapping rows that bm25 has real ordering work to do; if the
	// two forms scored differently, the sequences would diverge.
	seed := []struct{ key, summary, body string }{
		{"n.a", "memory recall ranking notes", "recall recall recall ranking"},
		{"n.b", "ranking memory", "memory memory memory recall ranking ranking"},
		{"n.c", "recall ranking memory index", "short"},
		{"n.d", "memory", "recall ranking memory " + strings.Repeat("filler ", 200)},
		{"n.e", "ranking recall memory ticket CW-20260519-0032", "exact identifier row"},
		{"n.f", "CW notes", "20260519 elsewhere and 0032 elsewhere too; memory recall ranking"},
	}
	for _, s := range seed {
		if _, err := ms.WriteRevision(ctx, bm25SampleInput(s.key, s.summary, s.body)); err != nil {
			t.Fatalf("write %s: %v", s.key, err)
		}
	}

	ordered := func(expr string) []string {
		rows, err := ms.db.QueryContext(ctx,
			`SELECT r.revision_id FROM memory_revisions_fts fts
			 INNER JOIN memory_revisions r ON r.rowid = fts.rowid
			 WHERE memory_revisions_fts MATCH ?
			 ORDER BY bm25(memory_revisions_fts), r.revision_id`, expr)
		if err != nil {
			t.Fatalf("query %q: %v", expr, err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, id)
		}
		return out
	}

	for _, raw := range []string{"memory recall ranking", "CW-20260519-0032", "memory"} {
		t.Run(raw, func(t *testing.T) {
			// The pre-quoting expression: bare tokens joined by spaces.
			var bare []string
			for _, group := range bm25Tokenize(raw) {
				bare = append(bare, group...)
			}
			bareExpr := strings.Join(bare, " ")

			got := ordered(sanitizeBM25Query(raw))
			want := ordered(bareExpr)
			if len(want) == 0 {
				t.Fatalf("control expression %q matched nothing — the fixture cannot detect a difference", bareExpr)
			}
			if len(got) != len(want) {
				t.Fatalf("quoted form returned %d rows, bare form %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("position %d: quoted=%s bare=%s — per-token quoting reordered the hybrid arm",
						i, got[i], want[i])
				}
			}
		})
	}
}

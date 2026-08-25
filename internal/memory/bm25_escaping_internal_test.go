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
	{"parentheses dropped, operator kept",
		"(memory OR recall)", `"memory" OR "recall"`, `"memory" "OR" "recall"`},
	{"leading AND — no left operand, so a literal term",
		"AND memory", `"AND" "memory"`, `"AND" "memory"`},
	{"trailing AND — no right operand, so a literal term",
		"memory AND", `"memory" "AND"`, `"memory" "AND"`},
	{"doubled operator — the second has no left operand",
		"memory AND OR recall", `"memory" AND "OR" "recall"`, `"memory" "AND" "OR" "recall"`},
	{"infix OR is preserved under hybrid, literal under lexical",
		"memory OR recall", `"memory" OR "recall"`, `"memory" "OR" "recall"`},
	{"infix NOT is preserved under hybrid, literal under lexical",
		"memory NOT recall", `"memory" NOT "recall"`, `"memory" "NOT" "recall"`},
	{"lowercase and is an ordinary token, not a keyword",
		"and memory", `"and" "memory"`, `"and" "memory"`},
	{"bare NEAR is never an operator (only NEAR( is)",
		"NEAR memory", `"NEAR" "memory"`, `"NEAR" "memory"`},
	{"NEAR with its parenthesis",
		"NEAR(memory recall, 3)", `"NEAR" "memory" "recall" "3"`, `"NEAR memory" "recall" "3"`},
	{"path-like symbol",
		"internal/memory/bm25.go", `"internal" "memory" "bm25" "go"`, `"internal memory bm25 go"`},
	{"symbol with underscores survives whole",
		"fetchBM25Candidates_v2", `"fetchBM25Candidates_v2"`, `"fetchBM25Candidates_v2"`},
	{"SQL-ish injection attempt",
		`x" OR 1=1 --`, `"x" OR "1" "1"`, `"x" "OR" "1 1"`},
	{"only punctuation",
		"--*^:()", "", ""},
	{"empty",
		"", "", ""},
}

// assertWellShaped checks the STRUCTURE of an emitted expression, independent
// of any particular input: every phrase is closed, every phrase body is
// [A-Za-z0-9_ ] only, and the only bare words outside quotes are the three
// operator keywords in a legal infix position.
//
// This replaced a blanket "no metacharacter may appear anywhere" check, which
// was correct only while the builder quoted everything. Loosening it to allow
// bare operators would have been the easy edit; the point of a shape check is
// that it still pins something once the shape changes, so it now says exactly
// which bare words are allowed and where.
func assertWellShaped(t *testing.T, expr string) {
	t.Helper()
	if expr == "" {
		return
	}
	rest := expr
	expectTerm := true
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, `"`):
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				t.Errorf("unbalanced quotes in %q — a phrase is left open", expr)
				return
			}
			body := rest[1 : 1+end]
			for _, r := range body {
				ok := r == ' ' || r == '_' ||
					(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
				if !ok {
					t.Errorf("phrase body %q in %q carries %q, which bm25Tokenize should have stripped",
						body, expr, r)
				}
			}
			rest = rest[1+end+1:]
			expectTerm = false
		default:
			word := rest
			if i := strings.Index(rest, " "); i >= 0 {
				word = rest[:i]
			}
			if !fts5Operators[word] {
				t.Errorf("bare word %q outside quotes in %q — only AND/OR/NOT may be emitted bare",
					word, expr)
				return
			}
			if expectTerm {
				t.Errorf("operator %q in operand position in %q — this is the fts5 syntax error "+
					"the builder exists to avoid", word, expr)
			}
			rest = rest[len(word):]
			expectTerm = true
		}
		rest = strings.TrimPrefix(rest, " ")
	}
	if expectTerm {
		t.Errorf("%q ends on an operator with no right operand — fts5 rejects that", expr)
	}
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
			for _, s := range []string{sanitizeBM25Query(tc.raw), sanitizeBM25Phrase(tc.raw)} {
				assertWellShaped(t, s)
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

// bareExprOf reproduces the expression sanitizeBM25Query emitted BEFORE this
// lane: bare tokens joined by spaces. It is spelled out here because once the
// builder quotes, nothing else in the repo can produce the control.
func bareExprOf(raw string) string {
	var bare []string
	for _, group := range bm25Tokenize(raw) {
		bare = append(bare, group...)
	}
	return strings.Join(bare, " ")
}

// quoteEveryToken is the REJECTED implementation: quote every token, including
// operator keywords. It lives in the test rather than in the package because
// its only remaining job is to be the thing the guard below must be able to
// SEE. A neutrality test that cannot distinguish it from the real builder is
// decoration, and this one could not: its first table held only
// {"memory recall ranking", "CW-20260519-0032", "memory"} — no operator word —
// so it passed against code that re-ranked the hybrid arm from position 6 down
// and dropped a revision out of the top 10.
func quoteEveryToken(raw string) string {
	var phrases []string
	for _, group := range bm25Tokenize(raw) {
		for _, tok := range group {
			phrases = append(phrases, quotePhrase([]string{tok}))
		}
	}
	return strings.Join(phrases, " ")
}

// neutralityQueries are queries that PARSE on main today, so the bare form is
// a computable control and the builder must reproduce it exactly.
//
// operatorBearing marks the ones whose whole point is the operator. For those
// the test additionally asserts the fixture can tell quoteEveryToken apart
// from the control — otherwise the guard is blind in exactly the case it
// exists for.
var neutralityQueries = []struct {
	raw             string
	operatorBearing bool
}{
	{"memory recall ranking", false},
	{"CW-20260519-0032", false},
	{"memory", false},
	{"memory OR recall", true},
	{"memory AND recall", true},
	{"memory NOT recall", true},
	{"memory AND recall OR ranking", true},
	{"and memory", false}, // lowercase: an ordinary token, not a keyword
}

// crashQueries are queries that do NOT parse on main: an operator keyword with
// no operand on one side. The builder must turn each into a legal expression,
// which is the crash fix, and that cannot be stated as neutrality because
// there is no control to be neutral against.
var crashQueries = []string{
	"AND memory",
	"memory AND",
	"NOT NULL constraint",
	"memory AND OR recall",
	"OR",
}

func seedNeutralityCorpus(t *testing.T, ms *Store) {
	t.Helper()
	// Row membership is chosen so the three operators separate: rows carrying
	// memory-without-recall (OR widens), rows carrying both without the literal
	// word "and" (quoting AND narrows), and one row carrying the operator words
	// themselves (so the rejected form returns a non-empty WRONG set rather
	// than an empty one — a difference of ordering, not just of emptiness).
	seed := []struct{ key, summary, body string }{
		{"n.a", "memory ranking notes", "recall of the ranking"},
		{"n.b", "ranking memory", "memory memory recall ranking ranking"},
		{"n.c", "recall ranking index", "brief"},
		{"n.d", "memory", strings.Repeat("filler ", 200)},
		{"n.e", "recall", "just this one"},
		{"n.f", "ticket CW-20260519-0032 exact", "identifier row"},
		{"n.g", "CW notes", "20260519 elsewhere; 0032 too; memory recall ranking"},
		{"n.h", "boolean or and not keywords", "memory recall ranking discussed"},
	}
	for _, s := range seed {
		if _, err := ms.WriteRevision(context.Background(),
			bm25SampleInput(s.key, s.summary, s.body)); err != nil {
			t.Fatalf("write %s: %v", s.key, err)
		}
	}
}

// The hybrid builder must not change what the arm retrieves or how it is
// ordered, for any query that parses today — operator-bearing ones included.
func TestSanitizeBM25Query_IsRankingNeutralIncludingOperators(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	seedNeutralityCorpus(t, ms)
	ordered := orderedMatcher(t, ms)

	sawOperatorDifference := 0
	for _, tc := range neutralityQueries {
		t.Run(tc.raw, func(t *testing.T) {
			control := bareExprOf(tc.raw)
			want, err := ordered(control)
			if err != nil {
				t.Fatalf("control %q does not parse; it belongs in crashQueries: %v", control, err)
			}
			if len(want) == 0 {
				t.Fatalf("control %q matched nothing — the fixture cannot detect a difference", control)
			}

			got, err := ordered(sanitizeBM25Query(tc.raw))
			if err != nil {
				t.Fatalf("builder emitted %q, which SQLite rejected: %v", sanitizeBM25Query(tc.raw), err)
			}
			if !sameIDs(got, want) {
				t.Errorf("hybrid arm moved.\n  query   %q\n  control %q -> %v\n  builder %q -> %v",
					tc.raw, control, want, sanitizeBM25Query(tc.raw), got)
			}

			if !tc.operatorBearing {
				return
			}
			// Anti-vacuity: the rejected implementation must be visibly wrong
			// here, or this subtest proves nothing about operator handling.
			rejected, err := ordered(quoteEveryToken(tc.raw))
			if err != nil {
				t.Fatalf("quoteEveryToken(%q) did not parse: %v", tc.raw, err)
			}
			if sameIDs(rejected, want) {
				t.Errorf("the fixture cannot tell quote-every-token apart from the control for %q "+
					"(both %v) — this guard is blind to the defect it is named for; fix the corpus",
					tc.raw, want)
				return
			}
			sawOperatorDifference++
			t.Logf("control %q -> %d rows | quote-every-token %q -> %d rows",
				control, len(want), quoteEveryToken(tc.raw), len(rejected))
		})
	}
	if sawOperatorDifference == 0 {
		t.Error("no operator query showed a difference against quote-every-token — " +
			"the table or the corpus no longer exercises operator handling at all")
	}
}

// The crash fix, stated as what it is: expressions that main cannot parse
// become expressions SQLite accepts. Neutrality is not the claim here, so it is
// not asserted here.
func TestSanitizeBM25Query_TurnsSyntaxErrorsIntoAnswers(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	seedNeutralityCorpus(t, ms)
	ordered := orderedMatcher(t, ms)

	for _, raw := range crashQueries {
		t.Run(raw, func(t *testing.T) {
			// If main could already answer this, it is not a crash query and
			// belongs in the neutrality table where the stronger claim applies.
			if _, err := ordered(bareExprOf(raw)); err == nil {
				t.Fatalf("control %q parses on its own; move this case to neutralityQueries "+
					"so it is held to exact neutrality instead", bareExprOf(raw))
			}
			expr := sanitizeBM25Query(raw)
			if _, err := ordered(expr); err != nil {
				t.Errorf("builder emitted %q, still rejected by SQLite: %v", expr, err)
			}
		})
	}
}

// orderedMatcher returns a function that runs one MATCH expression through the
// same ORDER BY fetchBM25Candidates uses and returns the ordered revision ids.
// It returns the SQLite error rather than failing, because several tests above
// assert that a particular expression IS rejected.
func orderedMatcher(t *testing.T, ms *Store) func(string) ([]string, error) {
	t.Helper()
	return func(expr string) ([]string, error) {
		rows, err := ms.db.QueryContext(context.Background(),
			`SELECT r.revision_id FROM memory_revisions_fts fts
			 INNER JOIN memory_revisions r ON r.rowid = fts.rowid
			 WHERE memory_revisions_fts MATCH ?
			 ORDER BY bm25(memory_revisions_fts), r.revision_id`, expr)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				return nil, scanErr
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

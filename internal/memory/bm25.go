package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// bm25CandidateDefault is the default top-N fetched for the BM25 arm of
// hybrid relevance recall. The epic guidance is N≈50–100; 100 gives RRF
// enough candidates to fuse against the cosine arm without over-fetching.
const bm25CandidateDefault = 100

// bm25IndexColumns is the column list of memory_revisions_fts, in declaration
// order. It exists so the weight vector below can be checked against the real
// index rather than against a comment (see TestBM25ColumnWeightsMatchIndexOrder).
var bm25IndexColumns = []string{"memory_key", "payload_summary", "payload_body", "tags"}

// bm25MemoryKeyWeight is the bm25() column weight on memory_key. Every other
// column stays at 1.0, the default.
//
// The weight is here because a hit on memory_key is not the same KIND of
// evidence as a hit in prose. A key is what a record IS; a body mention is a
// citation of some other record. Since records in this corpus cite each other
// by key in [[wikilink]] form, an unweighted index puts the citations above
// the referent for exactly the query -- the exact key -- where the referent is
// certainly what was wanted.
//
// 10.0 is measured, not chosen for roundness. Over all 1,383 distinct current
// memory_keys on the live corpus (1,770 revisions), each used verbatim as a
// search_mode=lexical query, the share where the key's OWN record ranks first:
//
//	weight   1.0    2.0    3.0    5.0   10.0   20.0   50.0
//	owner#1  1103   1294   1343   1360   1368   1360   1355
//
// It is a genuine peak, and the decline past it is BM25 doing what BM25 does:
// weight multiplies term frequency, term frequency saturates, and once every
// key hit is pinned at the ceiling the ranking can no longer tell the record
// whose key IS the query from one whose key merely CONTAINS it, so the order
// falls back to length normalization. Cranking it higher is not "more exact".
//
// What it does NOT do is disturb ordinary prose recall, and that is measured
// too rather than assumed. bm25() weights scale a per-column term frequency,
// so a column contributing zero occurrences contributes zero however it is
// weighted: across 3,904 prose queries (2-, 3- and 5-word prefixes of real
// payload summaries), the 35 with no query term appearing in ANY memory_key
// had their ordering changed by this weight in 0 cases. Where it does reorder,
// a record whose key matches the query moved up, which is the entire point.
//
// It applies to the hybrid arm as well, not only search_mode=lexical: the
// claim "a key hit is stronger evidence" is not specific to a mode, and RRF
// consumes this arm's rank order, so a split would make hybrid rank the key's
// owner below records citing it for no reason anyone could state.
const bm25MemoryKeyWeight = 10.0

// bm25RankExpr is the ORDER BY expression for the FTS5 arm: bm25() with the
// column weights above, positionally aligned to bm25IndexColumns.
//
// Built by concatenation rather than bound as parameters because SQLite does
// not accept bind parameters for bm25()'s weight arguments; the inputs are Go
// constants and never caller data, so nothing here is reachable from a query
// string.
var bm25RankExpr = buildBM25RankExpr()

func buildBM25RankExpr() string {
	var b strings.Builder
	b.WriteString("bm25(memory_revisions_fts")
	for _, col := range bm25IndexColumns {
		w := 1.0
		if col == "memory_key" {
			w = bm25MemoryKeyWeight
		}
		fmt.Fprintf(&b, ", %g", w)
	}
	b.WriteString(")")
	return b.String()
}

// buildRecallFilters returns the WHERE fragments and bind args shared by
// fetchCandidates (dense/metadata path) and fetchBM25Candidates (FTS5
// path). Fragments reference memory_revisions aliased as r; callers
// provide the FROM/JOIN and final ordering.
func buildRecallFilters(in RecallInput) ([]string, []interface{}) {
	var where []string
	var args []interface{}

	nsFrag, nsArgs := buildNamespaceClause(in.Namespaces)
	where = append(where, nsFrag)
	args = append(args, nsArgs...)

	if len(in.Filters.Statuses) > 0 {
		where = append(where, "r.status IN ("+placeholders(len(in.Filters.Statuses))+")")
		for _, st := range in.Filters.Statuses {
			args = append(args, string(st))
		}
	}

	if len(in.Filters.Domains) > 0 {
		where = append(where, "r.domain IN ("+placeholders(len(in.Filters.Domains))+")")
		for _, d := range in.Filters.Domains {
			args = append(args, string(d))
		}
	}

	if len(in.Filters.FacetKinds) > 0 {
		where = append(where, "r.facet_kind IN ("+placeholders(len(in.Filters.FacetKinds))+")")
		for _, k := range in.Filters.FacetKinds {
			args = append(args, k)
		}
	}
	if len(in.Filters.FacetSources) > 0 {
		where = append(where, "r.facet_source IN ("+placeholders(len(in.Filters.FacetSources))+")")
		for _, src := range in.Filters.FacetSources {
			args = append(args, src)
		}
	}

	// Pointer health is computed inline (pointerHealthStatusExpr) rather than
	// joined, because this builder produces WHERE fragments only — the two
	// callers own their own FROM/JOIN. Filtering here rather than after the
	// fact is what makes "list the dead pointers" return the dead pointers
	// instead of the dead pointers that happened to rank in the top N.
	if len(in.Filters.PointerHealth) > 0 {
		where = append(where, "("+pointerHealthStatusExpr+") IN ("+placeholders(len(in.Filters.PointerHealth))+")")
		for _, h := range in.Filters.PointerHealth {
			args = append(args, h)
		}
	}

	if len(in.Filters.Origins) > 0 {
		where = append(where, "r.origin IN ("+placeholders(len(in.Filters.Origins))+")")
		for _, o := range in.Filters.Origins {
			args = append(args, string(o))
		}
	}

	if in.Filters.ConfidenceMin > 0 {
		where = append(where, "r.confidence >= ?")
		args = append(args, in.Filters.ConfidenceMin)
	}

	if in.Filters.Since != nil {
		where = append(where, "r.created_at >= ?")
		args = append(args, in.Filters.Since.UTC().Format(memoryTimeFormat))
	}
	if in.Filters.Until != nil {
		where = append(where, "r.created_at <= ?")
		args = append(args, in.Filters.Until.UTC().Format(memoryTimeFormat))
	}

	now := time.Now().UTC().Format(memoryTimeFormat)
	where = append(where, "(r.expires_at IS NULL OR r.expires_at > ?)")
	args = append(args, now)

	return where, args
}

// bm25Tokenize extracts the alphanumeric + underscore runs from a user query,
// grouped the way whitespace groups them: one group per whitespace-separated
// run of the input, holding the tokens that run contained.
//
// Grouping is what distinguishes the two query builders below. `CW-20260519-0032`
// is a single whitespace-separated run holding three tokens, so it can be
// rendered either as three independent terms or as one adjacency-bearing
// phrase; `cursor paging` is two runs and can only be two terms.
//
// Everything outside [A-Za-z0-9_] is a separator, which is also what strips
// every FTS5 MATCH metacharacter (`"`, `*`, `:`, `^`, `-`, `(`, `)`) before
// the parser can see it.
//
// The consequence to know about is that this DISAGREES with the tokenizer the
// index was built with. memory_revisions_fts declares no tokenize= option, so
// it uses unicode61, which does not split on a non-ASCII letter — it folds one
// into a token. This function does split there: `naïve` becomes the two tokens
// `na` and `ve`, neither of which the index contains. It is a split, not a
// truncation; only a TRAILING accent looks like truncation. search_mode=lexical
// refuses such a query outright (see unrepresentableRune) rather than answer it
// with a confidently empty page. Under hybrid the behavior is pre-existing and
// the cosine arm still answers.
func bm25Tokenize(q string) [][]string {
	var groups [][]string
	var cur []string
	var tok strings.Builder

	flushTok := func() {
		if tok.Len() > 0 {
			cur = append(cur, tok.String())
			tok.Reset()
		}
	}
	flushGroup := func() {
		flushTok()
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
		}
	}

	for _, r := range q {
		switch {
		case r == '_',
			r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			tok.WriteRune(r)
		case r == ' ', r == '\t', r == '\n', r == '\r', r == '\v', r == '\f':
			flushGroup()
		default:
			// Punctuation inside a run: ends the token, keeps the group.
			flushTok()
		}
	}
	flushGroup()
	return groups
}

// quotePhrase renders tokens as one FTS5 phrase: `"a b c"`.
//
// No escaping of the tokens themselves is needed, and none is possible to get
// wrong: bm25Tokenize emits only [A-Za-z0-9_], so a token can never contain
// the `"` that would close the phrase early. That is what makes the output
// safe by construction rather than by enumerating what to escape.
func quotePhrase(tokens []string) string {
	return `"` + strings.Join(tokens, " ") + `"`
}

// fts5Operators are the tokens FTS5's MATCH grammar reads as infix operators.
//
// Case-sensitive, and that is the grammar's rule, not a shortcut here: only
// the uppercase spellings are keywords, so `and memory` is two ordinary tokens
// and has always answered fine.
//
// NEAR is deliberately absent. It is an operator only when immediately
// followed by `(`, and bm25Tokenize strips every parenthesis before this is
// consulted — so a NEAR reaching MATCH from here is always an ordinary token.
// `NEAR memory` matches the documents containing both words; it does not error.
var fts5Operators = map[string]bool{"AND": true, "OR": true, "NOT": true}

// unrepresentableRune returns the first rune of q that the BM25 builders cannot
// represent as a query against this index, or -1 if there is none.
//
// bm25Tokenize splits on everything outside [A-Za-z0-9_]; unicode61, which
// built the index, does not split on a non-ASCII letter, digit, or combining
// mark. Where the two disagree, no expression this file can emit will ever
// match the token the index actually holds: `naïve` is one indexed token and
// becomes the phrase `"na ve"` here, which matches nothing and never can.
//
// Non-ASCII PUNCTUATION is fine and is deliberately not flagged — unicode61
// splits on an em-dash or a curly quote too, so the grouping still lines up.
func unrepresentableRune(q string) rune {
	for _, r := range q {
		if r < utf8.RuneSelf {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.M) {
			return r
		}
	}
	return -1
}

// sanitizeBM25Query builds the recall-oriented MATCH expression used by the
// hybrid arm: every token quoted into its own single-token phrase, EXCEPT an
// operator keyword sitting where the grammar can actually take an operator,
// which is passed through bare.
//
// The selectivity is the whole design, and getting it wrong is not a tail
// effect. Quoting every token — which reads as the safe, uniform choice —
// turns the operator into a required literal term, a third interpretation
// narrower than either thing a caller could have meant. Measured on the live
// corpus (1,642 revisions, max created_at 2026-08-25T21:32:39.933565Z):
//
//	query                 today   quote-everything   this builder
//	nanite AND torque      100           96              100
//	nanite OR torque       927           57              927
//	nanite NOT torque      701           84              701
//	memory OR recall       390           53              390
//	a AND b OR c           254           46              254
//
// OR loses 94% of its rows that way, silently, and the divergence reaches the
// top of the ranking rather than the tail: for `nanite AND torque`, positions
// 1-5 held but position 6 onward differed and one revision dropped out of the
// top 10 entirely.
//
// Preserving the infix operator reproduces today exactly, and that is measured
// rather than argued: over 3,030 queries on that corpus — 2- 3- and 5-word
// prefixes of real payload summaries, plus every AND/OR/NOT pairing of eight
// corpus words — this builder produced an ordered (revision_id, bm25) sequence
// identical to the bare form in ALL of them, while quoting every token diverged
// in 146.
//
// Where the grammar CANNOT take an operator — leading, trailing, or doubled —
// the keyword is quoted into an ordinary term. That is the crash fix, and it
// changes no query that parses today, because today those queries do not parse
// at all: `AND memory`, `memory AND` and `NOT NULL constraint` are all
// `fts5: syntax error` on main and become 301, 301 and 1 here.
//
// One more thing worth stating, unchanged from before: the implicit operator
// between bare phrases in FTS5 is AND, not OR — so the recall-oriented arm is
// an intersection, and always was. Re-derive on any corpus with
//
//	SELECT COUNT(*) FROM memory_revisions_fts WHERE memory_revisions_fts MATCH ?
//
// for `CW 20260519 0032`, `CW AND 20260519 AND 0032` and
// `CW OR 20260519 OR 0032`: the first two agree, the third is far larger.
func sanitizeBM25Query(q string) string {
	var tokens []string
	for _, group := range bm25Tokenize(q) {
		tokens = append(tokens, group...)
	}

	out := make([]string, 0, len(tokens))
	// expectTerm tracks the grammar position. An operator is legal only after
	// a completed operand, and only if an operand follows it. Because a token
	// in term position is always quoted — an operator keyword included — the
	// "an operand follows" test is just "another token follows", and the
	// emitted expression can never be leading-, trailing-, or double-operator.
	expectTerm := true
	for i, tok := range tokens {
		if fts5Operators[tok] && !expectTerm && i+1 < len(tokens) {
			out = append(out, tok)
			expectTerm = true
			continue
		}
		out = append(out, quotePhrase([]string{tok}))
		expectTerm = false
	}
	return strings.Join(out, " ")
}

// sanitizeBM25Phrase builds the precision-oriented MATCH expression used by
// search_mode=lexical: each whitespace-separated run becomes ONE phrase, so
// the tokens a punctuated identifier was built from must appear adjacent.
//
// This is the difference between finding a ticket ID and finding documents
// that happen to mention its three pieces somewhere. Measured on the live
// corpus (1,642 revisions, max created_at 2026-08-25T21:32:39.933565Z), for
// `CW-20260519-0032`, which appears in exactly one revision:
//
//	term form   `"CW" "20260519" "0032"`   12 rows; the true row ranks 5th
//	phrase form `"CW 20260519 0032"`        1 row;  the true row ranks 1st
//
// Unlike sanitizeBM25Query this builder is operator-BLIND: every token is
// quoted, so AND/OR/NOT are literal words. That asymmetry is deliberate. The
// two builders differ on grouping AND on operator handling, because no single
// rule can serve both callers — the hybrid caller needs `nanite OR torque` to
// keep the row count the table above derives, and the lexical caller asked for
// the words they typed.
//
// KNOWN LIMIT, and it is in the tool description because a caller cannot infer
// it: adjacency is bound only for PUNCTUATION-joined runs, never for
// whitespace-joined ones. Multiple runs combine under implicit AND, so
// `CW-20260519-0032 cursor` is "that identifier, in a document that also says
// cursor" — but `sqlite NOT NULL` is the three words anywhere, not the phrase.
// On the same corpus:
//
//	"sqlite" "not" "null"   ->  2   what lexical builds
//	"NOT NULL"              ->  6   the phrase a caller may have meant
//	"null" "not"            -> 33   the terms, reversed: still 33
//	"null not"              ->  0   the phrase, reversed: adjacency is real
//
// Whitespace carries no signal about which of the two a caller wanted, and
// inferring either way is silently wrong for the other — the defect class this
// ticket exists to remove. Explicit phrase search is a separate affordance and
// is deliberately NOT smuggled in here.
func sanitizeBM25Phrase(q string) string {
	var phrases []string
	for _, group := range bm25Tokenize(q) {
		phrases = append(phrases, quotePhrase(group))
	}
	return strings.Join(phrases, " ")
}

// bm25MatchExpr picks the MATCH expression for in's search mode.
//
// It is the single place the two builders are chosen between, so the lexical
// entry point cannot acquire a different ESCAPING story from the hybrid one:
// both take their tokens from bm25Tokenize, so neither can emit a token
// carrying a character the MATCH parser treats as syntax. They differ only in
// what they do with those tokens — grouping and operator handling — which is
// the caller-visible part, not the safety part.
func bm25MatchExpr(in RecallInput) string {
	if in.SearchMode == SearchModeLexical {
		return sanitizeBM25Phrase(in.Query)
	}
	return sanitizeBM25Query(in.Query)
}

// fetchBM25Candidates returns up to n memory_revisions matching the
// query via FTS5 MATCH, subject to the same filters as fetchCandidates.
// Results are ordered best-first by FTS5 bm25(), which returns lower
// values for more relevant documents, then by revision_id. Returns an
// empty slice if the query is empty after sanitization or if n <= 0
// reduces to the default.
//
// The bm25() call carries the column weights in bm25RankExpr, which boost
// memory_key over the prose columns. Both callers -- lexical and the hybrid
// arm -- get the same expression, so the two modes cannot disagree about what
// a key match is worth.
//
// The revision_id tiebreak makes this a total order, which matters at the
// LIMIT boundary: without it, WHICH rows survive the arm cut is not
// determined by construction when bm25 scores tie there, so the same query
// could feed RRF a different candidate set. That is the defect class fixed
// in the Go comparators (sortRecallResults); this is the SQL half of it,
// and it sits on the relevance path — which resolveRecallDefaults makes the
// default for any query-bearing recall. Deterministic in practice on the
// current storage layout, so this is a correction by construction rather
// than a fix for observed breakage.
//
// Status filtering is applied at query time (WHERE r.status IN (...)),
// not via trigger-side exclusion, so freshly-deprecated revisions remain
// indexed and callers can opt back in by widening the status filter.
func (s *Store) fetchBM25Candidates(ctx context.Context, in RecallInput, n int) ([]Revision, error) {
	ftsQuery := bm25MatchExpr(in)
	if ftsQuery == "" {
		return nil, nil
	}
	if n <= 0 {
		n = bm25CandidateDefault
	}

	where, args := buildRecallFilters(in)
	whereClause := strings.Join(where, " AND ")

	var sqlText string
	switch in.RevisionScope {
	case "", RevisionScopeCurrent:
		sqlText = `SELECT ` + recallRevisionColumns + `
FROM memory_revisions_fts fts
INNER JOIN memory_revisions r ON r.rowid = fts.rowid
INNER JOIN memory_state s ON s.current_revision = r.revision_id
WHERE memory_revisions_fts MATCH ? AND ` + whereClause + `
ORDER BY ` + bm25RankExpr + `, r.revision_id
LIMIT ?`
	case RevisionScopeTimeline:
		sqlText = `SELECT ` + recallRevisionColumns + `
FROM memory_revisions_fts fts
INNER JOIN memory_revisions r ON r.rowid = fts.rowid
WHERE memory_revisions_fts MATCH ? AND ` + whereClause + `
ORDER BY ` + bm25RankExpr + `, r.revision_id
LIMIT ?`
	default:
		return nil, fmt.Errorf("unknown revision scope %q", in.RevisionScope)
	}

	ftsArgs := make([]interface{}, 0, len(args)+2)
	ftsArgs = append(ftsArgs, ftsQuery)
	ftsArgs = append(ftsArgs, args...)
	ftsArgs = append(ftsArgs, n)

	rows, err := s.db.QueryContext(ctx, sqlText, ftsArgs...)
	if err != nil {
		return nil, fmt.Errorf("bm25 query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var revs []Revision
	for rows.Next() {
		rev, scanErr := scanRevision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		revs = append(revs, rev)
	}
	return revs, rows.Err()
}

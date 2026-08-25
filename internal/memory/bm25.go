package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// bm25CandidateDefault is the default top-N fetched for the BM25 arm of
// hybrid relevance recall. The epic guidance is N≈50–100; 100 gives RRF
// enough candidates to fuse against the cosine arm without over-fetching.
const bm25CandidateDefault = 100

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
// the parser can see it. Note the consequence: the token set is ASCII-only, so
// a query in a non-Latin script reduces to nothing and an accented word is
// truncated at the accent. That is pre-existing behavior of the BM25 arm, not
// introduced here.
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
// Quoting is not cosmetic. Inside double quotes FTS5 treats AND, OR, NOT and
// NEAR as ordinary tokens rather than as operators, which is what makes this
// safe against arbitrary user input. Unquoted, a query whose first word is
// "and" reaches MATCH as `AND ...` and SQLite answers `fts5: syntax error near
// "AND"` — the whole recall fails. Unquoted "memory NOT recall" parses as a
// NOT operator and silently answers a different question than the caller asked.
//
// No escaping of the tokens themselves is needed, and none is possible to get
// wrong: bm25Tokenize emits only [A-Za-z0-9_], so a token can never contain
// the `"` that would close the phrase early.
func quotePhrase(tokens []string) string {
	return `"` + strings.Join(tokens, " ") + `"`
}

// sanitizeBM25Query builds the recall-oriented MATCH expression used by the
// hybrid arm: every token as its own single-token phrase, joined by FTS5's
// implicit AND.
//
// The implicit operator between bare phrases in FTS5 is AND, not OR. Measured
// against the live corpus (1,639 revisions, max created_at 2026-08-25T19:18:16Z):
// `CW 20260519 0032` and `CW AND 20260519 AND 0032` both return 12 rows, while
// `CW OR 20260519 OR 0032` returns 720.
//
// Per-token quoting is exactly ranking-neutral against the bare form it
// replaces — a one-token phrase is the same query as a bare token — so the
// hybrid arm's results and bm25 scores are unchanged. Verified on the same
// corpus for two queries: `CW 20260519 0032` (12 rows) and
// `memory recall ranking` (16 rows) each produce an identical ordered
// (revision_id, bm25 score) sequence in both forms, 0 positions differing.
// What quoting changes is only the failure modes described on quotePhrase.
func sanitizeBM25Query(q string) string {
	var phrases []string
	for _, group := range bm25Tokenize(q) {
		for _, tok := range group {
			phrases = append(phrases, quotePhrase([]string{tok}))
		}
	}
	return strings.Join(phrases, " ")
}

// sanitizeBM25Phrase builds the precision-oriented MATCH expression used by
// search_mode=lexical: each whitespace-separated run becomes ONE phrase, so
// the tokens a punctuated identifier was built from must appear adjacent.
//
// This is the difference between finding a ticket ID and finding documents
// that happen to mention its three pieces somewhere. Measured on the live
// corpus (1,639 revisions, max created_at 2026-08-25T19:18:16Z), for the query
// `CW-20260519-0032`, which appears in exactly one revision:
//
//	term form   `"CW" "20260519" "0032"`   12 rows; the true row ranks 5th
//	phrase form `"CW 20260519 0032"`        1 row;  the true row ranks 1st
//
// Multiple runs still combine under implicit AND, so `CW-20260519-0032 cursor`
// is "that identifier, in a document that also says cursor".
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
// entry point cannot acquire a different escaping story from the hybrid one:
// both go through bm25Tokenize, and neither can emit a character the MATCH
// parser treats as syntax.
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
ORDER BY bm25(memory_revisions_fts), r.revision_id
LIMIT ?`
	case RevisionScopeTimeline:
		sqlText = `SELECT ` + recallRevisionColumns + `
FROM memory_revisions_fts fts
INNER JOIN memory_revisions r ON r.rowid = fts.rowid
WHERE memory_revisions_fts MATCH ? AND ` + whereClause + `
ORDER BY bm25(memory_revisions_fts), r.revision_id
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

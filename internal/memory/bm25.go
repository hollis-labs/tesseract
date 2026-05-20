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

// sanitizeBM25Query extracts alphanumeric + underscore tokens from the
// user query so the FTS5 MATCH parser never sees its special characters
// (`"`, `-`, `*`, `^`, `(`, `)`, `AND`/`OR`/`NOT`). Space-separated bare
// tokens are treated by FTS5 as an implicit OR — recall-oriented, which
// is what the BM25 arm wants: pull many candidates, let RRF sort them.
func sanitizeBM25Query(q string) string {
	var tokens []string
	var cur strings.Builder
	for _, r := range q {
		switch {
		case r == '_',
			r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			cur.WriteRune(r)
		default:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return strings.Join(tokens, " ")
}

// fetchBM25Candidates returns up to n memory_revisions matching the
// query via FTS5 MATCH, subject to the same filters as fetchCandidates.
// Results are ordered best-first by FTS5 bm25(), which returns lower
// values for more relevant documents. Returns an empty slice if the
// query is empty after sanitization or if n <= 0 reduces to the default.
//
// Status filtering is applied at query time (WHERE r.status IN (...)),
// not via trigger-side exclusion, so freshly-deprecated revisions remain
// indexed and callers can opt back in by widening the status filter.
func (s *Store) fetchBM25Candidates(ctx context.Context, in RecallInput, n int) ([]Revision, error) {
	ftsQuery := sanitizeBM25Query(in.Query)
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
ORDER BY bm25(memory_revisions_fts)
LIMIT ?`
	case RevisionScopeTimeline:
		sqlText = `SELECT ` + recallRevisionColumns + `
FROM memory_revisions_fts fts
INNER JOIN memory_revisions r ON r.rowid = fts.rowid
WHERE memory_revisions_fts MATCH ? AND ` + whereClause + `
ORDER BY bm25(memory_revisions_fts)
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

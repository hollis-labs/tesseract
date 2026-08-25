package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/embedding"
)

// Ranking determines how recall results are ordered.
type Ranking string

const (
	RankingActivation    Ranking = "activation"
	RankingChronological Ranking = "chronological"
	RankingSimilarity    Ranking = "similarity"
	RankingRelevance     Ranking = "relevance"
)

// RevisionScope controls whether recall returns only the current revision
// per memory or all revisions (timeline).
type RevisionScope string

const (
	RevisionScopeCurrent  RevisionScope = "current"
	RevisionScopeTimeline RevisionScope = "timeline"
)

// ErrSimilarityUnavailable is returned when similarity ranking is requested
// but no embedder is configured.
//
// Deprecated: use ErrEmbedderUnavailable instead.
var ErrSimilarityUnavailable = ErrEmbedderUnavailable

// RecallInput carries parameters for a recall query.
type RecallInput struct {
	Namespaces    []string
	RevisionScope RevisionScope
	Ranking       Ranking
	Query         string
	Filters       RecallFilters
	Limit         int

	// Offset skips the first N rows of the ordered result set before Limit
	// is applied. It is how cursor paging is expressed to the store; surfaces
	// derive it from an opaque cursor rather than exposing it directly, so
	// that a resumed position is always checked against the ordering it was
	// issued for (see DecodeCursor).
	//
	// Offsets are positions in a re-derived ordering, not stable keys. On a
	// corpus that changes between pages, or under activation ranking whose
	// scores move with wall-clock time, a row can be seen twice or missed.
	// Within one pass over a settled corpus the ordering is a total order
	// (revision_id breaks every tie), so paging is exact.
	Offset int

	// Reranker, when non-empty, names a Reranker registered on the
	// Store via RegisterReranker. Applied after scoring/sort/truncate
	// to reorder the result set with a cross-encoder (or any custom
	// Reranker). Empty means no rerank (default).
	Reranker string
	// RerankerTopK caps how many of the top results the reranker sees
	// and returns. Defaults to len(results) when ≤ 0.
	RerankerTopK int
}

// RecallFilters constrains which revisions are returned.
type RecallFilters struct {
	Origins       []Origin
	Statuses      []Status
	Tags          []string
	ConfidenceMin float64
	Since         *time.Time
	Until         *time.Time

	// Domains filters to revisions written under any of the listed domains.
	// Empty means no domain filter (all domains returned).
	Domains []domains.Domain

	// FacetKinds / FacetSources constrain knowledge-domain revisions to the
	// listed facet values. Applied as SQL IN (...) filters when non-empty.
	FacetKinds   []string
	FacetSources []string
}

// RecallResult pairs a revision with its ranking score and the parent state.
//
// Score is ranking-relative: it is comparable only against other results in
// the same response, and its units differ per ranking mode —
//
//	activation   activation strength (recency x reinforcement x confidence)
//	similarity   cosine similarity between query and revision embeddings
//	relevance    RRF-fused BM25 + cosine, weighted by status/origin/activation
//	chronological  no score — nil
//
// Under chronological ranking the field has no job: ordering is already
// carried by array order plus Revision.CreatedAt, so a score there could only
// restate the sort key in units nothing else uses. It is a pointer rather
// than a bare float64 so that "no score" stays distinguishable from a real
// zero — cosine similarity is legitimately 0 (orthogonal) or negative.
type RecallResult struct {
	Revision Revision `json:"revision"`
	Score    *float64 `json:"score,omitempty"`
	State    State    `json:"state"`
}

// scoreOf reads a result's score, treating an absent score as zero. Used for
// ordering and threshold comparisons inside the package; callers that need to
// tell "no score" from "zero score" must check Score == nil themselves.
func scoreOf(r RecallResult) float64 {
	if r.Score == nil {
		return 0
	}
	return *r.Score
}

// scorePtr boxes a computed score for RecallResult.Score.
func scorePtr(v float64) *float64 { return &v }

// RecallPageResult is one window of an ordered recall, plus the size of the
// set that window was taken from.
//
// Total counts rows that matched before Offset and Limit were applied, which
// is what makes "you got 30 of 1,639" expressible. It is NOT a corpus count:
// under ranking=relevance the candidate arms are capped at relevanceArmLimit,
// so Total is bounded by the fused arm size.
type RecallPageResult struct {
	Results []RecallResult
	Total   int
	Offset  int
}

// resolveRecallDefaults fills in the defaults that determine the ordered
// candidate sequence. It is separated from Recall so that
// RecallOrderingFingerprint can hash the resolved form: an omitted ranking and
// an explicitly-passed "activation" must fingerprint identically, or paging
// would break for a caller who spelled its defaults out on the second page.
//
// Limit and Offset are deliberately untouched here — they window the sequence
// rather than determine it, and they are not part of the fingerprint.
func resolveRecallDefaults(in RecallInput) RecallInput {
	// When ranking is unspecified, pick relevance for queries (BM25 + cosine
	// fusion) and activation otherwise — the activation primitive remains the
	// sensible default for no-query recall, while agents asking a semantic
	// question get hybrid recall by default (EPIC-20260414-19124).
	if in.Ranking == "" {
		if strings.TrimSpace(in.Query) != "" {
			in.Ranking = RankingRelevance
		} else {
			in.Ranking = RankingActivation
		}
	}
	if in.RevisionScope == "" {
		in.RevisionScope = RevisionScopeCurrent
	}
	if len(in.Filters.Statuses) == 0 {
		in.Filters.Statuses = []Status{StatusCanonical, StatusReviewed, StatusDraft}
	}
	return in
}

// Recall retrieves revisions matching the given filters, ranked by the
// requested strategy. It is the main context-assembly retrieval operation.
//
// Recall is RecallPage's first page: it discards Total and returns only the
// window. Callers that need to know whether more rows matched — anything
// building a truncation envelope or a cursor — must use RecallPage.
func (s *Store) Recall(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	page, err := s.RecallPage(ctx, in)
	if err != nil {
		return nil, err
	}
	return page.Results, nil
}

// RecallPage runs a recall and returns one Offset/Limit window of the ordered
// result set together with the total number of rows that matched.
//
// The ordering is fully derived before windowing — the store scores and sorts
// every candidate, then slices — so an offset is exact within a single pass.
// The reranker, when one is configured, runs on the window rather than on the
// whole set, matching the pre-paging behavior where it ran on the truncated
// result.
func (s *Store) RecallPage(ctx context.Context, in RecallInput) (RecallPageResult, error) {
	// 1. Validate namespaces — shape is domain-dependent (memory requires
	// the legacy user/{id}[/project|session/{id}]/memory form; knowledge
	// namespaces carry a 'knowledge' segment). Require non-empty here and
	// defer shape checks to the domain policy on write.
	if len(in.Namespaces) == 0 {
		return RecallPageResult{}, fmt.Errorf("%w: at least one namespace is required", ErrInvalidInput)
	}
	for _, ns := range in.Namespaces {
		if strings.TrimSpace(ns) == "" {
			return RecallPageResult{}, fmt.Errorf("%w: namespace entries must be non-empty", ErrInvalidInput)
		}
	}
	if in.Offset < 0 {
		return RecallPageResult{}, fmt.Errorf("%w: offset must not be negative", ErrInvalidInput)
	}
	// Same reason RecallPaged refuses a reranker outright: applyReranker
	// reorders the window and RerankerTopK truncates it, so the rows returned
	// are not a prefix of the rows the offset consumed. Walking offsets by
	// hand over a reranked recall skips and repeats rows. Offset 0 is the
	// unpaged case and stays allowed — that is the path Recall uses.
	if in.Offset > 0 && in.Reranker != "" {
		return RecallPageResult{}, fmt.Errorf(
			"%w: reranker %q cannot be combined with a non-zero offset — the reranker "+
				"reorders within the window, so an offset does not name a stable position",
			ErrInvalidInput, in.Reranker)
	}

	// 2. Apply defaults.
	in = resolveRecallDefaults(in)
	if in.Limit <= 0 {
		in.Limit = DefaultRecallLimit
	}
	if in.Limit > MaxRecallLimit {
		in.Limit = MaxRecallLimit
	}

	// 3. Similarity ranking requires an embedder and a query.
	if in.Ranking == RankingSimilarity {
		if s.embedder == nil {
			return RecallPageResult{}, ErrEmbedderUnavailable
		}
		if in.Query == "" {
			return RecallPageResult{}, fmt.Errorf("%w: query is required for similarity ranking", ErrInvalidInput)
		}
	}

	ordered, err := s.recallOrdered(ctx, in)
	if err != nil {
		return RecallPageResult{}, err
	}

	total := len(ordered)
	start := in.Offset
	if start > total {
		start = total
	}
	end := start + in.Limit
	if end > total {
		end = total
	}
	window := ordered[start:end]

	// Optional per-call reranker pass to reorder the returned window.
	//
	// Recall does NOT reinforce activation/access_count: being returned by
	// a search is the system's guess, not a deliberate read. Reinforcement
	// happens only on the get paths (memory_get / memory_get_revision) —
	// see reinforceMemoryIDs in activation.go.
	window, err = s.applyReranker(ctx, in, window)
	if err != nil {
		return RecallPageResult{}, err
	}

	return RecallPageResult{Results: window, Total: total, Offset: start}, nil
}

// recallOrdered produces the complete ordered result set for in, before any
// offset/limit windowing and before the reranker. The ordering it produces is
// a TOTAL order: every comparator breaks ties on revision_id, so two calls
// with the same inputs over an unchanged corpus return the same sequence.
//
// That property is a precondition for offset paging, not a nicety. The
// pre-paging sorts compared score (or created_at) alone, and fetchCandidates
// issues no ORDER BY, so tied rows previously came back in whatever order
// SQLite produced — two identical calls could interleave them differently and
// an offset would then skip one row and repeat another with nothing to
// indicate it. relevanceRecall already broke ties on revision_id; the other
// rankings now do the same.
func (s *Store) recallOrdered(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	// Relevance ranking has its own pipeline (BM25 + optional cosine,
	// fused via RRF and weighted by activation-style modifiers). The
	// embedder is optional — BM25-only is intentional for freshly-written
	// memories that haven't been embedded yet.
	if in.Ranking == RankingRelevance {
		return s.relevanceOrdered(ctx, in)
	}

	// 4. Fetch candidate revisions.
	candidates, err := s.fetchCandidates(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("fetchCandidates: %w", err)
	}

	// Post-filter by tags if requested.
	if len(in.Filters.Tags) > 0 {
		filtered := candidates[:0]
		for _, c := range candidates {
			if tagsAnyMatch(c.Tags, in.Filters.Tags) {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	// 4b. Embed the query for similarity ranking.
	var queryVec []float32
	if in.Ranking == RankingSimilarity {
		result, embedErr := s.embedder.Embed(ctx, in.Query, s.embeddingModel)
		if embedErr != nil {
			return nil, fmt.Errorf("query embedding: %w", embedErr)
		}
		queryVec = result.Embedding
	}

	// 5. Batch-load states for all distinct memory IDs.
	states, err := s.fetchStates(ctx, distinctMemoryIDs(candidates))
	if err != nil {
		return nil, fmt.Errorf("fetchStates: %w", err)
	}

	// 6. Score and sort into a total order.
	now := time.Now().UTC()
	results := make([]RecallResult, 0, len(candidates))
	for _, rev := range candidates {
		st := states[rev.MemoryID]
		rr := RecallResult{Revision: rev, State: st}
		switch in.Ranking {
		case RankingActivation:
			rr.Score = scorePtr(activationScore(rev, st, now))
		case RankingSimilarity:
			rr.Score = scorePtr(similarityScore(rev, queryVec))
		case RankingChronological:
			// No score. Ordering is carried by array order plus
			// Revision.CreatedAt; the sort below uses the timestamp
			// directly rather than smuggling it through Score.
		}
		results = append(results, rr)
	}

	sortRecallResults(results, in.Ranking)

	// Filter out unembedded revisions for similarity ranking. We check
	// for embedding presence rather than score > 0 because cosine similarity
	// can legitimately be 0 (orthogonal) or negative (opposite).
	if in.Ranking == RankingSimilarity {
		filtered := results[:0]
		for _, r := range results {
			if len(r.Revision.EmbeddingVector) > 0 {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// Windowing and the reranker are RecallPage's job — recallOrdered hands
	// back the complete sequence so the caller can report how much of it it
	// is about to withhold.
	return results, nil
}

// sortRecallResults orders a scored candidate set in place.
//
// revision_id breaks every tie, which is what makes both comparators TOTAL
// orders rather than partial ones. Without it, equal timestamps
// (chronological) or equal scores (activation/similarity) leave the tied rows
// in whatever order fetchCandidates produced — and fetchCandidates issues no
// ORDER BY, so that order is SQLite's to choose. An offset cursor over a
// partial order silently skips one row and repeats another.
//
// It is a named function rather than two inline closures so the property can
// be tested against a deliberately shuffled input. Through the store the
// tiebreaker is unobservable: rows come back in rowid order, revision_ids are
// ULIDs that increase with insertion, so SQLite's natural order ALREADY equals
// the tiebreak order and removing the tiebreaker changes nothing you can see.
// A test that goes through the store therefore cannot tell a total order from
// a partial one — see TestSortRecallResults_TotalOrder.
//
// relevanceRecall has always broken ties on revision_id; this brings the other
// rankings in line.
func sortRecallResults(results []RecallResult, ranking Ranking) {
	if ranking == RankingChronological {
		sort.Slice(results, func(i, j int) bool {
			ki, kj := chronologicalKey(results[i].Revision), chronologicalKey(results[j].Revision)
			if ki == kj {
				return results[i].Revision.RevisionID < results[j].Revision.RevisionID
			}
			return ki > kj
		})
		return
	}
	sort.Slice(results, func(i, j int) bool {
		si, sj := scoreOf(results[i]), scoreOf(results[j])
		if si == sj {
			return results[i].Revision.RevisionID < results[j].Revision.RevisionID
		}
		return si > sj
	})
}

// fetchCandidates builds a dynamic SQL query with parameterized filters.
func (s *Store) fetchCandidates(ctx context.Context, in RecallInput) ([]Revision, error) {
	where, args := buildRecallFilters(in)
	whereClause := strings.Join(where, " AND ")

	var query string
	switch in.RevisionScope {
	case RevisionScopeCurrent:
		query = `SELECT ` + recallRevisionColumns + `
FROM memory_revisions r
INNER JOIN memory_state s ON s.current_revision = r.revision_id
WHERE ` + whereClause
	case RevisionScopeTimeline:
		query = `SELECT ` + recallRevisionColumns + `
FROM memory_revisions r
WHERE ` + whereClause
	default:
		return nil, fmt.Errorf("unknown revision scope %q", in.RevisionScope)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
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

// recallRevisionColumns is the same column list as revisionColumns but with
// explicit r. prefix to avoid ambiguity when JOINing with memory_state.
const recallRevisionColumns = `r.revision_id, r.memory_id, r.domain, r.namespace, COALESCE(r.memory_key, ''),
       r.status, COALESCE(r.supersedes, ''), r.created_at,
       r.author_agent_id, r.author_version, r.trigger, r.session_id, r.origin,
       r.confidence, r.tags, COALESCE(r.ttl_seconds, 0), r.expires_at,
       COALESCE(r.payload_summary, ''), COALESCE(r.payload_body, ''),
       COALESCE(r.embedding_model, ''), r.embedding_vector,
       r.facet_kind, r.facet_source,
       r.facet_pointer_scheme, r.facet_pointer_locator, r.facet_pointer_resolved_at`

// fetchStates loads memory_state rows for a set of memory IDs.
func (s *Store) fetchStates(ctx context.Context, memoryIDs []string) (map[string]State, error) {
	if len(memoryIDs) == 0 {
		return map[string]State{}, nil
	}

	ph := placeholders(len(memoryIDs))
	query := fmt.Sprintf( //nolint:gosec // ph is parameterized ?s, not user input
		`SELECT memory_id, domain, namespace, COALESCE(memory_key, ''), COALESCE(current_revision, ''),
       activation, access_count, last_accessed_at, created_at
FROM memory_state WHERE memory_id IN (%s)`, ph)

	args := make([]interface{}, len(memoryIDs))
	for i, id := range memoryIDs {
		args[i] = id
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make(map[string]State, len(memoryIDs))
	for rows.Next() {
		var st State
		var domain string
		var lastAccessed, created sql.NullString
		if err := rows.Scan(
			&st.MemoryID, &domain, &st.Namespace, &st.MemoryKey, &st.CurrentRevision,
			&st.Activation, &st.AccessCount, &lastAccessed, &created,
		); err != nil {
			return nil, err
		}
		st.Domain = domains.Domain(domain)
		if lastAccessed.Valid {
			t, _ := parseMemoryTime(lastAccessed.String)
			st.LastAccessedAt = &t
		}
		if created.Valid {
			st.CreatedAt, _ = parseMemoryTime(created.String)
		}
		states[st.MemoryID] = st
	}
	return states, rows.Err()
}

// tagsAnyMatch returns true if any element in want appears in have.
func tagsAnyMatch(have, want []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, t := range have {
		set[t] = struct{}{}
	}
	for _, t := range want {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

// distinctMemoryIDs extracts unique memory IDs from a slice of revisions.
func distinctMemoryIDs(revs []Revision) []string {
	seen := make(map[string]struct{}, len(revs))
	var ids []string
	for _, r := range revs {
		if _, ok := seen[r.MemoryID]; !ok {
			seen[r.MemoryID] = struct{}{}
			ids = append(ids, r.MemoryID)
		}
	}
	return ids
}

// similarityScore computes the cosine similarity between a revision's
// embedding vector and the query vector.
func similarityScore(rev Revision, queryVec []float32) float64 {
	if len(rev.EmbeddingVector) == 0 || len(queryVec) == 0 {
		return 0
	}
	return embedding.CosineSimilarity(queryVec, rev.EmbeddingVector)
}

// placeholders returns a comma-separated string of n question marks.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/vanta-conduit/domains"
	"github.com/hollis-labs/vanta-conduit/internal/embedding"
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

// RecallResult pairs a revision with its computed score and the parent state.
type RecallResult struct {
	Revision Revision
	Score    float64
	State    State
}

const defaultRecallLimit = 30
const maxRecallLimit = 500

// Recall retrieves revisions matching the given filters, ranked by the
// requested strategy. It is the main context-assembly retrieval operation.
func (s *Store) Recall(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	// 1. Validate namespaces — shape is domain-dependent (memory requires
	// the legacy user/{id}[/project|session/{id}]/memory form; knowledge
	// namespaces carry a 'knowledge' segment). Require non-empty here and
	// defer shape checks to the domain policy on write.
	if len(in.Namespaces) == 0 {
		return nil, fmt.Errorf("%w: at least one namespace is required", ErrInvalidInput)
	}
	for _, ns := range in.Namespaces {
		if strings.TrimSpace(ns) == "" {
			return nil, fmt.Errorf("%w: namespace entries must be non-empty", ErrInvalidInput)
		}
	}

	// 2. Apply defaults. When ranking is unspecified, pick relevance for
	// queries (BM25 + cosine fusion) and activation otherwise — the
	// activation primitive remains the sensible default for no-query
	// recall, while agents asking a semantic question get hybrid recall
	// by default (EPIC-20260414-19124).
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
	if in.Limit <= 0 {
		in.Limit = defaultRecallLimit
	}
	if in.Limit > maxRecallLimit {
		in.Limit = maxRecallLimit
	}
	if len(in.Filters.Statuses) == 0 {
		in.Filters.Statuses = []Status{StatusCanonical, StatusReviewed, StatusDraft}
	}

	// 3. Similarity ranking requires an embedder and a query.
	if in.Ranking == RankingSimilarity {
		if s.embedder == nil {
			return nil, ErrEmbedderUnavailable
		}
		if in.Query == "" {
			return nil, fmt.Errorf("%w: query is required for similarity ranking", ErrInvalidInput)
		}
	}

	// 3b. Relevance ranking has its own pipeline (BM25 + optional cosine,
	// fused via RRF and weighted by activation-style modifiers). The
	// embedder is optional — BM25-only is intentional for freshly-written
	// memories that haven't been embedded yet.
	if in.Ranking == RankingRelevance {
		return s.relevanceRecall(ctx, in)
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

	// 6. Score, sort, truncate.
	now := time.Now().UTC()
	results := make([]RecallResult, 0, len(candidates))
	for _, rev := range candidates {
		st := states[rev.MemoryID]
		var score float64
		switch in.Ranking {
		case RankingActivation:
			score = activationScore(rev, st, now)
		case RankingChronological:
			score = float64(chronologicalKey(rev))
		case RankingSimilarity:
			score = similarityScore(rev, queryVec)
		}
		results = append(results, RecallResult{
			Revision: rev,
			Score:    score,
			State:    st,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

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

	if len(results) > in.Limit {
		results = results[:in.Limit]
	}

	// 7. Best-effort reinforce access for activation ranking.
	if in.Ranking == RankingActivation {
		_ = s.reinforceAccess(ctx, results)
	}

	return results, nil
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

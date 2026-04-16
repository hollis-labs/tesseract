package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// rrfK is the Reciprocal Rank Fusion smoothing constant. 60 is the
// standard value from the original RRF paper (Cormack et al., 2009) and
// is what the hybrid-relevance epic (EPIC-20260414-19124) specifies.
const rrfK = 60.0

// relevanceArmLimit is the per-arm top-N cap for BM25 and cosine. Epic
// guidance: N≈50–100; 100 keeps RRF well-defined while bounding cost.
const relevanceArmLimit = 100

// relevanceRecall implements RankingRelevance: combines BM25 and cosine
// rankings via Reciprocal Rank Fusion, then multiplies by the same
// modifiers activation mode uses (status, origin, confidence, recency,
// activation). Embedder-optional: BM25-only path fires when no embedder
// is configured so freshly-written memories surface immediately.
func (s *Store) relevanceRecall(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("%w: query is required for relevance ranking", ErrInvalidInput)
	}

	bm25, err := s.fetchBM25Candidates(ctx, in, relevanceArmLimit)
	if err != nil {
		return nil, fmt.Errorf("relevance bm25 arm: %w", err)
	}

	var cosine []Revision
	if s.embedder != nil {
		cosine, err = s.fetchCosineCandidates(ctx, in, relevanceArmLimit)
		if err != nil {
			return nil, fmt.Errorf("relevance cosine arm: %w", err)
		}
	}

	if len(in.Filters.Tags) > 0 {
		bm25 = filterByTags(bm25, in.Filters.Tags)
		cosine = filterByTags(cosine, in.Filters.Tags)
	}

	if len(bm25) == 0 && len(cosine) == 0 {
		return nil, nil
	}

	rrf := make(map[string]float64, len(bm25)+len(cosine))
	revByID := make(map[string]Revision, len(bm25)+len(cosine))
	for i, r := range bm25 {
		rrf[r.RevisionID] += 1.0 / (rrfK + float64(i+1))
		revByID[r.RevisionID] = r
	}
	for i, r := range cosine {
		rrf[r.RevisionID] += 1.0 / (rrfK + float64(i+1))
		if _, ok := revByID[r.RevisionID]; !ok {
			revByID[r.RevisionID] = r
		}
	}

	memoryIDs := make([]string, 0, len(revByID))
	seen := make(map[string]struct{}, len(revByID))
	for _, r := range revByID {
		if _, ok := seen[r.MemoryID]; !ok {
			seen[r.MemoryID] = struct{}{}
			memoryIDs = append(memoryIDs, r.MemoryID)
		}
	}
	states, err := s.fetchStates(ctx, memoryIDs)
	if err != nil {
		return nil, fmt.Errorf("relevance fetchStates: %w", err)
	}

	now := time.Now().UTC()
	results := make([]RecallResult, 0, len(revByID))
	for id, score := range rrf {
		rev := revByID[id]
		st := states[rev.MemoryID]
		sw := statusWeights[rev.Status]
		ow := originWeights[rev.Origin]
		rf := recencyFactor(st.LastAccessedAt, now)
		final := score * sw * ow * rev.Confidence * rf * st.Activation
		results = append(results, RecallResult{Revision: rev, Score: final, State: st})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Revision.RevisionID < results[j].Revision.RevisionID
		}
		return results[i].Score > results[j].Score
	})

	if in.Limit > 0 && len(results) > in.Limit {
		results = results[:in.Limit]
	}

	// Rerank before reinforce so access metrics reflect only the
	// post-rerank result set the caller actually sees.
	results, err = s.applyReranker(ctx, in, results)
	if err != nil {
		return nil, err
	}

	_ = s.reinforceAccess(ctx, results)

	return results, nil
}

// fetchCosineCandidates runs the dense-retrieval arm: fetch all filter-
// matching candidates (no SQL-level top-N since SQLite has no ANN
// index), score by cosine against the query vector, and return the
// top n sorted best-first. Revisions without an embedding contribute
// nothing to this arm — they remain reachable via BM25.
func (s *Store) fetchCosineCandidates(ctx context.Context, in RecallInput, n int) ([]Revision, error) {
	candidates, err := s.fetchCandidates(ctx, in)
	if err != nil {
		return nil, err
	}

	result, err := s.embedder.Embed(ctx, in.Query, s.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}
	queryVec := result.Embedding

	type scored struct {
		rev   Revision
		score float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, r := range candidates {
		if len(r.EmbeddingVector) == 0 {
			continue
		}
		ranked = append(ranked, scored{r, similarityScore(r, queryVec)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].rev.RevisionID < ranked[j].rev.RevisionID
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	out := make([]Revision, len(ranked))
	for i, s := range ranked {
		out[i] = s.rev
	}
	return out, nil
}

// filterByTags returns the subset of revs whose tags match any of want.
func filterByTags(revs []Revision, want []string) []Revision {
	if len(revs) == 0 {
		return revs
	}
	out := revs[:0]
	for _, r := range revs {
		if tagsAnyMatch(r.Tags, want) {
			out = append(out, r)
		}
	}
	return out
}

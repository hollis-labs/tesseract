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

// relevanceOrdered implements RankingRelevance: combines BM25 and cosine
// rankings via Reciprocal Rank Fusion, then multiplies by the same
// modifiers activation mode uses (status, origin, confidence, recency,
// activation). Embedder-optional: BM25-only path fires when no embedder
// is configured so freshly-written memories surface immediately.
//
// It returns the complete fused ordering. Windowing and the reranker belong
// to RecallPage, which is the only caller — the same split the other rankings
// use in recallOrdered.
//
// Both arms are capped at relevanceArmLimit, so the sequence this returns is
// bounded by the fused arm size rather than by the number of rows that match
// the filters. A Total derived from it is a candidate count, not a corpus
// count, and RecallPage's callers document it as such.
func (s *Store) relevanceOrdered(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("%w: query is required for relevance ranking", ErrInvalidInput)
	}

	// search_mode selects the arms. hybrid is the default and the fall-through,
	// so an unresolved (empty) mode behaves exactly as it did before the knob
	// existed — relevanceOrdered is reachable only via RecallPage, which
	// resolves defaults, but the fall-through keeps that from being load-bearing.
	switch in.SearchMode {
	case SearchModeLexical:
		return s.lexicalOrdered(ctx, in)
	case SearchModeSemantic:
		return s.semanticOrdered(ctx, in)
	case SearchModeHybrid:
		// The fusion path below. Named rather than left to the fall-through so
		// a fourth mode added later fails the exhaustive check here instead of
		// silently retrieving as hybrid.
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
		results = append(results, RecallResult{Revision: rev, Score: scorePtr(final), State: st})
	}

	sort.Slice(results, func(i, j int) bool {
		si, sj := scoreOf(results[i]), scoreOf(results[j])
		if si == sj {
			return results[i].Revision.RevisionID < results[j].Revision.RevisionID
		}
		return si > sj
	})

	return results, nil
}

// lexicalOrdered implements search_mode=lexical: the BM25 arm alone, in the
// order SQL returned it (bm25(), then revision_id — already a total order, and
// the tiebreak is what makes it one).
//
// The arm's ordering is passed through untouched. It is NOT re-scored by the
// status/origin/confidence/recency/activation modifiers hybrid applies,
// because those modifiers are what move an exact identifier match off the top:
// on the live corpus the one revision containing CW-20260519-0032 has no
// special activation, and any weighting by how recently it was read reorders
// it against documents that merely share its tokens. Precision retrieval means
// the retrieval score decides.
//
// Score is left nil, the same as chronological ranking. RecallResult.Score is
// documented as higher-is-better in every mode that populates it, and SQLite's
// bm25() is lower-is-better — publishing it would invert the field's meaning
// for one mode while leaving the type unchanged, which is exactly the kind of
// same-shape-different-meaning value this domain has already been bitten by.
// Under lexical the ORDER is the signal, and array order carries it.
func (s *Store) lexicalOrdered(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	// An all-punctuation or non-ASCII query survives tokenization as nothing,
	// and a MATCH against nothing returns nothing. Under hybrid that is
	// harmless (the cosine arm still answers), but under lexical it would hand
	// back an empty page that looks exactly like "no such memory exists". Say
	// which of the two it is.
	if bm25MatchExpr(in) == "" {
		return nil, fmt.Errorf(
			"%w: query %q contains no searchable tokens — search_mode=lexical matches on "+
				"[A-Za-z0-9_] tokens, so a query of only punctuation or non-Latin script has "+
				"nothing to match; use search_mode=semantic for those",
			ErrInvalidInput, in.Query)
	}

	revs, err := s.fetchBM25Candidates(ctx, in, relevanceArmLimit)
	if err != nil {
		return nil, fmt.Errorf("lexical bm25 arm: %w", err)
	}
	if len(in.Filters.Tags) > 0 {
		revs = filterByTags(revs, in.Filters.Tags)
	}
	if len(revs) == 0 {
		return nil, nil
	}

	states, err := s.fetchStates(ctx, distinctMemoryIDs(revs))
	if err != nil {
		return nil, fmt.Errorf("lexical fetchStates: %w", err)
	}
	results := make([]RecallResult, 0, len(revs))
	for _, rev := range revs {
		results = append(results, RecallResult{Revision: rev, State: states[rev.MemoryID]})
	}
	return results, nil
}

// semanticOrdered implements search_mode=semantic: the cosine arm alone, in
// similarity order, carrying the cosine score.
//
// With no embedder configured this returns ErrEmbedderUnavailable rather than
// falling back to BM25. The fallback would be the worse failure: the caller
// asked for meaning-matching and would receive token-matching under that name,
// with a well-formed response and nothing in it saying so. Erroring is also
// what ranking=similarity already does in the same situation (RecallPage step
// 3), so the two cosine-only paths agree.
//
// hybrid's BM25-only fallback when no embedder is configured is deliberately
// left alone: it is documented, it is what freshly-written unembedded memories
// depend on, and hybrid promises fusion "where available" rather than cosine.
func (s *Store) semanticOrdered(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	if s.embedder == nil {
		return nil, fmt.Errorf(
			"%w: search_mode=semantic requires an embedder; none is configured. Use "+
				"search_mode=lexical for token matching, or search_mode=hybrid, which "+
				"falls back to the BM25 arm when no embedder is available",
			ErrEmbedderUnavailable)
	}

	scored, err := s.fetchCosineScored(ctx, in, relevanceArmLimit)
	if err != nil {
		return nil, fmt.Errorf("semantic cosine arm: %w", err)
	}
	if len(scored) == 0 {
		return nil, nil
	}

	revs := make([]Revision, 0, len(scored))
	for _, sc := range scored {
		revs = append(revs, sc.rev)
	}
	if len(in.Filters.Tags) > 0 {
		kept := filterByTags(revs, in.Filters.Tags)
		keep := make(map[string]struct{}, len(kept))
		for _, r := range kept {
			keep[r.RevisionID] = struct{}{}
		}
		filtered := scored[:0]
		for _, sc := range scored {
			if _, ok := keep[sc.rev.RevisionID]; ok {
				filtered = append(filtered, sc)
			}
		}
		scored = filtered
		revs = kept
	}
	if len(scored) == 0 {
		return nil, nil
	}

	states, err := s.fetchStates(ctx, distinctMemoryIDs(revs))
	if err != nil {
		return nil, fmt.Errorf("semantic fetchStates: %w", err)
	}
	results := make([]RecallResult, 0, len(scored))
	for _, sc := range scored {
		results = append(results, RecallResult{
			Revision: sc.rev,
			// scorePtr, not a bare float64: cosine similarity is legitimately
			// 0 (orthogonal) or negative (opposite), and Score carries
			// omitempty. A value type here would drop the field on exactly the
			// worst matches.
			Score: scorePtr(sc.score),
			State: states[sc.rev.MemoryID],
		})
	}
	return results, nil
}

// scoredRevision pairs a revision with its cosine similarity to the query.
type scoredRevision struct {
	rev   Revision
	score float64
}

// fetchCosineScored runs the dense-retrieval arm: fetch all filter-matching
// candidates (no SQL-level top-N since SQLite has no ANN index), score by
// cosine against the query vector, and return the top n sorted best-first with
// their scores. Revisions without an embedding contribute nothing to this arm
// — they remain reachable via BM25.
func (s *Store) fetchCosineScored(ctx context.Context, in RecallInput, n int) ([]scoredRevision, error) {
	candidates, err := s.fetchCandidates(ctx, in)
	if err != nil {
		return nil, err
	}

	result, err := s.embedder.Embed(ctx, in.Query, s.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}
	queryVec := result.Embedding

	ranked := make([]scoredRevision, 0, len(candidates))
	for _, r := range candidates {
		if len(r.EmbeddingVector) == 0 {
			continue
		}
		ranked = append(ranked, scoredRevision{r, similarityScore(r, queryVec)})
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
	return ranked, nil
}

// fetchCosineCandidates is fetchCosineScored without the scores — the shape
// the RRF arm wants, since fusion ranks by position rather than by score.
func (s *Store) fetchCosineCandidates(ctx context.Context, in RecallInput, n int) ([]Revision, error) {
	ranked, err := s.fetchCosineScored(ctx, in, n)
	if err != nil {
		return nil, err
	}
	out := make([]Revision, len(ranked))
	for i, sc := range ranked {
		out[i] = sc.rev
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

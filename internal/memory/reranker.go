package memory

import (
	"context"
	"errors"
	"fmt"
)

// ErrRerankerUnavailable is returned when a recall call names a
// Reranker that has not been registered on the Store.
var ErrRerankerUnavailable = errors.New("reranker unavailable")

// Reranker re-orders a candidate set against a query, typically via a
// cross-encoder model (Cohere, Voyage, bge-reranker). Implementations
// must preserve the candidate set: every Revision returned must have
// come from the input slice. They may truncate to topK when topK > 0.
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Revision, topK int) ([]Revision, error)
}

// RerankerFunc adapts a function to the Reranker interface so in-process
// rerankers (tests, custom scoring) can be wired without a struct.
type RerankerFunc func(ctx context.Context, query string, candidates []Revision, topK int) ([]Revision, error)

// Rerank implements the Reranker interface.
func (f RerankerFunc) Rerank(ctx context.Context, query string, candidates []Revision, topK int) ([]Revision, error) {
	return f(ctx, query, candidates, topK)
}

// RegisterReranker wires a Reranker onto this Store under the given
// name. RecallInput.Reranker selects one per call. Reregistering under
// the same name overwrites the previous entry.
func (s *Store) RegisterReranker(name string, r Reranker) {
	if s.rerankers == nil {
		s.rerankers = make(map[string]Reranker)
	}
	s.rerankers[name] = r
}

// applyReranker reorders results using the named reranker when
// in.Reranker is set. Returns results unchanged for the no-reranker
// case. Preserves per-result State and Score — only the ordering
// (and optional truncation to RerankerTopK) changes.
func (s *Store) applyReranker(ctx context.Context, in RecallInput, results []RecallResult) ([]RecallResult, error) {
	if in.Reranker == "" {
		return results, nil
	}
	r, ok := s.rerankers[in.Reranker]
	if !ok {
		return nil, fmt.Errorf("%w: %q not registered", ErrRerankerUnavailable, in.Reranker)
	}
	if len(results) == 0 {
		return results, nil
	}

	revs := make([]Revision, len(results))
	byID := make(map[string]int, len(results))
	for i, rr := range results {
		revs[i] = rr.Revision
		byID[rr.Revision.RevisionID] = i
	}

	topK := in.RerankerTopK
	if topK <= 0 {
		topK = len(revs)
	}

	reordered, err := r.Rerank(ctx, in.Query, revs, topK)
	if err != nil {
		return nil, fmt.Errorf("rerank %q: %w", in.Reranker, err)
	}

	out := make([]RecallResult, 0, len(reordered))
	for _, rev := range reordered {
		if i, ok := byID[rev.RevisionID]; ok {
			out = append(out, results[i])
		}
	}
	return out, nil
}

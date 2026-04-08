package memory

import (
	"context"
	"fmt"
)

// findSemanticMatch searches for a similar revision in the target namespace.
// Returns the matching revision ID and whether it's the same memory key.
// Returns ("", false, nil) if no match above threshold.
func (s *Store) findSemanticMatch(ctx context.Context, namespace, memoryKey string, payloadText string, threshold float64) (matchRevisionID string, sameKey bool, err error) {
	if s.embedder == nil {
		return "", false, ErrEmbedderUnavailable
	}

	if payloadText == "" {
		return "", false, nil
	}

	results, err := s.Recall(ctx, RecallInput{
		Namespaces: []string{namespace},
		Ranking:    RankingSimilarity,
		Query:      payloadText,
		Limit:      1,
	})
	if err != nil {
		return "", false, fmt.Errorf("dedup recall: %w", err)
	}

	if len(results) == 0 {
		return "", false, nil
	}

	top := results[0]
	if top.Score < threshold {
		return "", false, nil
	}

	matchID := top.Revision.RevisionID
	isSameKey := memoryKey != "" && top.Revision.MemoryKey == memoryKey
	return matchID, isSameKey, nil
}

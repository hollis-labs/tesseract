package memory

import (
	"context"
	"fmt"

	"github.com/hollis-labs/tesseract/domains"
)

// findSemanticMatch searches for a similar revision in the target namespace,
// within the domain being written.
//
// Returns the matching revision ID and whether it's the same memory key, or
// ("", false, nil) if no match clears the threshold.
//
// The domain filter is passed explicitly rather than left to recall's default,
// and that became load-bearing with CW-20260909-0035. Recall now defaults to
// the curated corpus (memory + knowledge) when the caller names no domains, so
// a dedup check on an event write would have searched a corpus that by
// definition contains no event rows and reported "no duplicate" every time —
// a silently wrong answer on the write path, which is worse than no dedup at
// all. Naming the domain also tightens the memory and knowledge cases from
// "whatever the namespace happens to hold" to "this domain", which is what the
// caller meant; it changes nothing for them today, because their namespace
// grammars already keep the domains apart.
func (s *Store) findSemanticMatch(ctx context.Context, domain domains.Domain, namespace, memoryKey string, payloadText string, threshold float64) (matchRevisionID string, sameKey bool, err error) {
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
		Filters:    RecallFilters{Domains: []domains.Domain{domain}},
	})
	if err != nil {
		return "", false, fmt.Errorf("dedup recall: %w", err)
	}

	if len(results) == 0 {
		return "", false, nil
	}

	// Similarity ranking always attaches a score; a missing one means the
	// candidate carries no comparable signal, so treat it as no match.
	top := results[0]
	if top.Score == nil || *top.Score < threshold {
		return "", false, nil
	}

	matchID := top.Revision.RevisionID
	isSameKey := memoryKey != "" && top.Revision.MemoryKey == memoryKey
	return matchID, isSameKey, nil
}

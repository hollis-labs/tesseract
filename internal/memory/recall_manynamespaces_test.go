package memory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestRecall_AcrossEveryRegisteredNamespace is the end-to-end guard for the
// memory review queue: that page lists every registered namespace and recalls
// across all of them at once. Against a store with a four-figure namespace
// count the recall used to fail outright —
//
//	fetchCandidates: SQL logic error: Expression tree is too large
//	(maximum depth 1000) (1)
//
// — because the namespace filter rendered one OR level per namespace and
// SQLite refuses to parse an expression deeper than SQLITE_MAX_EXPR_DEPTH.
//
// The count here is deliberately past 1000, and the shapes are mixed so both
// the IN-list arm (exact namespaces) and the LIKE arm (prefix namespaces)
// carry more terms than a flat chain could.
func TestRecall_AcrossEveryRegisteredNamespace(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	in := sampleInput("prefs.output_style")
	if _, err := ms.WriteRevision(ctx, in); err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	// One namespace that actually holds the row, plus enough empty ones to
	// push the filter past the old ceiling.
	namespaces := []string{in.Namespace}
	for i := 0; i < 1200; i++ {
		if i%2 == 0 {
			namespaces = append(namespaces, fmt.Sprintf("user/filler%d/memory/notes", i))
		} else {
			namespaces = append(namespaces, fmt.Sprintf("user/filler%d/memory", i))
		}
	}

	for _, tc := range []struct {
		name  string
		input memory.RecallInput
	}{
		{"metadata path", memory.RecallInput{Ranking: memory.RankingActivation}},
		{"chronological path", memory.RecallInput{Ranking: memory.RankingChronological}},
		{"lexical path", memory.RecallInput{
			Ranking:    memory.RankingRelevance,
			SearchMode: memory.SearchModeLexical,
			Query:      "terse",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			input.Namespaces = namespaces
			results, err := ms.Recall(ctx, input)
			if err != nil {
				t.Fatalf("Recall: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected the single written revision, got %d results", len(results))
			}
			if results[0].Revision.Namespace != in.Namespace {
				t.Errorf("namespace = %q, want %q", results[0].Revision.Namespace, in.Namespace)
			}
		})
	}
}

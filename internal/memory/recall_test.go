package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/cortex/internal/memory"
)

func writeWithOrigin(t *testing.T, ms *memory.Store, key string, origin memory.Origin) memory.Revision {
	t.Helper()
	in := sampleInput(key)
	in.Origin = origin
	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("WriteRevision(%s): %v", key, err)
	}
	return rev
}

func TestRecall_ActivationRanking(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	writeWithOrigin(t, ms, "act.project", memory.OriginProject)
	writeWithOrigin(t, ms, "act.user", memory.OriginUser)
	writeWithOrigin(t, ms, "act.feedback", memory.OriginFeedback)

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingActivation,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Feedback (1.3) > User (1.1) > Project (1.0) by origin weight.
	if results[0].Revision.Origin != memory.OriginFeedback {
		t.Errorf("expected feedback first, got %s", results[0].Revision.Origin)
	}
	if results[1].Revision.Origin != memory.OriginUser {
		t.Errorf("expected user second, got %s", results[1].Revision.Origin)
	}
	if results[2].Revision.Origin != memory.OriginProject {
		t.Errorf("expected project third, got %s", results[2].Revision.Origin)
	}
}

func TestRecall_ChronologicalOrdering(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// created_at is stored with second precision (time.DateTime format),
	// so we need 1+ second gaps to ensure distinct timestamps.
	rev1, err := ms.WriteRevision(context.Background(), sampleInput("chrono.a"))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)

	rev2, err := ms.WriteRevision(context.Background(), sampleInput("chrono.b"))
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)

	rev3, err := ms.WriteRevision(context.Background(), sampleInput("chrono.c"))
	if err != nil {
		t.Fatalf("write 3: %v", err)
	}
	_ = rev1

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingChronological,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Newest first.
	if results[0].Revision.RevisionID != rev3.RevisionID {
		t.Errorf("expected newest first, got %s", results[0].Revision.RevisionID)
	}
	if results[1].Revision.RevisionID != rev2.RevisionID {
		t.Errorf("expected second newest second, got %s", results[1].Revision.RevisionID)
	}
}

func TestRecall_MultiNamespace(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	ns1 := "user/chrispian/memory"
	ns2 := "user/chrispian/project/cortex/memory"

	in1 := sampleInput("multi.a")
	in1.Namespace = ns1
	if _, err := ms.WriteRevision(context.Background(), in1); err != nil {
		t.Fatalf("write ns1: %v", err)
	}

	in2 := sampleInput("multi.b")
	in2.Namespace = ns2
	if _, err := ms.WriteRevision(context.Background(), in2); err != nil {
		t.Fatalf("write ns2: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{ns1, ns2},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestRecall_DeprecatedFilteredByDefault(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev1, err := ms.WriteRevision(context.Background(), sampleInput("dep.key"))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}

	// Supersede rev1, which auto-deprecates it.
	in2 := sampleInput("dep.key")
	in2.Supersedes = rev1.RevisionID
	_, err = ms.WriteRevision(context.Background(), in2)
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	// Default statuses are canonical, reviewed, draft — deprecated excluded.
	for _, r := range results {
		if r.Revision.Status == memory.StatusDeprecated {
			t.Fatalf("deprecated revision should not appear in default recall")
		}
	}
}

func TestRecall_DeprecatedIncludedWhenRequested(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev1, err := ms.WriteRevision(context.Background(), sampleInput("dep2.key"))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}

	in2 := sampleInput("dep2.key")
	in2.Supersedes = rev1.RevisionID
	_, err = ms.WriteRevision(context.Background(), in2)
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces:    []string{"user/chrispian/memory"},
		RevisionScope: memory.RevisionScopeTimeline,
		Filters: memory.RecallFilters{
			Statuses: []memory.Status{memory.StatusDeprecated},
		},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one deprecated revision")
	}
	for _, r := range results {
		if r.Revision.Status != memory.StatusDeprecated {
			t.Fatalf("expected only deprecated, got %s", r.Revision.Status)
		}
	}
}

func TestRecall_TTLExpiry(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("ttl.key")
	in.TTL = time.Millisecond // very short TTL
	if _, err := ms.WriteRevision(context.Background(), in); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Sleep to ensure expiry.
	time.Sleep(50 * time.Millisecond)

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, r := range results {
		if r.Revision.MemoryKey == "ttl.key" {
			t.Fatal("expired revision should not be returned")
		}
	}
}

func TestRecall_OriginFilter(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	writeWithOrigin(t, ms, "orig.fb", memory.OriginFeedback)
	writeWithOrigin(t, ms, "orig.usr", memory.OriginUser)
	writeWithOrigin(t, ms, "orig.prj", memory.OriginProject)

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Filters: memory.RecallFilters{
			Origins: []memory.Origin{memory.OriginFeedback},
		},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Revision.Origin != memory.OriginFeedback {
		t.Fatalf("expected feedback origin, got %s", results[0].Revision.Origin)
	}
}

func TestRecall_ConfidenceFilter(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	inLow := sampleInput("conf.low")
	inLow.Confidence = 0.5
	if _, err := ms.WriteRevision(context.Background(), inLow); err != nil {
		t.Fatalf("write low: %v", err)
	}

	inHigh := sampleInput("conf.high")
	inHigh.Confidence = 0.9
	if _, err := ms.WriteRevision(context.Background(), inHigh); err != nil {
		t.Fatalf("write high: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Filters: memory.RecallFilters{
			ConfidenceMin: 0.8,
		},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Revision.MemoryKey != "conf.high" {
		t.Fatalf("expected high confidence memory, got %s", results[0].Revision.MemoryKey)
	}
}

func TestRecall_TagAnyMatch(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	in1 := sampleInput("tag.a")
	in1.Tags = []string{"go", "testing"}
	if _, err := ms.WriteRevision(context.Background(), in1); err != nil {
		t.Fatalf("write 1: %v", err)
	}

	in2 := sampleInput("tag.b")
	in2.Tags = []string{"python", "ml"}
	if _, err := ms.WriteRevision(context.Background(), in2); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	in3 := sampleInput("tag.c")
	in3.Tags = []string{"go", "concurrency"}
	if _, err := ms.WriteRevision(context.Background(), in3); err != nil {
		t.Fatalf("write 3: %v", err)
	}

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Filters: memory.RecallFilters{
			Tags: []string{"go"},
		},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestRecall_TimelineScope(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	key := "timeline.key"
	rev1, err := ms.WriteRevision(context.Background(), sampleInput(key))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}

	in2 := sampleInput(key)
	in2.Supersedes = rev1.RevisionID
	rev2, err := ms.WriteRevision(context.Background(), in2)
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}

	in3 := sampleInput(key)
	in3.Supersedes = rev2.RevisionID
	_, err = ms.WriteRevision(context.Background(), in3)
	if err != nil {
		t.Fatalf("write 3: %v", err)
	}

	// Timeline scope should return all 3 revisions (including deprecated).
	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces:    []string{"user/chrispian/memory"},
		RevisionScope: memory.RevisionScopeTimeline,
		Filters: memory.RecallFilters{
			Statuses: []memory.Status{
				memory.StatusDraft,
				memory.StatusDeprecated,
			},
		},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 revisions in timeline, got %d", len(results))
	}
}

func TestRecall_SimilarityReturnsUnavailable(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	_, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingSimilarity,
		Query:      "test query",
	})
	if err == nil {
		t.Fatal("expected error for similarity ranking")
	}
	if !errors.Is(err, memory.ErrSimilarityUnavailable) {
		t.Fatalf("expected ErrSimilarityUnavailable, got %v", err)
	}
}

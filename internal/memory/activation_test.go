package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func TestReinforceAccessIncrementsActivation(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))
	st1, _ := ms.GetState(ctx, rev.MemoryID)
	if st1.Activation != 1.0 {
		t.Fatalf("initial activation: %v", st1.Activation)
	}

	// Trigger a recall with activation ranking. This should reinforce.
	_, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingActivation,
	})
	if err != nil {
		t.Fatal(err)
	}

	st2, _ := ms.GetState(ctx, rev.MemoryID)
	if st2.Activation <= st1.Activation {
		t.Errorf("expected activation to increase, got %v -> %v", st1.Activation, st2.Activation)
	}
	if st2.AccessCount != 1 {
		t.Errorf("expected access_count=1, got %d", st2.AccessCount)
	}
	if st2.LastAccessedAt == nil {
		t.Error("expected last_accessed_at to be set")
	}
}

func TestReinforcementDiminishingReturns(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))

	// Reinforce 100 times; activation should stay below 2.5 (rough cap).
	for i := 0; i < 100; i++ {
		_, _ = ms.Recall(ctx, memory.RecallInput{
			Namespaces: []string{"user/chrispian/memory"},
			Ranking:    memory.RankingActivation,
		})
	}
	st, _ := ms.GetState(ctx, rev.MemoryID)
	if st.Activation > 2.5 {
		t.Errorf("activation runaway: %v (expected below 2.5)", st.Activation)
	}
	if st.Activation <= 1.0 {
		t.Errorf("activation did not grow: %v", st.Activation)
	}
}

// TestReinforceAccessAppliesToSimilarityRanking widens the reinforcement
// invariant beyond activation mode: dense-only queries must also count
// as access so hot memories don't stop decaying when agents switch to
// semantic recall (EPIC-20260414-19124, TASK-005).
func TestReinforceAccessAppliesToSimilarityRanking(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("sim.reinforce"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed: %v", err)
	}

	before, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("get state before: %v", err)
	}

	if _, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingSimilarity,
		Query:      "test query",
	}); err != nil {
		t.Fatalf("Recall similarity: %v", err)
	}

	after, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("get state after: %v", err)
	}
	if after.AccessCount <= before.AccessCount {
		t.Errorf("similarity ranking must reinforce access; before=%d after=%d",
			before.AccessCount, after.AccessCount)
	}
	if after.LastAccessedAt == nil {
		t.Error("last_accessed_at should be set after similarity recall")
	}
}

// TestReinforceAccessAppliesToChronologicalRanking widens the invariant
// to chronological recall too — any caller expressing interest in a
// memory counts as an access (TASK-005).
func TestReinforceAccessAppliesToChronologicalRanking(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("chrono.reinforce"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	before, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("get state before: %v", err)
	}

	if _, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingChronological,
	}); err != nil {
		t.Fatalf("Recall chronological: %v", err)
	}

	after, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("get state after: %v", err)
	}
	if after.AccessCount <= before.AccessCount {
		t.Errorf("chronological ranking must reinforce access; before=%d after=%d",
			before.AccessCount, after.AccessCount)
	}
}

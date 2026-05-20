package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestGetCurrentReinforcedIncrementsActivation verifies that a deliberate
// read via GetCurrentReinforced (memory_get) reinforces activation,
// access_count, and last_accessed_at.
func TestGetCurrentReinforcedIncrementsActivation(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))
	st1, _ := ms.GetState(ctx, rev.MemoryID)
	if st1.Activation != 1.0 {
		t.Fatalf("initial activation: %v", st1.Activation)
	}

	// A deliberate head read should reinforce.
	if _, err := ms.GetCurrentReinforced(ctx, rev.Namespace, rev.MemoryKey); err != nil {
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

// TestGetRevisionByIDReinforcedIncrementsActivation verifies that pulling a
// specific revision by ID (memory_get_revision) reinforces the parent memory.
func TestGetRevisionByIDReinforcedIncrementsActivation(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))
	st1, _ := ms.GetState(ctx, rev.MemoryID)

	if _, err := ms.GetRevisionByIDReinforced(ctx, rev.RevisionID); err != nil {
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

// TestReinforcementDiminishingReturns verifies the diminishing-returns
// formula keeps activation bounded under repeated deliberate reads.
func TestReinforcementDiminishingReturns(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))

	// Reinforce 100 times via deliberate reads; activation should stay
	// below 2.5 (rough cap) but grow above its 1.0 starting point.
	for i := 0; i < 100; i++ {
		if _, err := ms.GetCurrentReinforced(ctx, rev.Namespace, rev.MemoryKey); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := ms.GetState(ctx, rev.MemoryID)
	if st.Activation > 2.5 {
		t.Errorf("activation runaway: %v (expected below 2.5)", st.Activation)
	}
	if st.Activation <= 1.0 {
		t.Errorf("activation did not grow: %v", st.Activation)
	}
}

// TestRecallDoesNotReinforceActivation locks in the corrected design:
// being returned by a search is the system's guess, not a deliberate
// read, so recall must NOT touch activation/access_count/last_accessed.
func TestRecallDoesNotReinforceActivation(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, _ := ms.WriteRevision(ctx, sampleInput("user.x"))
	before, _ := ms.GetState(ctx, rev.MemoryID)

	if _, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingActivation,
	}); err != nil {
		t.Fatal(err)
	}

	after, _ := ms.GetState(ctx, rev.MemoryID)
	if after.Activation != before.Activation {
		t.Errorf("recall must not change activation; before=%v after=%v",
			before.Activation, after.Activation)
	}
	if after.AccessCount != before.AccessCount {
		t.Errorf("recall must not change access_count; before=%d after=%d",
			before.AccessCount, after.AccessCount)
	}
	if after.LastAccessedAt != before.LastAccessedAt {
		t.Errorf("recall must not change last_accessed_at; before=%v after=%v",
			before.LastAccessedAt, after.LastAccessedAt)
	}
}

// TestSimilarityRecallDoesNotReinforceAccess confirms the no-reinforce
// invariant holds for similarity ranking too.
func TestSimilarityRecallDoesNotReinforceAccess(t *testing.T) {
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
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingSimilarity,
		Query:      "test query",
	}); err != nil {
		t.Fatalf("Recall similarity: %v", err)
	}

	after, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("get state after: %v", err)
	}
	if after.AccessCount != before.AccessCount {
		t.Errorf("similarity recall must not reinforce access; before=%d after=%d",
			before.AccessCount, after.AccessCount)
	}
}

// TestChronologicalRecallDoesNotReinforceAccess confirms the no-reinforce
// invariant holds for chronological ranking too.
func TestChronologicalRecallDoesNotReinforceAccess(t *testing.T) {
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
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}); err != nil {
		t.Fatalf("Recall chronological: %v", err)
	}

	after, err := ms.GetState(ctx, rev.MemoryID)
	if err != nil {
		t.Fatalf("get state after: %v", err)
	}
	if after.AccessCount != before.AccessCount {
		t.Errorf("chronological recall must not reinforce access; before=%d after=%d",
			before.AccessCount, after.AccessCount)
	}
}

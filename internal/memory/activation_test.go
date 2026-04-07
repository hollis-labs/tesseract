package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/cortex/internal/memory"
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

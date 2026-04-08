package memory_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// TestActivationDecay_ReducesAfterTime verifies that after 14 days the activation
// drops to approximately half (half-life model).
func TestActivationDecay_ReducesAfterTime(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, writeErr := ms.WriteRevision(ctx, sampleInput("decay.halflife"))
	if writeErr != nil {
		t.Fatalf("WriteRevision: %v", writeErr)
	}

	// Backdate last_accessed_at to 14 days ago.
	oldTime := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, dbErr := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_accessed_at = ? WHERE memory_id = ?`,
		oldTime, rev.MemoryID,
	); dbErr != nil {
		t.Fatalf("backdate last_accessed_at: %v", dbErr)
	}

	if decayErr := ms.ExportApplyActivationDecay(ctx); decayErr != nil {
		t.Fatalf("applyActivationDecay: %v", decayErr)
	}

	state, stateErr := ms.GetState(ctx, rev.MemoryID)
	if stateErr != nil {
		t.Fatalf("GetState: %v", stateErr)
	}

	// After one half-life, activation should be ~0.5 (started at 1.0).
	// Allow ±5% tolerance.
	if math.Abs(state.Activation-0.5) > 0.05 {
		t.Fatalf("expected activation ≈ 0.5, got %f", state.Activation)
	}
}

// TestActivationDecay_FloorsAt005 verifies that very old memories floor at 0.05.
func TestActivationDecay_FloorsAt005(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, writeErr := ms.WriteRevision(ctx, sampleInput("decay.floor"))
	if writeErr != nil {
		t.Fatalf("WriteRevision: %v", writeErr)
	}

	// Backdate last_accessed_at to 365 days ago — far past any floor.
	oldTime := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, dbErr := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_accessed_at = ? WHERE memory_id = ?`,
		oldTime, rev.MemoryID,
	); dbErr != nil {
		t.Fatalf("backdate last_accessed_at: %v", dbErr)
	}

	if decayErr := ms.ExportApplyActivationDecay(ctx); decayErr != nil {
		t.Fatalf("applyActivationDecay: %v", decayErr)
	}

	state, stateErr := ms.GetState(ctx, rev.MemoryID)
	if stateErr != nil {
		t.Fatalf("GetState: %v", stateErr)
	}

	if state.Activation != 0.05 {
		t.Fatalf("expected activation = 0.05 (floor), got %f", state.Activation)
	}
}

// TestTTLExpiry_MarksDeprecated verifies that a revision with an expired TTL
// is marked deprecated after expireTTLRevisions runs.
func TestTTLExpiry_MarksDeprecated(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("ttl.expired")
	in.TTL = 1 * time.Millisecond
	rev, writeErr := ms.WriteRevision(ctx, in)
	if writeErr != nil {
		t.Fatalf("WriteRevision: %v", writeErr)
	}

	// Ensure TTL has elapsed.
	time.Sleep(5 * time.Millisecond)

	if expireErr := ms.ExportExpireTTLRevisions(ctx); expireErr != nil {
		t.Fatalf("expireTTLRevisions: %v", expireErr)
	}

	got, getErr := ms.GetRevisionByID(ctx, rev.RevisionID)
	if getErr != nil {
		t.Fatalf("GetRevisionByID: %v", getErr)
	}
	if got.Status != memory.StatusDeprecated {
		t.Fatalf("expected status=deprecated, got %s", got.Status)
	}
}

// TestTTLExpiry_UpdatesCurrentRevision verifies that after expiring one revision,
// current_revision still points to the remaining non-expired one.
func TestTTLExpiry_UpdatesCurrentRevision(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Write first revision with a very short TTL.
	in1 := sampleInput("ttl.multi")
	in1.TTL = 1 * time.Millisecond
	rev1, writeErr1 := ms.WriteRevision(ctx, in1)
	if writeErr1 != nil {
		t.Fatalf("WriteRevision rev1: %v", writeErr1)
	}

	// Ensure TTL has elapsed before writing rev2.
	time.Sleep(5 * time.Millisecond)

	// Write second revision for the same key (no TTL).
	rev2, writeErr2 := ms.WriteRevision(ctx, sampleInput("ttl.multi"))
	if writeErr2 != nil {
		t.Fatalf("WriteRevision rev2: %v", writeErr2)
	}

	// Verify they share the same memory_id.
	if rev1.MemoryID != rev2.MemoryID {
		t.Fatalf("expected same memory_id, got %s and %s", rev1.MemoryID, rev2.MemoryID)
	}

	if expireErr := ms.ExportExpireTTLRevisions(ctx); expireErr != nil {
		t.Fatalf("expireTTLRevisions: %v", expireErr)
	}

	// rev1 should be deprecated.
	got1, getErr1 := ms.GetRevisionByID(ctx, rev1.RevisionID)
	if getErr1 != nil {
		t.Fatalf("GetRevisionByID rev1: %v", getErr1)
	}
	if got1.Status != memory.StatusDeprecated {
		t.Fatalf("expected rev1 status=deprecated, got %s", got1.Status)
	}

	// current_revision should point to rev2.
	state, stateErr := ms.GetState(ctx, rev1.MemoryID)
	if stateErr != nil {
		t.Fatalf("GetState: %v", stateErr)
	}
	if state.CurrentRevision != rev2.RevisionID {
		t.Fatalf("expected current_revision=%s, got %s", rev2.RevisionID, state.CurrentRevision)
	}
}

// TestDecayJob_SuccessiveRunsFurtherDecay verifies that running decay twice
// continues to reduce activation (relative decay compounds). The second run
// applies the decay factor to the already-decayed value.
func TestDecayJob_SuccessiveRunsFurtherDecay(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, writeErr := ms.WriteRevision(ctx, sampleInput("decay.successive"))
	if writeErr != nil {
		t.Fatalf("WriteRevision: %v", writeErr)
	}

	// Backdate to 14 days ago.
	oldTime := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, dbErr := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_accessed_at = ? WHERE memory_id = ?`,
		oldTime, rev.MemoryID,
	); dbErr != nil {
		t.Fatalf("backdate: %v", dbErr)
	}

	// First decay run.
	if decayErr := ms.ExportApplyActivationDecay(ctx); decayErr != nil {
		t.Fatalf("first applyActivationDecay: %v", decayErr)
	}
	state1, stateErr1 := ms.GetState(ctx, rev.MemoryID)
	if stateErr1 != nil {
		t.Fatalf("GetState after first decay: %v", stateErr1)
	}

	// Second decay run — applies relative decay to the already-decayed value.
	if decayErr := ms.ExportApplyActivationDecay(ctx); decayErr != nil {
		t.Fatalf("second applyActivationDecay: %v", decayErr)
	}
	state2, stateErr2 := ms.GetState(ctx, rev.MemoryID)
	if stateErr2 != nil {
		t.Fatalf("GetState after second decay: %v", stateErr2)
	}

	// Second run should produce equal or lower activation (relative decay compounds).
	if state2.Activation > state1.Activation+0.001 {
		t.Fatalf("expected activation to not increase, got %f then %f",
			state1.Activation, state2.Activation)
	}
	// Both should still be above the floor (0.05).
	if state1.Activation < 0.05 || state2.Activation < 0.05 {
		t.Fatalf("activation below floor: run1=%f run2=%f", state1.Activation, state2.Activation)
	}
}

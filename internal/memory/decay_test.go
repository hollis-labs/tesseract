package memory_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
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

	// Backdate the decay baseline to 14 days ago.
	oldTime := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, dbErr := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_decayed_at = ? WHERE memory_id = ?`,
		oldTime, rev.MemoryID,
	); dbErr != nil {
		t.Fatalf("backdate last_decayed_at: %v", dbErr)
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

	// Backdate the decay baseline to 365 days ago — far past any floor.
	oldTime := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, dbErr := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_decayed_at = ? WHERE memory_id = ?`,
		oldTime, rev.MemoryID,
	); dbErr != nil {
		t.Fatalf("backdate last_decayed_at: %v", dbErr)
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

func TestTTLExpiryComparesRFC3339NanoChronologically(t *testing.T) {
	cases := []struct {
		name       string
		expiresAt  string
		now        time.Time
		wantStatus memory.Status
	}{
		{
			name:       "later prefix timestamp remains live",
			expiresAt:  "2026-09-04T13:34:55.092342000Z",
			now:        time.Date(2026, 9, 4, 13, 34, 55, 92_340_000, time.UTC),
			wantStatus: memory.StatusDraft,
		},
		{
			name:       "earlier short timestamp expires",
			expiresAt:  "2026-09-04T13:34:55.092340000Z",
			now:        time.Date(2026, 9, 4, 13, 34, 55, 92_340_001, time.UTC),
			wantStatus: memory.StatusDeprecated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ms, cleanup := newTestStore(t)
			defer cleanup()

			in := sampleInput("ttl.prefix")
			in.TTL = time.Hour
			rev, writeErr := ms.WriteRevision(ctx, in)
			if writeErr != nil {
				t.Fatalf("WriteRevision: %v", writeErr)
			}
			if _, dbErr := ms.DB().ExecContext(ctx,
				`UPDATE memory_revisions SET expires_at = ? WHERE revision_id = ?`,
				tc.expiresAt, rev.RevisionID); dbErr != nil {
				t.Fatalf("set deterministic expiry: %v", dbErr)
			}

			if expireErr := ms.ExportExpireTTLRevisionsAt(ctx, tc.now); expireErr != nil {
				t.Fatalf("ExportExpireTTLRevisionsAt: %v", expireErr)
			}
			got, getErr := ms.GetRevisionByID(ctx, rev.RevisionID)
			if getErr != nil {
				t.Fatalf("GetRevisionByID: %v", getErr)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s (expires_at=%s now=%s)",
					got.Status, tc.wantStatus, tc.expiresAt, tc.now.Format(time.RFC3339Nano))
			}
		})
	}
}

// TestDecayJob_SuccessiveRunsDecayFurtherOnlyAsTimePasses verifies the two
// halves of "successive runs": a second run over a further interval reduces
// activation again, and the reduction is bounded by the floor. Whether a repeat
// run with no interval between changes anything is the separate and sharper
// property in decay_baseline_test.go.
func TestDecayJob_SuccessiveRunsDecayFurtherOnlyAsTimePasses(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, writeErr := ms.WriteRevision(ctx, sampleInput("decay.successive"))
	if writeErr != nil {
		t.Fatalf("WriteRevision: %v", writeErr)
	}

	base := time.Now().UTC()
	if _, dbErr := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_decayed_at = ? WHERE memory_id = ?`,
		base.Format(time.RFC3339Nano), rev.MemoryID,
	); dbErr != nil {
		t.Fatalf("set decay baseline: %v", dbErr)
	}

	// First run: one half-life on from the baseline.
	if decayErr := ms.ExportApplyActivationDecayAt(ctx, base.Add(336*time.Hour)); decayErr != nil {
		t.Fatalf("first applyActivationDecay: %v", decayErr)
	}
	state1, stateErr1 := ms.GetState(ctx, rev.MemoryID)
	if stateErr1 != nil {
		t.Fatalf("GetState after first decay: %v", stateErr1)
	}

	// Second run: another half-life on again.
	if decayErr := ms.ExportApplyActivationDecayAt(ctx, base.Add(672*time.Hour)); decayErr != nil {
		t.Fatalf("second applyActivationDecay: %v", decayErr)
	}
	state2, stateErr2 := ms.GetState(ctx, rev.MemoryID)
	if stateErr2 != nil {
		t.Fatalf("GetState after second decay: %v", stateErr2)
	}

	if state2.Activation >= state1.Activation {
		t.Fatalf("a further half-life did not reduce activation: %f then %f",
			state1.Activation, state2.Activation)
	}
	// Both should still be above the floor (0.05): two half-lives from the 1.0
	// insert default is 0.25, well clear of it.
	if state1.Activation < 0.05 || state2.Activation < 0.05 {
		t.Fatalf("activation below floor: run1=%f run2=%f", state1.Activation, state2.Activation)
	}
}

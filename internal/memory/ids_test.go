package memory_test

import (
	"testing"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func TestNewULIDUnique(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := memory.NewULID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ULID at iteration %d: %s", i, id)
		}
		seen[id] = struct{}{}
		if len(id) != 26 {
			t.Fatalf("expected ULID length 26, got %d for %q", len(id), id)
		}
	}
}

// TestNewULIDSortableAcrossTime verifies the coarse sortability property
// that D-core ranking and history ordering rely on: ULIDs generated later
// in wall-clock time sort greater than ULIDs generated earlier, once a
// millisecond boundary has been crossed. ULID's time prefix guarantees
// this at millisecond granularity; within a single millisecond, order is
// not guaranteed (NewULID does not use monotonic entropy).
func TestNewULIDSortableAcrossTime(t *testing.T) {
	const batchSize = 50
	before := make([]string, batchSize)
	for i := range before {
		before[i] = memory.NewULID()
	}
	time.Sleep(3 * time.Millisecond)
	after := make([]string, batchSize)
	for i := range after {
		after[i] = memory.NewULID()
	}

	var maxBefore string
	for _, id := range before {
		if id > maxBefore {
			maxBefore = id
		}
	}
	for i, id := range after {
		if id <= maxBefore {
			t.Fatalf("after[%d]=%q sorts <= max(before)=%q; ULIDs are not sortable across milliseconds", i, id, maxBefore)
		}
	}
}

func TestValidOriginTrigger(t *testing.T) {
	t.Run("origin", func(t *testing.T) {
		for _, o := range []memory.Origin{
			memory.OriginUser, memory.OriginFeedback, memory.OriginProject,
			memory.OriginReference, memory.OriginObservation,
		} {
			if !o.Valid() {
				t.Errorf("expected %q to be valid", o)
			}
		}
		if memory.Origin("bogus").Valid() {
			t.Errorf("expected 'bogus' origin to be invalid")
		}
	})
	t.Run("trigger", func(t *testing.T) {
		for _, tr := range []memory.Trigger{
			memory.TriggerExplicit, memory.TriggerPostCompact, memory.TriggerPerTurn,
			memory.TriggerPromotion, memory.TriggerManual,
		} {
			if !tr.Valid() {
				t.Errorf("expected %q to be valid", tr)
			}
		}
		if memory.Trigger("nope").Valid() {
			t.Errorf("expected 'nope' trigger to be invalid")
		}
	})
}

func TestValidStatus(t *testing.T) {
	for _, s := range []memory.Status{
		memory.StatusDraft, memory.StatusReviewed, memory.StatusCanonical, memory.StatusDeprecated,
	} {
		if !s.Valid() {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if memory.Status("bogus").Valid() {
		t.Errorf("expected 'bogus' status to be invalid")
	}
}

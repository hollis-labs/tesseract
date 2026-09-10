package memory

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// TestEventLogKeysetBreaksTiesInsideOneTimestamp is the tiebreaker's own test,
// and it is an internal one because the case it covers cannot be produced
// through the public write path.
//
// The keyset predicate and the ORDER BY both fall back to revision_id when two
// rows share a created_at. That fallback has to exist in BOTH or a page
// boundary landing inside a group of equal timestamps skips a row (predicate
// stricter than the sort) or repeats one (predicate looser). A first draft of
// the external log tests claimed writing in a tight loop would produce
// collisions and prove this; it does not — every WriteRevision runs a
// transaction, so the timestamps are always distinct in practice, and dropping
// the tiebreaker from the predicate left that suite green.
//
// So the collision is manufactured: write the rows normally, then stamp three
// of them with one identical created_at through the store's own handle. That
// is not a state the write path can reach, and it is exactly the state a
// coarser clock, a restored backup or a batch import would reach.
//
// What this covers and what it cannot: removing the tiebreaker from the KEYSET
// PREDICATE fails here (rows go missing from the paged walk). Removing it from
// the ORDER BY does not, and cannot — SQLite returns tied rows in rowid order,
// revision_ids are ULIDs that increase with insertion, so the natural order
// already equals the tiebreak order and dropping it changes nothing observable
// through the store. That is the same blind spot sortRecallResults documents
// for recall's comparator, where the answer was to test the comparator
// directly; this ordering lives in SQL and has no comparator to reach. It stays
// in the ORDER BY because the predicate depends on it: the predicate is written
// to walk the sort, and a sort that stopped promising the tiebreak would leave
// the predicate walking an ordering nothing guarantees.
func TestEventLogKeysetBreaksTiesInsideOneTimestamp(t *testing.T) {
	ms, cleanup := newEventLogTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const ns = "user/chrispian/event/reasoning"
	var ids []string
	for i := 0; i < 5; i++ {
		rev, err := ms.WriteRevision(ctx, WriteInput{
			Domain:     domains.Event,
			Namespace:  ns,
			Author:     Author{AgentID: "test-agent"},
			Trigger:    TriggerManual,
			SessionID:  "manual:tie",
			Origin:     OriginObservation,
			Confidence: 0.9,
			Status:     StatusCanonical,
			Payload:    Payload{Summary: "tie candidate"},
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		ids = append(ids, rev.RevisionID)
	}

	// EVERY timestamp is pinned, not just the tied ones. Stamping a subset
	// leaves the untouched rows at wall-clock time, which moves the tied group
	// to one end of the ordering depending on when the suite runs — a test that
	// asserts an order has to control the whole order.
	//
	// The middle three collapse onto one instant. revision_ids are ULIDs that
	// increase with insertion, so within the tie the expected order is still
	// ids[1] < ids[2] < ids[3]; that is what makes the assertion checkable
	// rather than arbitrary.
	const tied = "2026-09-10T12:00:00.000000000Z"
	stamps := []string{
		"2026-09-10T09:00:00.000000000Z",
		tied, tied, tied,
		"2026-09-10T18:00:00.000000000Z",
	}
	for i, id := range ids {
		if _, err := ms.db.ExecContext(ctx,
			`UPDATE memory_revisions SET created_at = ? WHERE revision_id = ?`, stamps[i], id); err != nil {
			t.Fatalf("pin timestamp %d: %v", i, err)
		}
	}

	for _, dir := range []LogDirection{LogNewestFirst, LogOldestFirst} {
		t.Run(string(dir), func(t *testing.T) {
			// Page size 1 puts a boundary between every pair, including the two
			// boundaries inside the tied group.
			var seen []string
			cursor := ""
			for page := 0; page < 20; page++ {
				got, err := ms.ReadEventLog(ctx, EventLogInput{
					Namespaces: []string{ns},
					Direction:  dir,
					Limit:      1,
					Cursor:     cursor,
				})
				if err != nil {
					t.Fatalf("page %d: %v", page, err)
				}
				for _, rev := range got.Entries {
					seen = append(seen, rev.RevisionID)
				}
				if got.Manifest.NextCursor == nil {
					break
				}
				cursor = *got.Manifest.NextCursor
			}

			// Exactness first: no row skipped, none repeated.
			counts := map[string]int{}
			for _, id := range seen {
				counts[id]++
			}
			for _, id := range ids {
				if counts[id] != 1 {
					t.Errorf("revision %s appeared %d times across the paged walk, want exactly 1 "+
						"(the keyset predicate and the ORDER BY disagree inside a timestamp tie)",
						id, counts[id])
				}
			}
			if len(seen) != len(ids) {
				t.Fatalf("paged walk saw %d rows, want %d", len(seen), len(ids))
			}

			// And the order the tiebreaker specifies, not merely some order.
			want := make([]string, len(ids))
			copy(want, ids)
			if dir == LogNewestFirst {
				for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
					want[i], want[j] = want[j], want[i]
				}
			}
			for i := range want {
				if seen[i] != want[i] {
					t.Errorf("seen[%d] = %s, want %s", i, seen[i], want[i])
				}
			}
		})
	}
}

// TestEventLogSingleQueryMatchesPagedWalk pins the two readings together: one
// unpaged read and a paged walk over the same tied corpus must produce the same
// sequence. A predicate that agreed with itself but not with the ORDER BY would
// pass the exactness check above by shifting the whole order consistently.
func TestEventLogSingleQueryMatchesPagedWalk(t *testing.T) {
	ms, cleanup := newEventLogTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const ns = "user/chrispian/event/journal"
	var ids []string
	for i := 0; i < 6; i++ {
		rev, err := ms.WriteRevision(ctx, WriteInput{
			Domain:     domains.Event,
			Namespace:  ns,
			Author:     Author{AgentID: "test-agent"},
			Trigger:    TriggerManual,
			SessionID:  "manual:tie",
			Origin:     OriginObservation,
			Confidence: 0.9,
			Status:     StatusCanonical,
			Payload:    Payload{Summary: "entry"},
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		ids = append(ids, rev.RevisionID)
	}
	// Two separate ties, so a boundary can land inside either. Every row is
	// pinned for the same reason as above: a partially-pinned corpus reorders
	// itself with the wall clock.
	stamps := []string{
		"2026-09-10T09:00:00.000000000Z",
		"2026-09-10T09:00:00.000000000Z",
		"2026-09-10T12:00:00.000000000Z",
		"2026-09-10T15:00:00.000000000Z",
		"2026-09-10T18:00:00.000000000Z",
		"2026-09-10T18:00:00.000000000Z",
	}
	for i, id := range ids {
		if _, err := ms.db.ExecContext(ctx,
			`UPDATE memory_revisions SET created_at = ? WHERE revision_id = ?`, stamps[i], id); err != nil {
			t.Fatalf("pin timestamp %d: %v", i, err)
		}
	}

	whole, err := ms.ReadEventLog(ctx, EventLogInput{Namespaces: []string{ns}, Limit: 100})
	if err != nil {
		t.Fatalf("unpaged read: %v", err)
	}

	var paged []string
	cursor := ""
	for page := 0; page < 20; page++ {
		got, err := ms.ReadEventLog(ctx, EventLogInput{
			Namespaces: []string{ns}, Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, rev := range got.Entries {
			paged = append(paged, rev.RevisionID)
		}
		if got.Manifest.NextCursor == nil {
			break
		}
		cursor = *got.Manifest.NextCursor
	}

	if len(paged) != len(whole.Entries) {
		t.Fatalf("paged walk returned %d rows, unpaged read returned %d", len(paged), len(whole.Entries))
	}
	for i, rev := range whole.Entries {
		if paged[i] != rev.RevisionID {
			t.Errorf("position %d: paged=%s unpaged=%s — the keyset predicate does not walk "+
				"the ordering the ORDER BY produces", i, paged[i], rev.RevisionID)
		}
	}
}

func newEventLogTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := NewStore(cs.DB(), nil, "", 0, NoopQueue{})
	return ms, func() { _ = cs.Close() }
}

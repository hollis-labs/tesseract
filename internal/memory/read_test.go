package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestGetCurrent(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_, err := ms.WriteRevision(ctx, sampleInput("user.preferences.verbosity"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	in2 := sampleInput("user.preferences.verbosity")
	in2.Summary = "updated"
	rev2, err := ms.WriteRevision(ctx, in2)
	if err != nil {
		t.Fatal(err)
	}

	cur, err := ms.GetCurrent(ctx, "user/chrispian/memory/notes", "user.preferences.verbosity")
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if cur.RevisionID != rev2.RevisionID {
		t.Errorf("got %q, want latest %q", cur.RevisionID, rev2.RevisionID)
	}
	if cur.Payload.Summary != "updated" {
		t.Errorf("payload summary: got %q, want %q", cur.Payload.Summary, "updated")
	}
}

func TestGetCurrentNotFound(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	_, err := ms.GetCurrent(context.Background(), "user/chrispian/memory/notes", "nothing.here")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetHistoryReturnsAllRevisionsNewestFirst(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	var written []string
	for i := 0; i < 3; i++ {
		in := sampleInput("user.preferences.verbosity")
		in.Summary = "v" + string(rune('0'+i))
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, rev.RevisionID)
	}

	// Fixed-width fractional seconds keep SQLite's TEXT order chronological
	// even for the prefix-shaped instants that RFC3339Nano used to invert.
	stamps := []string{
		"2026-09-04T13:34:55.092339000Z",
		"2026-09-04T13:34:55.092340000Z",
		"2026-09-04T13:34:55.092342000Z",
	}
	for i, revisionID := range written {
		if _, err := ms.DB().ExecContext(ctx,
			`UPDATE memory_revisions SET created_at = ? WHERE revision_id = ?`,
			stamps[i], revisionID); err != nil {
			t.Fatalf("set deterministic timestamp for revision %d: %v", i, err)
		}
	}

	revs, err := ms.GetHistory(ctx, "user/chrispian/memory/notes", "user.preferences.verbosity")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 {
		t.Fatalf("got %d revisions, want 3", len(revs))
	}
	// Newest first
	if revs[0].RevisionID != written[2] {
		t.Errorf("first = %q, want newest %q", revs[0].RevisionID, written[2])
	}
	if revs[2].RevisionID != written[0] {
		t.Errorf("last = %q, want oldest %q", revs[2].RevisionID, written[0])
	}
}

func TestItemIDReadsPreserveStableIdentityAndExactRevisions(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	firstInput := sampleInput("item.identity")
	firstInput.Summary = "first"
	first, err := ms.WriteRevision(ctx, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := sampleInput("item.identity")
	secondInput.Summary = "second"
	second, err := ms.WriteRevision(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}

	if first.ItemID == "" || first.ItemID != first.MemoryID {
		t.Fatalf("first aliases item_id=%q memory_id=%q", first.ItemID, first.MemoryID)
	}
	if second.ItemID != first.ItemID || second.MemoryID != first.MemoryID {
		t.Fatalf("stable identity changed across revisions: first=%q second=%q", first.ItemID, second.ItemID)
	}

	current, err := ms.GetCurrentByItemID(ctx, first.ItemID)
	if err != nil {
		t.Fatalf("GetCurrentByItemID: %v", err)
	}
	if current.RevisionID != second.RevisionID || current.ItemID != current.MemoryID {
		t.Fatalf("current = revision %q aliases %q/%q, want revision %q", current.RevisionID, current.ItemID, current.MemoryID, second.RevisionID)
	}

	history, err := ms.GetHistoryByItemID(ctx, first.ItemID)
	if err != nil {
		t.Fatalf("GetHistoryByItemID: %v", err)
	}
	if len(history) != 2 || history[0].RevisionID != second.RevisionID || history[1].RevisionID != first.RevisionID {
		t.Fatalf("history revisions = %#v", history)
	}
	for _, revision := range history {
		if revision.ItemID != first.ItemID || revision.MemoryID != first.ItemID {
			t.Errorf("history aliases = %q/%q, want %q", revision.ItemID, revision.MemoryID, first.ItemID)
		}
	}

	exact, err := ms.GetRevisionByID(ctx, first.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if exact.Payload.Summary != "first" || exact.RevisionID != first.RevisionID {
		t.Fatalf("exact revision moved: %#v", exact)
	}
}

func TestItemIDReadsReachKeylessItems(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	input := sampleInput("")
	input.Summary = "keyless"
	written, err := ms.WriteRevision(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if written.MemoryKey != "" {
		t.Fatalf("seed unexpectedly has key %q", written.MemoryKey)
	}

	current, err := ms.GetCurrentByItemID(ctx, written.ItemID)
	if err != nil {
		t.Fatalf("GetCurrentByItemID: %v", err)
	}
	history, err := ms.GetHistoryByItemID(ctx, written.ItemID)
	if err != nil {
		t.Fatalf("GetHistoryByItemID: %v", err)
	}
	if current.RevisionID != written.RevisionID || len(history) != 1 || history[0].RevisionID != written.RevisionID {
		t.Fatalf("keyless reads current=%q history=%#v", current.RevisionID, history)
	}

	for _, itemID := range []string{"", "01NEVEREXISTED"} {
		_, currentErr := ms.GetCurrentByItemID(ctx, itemID)
		if itemID == "" && !errors.Is(currentErr, memory.ErrInvalidInput) {
			t.Errorf("empty item current error = %v, want invalid input", currentErr)
		}
		if itemID != "" && !errors.Is(currentErr, memory.ErrNotFound) {
			t.Errorf("missing item current error = %v, want not found", currentErr)
		}
	}
}

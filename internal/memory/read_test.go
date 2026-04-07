package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/memory"
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
	in2.Payload.Summary = "updated"
	rev2, err := ms.WriteRevision(ctx, in2)
	if err != nil {
		t.Fatal(err)
	}

	cur, err := ms.GetCurrent(ctx, "user/chrispian/memory", "user.preferences.verbosity")
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
	_, err := ms.GetCurrent(context.Background(), "user/chrispian/memory", "nothing.here")
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
		in.Payload.Summary = "v" + string(rune('0'+i))
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, rev.RevisionID)
		time.Sleep(2 * time.Millisecond)
	}

	revs, err := ms.GetHistory(ctx, "user/chrispian/memory", "user.preferences.verbosity")
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

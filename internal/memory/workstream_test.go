package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func receiverContext(sessionID, workstreamID string) context.Context {
	return memory.ContextWithWriteContext(context.Background(), memory.WriteContext{
		Issuer: "tether", Verification: "unverified", SessionID: sessionID, WorkstreamID: workstreamID,
	})
}

func TestEventLogWorkstreamFilterAndCursorBinding(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	for _, ws := range []string{"ws-log-a", "ws-log-b"} {
		in := sampleInput("")
		in.Domain = domains.Event
		in.Namespace = "user/chrispian/event/reasoning"
		in.WorkstreamID = &ws
		if _, err := store.WriteRevision(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{"user/chrispian/event/reasoning"}, WorkstreamID: "ws-log-a", Limit: 1,
	})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].WorkstreamID != "ws-log-a" {
		t.Fatalf("filtered event log = %#v, err=%v", page, err)
	}
	all, err := store.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{"user/chrispian/event/reasoning"}, Limit: 1,
	})
	if err != nil || all.Manifest.NextCursor == nil {
		t.Fatalf("unfiltered first page = %#v, err=%v", all, err)
	}
	_, err = store.ReadEventLog(context.Background(), memory.EventLogInput{
		Namespaces: []string{"user/chrispian/event/reasoning"}, WorkstreamID: "ws-log-a", Cursor: *all.Manifest.NextCursor,
	})
	if !errors.Is(err, memory.ErrInvalidCursor) {
		t.Fatalf("cursor reused across workstream filter: %v", err)
	}
}

func TestRevisionWorkstreamAssociationAndWriteReceipts(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	first, err := store.WriteRevision(receiverContext("tether-a", "ws-a"), sampleInput("workstream.item"))
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkstreamID != "ws-a" || first.Provenance == nil || first.Provenance.WriteContext.SessionID != "tether-a" {
		t.Fatalf("context-derived first write = %#v", first)
	}
	explicit := sampleInput("workstream.explicit")
	explicitID := "ws-explicit"
	explicit.WorkstreamID = &explicitID
	explicitRevision, err := store.WriteRevision(receiverContext("tether-explicit", "ws-envelope"), explicit)
	if err != nil {
		t.Fatal(err)
	}
	if explicitRevision.WorkstreamID != explicitID || explicitRevision.Provenance.WriteContext.WorkstreamID != "ws-envelope" {
		t.Fatalf("explicit association did not win independently of receipt: %#v", explicitRevision)
	}

	secondInput := sampleInput("workstream.item")
	secondInput.Supersedes = first.RevisionID
	second, err := store.WriteRevision(receiverContext("tether-b", "ws-b"), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if second.WorkstreamID != "ws-a" {
		t.Fatalf("omitted association followed fresh context: %q", second.WorkstreamID)
	}
	if second.Provenance.WriteContext.WorkstreamID != "ws-b" {
		t.Fatalf("second receipt did not retain received context: %#v", second.Provenance)
	}

	clearedID := ""
	thirdInput := sampleInput("workstream.item")
	thirdInput.Supersedes = second.RevisionID
	thirdInput.WorkstreamID = &clearedID
	third, err := store.WriteRevision(receiverContext("tether-c", "ws-c"), thirdInput)
	if err != nil {
		t.Fatal(err)
	}
	if third.WorkstreamID != "" || third.Provenance.WriteContext.WorkstreamID != "ws-c" {
		t.Fatalf("explicit clear or receipt lost: %#v", third)
	}

	fourthInput := sampleInput("workstream.item")
	fourthInput.Supersedes = third.RevisionID
	fourth, err := store.WriteRevision(receiverContext("tether-d", "ws-d"), fourthInput)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.WorkstreamID != "" {
		t.Fatalf("omission did not preserve cleared association: %q", fourth.WorkstreamID)
	}

	old, err := store.GetRevisionByID(context.Background(), first.RevisionID)
	if err != nil || old.WorkstreamID != "ws-a" || old.Provenance.WriteContext.SessionID != "tether-a" {
		t.Fatalf("immutable first receipt changed: rev=%#v err=%v", old, err)
	}
	hits, err := store.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{first.Namespace}, RevisionScope: memory.RevisionScopeTimeline,
		Ranking: memory.RankingChronological, Filters: memory.RecallFilters{WorkstreamID: "ws-a", Statuses: []memory.Status{memory.StatusDraft, memory.StatusReviewed, memory.StatusCanonical, memory.StatusDeprecated}},
	})
	if err != nil || len(hits) != 2 {
		t.Fatalf("timeline workstream filter = %d hits, err=%v", len(hits), err)
	}
}

func TestWorkstreamFilterRunsBeforeLimitAndRejectsPadding(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	for _, key := range []string{"unrelated.a", "unrelated.b"} {
		if _, err := store.WriteRevision(context.Background(), sampleInput(key)); err != nil {
			t.Fatal(err)
		}
	}
	in := sampleInput("selected")
	ws := "ws-selected"
	in.WorkstreamID = &ws
	selected, err := store.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Recall(context.Background(), memory.RecallInput{Namespaces: []string{selected.Namespace}, Limit: 1, Filters: memory.RecallFilters{WorkstreamID: ws}})
	if err != nil || len(hits) != 1 || hits[0].Revision.RevisionID != selected.RevisionID {
		t.Fatalf("filtered limited recall = %#v, err=%v", hits, err)
	}
	bad := sampleInput("bad.workstream")
	padded := " ws"
	bad.WorkstreamID = &padded
	if _, err := store.WriteRevision(context.Background(), bad); !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("padded workstream error = %v", err)
	}
}

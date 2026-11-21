package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEventStreamAcrossProjects(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://service/test/dispatcher"})
	var selected []memory.Revision
	for _, row := range []struct{ ns, ws, task, run string }{
		{"project/alpha/event/status", "ws", "T-1", "R-1"},
		{"project/beta/event/status", "ws", "T-1", "R-1"},
		{"project/beta/event/reasoning", "ws", "T-1", "R-1"},
		{"project/beta/event/journal", "ws", "T-1", "R-1"},
		{"project/gamma/event/status", "other", "T-1", "R-1"},
		{"project/gamma/event/status", "ws", "T-2", "R-1"},
		{"project/gamma/event/status", "ws", "T-1", "R-2"},
		{"session/alpha/event/reasoning", "ws", "T-1", "R-1"},
	} {
		in := statusInput("dispatcher", "done", row.ws)
		in.Namespace = row.ns
		state, _ := json.Marshal(map[string]string{"state": "done", "subject_urn": "msg://agent/test/a", "task_id": row.task, "run_id": row.run})
		in.ConsumerState = state
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(selected) < 2 {
			selected = append(selected, rev)
		}
	}
	since := selected[0].CreatedAt
	until := time.Now().Add(time.Hour)
	for _, scope := range []string{"project/", "project/*"} {
		for _, direction := range []memory.LogDirection{memory.LogOldestFirst, memory.LogNewestFirst} {
			in := memory.EventLogInput{Namespaces: []string{scope}, Stream: "status", WorkstreamID: "ws", Since: &since, Until: &until, Direction: direction, Limit: 1,
				StateFilters: []memory.StateFilter{{Field: "task_id", Values: []any{"T-1"}}, {Field: "run_id", Values: []any{"R-1"}}}}
			seen := map[string]bool{}
			for n := 0; n < 2; n++ {
				page, err := ms.ReadEventLog(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Entries) != 1 {
					t.Fatalf("page %d: %+v", n, page)
				}
				rev := page.Entries[0]
				if seen[rev.RevisionID] {
					t.Fatal("duplicate across pages")
				}
				seen[rev.RevisionID] = true
				want := selected[n]
				if direction == memory.LogNewestFirst {
					want = selected[1-n]
				}
				if rev.RevisionID != want.RevisionID || rev.Namespace != want.Namespace || rev.WriterPrincipal != "msg://service/test/dispatcher" {
					t.Fatalf("identity lost: %+v", rev)
				}
				for _, mode := range []memory.PayloadMode{memory.PayloadModeKeys, memory.PayloadModeSummary, memory.PayloadModeFull} {
					raw, _ := json.Marshal(memory.ProjectEventLogPage(page, mode))
					// Check JSON field names directly; all payload modes must retain the receipt.
					var obj map[string]any
					if err := json.Unmarshal(raw, &obj); err != nil {
						t.Fatal(err)
					}
					entry := obj["entries"].([]any)[0].(map[string]any)
					if entry["namespace"] != rev.Namespace || entry["writer_principal"] != rev.WriterPrincipal {
						t.Fatalf("%s projection lost identity: %s", mode, raw)
					}
				}
				if n == 0 {
					if page.Manifest.NextCursor == nil {
						t.Fatal("missing cursor")
					}
					in.Cursor = *page.Manifest.NextCursor
					for _, stream := range []string{"", "reasoning"} {
						changed := in
						changed.Stream = stream
						if _, err := ms.ReadEventLog(ctx, changed); !errors.Is(err, memory.ErrInvalidCursor) {
							t.Fatalf("changed stream cursor: %v", err)
						}
					}
				} else if page.Manifest.HasMore {
					t.Fatal("unexpected third page")
				}
			}
		}
	}
	// Without stream or other filters the historical prefix behavior reads every stream.
	all, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/*"}})
	if err != nil || len(all.Entries) != 7 {
		t.Fatalf("legacy prefix: %+v %v", all, err)
	}
	for _, stream := range []string{"STATUS", "status/", "*", "unknown", " status", "status' OR 1=1 --"} {
		_, readErr := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/"}, Stream: stream})
		if !errors.Is(readErr, memory.ErrInvalidInput) {
			t.Fatalf("invalid stream %q: %v", stream, readErr)
		}
	}
	// Every supported stream is independently selectable, with the scope still applied.
	for stream, count := range map[string]int{"status": 5, "reasoning": 1, "journal": 1} {
		page, readErr := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/"}, Stream: stream})
		if readErr != nil || len(page.Entries) != count {
			t.Fatalf("stream %s: %+v %v", stream, page, readErr)
		}
	}
	// An exact namespace still intersects with the stream filter.
	exact, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{selected[0].Namespace}, Stream: "reasoning"})
	if err != nil || len(exact.Entries) != 0 {
		t.Fatalf("exact namespace widened: %+v %v", exact, err)
	}
	// Time bounds are inclusive even across namespaces.
	until = selected[1].CreatedAt
	window, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/"}, Stream: "status", Since: &since, Until: &until})
	if err != nil || len(window.Entries) != 2 {
		t.Fatalf("inclusive time window: %+v %v", window, err)
	}
	// Time bounds continue to exclude rows even across namespaces.
	future := time.Now().Add(time.Hour)
	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/"}, Stream: "status", Since: &future})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("time filter: %+v %v", page, err)
	}
}

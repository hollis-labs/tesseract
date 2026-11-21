package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEventWorkstreamListAndSinceCursor(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	const a = "01M7WQC9BNP2BY914K3H69WHR9"
	const b = "01M7WQC9BNP2BY914K3H69WHRA"
	var selected []memory.Revision
	for i, ws := range []string{a, b, a, "unselected"} {
		in := statusInput("dispatcher", "done", ws)
		in.Namespace = fmt.Sprintf("project/child-%d/event/status", i)
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			selected = append(selected, rev)
		}
	}
	since := selected[0].CreatedAt
	base := memory.EventLogInput{Namespaces: []string{"project/"}, Stream: "status", WorkstreamIDs: []string{a, b}, Limit: 1, Since: &since, Direction: memory.LogOldestFirst,
		StateFilters: []memory.StateFilter{{Field: "task", Values: []any{"T-1"}}, {Field: "run", Values: []any{"run-1"}}}}
	in := base
	for i := 0; i < 3; i++ {
		page, err := ms.ReadEventLog(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 || page.Entries[0].RevisionID != selected[i].RevisionID {
			t.Fatalf("page %d: %+v", i, page)
		}
		if i == 2 {
			if page.Manifest.HasMore {
				t.Fatal("unexpected extra association")
			}
			break
		}
		if page.Manifest.NextCursor == nil {
			t.Fatal("missing cursor")
		}
		in = base
		in.WorkstreamIDs = []string{b, a, a}
		in.Since = nil
		if err := memory.ApplyEventLogSince(&in, *page.Manifest.NextCursor); err != nil {
			t.Fatal(err)
		}
		if !in.Since.Equal(since) {
			t.Fatal("since cursor lost original time bound")
		}
		changed := in
		changed.WorkstreamIDs = []string{a}
		if _, err := ms.ReadEventLog(ctx, changed); !errors.Is(err, memory.ErrInvalidCursor) {
			t.Fatalf("changed list cursor: %v", err)
		}
		conflicting := in
		if err := memory.ApplyEventLogSince(&conflicting, *page.Manifest.NextCursor); !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("ambiguous cursor accepted: %v", err)
		}
	}
	// A singleton native list can resume an existing scalar association cursor.
	scalar := base
	scalar.WorkstreamIDs = nil
	scalar.WorkstreamID = a
	scalar.Since = nil
	first, err := ms.ReadEventLog(ctx, scalar)
	if err != nil || first.Manifest.NextCursor == nil {
		t.Fatalf("scalar page: %+v %v", first, err)
	}
	scalar.WorkstreamID = ""
	scalar.WorkstreamIDs = []string{a}
	scalar.Cursor = *first.Manifest.NextCursor
	if page, err := ms.ReadEventLog(ctx, scalar); err != nil || len(page.Entries) != 1 {
		t.Fatalf("scalar-list compatibility: %+v %v", page, err)
	}
	for _, ids := range [][]string{{}, {""}, {" a"}, make([]string, memory.MaxEventWorkstreamIDs+1)} {
		if _, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/"}, WorkstreamIDs: ids}); !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("invalid list accepted: %v %v", ids, err)
		}
	}
	for _, raw := range []string{"[]", "[null]", "[1]", "[\"\"]", "[", ""} {
		if _, err := memory.ParseEventWorkstreamSelectors([]string{raw}); !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("invalid HTTP selector %q: %v", raw, err)
		}
	}
	for _, values := range [][]string{{a, b}, {`["` + b + `","` + a + `"]`}} {
		got, err := memory.ParseEventWorkstreamSelectors(values)
		if err != nil || !reflect.DeepEqual(got, []string{a, b}) {
			t.Fatalf("parse: %v %v", got, err)
		}
	}
	for _, raw := range []string{"not-a-time", "e30"} {
		if err := memory.ApplyEventLogSince(&memory.EventLogInput{}, raw); !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("invalid since accepted: %v", err)
		}
	}
}

func TestEventConsumerAliasesRespectProjection(t *testing.T) {
	stamp := time.Date(2026, 11, 21, 1, 0, 0, 123, time.UTC)
	rev := memory.Revision{RevisionID: "revision-1", Namespace: "project/example/event/status", CreatedAt: stamp, WriterPrincipal: "msg://service/test/dispatcher", WorkstreamID: "ws",
		Payload:       memory.Payload{Summary: "Validation complete", Body: "Detailed verification", Data: json.RawMessage(`{"ref":"commit:abc"}`)},
		ConsumerState: json.RawMessage(`{"state":"done","task_id":"T-explicit","task":"T-fallback","run":"run-fallback","team_id":"team-1","workstream_key":"status-writing"}`)}
	for _, mode := range []memory.PayloadMode{memory.PayloadModeKeys, memory.PayloadModeSummary, memory.PayloadModeFull} {
		raw, err := json.Marshal(memory.ProjectEventLogPage(memory.EventLogPage{Entries: []memory.Revision{rev}}, mode))
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Entries []map[string]json.RawMessage `json:"entries"`
		}
		if decodeErr := json.Unmarshal(raw, &wire); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		row := wire.Entries[0]
		read := func(key string) string { var value string; _ = json.Unmarshal(row[key], &value); return value }
		if read("id") != rev.RevisionID || read("revision_id") != rev.RevisionID || read("namespace") != rev.Namespace || read("writer_principal") != rev.WriterPrincipal || read("workstream_id") != "ws" {
			t.Fatalf("identity: %s", raw)
		}
		at, err := time.Parse(time.RFC3339Nano, read("at"))
		if err != nil || !at.Equal(stamp) {
			t.Fatalf("at: %s", raw)
		}
		if mode == memory.PayloadModeKeys {
			for _, key := range []string{"summary", "state", "task_id", "run_id", "team_id", "detail", "link", "workstream_key", "payload", "consumer_state"} {
				if _, ok := row[key]; ok {
					t.Fatalf("keys leaked %s: %s", key, raw)
				}
			}
			continue
		}
		for key, want := range map[string]string{"summary": rev.Payload.Summary, "state": "done", "task_id": "T-explicit", "run_id": "run-fallback", "team_id": "team-1", "workstream_key": "status-writing"} {
			if read(key) != want {
				t.Fatalf("%s alias %s: %s", mode, key, raw)
			}
		}
		if _, ok := row["consumer_state"]; !ok {
			t.Fatal("original consumer_state lost")
		}
		if mode == memory.PayloadModeFull {
			if read("detail") != rev.Payload.Body || read("link") != "commit:abc" {
				t.Fatalf("full details: %s", raw)
			}
		} else {
			if _, ok := row["detail"]; ok {
				t.Fatal("summary leaked body")
			}
			if _, ok := row["link"]; ok {
				t.Fatal("summary leaked data")
			}
		}
	}
}

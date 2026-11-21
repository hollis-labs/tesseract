package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEventConsumerMCPWorkstreamListAndSinceCursor(t *testing.T) {
	a, _ := newEventAdapter(t)
	const first = "01M7WQC9BNP2BY914K3H69WHR9"
	const second = "01M7WQC9BNP2BY914K3H69WHRA"
	for i, ws := range []string{first, second, "other"} {
		ns := []string{"project/alpha/event/status", "project/beta/event/status", "project/gamma/event/status"}[i]
		_, err := a.EventStore.Write(context.Background(), event.WriteInput{Namespace: ns, WorkstreamID: &ws, Summary: "done", Author: memory.Author{AgentID: "dispatcher"}, SessionID: "s", ConsumerState: json.RawMessage(`{"state":"done","subject_urn":"msg://agent/test/a","task":"T-1","run":"R-1"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := gomcpserver.NewServer("event-consumer", "0")
	a.RegisterAllTools(srv)
	call := func(args map[string]any) string {
		res, err := srv.CallTool(context.Background(), "event_list", args)
		if err != nil {
			t.Fatal(err)
		}
		return mustJSONText(t, res)
	}
	for _, selector := range []any{[]any{first, second}, []string{second, first}, `["` + first + `","` + second + `"]`} {
		args := map[string]any{"namespaces": `["project/"]`, "stream": "status", "workstream_id": selector, "since": "2000-01-01T00:00:00Z", "limit": 1}
		var previous string
		for n := 0; n < 2; n++ {
			raw := call(args)
			var wire struct {
				Entries []struct {
					ID           string `json:"id"`
					RevisionID   string `json:"revision_id"`
					At           string `json:"at"`
					WorkstreamID string `json:"workstream_id"`
					TaskID       string `json:"task_id"`
					RunID        string `json:"run_id"`
					State        string `json:"state"`
					Summary      string `json:"summary"`
				}
				Manifest memory.EventLogManifest
			}
			if err := json.Unmarshal([]byte(raw), &wire); err != nil || len(wire.Entries) != 1 {
				t.Fatalf("page: %s %v", raw, err)
			}
			row := wire.Entries[0]
			if row.ID == "" || row.ID != row.RevisionID || row.ID == previous || row.At == "" || row.TaskID != "T-1" || row.RunID != "R-1" || row.State != "done" || row.Summary != "done" || (row.WorkstreamID != first && row.WorkstreamID != second) {
				t.Fatal(raw)
			}
			previous = row.ID
			if n == 0 {
				if wire.Manifest.NextCursor == nil {
					t.Fatal("missing cursor")
				}
				args["since"] = *wire.Manifest.NextCursor
				args["workstream_id"] = []any{second, first, first}
			} else if wire.Manifest.HasMore {
				t.Fatal("list failed to filter before paging")
			}
		}
		args["workstream_id"] = first
		if raw := call(args); !strings.Contains(raw, "different log read") {
			t.Fatal(raw)
		}
	}
	for _, value := range []any{nil, true, 1, map[string]any{"id": first}, []any{}, []any{nil}, []any{1}, []string{""}, "[]", "[1]", "[null]", "[", ""} {
		raw := call(map[string]any{"namespaces": `["project/"]`, "stream": "status", "workstream_id": value})
		if !strings.Contains(raw, "validation_error") {
			t.Fatalf("invalid list %v: %s", value, raw)
		}
	}
	// Scalar association remains accepted through the same registered schema.
	raw := call(map[string]any{"namespaces": `["project/"]`, "stream": "status", "workstream_id": first})
	var page memory.EventLogPage
	if err := json.Unmarshal([]byte(raw), &page); err != nil || len(page.Entries) != 1 || page.Entries[0].WorkstreamID != first {
		t.Fatalf("scalar: %s %v", raw, err)
	}
}

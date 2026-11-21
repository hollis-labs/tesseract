package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestEventStreamMCPAcrossProjects(t *testing.T) {
	a, _ := newEventAdapter(t)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "msg://service/test/dispatcher"})
	ws := "ws"
	for _, ns := range []string{"project/alpha/event/status", "project/beta/event/status", "project/beta/event/reasoning"} {
		_, err := a.EventStore.Write(ctx, event.WriteInput{Namespace: ns, WorkstreamID: &ws, Summary: "done", Author: memory.Author{AgentID: "dispatcher"}, SessionID: "s", ConsumerState: json.RawMessage(`{"state":"done","subject_urn":"msg://agent/test/a","task_id":"T-1","run_id":"R-1"}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := gomcpserver.NewServer("event-stream", "0")
	a.RegisterAllTools(srv)
	args := map[string]any{"namespaces": `["project/"]`, "stream": "status", "workstream_id": "ws", "state_filters": `[{"field":"task_id","values":["T-1"]},{"field":"run_id","values":["R-1"]}]`, "limit": 1}
	call := func() string {
		res, err := srv.CallTool(context.Background(), "event_list", args)
		if err != nil {
			t.Fatal(err)
		}
		return mustJSONText(t, res)
	}
	seen := map[string]bool{}
	for n := 0; n < 2; n++ {
		raw := call()
		var page memory.EventLogPage
		if err := json.Unmarshal([]byte(raw), &page); err != nil || len(page.Entries) != 1 {
			t.Fatalf("page: %s %v", raw, err)
		}
		rev := page.Entries[0]
		if seen[rev.Namespace] || rev.WriterPrincipal != "msg://service/test/dispatcher" {
			t.Fatal(raw)
		}
		seen[rev.Namespace] = true
		if n == 0 {
			if page.Manifest.NextCursor == nil {
				t.Fatal("missing cursor")
			}
			args["cursor"] = *page.Manifest.NextCursor
		}
	}
	args["stream"] = "reasoning"
	if raw := call(); !strings.Contains(raw, "different log read") {
		t.Fatal(raw)
	}
	delete(args, "cursor")
	args["stream"] = "bogus"
	if raw := call(); !strings.Contains(raw, "validation_error") {
		t.Fatal(raw)
	}
	// Single-project grants cannot authorize project/ or project/* reads.
	limited := referenceAdapter(t, []string{"project/alpha/*"})
	for _, selector := range []string{`["project/"]`, `["project/*"]`} {
		raw := callEventTool(t, limited.handleEventList, map[string]any{"namespaces": selector, "stream": "status"})
		if !strings.Contains(raw, "namespace_not_permitted") {
			t.Fatal(raw)
		}
	}
}

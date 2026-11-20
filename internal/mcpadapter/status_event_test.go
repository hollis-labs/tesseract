package mcpadapter

import (
	"context"
	"encoding/json"
	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestStatusMCPRetryFiltersAndCursor(t *testing.T) {
	a, _ := newEventAdapter(t)
	args := map[string]any{"namespace": "project/status-mcp/event/status", "summary": "done", "author_agent_id": "dispatcher", "session_id": "s", "consumer_state": `{"state":"done","subject_urn":"msg://agent/test/a"}`, "idempotency_key": "retry-1"}
	write := func() memory.Revision {
		raw := callEventTool(t, a.handleEventWrite, args)
		var rev memory.Revision
		if err := json.Unmarshal([]byte(raw), &rev); err != nil || rev.RevisionID == "" {
			t.Fatalf("write: %s %v", raw, err)
		}
		return rev
	}
	first := write()
	if second := write(); second.RevisionID != first.RevisionID {
		t.Fatal("MCP retry appended")
	}
	delete(args, "idempotency_key")
	write()
	args["author_agent_id"] = "other"
	write()
	write()
	read := map[string]any{"namespaces": `["project/status-mcp/event/status"]`, "author_agent_id": "dispatcher", "state_filters": `[{"field":"state","values":["done"]}]`, "limit": 1, "payload_mode": "full"}
	raw := callEventTool(t, a.handleEventList, read)
	var page memory.EventLogPage
	if err := json.Unmarshal([]byte(raw), &page); err != nil || len(page.Entries) != 1 || page.Entries[0].Author.AgentID != "dispatcher" || page.Manifest.NextCursor == nil {
		t.Fatalf("MCP filter before LIMIT: %s %v", raw, err)
	}
	read["cursor"] = *page.Manifest.NextCursor
	read["author_agent_id"] = "other"
	raw = callEventTool(t, a.handleEventList, read)
	if !strings.Contains(raw, "different log read") {
		t.Fatalf("MCP author cursor accepted: %s", raw)
	}
	read["author_agent_id"] = "dispatcher"
	read["state_filters"] = `[{"field":"state","values":["blocked"]}]`
	raw = callEventTool(t, a.handleEventList, read)
	if !strings.Contains(raw, "different log read") {
		t.Fatalf("MCP state cursor accepted: %s", raw)
	}
	delete(read, "cursor")
	read["state_filters"] = "bad-json"
	raw = callEventTool(t, a.handleEventList, read)
	if !strings.Contains(raw, "validation_error") {
		t.Fatalf("MCP invalid filters accepted: %s", raw)
	}
}

func TestStatusMCPRefusesCallerWriterPrincipal(t *testing.T) {
	a, _ := newEventAdapter(t)
	srv := gomcpserver.NewServer("status-test", "0.0.0")
	a.RegisterAllTools(srv)
	res, err := srv.CallTool(context.Background(), "event_write", map[string]any{"writer_principal": "msg://service/trusted/dispatcher"})
	if err != nil {
		t.Fatal(err)
	}
	raw := mustJSONText(t, res)
	if !strings.Contains(raw, "writer_principal") || !strings.Contains(raw, "validation_error") {
		t.Fatalf("spoofed principal accepted: %s", raw)
	}
}

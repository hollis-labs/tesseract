package mcpadapter

import (
	"context"
	"encoding/json"
	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
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

func TestStatusLegacyMCPCredentialProvenanceAndRetryIsolation(t *testing.T) {
	a, ms := newEventAdapter(t)
	ctx := context.Background()
	firstClaims, err := a.Store.ValidateAuthTokenWithClaims(ctx, a.Token)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := a.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{Label: "trusted-dispatcher", Scopes: []string{"memory:read", "memory:write", "write"}})
	if err != nil {
		t.Fatal(err)
	}
	secondClaims, err := a.Store.ValidateAuthTokenWithClaims(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	registry := gomcpserver.NewServer("status-legacy-auth", "0")
	a.RegisterAllTools(registry)
	args := map[string]any{"namespace": "project/status-legacy/event/status", "summary": "done", "author_agent_id": "trusted-dispatcher", "session_id": "s", "idempotency_key": "same-retry-key", "consumer_state": `{"state":"done","subject_urn":"msg://agent/test/a"}`}
	seen := map[string]bool{}
	for _, credential := range []struct{ token, id string }{{a.Token, firstClaims.TokenID}, {second, secondClaims.TokenID}} {
		a.Token = credential.token
		var first memory.Revision
		for attempt := 0; attempt < 2; attempt++ {
			result, err := registry.CallTool(ctx, "event_write", args)
			if err != nil {
				t.Fatal(err)
			}
			raw := mustJSONText(t, result)
			var rev memory.Revision
			if err := json.Unmarshal([]byte(raw), &rev); err != nil || rev.RevisionID == "" {
				t.Fatalf("write: %s %v", raw, err)
			}
			if rev.WriterPrincipal != "tesseract-token/"+credential.id {
				t.Fatalf("legacy MCP lost credential identity: %q", rev.WriterPrincipal)
			}
			if attempt == 0 {
				first = rev
				if seen[rev.RevisionID] {
					t.Fatal("distinct MCP credentials shared retry scope")
				}
				seen[rev.RevisionID] = true
			} else if first.RevisionID != rev.RevisionID {
				t.Fatal("MCP retry appended")
			}
		}
	}
	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{"project/status-legacy/event/status"}})
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("expected two isolated receipts: %+v %v", page, err)
	}
	a.Token = "invalid-test-credential"
	result, err := registry.CallTool(ctx, "event_write", args)
	if err != nil {
		t.Fatal(err)
	}
	if raw := mustJSONText(t, result); !strings.Contains(raw, "auth_required") && !strings.Contains(raw, "namespace_not_permitted") {
		t.Fatalf("invalid legacy MCP credential admitted: %s", raw)
	}
}

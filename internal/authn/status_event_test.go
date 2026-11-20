package authn

import (
	"context"
	"encoding/json"
	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// Use real credential verification and transport auth, rather than a context
// injected by the test, to prove caller authors cannot impersonate writers.
func TestStatusAuthenticatedHTTPAndMCPAuthorSpoof(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce"}, nil)
	s.srv.EventStore = event.New(s.srv.MemoryStore)
	tok := f.token("untrusted-worker", []string{"memory:write", "memory:read"}, []string{"project/status-auth/event/*"})
	claims, err := f.store.ValidateAuthTokenWithClaims(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	expected := "tesseract-token/" + claims.TokenID
	body := map[string]any{"namespace": "project/status-auth/event/status", "summary": "done", "author": map[string]any{"agent_id": "trusted-dispatcher"}, "session_id": "s", "consumer_state": map[string]any{"state": "done", "subject_urn": "msg://agent/test/a"}}
	rec := s.do("POST", "/v1/event/write", tok, body)
	wantStatus(t, rec, 200, "authenticated status write")
	var rev memory.Revision
	if err := json.Unmarshal(rec.Body.Bytes(), &rev); err != nil {
		t.Fatal(err)
	}
	if rev.Author.AgentID != "trusted-dispatcher" || rev.WriterPrincipal != expected {
		t.Fatalf("author spoof altered provenance: author=%q principal=%q", rev.Author.AgentID, rev.WriterPrincipal)
	}
	fetched, err := s.srv.MemoryStore.GetRevisionByID(context.Background(), rev.RevisionID)
	if err != nil || fetched.WriterPrincipal != expected {
		t.Fatalf("stored principal: %+v %v", fetched, err)
	}

	for _, mode := range []string{"keys", "summary", "full"} {
		response := s.do("GET", "/v1/event/log?namespace=project/status-auth/event/status&payload_mode="+mode, tok, nil)
		wantStatus(t, response, 200, "authenticated status log projection")
		var page memory.EventLogPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 || page.Entries[0].WriterPrincipal != expected {
			t.Fatalf("%s projection dropped credential receipt: %s", mode, response.Body.String())
		}
	}
	body["writer_principal"] = "trusted-dispatcher"
	wantStatus(t, s.do("POST", "/v1/event/write", tok, body), 400, "caller writer_principal")

	h := f.mcp(config.AuthConfig{Mode: "enforce"}, tok, nil)
	adapter := mcpadapter.New(f.store, tok)
	adapter.MemoryStore = s.srv.MemoryStore
	adapter.EventStore = s.srv.EventStore
	adapter.Auth = h.rt.NewMCPSession(func() (string, error) { return tok, nil })
	registry := gomcpserver.NewServer("status-auth", "0")
	adapter.RegisterAllTools(registry)
	result, err := registry.CallTool(context.Background(), "event_write", map[string]any{
		"namespace": "project/status-auth/event/status", "summary": "done", "author_agent_id": "trusted-ops-server", "session_id": "s",
		"consumer_state": `{"state":"done","subject_urn":"msg://agent/test/a"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), `"code":`) {
		t.Fatalf("MCP write failed: %s", encoded)
	}
	page, err := s.srv.MemoryStore.ReadEventLog(context.Background(), memory.EventLogInput{Namespaces: []string{"project/status-auth/event/status"}, AuthorAgentID: "trusted-ops-server"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].WriterPrincipal != expected {
		t.Fatalf("MCP authenticated principal not retained: %+v %v", page, err)
	}
}

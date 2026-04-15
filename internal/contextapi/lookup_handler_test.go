package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/domains"
	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func newLookupServer(t *testing.T) *Server {
	t.Helper()
	srv := newMemoryTestServer(t)
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)
	return srv
}

func seedMemory(t *testing.T, srv *Server) {
	t.Helper()
	_, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  "user/chrispian/memory",
		MemoryKey:  "prefs.terse",
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "terse output"},
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}
}

func seedKnowledge(t *testing.T, srv *Server) {
	t.Helper()
	_, err := srv.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: "user/chrispian/knowledge/framework",
		Key:       "framework.go-providers",
		Kind:      "package",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/pkg/go-providers"},
		Summary:   "go-providers multi-provider adapter",
		Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		SessionID: "indexer:01HX",
	})
	if err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}
}

func TestConduitLookup_UnifiedAcrossDomains(t *testing.T) {
	srv := newLookupServer(t)
	seedMemory(t, srv)
	seedKnowledge(t, srv)

	body := `{
		"namespaces":["user/chrispian/memory","user/chrispian/knowledge/framework"],
		"ranking":"chronological"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/conduit/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	var resp conduitLookupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("want 2 results across domains, got %d", len(resp.Results))
	}
	if resp.Facets.Domains["memory"] != 1 || resp.Facets.Domains["knowledge"] != 1 {
		t.Errorf("facets.Domains = %+v, want memory:1 knowledge:1", resp.Facets.Domains)
	}
	if resp.Facets.Kinds["package"] != 1 {
		t.Errorf("facets.Kinds = %+v, want package:1", resp.Facets.Kinds)
	}
}

func TestConduitLookup_DomainFilter(t *testing.T) {
	srv := newLookupServer(t)
	seedMemory(t, srv)
	seedKnowledge(t, srv)

	body := `{
		"namespaces":["user/chrispian/memory","user/chrispian/knowledge/framework"],
		"ranking":"chronological",
		"domains":["knowledge"]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/conduit/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	var resp conduitLookupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 knowledge result, got %d", len(resp.Results))
	}
	if resp.Results[0].Revision.Domain != domains.Knowledge {
		t.Errorf("Domain = %q, want knowledge", resp.Results[0].Revision.Domain)
	}
}

func TestConduitLookup_FacetKindFilter(t *testing.T) {
	srv := newLookupServer(t)
	seedKnowledge(t, srv)

	body := `{
		"namespaces":["user/chrispian/knowledge/framework"],
		"ranking":"chronological",
		"facet_kinds":["package"]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/conduit/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	var resp conduitLookupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result for kind=package, got %d", len(resp.Results))
	}
}

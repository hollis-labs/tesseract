package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func newKnowledgeTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newMemoryTestServer(t)
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)
	return srv
}

func TestKnowledgeWrite_Success(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	body := `{
		"namespace":"user/chrispian/knowledge/framework",
		"key":"framework.go-providers",
		"kind":"package",
		"source":"filesystem",
		"pointer":{"scheme":"file","locator":"/pkg/go-providers"},
		"summary":"go-providers multi-provider adapter",
		"author":{"agent_id":"indexer","agent_version":"1.0"},
		"session_id":"indexer:01HX"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rev.Domain != "knowledge" {
		t.Errorf("rev.Domain = %q, want knowledge", rev.Domain)
	}
	if rev.Facets.Kind != "package" || rev.Facets.Source != "filesystem" {
		t.Errorf("facets = %+v, want kind=package source=filesystem", rev.Facets)
	}
	if rev.Facets.Pointer == nil || rev.Facets.Pointer.Scheme != "file" {
		t.Errorf("pointer = %+v, want scheme=file", rev.Facets.Pointer)
	}
}

func TestKnowledgeWrite_NoStoreReturns503(t *testing.T) {
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := NewServer(cs, contextpolicy.New())
	// No KnowledgeStore wired.

	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/write",
		bytes.NewBufferString(`{"namespace":"user/x/knowledge"}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func TestKnowledgeWrite_MissingFacetsReturns400(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	// Missing kind, source, pointer.
	body := `{
		"namespace":"user/chrispian/knowledge/framework",
		"summary":"no facets",
		"author":{"agent_id":"x","agent_version":"1.0"},
		"session_id":"manual:01HX"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/write", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "validation_error") {
		t.Errorf("body missing validation_error code: %s", rr.Body.String())
	}
}

func TestKnowledgeGetCurrent_Success(t *testing.T) {
	srv := newKnowledgeTestServer(t)
	writeBody := `{
		"namespace":"user/chrispian/knowledge/framework",
		"key":"framework.go-providers",
		"kind":"package",
		"source":"filesystem",
		"pointer":{"scheme":"file","locator":"/pkg/go-providers"},
		"summary":"initial summary",
		"author":{"agent_id":"indexer","agent_version":"1.0"},
		"session_id":"indexer:01HX"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/write", bytes.NewBufferString(writeBody))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("write status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet,
		"/v1/knowledge/current?namespace=user/chrispian/knowledge/framework&key=framework.go-providers", nil)
	getRR := httptest.NewRecorder()
	srv.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", getRR.Code, getRR.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(getRR.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rev.Domain != "knowledge" {
		t.Errorf("rev.Domain = %q, want knowledge", rev.Domain)
	}
	if rev.Payload.Summary != "initial summary" {
		t.Errorf("summary = %q, want 'initial summary'", rev.Payload.Summary)
	}
}

func TestKnowledgeGetCurrent_NotFound(t *testing.T) {
	srv := newKnowledgeTestServer(t)
	req := httptest.NewRequest(http.MethodGet,
		"/v1/knowledge/current?namespace=user/chrispian/knowledge/missing&key=nope", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestKnowledgeGetCurrent_MissingParams(t *testing.T) {
	srv := newKnowledgeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/knowledge/current", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestKnowledgeGetHistory_OrdersNewestFirst(t *testing.T) {
	srv := newKnowledgeTestServer(t)
	writeBody := func(summary, supersedes string) string {
		return `{
			"namespace":"user/chrispian/knowledge/framework",
			"key":"framework.go-providers",
			"kind":"package",
			"source":"filesystem",
			"pointer":{"scheme":"file","locator":"/pkg/go-providers"},
			"summary":"` + summary + `",
			"supersedes":"` + supersedes + `",
			"author":{"agent_id":"indexer","agent_version":"1.0"},
			"session_id":"indexer:01HX"
		}`
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/write", bytes.NewBufferString(writeBody("v1", "")))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("write v1 status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var first memory.Revision
	_ = json.Unmarshal(rr.Body.Bytes(), &first)

	req2 := httptest.NewRequest(http.MethodPost, "/v1/knowledge/write", bytes.NewBufferString(writeBody("v2", first.RevisionID)))
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("write v2 status = %d, body=%s", rr2.Code, rr2.Body.String())
	}

	histReq := httptest.NewRequest(http.MethodGet,
		"/v1/knowledge/history?namespace=user/chrispian/knowledge/framework&key=framework.go-providers", nil)
	histRR := httptest.NewRecorder()
	srv.ServeHTTP(histRR, histReq)
	if histRR.Code != http.StatusOK {
		t.Fatalf("history status = %d, body=%s", histRR.Code, histRR.Body.String())
	}
	var revs []memory.Revision
	if err := json.Unmarshal(histRR.Body.Bytes(), &revs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("len(revs) = %d, want 2", len(revs))
	}
	if revs[0].Payload.Summary != "v2" {
		t.Errorf("newest-first broken: revs[0].summary = %q, want v2", revs[0].Payload.Summary)
	}
}

// TestMemoryWrite_RejectsKnowledgeDomain verifies the memory endpoint refuses
// any attempt to write under a non-memory domain — knowledge writes must go
// through the dedicated endpoint so facet invariants are enforced.
func TestMemoryWrite_RejectsKnowledgeDomain(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"domain":"knowledge",
		"namespace":"user/chrispian/knowledge/framework",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"status":"draft",
		"payload":{"summary":"sneak knowledge in"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "wrong_endpoint") {
		t.Errorf("body missing wrong_endpoint code: %s", rr.Body.String())
	}
}

package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
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
		"/v1/knowledge/current?namespace=user/chrispian/knowledge/framework&memory_key=framework.go-providers", nil)
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
		"/v1/knowledge/current?namespace=user/chrispian/knowledge/missing&memory_key=nope", nil)
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
		"/v1/knowledge/history?namespace=user/chrispian/knowledge/framework&memory_key=framework.go-providers", nil)
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

// --- Strict body decoding ---------------------------------------------------
//
// These routes used to decode with a lenient json.Decoder, so a body shaped
// for the MCP tool surface — which takes the same facts as FLAT scalars —
// decoded "successfully" into a struct with a zero-valued Pointer and then
// failed as a complaint about missing pointer facets that named none of the
// fields the caller had actually sent. The tests below pin the loud failure
// that replaced it.

// rejectionEnvelope is the error envelope these routes return for a body they
// refuse, decoded far enough to assert on the decode-specific details.
type rejectionEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details struct {
		UnknownField   string   `json:"unknown_field"`
		ExpectedField  string   `json:"expected_field"`
		AcceptedFields []string `json:"accepted_fields"`
	} `json:"details"`
}

// postRawJSON posts a body verbatim, without round-tripping it through a Go
// struct — the point of every test here is a key no struct declares.
func postRawJSON(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// mustRejectUnknownField asserts the body was refused as a validation_error
// naming `field`, and returns the envelope for further assertions.
func mustRejectUnknownField(t *testing.T, srv *Server, path, body, field string) rejectionEnvelope {
	t.Helper()
	rr := postRawJSON(t, srv, path, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("%s status = %d, want 400; body=%s", path, rr.Code, rr.Body.String())
	}
	var env rejectionEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; raw=%s", err, rr.Body.String())
	}
	if env.Code != "validation_error" {
		t.Errorf("code = %q, want validation_error; body=%s", env.Code, rr.Body.String())
	}
	if env.Details.UnknownField != field {
		t.Errorf("details.unknown_field = %q, want %q", env.Details.UnknownField, field)
	}
	// The name has to reach the caller who reads only the message, too — an
	// error that does not repeat the offending field is the failure mode this
	// whole change exists to remove.
	if !strings.Contains(env.Message, field) {
		t.Errorf("message does not name the offending field %q: %s", field, env.Message)
	}
	if len(env.Details.AcceptedFields) == 0 {
		t.Errorf("details.accepted_fields is empty; the canonical shape stays undiscoverable: %s", rr.Body.String())
	}
	return env
}

// TestKnowledgeWrite_FlatMCPBodyRejectedWithNestedHint drives the exact
// mistake this endpoint used to swallow: the MCP knowledge_write argument set,
// posted as an HTTP body.
func TestKnowledgeWrite_FlatMCPBodyRejectedWithNestedHint(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	// The MCP tool's flat shape, in its own field order. tags is a
	// JSON-encoded string there, not an array — another thing the old decoder
	// would have accepted into nothing.
	body := `{
		"namespace":"user/chrispian/knowledge/framework",
		"kind":"package",
		"source":"filesystem",
		"pointer_scheme":"file",
		"pointer_locator":"/pkg/go-providers",
		"summary":"go-providers multi-provider adapter",
		"author_agent_id":"indexer",
		"author_version":"1.0",
		"session_id":"indexer:01HX",
		"tags":"[\"framework\"]"
	}`
	env := mustRejectUnknownField(t, srv, "/v1/knowledge/write", body, "pointer_scheme")

	if env.Details.ExpectedField != "pointer.scheme" {
		t.Errorf("details.expected_field = %q, want pointer.scheme", env.Details.ExpectedField)
	}
	if !strings.Contains(env.Message, "pointer.scheme") {
		t.Errorf("message does not name the nested equivalent: %s", env.Message)
	}
	// The old failure said "pointer.scheme and pointer.locator are required"
	// while the caller had sent both, under other names. Naming what was sent
	// is the fix; naming what it maps to is what makes one retry enough.
	if !slices.Contains(env.Details.AcceptedFields, "pointer") {
		t.Errorf("accepted_fields does not list the nested pointer object: %v", env.Details.AcceptedFields)
	}
}

// TestKnowledgeWrite_FlatAuthorFieldsHintTheNestedShape covers the author
// half of the pair, including the MCP spelling author_version — which is NOT
// author_agent_version, the name the nested object uses.
func TestKnowledgeWrite_FlatAuthorFieldsHintTheNestedShape(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	for _, tc := range []struct{ field, want string }{
		{"author_agent_id", "author.agent_id"},
		{"author_version", "author.agent_version"},
		{"author_agent_version", "author.agent_version"},
		{"pointer_locator", "pointer.locator"},
		{"pointer_resolved_at", "pointer.resolved_at"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			body := `{"namespace":"user/chrispian/knowledge/framework","` + tc.field + `":"x"}`
			env := mustRejectUnknownField(t, srv, "/v1/knowledge/write", body, tc.field)
			if env.Details.ExpectedField != tc.want {
				t.Errorf("details.expected_field = %q, want %q", env.Details.ExpectedField, tc.want)
			}
		})
	}
}

// TestKnowledgeWrite_UnrecognizedFieldStillNamesAcceptedShape: a field with no
// known nested equivalent is still reported by name, with the accepted keys —
// a typo has to be as diagnosable as a cross-surface mix-up.
func TestKnowledgeWrite_UnrecognizedFieldStillNamesAcceptedShape(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	body := `{"namespace":"user/chrispian/knowledge/framework","sumary":"typo"}`
	env := mustRejectUnknownField(t, srv, "/v1/knowledge/write", body, "sumary")
	if env.Details.ExpectedField != "" {
		t.Errorf("details.expected_field = %q, want none for an unrecognized field", env.Details.ExpectedField)
	}
	if !slices.Contains(env.Details.AcceptedFields, "summary") {
		t.Errorf("accepted_fields does not list summary: %v", env.Details.AcceptedFields)
	}
}

// TestKnowledgeWrite_NestedBodyStillAccepted is the other half of the
// contract: strictness must not have narrowed the shape this endpoint always
// took. Every optional field is present, including pointer.resolved_at.
func TestKnowledgeWrite_NestedBodyStillAccepted(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	body := `{
		"namespace":"user/chrispian/knowledge/framework",
		"key":"framework.go-providers",
		"kind":"package",
		"source":"filesystem",
		"pointer":{"scheme":"file","locator":"/pkg/go-providers","resolved_at":"2026-05-14T10:00:00Z"},
		"summary":"go-providers multi-provider adapter",
		"body":"longer body",
		"author":{"agent_id":"indexer","agent_version":"1.0"},
		"session_id":"indexer:01HX",
		"tags":["framework","go"],
		"ttl_seconds":86400,
		"confidence":0.8
	}`
	rr := postRawJSON(t, srv, "/v1/knowledge/write", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rev.Facets.Pointer == nil || rev.Facets.Pointer.ResolvedAt == nil {
		t.Errorf("pointer.resolved_at did not survive the decode: %+v", rev.Facets.Pointer)
	}
	if len(rev.Tags) != 2 {
		t.Errorf("tags = %v, want 2", rev.Tags)
	}
}

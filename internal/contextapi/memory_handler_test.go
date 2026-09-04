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
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newMemoryTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := NewServer(cs, contextpolicy.New())
	srv.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return srv
}

func TestMemoryWrite_ReturnsRevisionWithDomain(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"prefs.output_style",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"status":"draft",
		"payload":{"summary":"terse output"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
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
	if rev.RevisionID == "" {
		t.Error("empty revision_id")
	}
	if rev.Domain != "memory" {
		t.Errorf("domain = %q, want memory", rev.Domain)
	}
}

func TestMemoryWrite_NoStoreReturns503(t *testing.T) {
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := NewServer(cs, contextpolicy.New())
	// No MemoryStore wired.

	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(`{"namespace":"user/x/memory/notes"}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMemoryHistory_RoundtripViaHTTP(t *testing.T) {
	srv := newMemoryTestServer(t)

	write := func(body string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("write status = %d; body=%s", rr.Code, rr.Body.String())
		}
	}
	base := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"prefs.history_test",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"status":"draft",
		"payload":{"summary":"v%d"}
	}`
	for i := 1; i <= 2; i++ {
		write(bytesFormat(base, i))
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/memory/history?namespace=user/chrispian/memory/notes&memory_key=prefs.history_test", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("history status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var revs []memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &revs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("history len = %d, want 2", len(revs))
	}
}

// bytesFormat is a minimal fmt.Sprintf stand-in that keeps this test file
// free of the extra import when a single %d substitution is enough.
func bytesFormat(tmpl string, n int) string {
	s := tmpl
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if i+1 < len(s) && s[i] == '%' && s[i+1] == 'd' {
			out = append(out, byte('0'+n))
			i++
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

// TestMemoryWrite_FlatMCPBodyRejectedWithNestedHint is the knowledge-write
// story on its memory-domain peer: memory_write takes author and payload as
// flat scalars over MCP (author_agent_id, author_version, payload_summary,
// payload_body) and as objects over HTTP. Posting the flat set used to write a
// revision-shaped nothing; it now names the field and the nested equivalent.
func TestMemoryWrite_FlatMCPBodyRejectedWithNestedHint(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"prefs.output_style",
		"payload_summary":"terse output",
		"payload_body":"longer",
		"author_agent_id":"test",
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9
	}`
	env := mustRejectUnknownField(t, srv, "/v1/memory/write", body, "payload_summary")
	if env.Details.ExpectedField != "payload.summary" {
		t.Errorf("details.expected_field = %q, want payload.summary", env.Details.ExpectedField)
	}
	if !strings.Contains(env.Message, "payload.summary") {
		t.Errorf("message does not name the nested equivalent: %s", env.Message)
	}
}

// TestMemoryPromote_FlatHintWithheldWhenRouteHasNoSuchObject guards the hint
// against becoming misinformation. /v1/memory/promote carries an actor, not an
// author, so a caller who sends author_version there must be told the field is
// unknown WITHOUT being advised to nest an object this body does not have.
func TestMemoryPromote_FlatHintWithheldWhenRouteHasNoSuchObject(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"source_namespace":"user/chrispian/session/s1/memory/notes",
		"source_memory_id":"01HX",
		"target_namespace":"user/chrispian/memory/notes",
		"actor_agent_id":"test",
		"author_version":"1.0"
	}`
	env := mustRejectUnknownField(t, srv, "/v1/memory/promote", body, "author_version")
	if env.Details.ExpectedField != "" {
		t.Errorf("details.expected_field = %q, want none — this route declares no author object",
			env.Details.ExpectedField)
	}
	if !slices.Contains(env.Details.AcceptedFields, "actor_version") {
		t.Errorf("accepted_fields does not list actor_version, the field they meant: %v",
			env.Details.AcceptedFields)
	}
}

// TestPostRoutesRejectUnknownFields sweeps every JSON body route these
// handlers own. They shared one defect — a bare, lenient decoder — so they are
// asserted together: a body carrying a field the endpoint does not declare is
// refused by name rather than decoded into a zero value and failed later.
func TestPostRoutesRejectUnknownFields(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	for _, tc := range []struct {
		name  string
		path  string
		body  string
		field string
	}{
		{
			name:  "memory_write",
			path:  "/v1/memory/write",
			body:  `{"namespace":"user/chrispian/memory/notes","memroy_key":"typo"}`,
			field: "memroy_key",
		},
		{
			// The singular of the field this route actually takes: close
			// enough to read past, and silently a recall over no namespaces.
			name:  "memory_recall",
			path:  "/v1/memory/recall",
			body:  `{"namespace":"user/chrispian/memory/notes"}`,
			field: "namespace",
		},
		{
			name:  "memory_deprecate",
			path:  "/v1/memory/deprecate",
			body:  `{"revision_id":"01HX","reason":"superseded"}`,
			field: "reason",
		},
		{
			// The negative control in internal/mcpadapter/touch_parity_test.go
			// sends exactly this: the door used to answer 200 with touched=0.
			name:  "memory_touch",
			path:  "/v1/memory/touch",
			body:  `{"revisions":["01HX"]}`,
			field: "revisions",
		},
		{
			name:  "memory_promote",
			path:  "/v1/memory/promote",
			body:  `{"source_namespace":"a","source_memory_id":"01HX","target_namespace":"b","actor":"test"}`,
			field: "actor",
		},
		{
			name:  "knowledge_write",
			path:  "/v1/knowledge/write",
			body:  `{"namespace":"user/chrispian/knowledge/framework","facets":{"kind":"package"}}`,
			field: "facets",
		},
		{
			// /v1/memory/recall nests these filters; /v1/tesseract/lookup
			// declares them flat. Mixing the two up is the same class of
			// mistake as mixing up MCP and HTTP.
			name:  "tesseract_lookup",
			path:  "/v1/tesseract/lookup",
			body:  `{"namespaces":["user/chrispian/memory/notes"],"filters":{"tags":["a"]}}`,
			field: "filters",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustRejectUnknownField(t, srv, tc.path, tc.body, tc.field)
		})
	}
}

// TestPostRoutesStillAcceptTheirCanonicalBodies is the regression half of the
// sweep above: DisallowUnknownFields must reject only what was never declared.
func TestPostRoutesStillAcceptTheirCanonicalBodies(t *testing.T) {
	srv := newKnowledgeTestServer(t)

	write := postRawJSON(t, srv, "/v1/memory/write", `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"prefs.output_style",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"status":"draft",
		"tags":["style"],
		"payload":{"summary":"terse output","body":"longer"}
	}`)
	if write.Code != http.StatusOK {
		t.Fatalf("memory write status = %d, want 200; body=%s", write.Code, write.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(write.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode written revision: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{
			// Every knob at once, flat and nested alike, including the
			// embedded pageArgs fields promoted into the top level.
			name: "memory_recall",
			path: "/v1/memory/recall",
			body: `{
				"namespaces":["user/chrispian/memory/notes"],
				"revision_scope":"current",
				"ranking":"relevance",
				"query":"terse",
				"filters":{"Tags":["style"]},
				"limit":5,
				"search_mode":"lexical",
				"payload_mode":"full",
				"budget_bytes":100000,
				"budget_tokens":20000,
				"estimate_only":false
			}`,
		},
		{
			name: "tesseract_lookup",
			path: "/v1/tesseract/lookup",
			body: `{
				"namespaces":["user/chrispian/memory/notes"],
				"revision_scope":"current",
				"ranking":"activation",
				"limit":5,
				"domains":["memory"],
				"statuses":["draft"],
				"tags":["style"],
				"confidence_min":0.1,
				"payload_mode":"full",
				"cursor":""
			}`,
		},
		{
			name: "memory_touch",
			path: "/v1/memory/touch",
			body: `{"revision_ids":["` + rev.RevisionID + `"]}`,
		},
		{
			name: "memory_deprecate",
			path: "/v1/memory/deprecate",
			body: `{"revision_id":"` + rev.RevisionID + `"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := postRawJSON(t, srv, tc.path, tc.body)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

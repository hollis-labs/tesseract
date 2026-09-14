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
		"actor":"user",
		"memory_key":"prefs.output_style",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"user",
		"confidence":0.9,
		"status":"draft",
		"summary":"terse output"
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
	if rev.ItemID == "" || rev.ItemID != rev.MemoryID {
		t.Errorf("response aliases item_id=%q memory_id=%q", rev.ItemID, rev.MemoryID)
	}
	if rev.Domain != "memory" {
		t.Errorf("domain = %q, want memory", rev.Domain)
	}
}

func TestMemoryHTTPWorkstreamWriteFilterAndNullRejection(t *testing.T) {
	srv := newMemoryTestServer(t)
	writeBody := `{
		"namespace":"user/chrispian/memory/notes", "actor":"user", "memory_key":"workstream.http",
		"workstream_id":"ws-http", "author":{"agent_id":"test"}, "trigger":"manual",
		"session_id":"http-session", "derived_from":"observation", "confidence":0.9,
		"summary":"workstream http"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(writeBody))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"workstream_id":"ws-http"`) {
		t.Fatalf("write status=%d body=%s", rr.Code, rr.Body.String())
	}
	recall := httptest.NewRequest(http.MethodPost, "/v1/memory/recall", bytes.NewBufferString(`{
		"namespaces":["user/chrispian/memory/notes"], "workstream_id":"ws-http", "payload_mode":"full"
	}`))
	recallResult := httptest.NewRecorder()
	srv.ServeHTTP(recallResult, recall)
	if recallResult.Code != http.StatusOK || !strings.Contains(recallResult.Body.String(), "workstream.http") {
		t.Fatalf("recall status=%d body=%s", recallResult.Code, recallResult.Body.String())
	}

	nullBody := strings.Replace(writeBody, `"workstream_id":"ws-http"`, `"workstream_id":null`, 1)
	nullReq := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(nullBody))
	nullResult := httptest.NewRecorder()
	srv.ServeHTTP(nullResult, nullReq)
	if nullResult.Code != http.StatusBadRequest || !strings.Contains(nullResult.Body.String(), "null is not omission") {
		t.Fatalf("null status=%d body=%s", nullResult.Code, nullResult.Body.String())
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
		"actor":"user",
		"memory_key":"prefs.history_test",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"user",
		"confidence":0.9,
		"status":"draft",
		"summary":"v%d"
	}`
	for i := 1; i <= 2; i++ {
		write(bytesFormat(base, i))
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/memory/history?namespace=user/chrispian/memory/notes&key=prefs.history_test", nil)
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

// TestMemoryWrite_MCPContentNamesRejectedWithFlatHint checks that the retained
// MCP payload_summary spelling points HTTP callers at top-level summary.
func TestMemoryWrite_MCPContentNamesRejectedWithFlatHint(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"prefs.output_style",
		"payload_summary":"terse output",
		"payload_body":"longer",
		"author_agent_id":"test",
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"user",
		"confidence":0.9
	}`
	env := mustRejectUnknownField(t, srv, "/v1/memory/write", body, "payload_summary")
	if env.Details.ExpectedField != "summary" {
		t.Errorf("details.expected_field = %q, want summary", env.Details.ExpectedField)
	}
	if !strings.Contains(env.Message, "summary") {
		t.Errorf("message does not name the HTTP equivalent: %s", env.Message)
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
		"actor":"user",
		"memory_key":"prefs.output_style",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"user",
		"confidence":0.9,
		"status":"draft",
		"tags":["style"],
		"summary":"terse output","body":"longer"
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

// TestMemoryWrite_RetiredOriginNamesItsReplacement covers the HTTP half of the
// `origin` → `derived_from` rename (2026-09-12).
//
// The REFUSAL is free here and always has been: decodeJSON runs
// DisallowUnknownFields, so an HTTP caller sending the old name fails closed
// rather than having its value dropped. What this pins is the other half —
// that the rejection tells a migrating client where the value went. A bare
// `unknown field "origin"` is a correct denial and a useless one: it says the
// field is wrong without saying what is right, which is the shape
// `actionable-failures.md` calls a denial that costs another turn.
func TestMemoryWrite_RetiredOriginNamesItsReplacement(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"namespace":"user/chrispian/memory/notes",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"observation",
		"confidence":0.9,
		"summary":"a write using the retired field name"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — the retired name was ACCEPTED, and a write that "+
			"silently drops derived_from takes a default ranking weight nobody chose; body=%s",
			rr.Code, rr.Body.String())
	}
	// Asserting only that the body mentions `derived_from` would be VACUOUS:
	// every unknown-field rejection already lists the accepted fields, and
	// `derived_from` is one of them. What the hint uniquely adds is the
	// CORRESPONDENCE — that this retired name maps to that new one — so that is
	// what gets pinned, structurally in details and as an explanation in the
	// message.
	var env struct {
		Message string `json:"message"`
		Details struct {
			RenamedTo string `json:"renamed_to"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Details.RenamedTo != "derived_from" {
		t.Errorf("details.renamed_to = %q, want %q — the caller is told its field is unknown "+
			"but not where the value went: %s", env.Details.RenamedTo, "derived_from", rr.Body.String())
	}
	if !strings.Contains(env.Message, "renamed") {
		t.Errorf("the message does not explain that the field was renamed, so a caller reading "+
			"prose rather than parsing details learns nothing: %s", env.Message)
	}
}

// Retiring payload must not silently discard content, even when both shapes
// arrive together. The successful control also pins flat-in / nested-out.
func TestMemoryWriteRetiredPayloadRefused(t *testing.T) {
	srv := newMemoryTestServer(t)
	base := `{"namespace":"user/chrispian/memory/notes","actor":"user","memory_key":"flat.write","author":{"agent_id":"test"},"trigger":"explicit","session_id":"s1","derived_from":"user","confidence":0.9`
	for _, content := range []string{
		`,"payload":{"summary":"old"}}`,
		`,"summary":"new","payload":{"summary":"old"}}`,
		`,"payload":{"summary":"old"},"summary":"new"}`,
		`,"summary":"new","payload":null}`,
	} {
		env := mustRejectUnknownField(t, srv, "/v1/memory/write", base+content, "payload")
		for _, field := range []string{"summary", "body", "data", "data_schema_hash"} {
			if !strings.Contains(env.Message, field) {
				t.Errorf("refusal omits %s: %s", field, env.Message)
			}
		}
	}
	rr, _ := postJSON(t, srv, "/v1/memory/write", base+`,"summary":"new","body":"prose","data":{"n":9007199254740993}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("flat write: %d %s", rr.Code, rr.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatal(err)
	}
	if rev.MemoryKey != "flat.write" || rev.Payload.Summary != "new" || rev.Payload.Body != "prose" || string(rev.Payload.Data) != `{"n":9007199254740993}` {
		t.Fatalf("flat write lost nested response content: %s", rr.Body.String())
	}
	history, err := srv.MemoryStore.GetHistory(context.Background(), "user/chrispian/memory/notes", "flat.write")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].RevisionID != rev.RevisionID {
		t.Fatalf("rejected shapes wrote revisions: %+v", history)
	}
}

func TestMemoryWrite_UserScopeRejectionTeachesCorrectScope(t *testing.T) {
	srv := newMemoryTestServer(t)

	// Writing to user scope without actor=user returns 403 policy_denied with the teaching message.
	body := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"prefs.output_style",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"user",
		"confidence":0.9,
		"status":"draft",
		"summary":"terse output"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 Forbidden; body=%s", rr.Code, rr.Body.String())
	}
	var errResp struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Code != "policy_denied" {
		t.Errorf("code = %q, want policy_denied", errResp.Code)
	}
	for _, needle := range []string{
		"writes to protected namespace",
		"require actor=user",
		"project/{slug}",
		"system",
		"tesseract_skills namespaces",
	} {
		if !strings.Contains(errResp.Message, needle) {
			t.Errorf("error message missing %q:\n%s", needle, errResp.Message)
		}
	}
}

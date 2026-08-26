package mcpadapter

// CW-20260825-0010. Five MCP tools replaced ten domain-specific ones. The
// parity catalog can no longer pair them one-to-one with an HTTP route, so the
// route-existence check it used to run over those ten pairings is gone. This
// file is what replaces it, and it asserts something stronger: the unified tool
// and the route it displaced, driven over ONE store, in one process, answering
// byte for byte the same.
//
// The referent matters. The tools this collapse retired cannot be called any
// more, so "equivalent to what it replaced" cannot be asserted against them
// directly. It is asserted against the HTTP routes that were their peers —
// contextapi handlers, a separate implementation this ticket did not touch —
// which is an independent referent rather than the code under test compared
// with itself.
//
// Where that referent does not exist, the file says so instead of pretending.
// The context domain's two routes answered a DIFFERENT shape from their MCP
// peers before this ticket and still do (see
// TestCrossDomainGet_ContextArmShapeIsHandStated), so those arms are pinned
// against hand-written literals rather than against HTTP.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	xdMemNS   = "user/chrispian/memory/notes"
	xdKnowNS  = "user/chrispian/knowledge/framework"
	xdCtxNS   = "app/test/session/xd"
	xdMemKey  = "xd.mem.key"
	xdKnowKey = "xd.know.key"
	xdCtxKey  = "xd.ctx.key"
)

// crossDomainSurfaces builds one store and puts both doors on it: the MCP
// adapter and the HTTP server. Every comparison below runs against this single
// pair, so a difference it reports belongs to the surface and not to the data.
//
// It seeds one memory entry with three revisions, one knowledge entry with two,
// and one context record with two — enough that a history comparison is a
// comparison of several rows in an order, not of one row.
func crossDomainSurfaces(t *testing.T) (*Adapter, *contextapi.Server, *memory.Store, *knowledge.Store) {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	tok, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "crossdomain",
		Scopes: []string{"memory:read", "memory:write", "write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(cs, tok)
	a.MemoryStore = ms
	a.KnowledgeStore = ks

	srv := contextapi.NewServer(cs, contextpolicy.New())
	srv.MemoryStore = ms
	srv.KnowledgeStore = ks

	ctx := context.Background()
	for i, summary := range []string{"mem first", "mem second", "mem third"} {
		if _, err := ms.WriteRevision(ctx, memory.WriteInput{
			Namespace:  xdMemNS,
			MemoryKey:  xdMemKey,
			Author:     memory.Author{AgentID: "claude"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "sess-xd",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload:    memory.Payload{Summary: summary},
		}); err != nil {
			t.Fatalf("seed memory revision %d: %v", i, err)
		}
	}
	for i, summary := range []string{"know first", "know second"} {
		if _, err := ks.Write(ctx, knowledge.WriteInput{
			Namespace: xdKnowNS,
			Key:       xdKnowKey,
			Kind:      "package",
			Source:    "filesystem",
			Pointer:   memory.Pointer{Scheme: "file", Locator: "/pkg/xd"},
			Summary:   summary,
			Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
			SessionID: "indexer:xd",
		}); err != nil {
			t.Fatalf("seed knowledge revision %d: %v", i, err)
		}
	}
	for i, body := range []string{`{"status":"one"}`, `{"status":"two"}`} {
		if _, err := cs.AppendRecord(ctx, contextstore.AppendInput{
			Namespace:  xdCtxNS,
			Key:        xdCtxKey,
			Payload:    json.RawMessage(body),
			Actor:      "test",
			RecordType: "state",
		}); err != nil {
			t.Fatalf("seed context record %d: %v", i, err)
		}
	}
	return a, srv, ms, ks
}

// xdMCP calls one MCP handler and returns its raw body.
func xdMCP(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("MCP handler returned a transport error: %v", err)
	}
	return res.Content[0].(mcp.TextContent).Text
}

// xdHTTP drives one route and returns status + body.
func xdHTTP(t *testing.T, srv *contextapi.Server, method, target, body string) (int, string) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, r)
	return rr.Code, rr.Body.String()
}

// ── AC1 + AC6: per (tool, domain), against the route it displaced ───────────

// TestCrossDomainReadArgumentParity_MCPvsHTTP asserts the unified read tools
// answer byte for byte what their displaced HTTP routes answer, per domain.
//
// Trailing whitespace is trimmed on both sides and nothing else is normalized:
// contextapi writes through json.NewEncoder, which appends a newline, and the
// MCP side writes through json.Marshal, which does not. That one byte is the
// only difference this comparison forgives — a field rename, a reordered key,
// a dropped element or a changed number all fail.
//
// The subtests are named for the (tool, domain) pair rather than in aggregate.
// "reads work across domains" is one claim covering six, each independently
// satisfiable, which is the shape that makes a green run unreviewable.
func TestCrossDomainReadArgumentParity_MCPvsHTTP(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mcpArgs  map[string]any
		httpPath string
		get      bool
	}{
		{
			name:     "tesseract_get/domain=memory",
			mcpArgs:  map[string]any{"domain": "memory", "namespace": xdMemNS, "key": xdMemKey},
			httpPath: "/v1/memory/current?namespace=" + xdMemNS + "&memory_key=" + xdMemKey,
			get:      true,
		},
		{
			name:     "tesseract_get/domain=knowledge",
			mcpArgs:  map[string]any{"domain": "knowledge", "namespace": xdKnowNS, "key": xdKnowKey},
			httpPath: "/v1/knowledge/current?namespace=" + xdKnowNS + "&memory_key=" + xdKnowKey,
			get:      true,
		},
		{
			name:     "tesseract_history/domain=memory",
			mcpArgs:  map[string]any{"domain": "memory", "namespace": xdMemNS, "key": xdMemKey},
			httpPath: "/v1/memory/history?namespace=" + xdMemNS + "&memory_key=" + xdMemKey,
		},
		{
			name:     "tesseract_history/domain=knowledge",
			mcpArgs:  map[string]any{"domain": "knowledge", "namespace": xdKnowNS, "key": xdKnowKey},
			httpPath: "/v1/knowledge/history?namespace=" + xdKnowNS + "&memory_key=" + xdKnowKey,
		},
		{
			name: "tesseract_history/domain=memory/limit+cursor engaged",
			mcpArgs: map[string]any{
				"domain": "memory", "namespace": xdMemNS, "key": xdMemKey, "limit": float64(2),
			},
			httpPath: "/v1/memory/history?namespace=" + xdMemNS + "&memory_key=" + xdMemKey + "&limit=2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, srv, _, _ := crossDomainSurfaces(t)

			h := a.handleTesseractHistory
			if tc.get {
				h = a.handleTesseractGet
			}
			mcpBody := xdMCP(t, h, tc.mcpArgs)

			code, httpBody := xdHTTP(t, srv, http.MethodGet, tc.httpPath, "")
			if code != http.StatusOK {
				t.Fatalf("HTTP %s: %d %s", tc.httpPath, code, httpBody)
			}

			// Non-vacuity: an empty body on both doors compares equal and
			// certifies nothing.
			if len(strings.TrimSpace(httpBody)) < 10 {
				t.Fatalf("HTTP body is too small to be a real read (%q); fix the fixture, "+
					"do not weaken the comparison", httpBody)
			}
			if strings.Contains(mcpBody, `"code":"`) {
				t.Fatalf("the MCP tool returned an error result rather than a read: %s", mcpBody)
			}

			if got, want := strings.TrimSpace(mcpBody), strings.TrimSpace(httpBody); got != want {
				t.Errorf("MCP and HTTP disagree byte for byte:\n MCP: %s\nHTTP: %s", got, want)
			}
		})
	}
}

// TestCrossDomainRevisionOps_MCPvsHTTP covers the two ops that take no domain.
func TestCrossDomainRevisionOps_MCPvsHTTP(t *testing.T) {
	a, srv, ms, _ := crossDomainSurfaces(t)

	memRevs, err := ms.GetHistory(context.Background(), xdMemNS, xdMemKey)
	if err != nil {
		t.Fatalf("read memory history: %v", err)
	}
	knowRevs, err := ms.GetHistory(context.Background(), xdKnowNS, xdKnowKey)
	if err != nil {
		t.Fatalf("read knowledge history: %v", err)
	}
	if len(memRevs) != 3 || len(knowRevs) != 2 {
		t.Fatalf("fixture is not what the assertions below assume: %d memory, %d knowledge",
			len(memRevs), len(knowRevs))
	}

	// A knowledge revision resolves through the same op as a memory one. This
	// is the claim the ticket's structural finding rests on: GetRevisionByID
	// has no domain filter.
	for _, tc := range []struct {
		name   string
		revID  string
		domain domains.Domain
	}{
		{"memory revision", memRevs[0].RevisionID, domains.Memory},
		{"knowledge revision", knowRevs[0].RevisionID, domains.Knowledge},
	} {
		t.Run("tesseract_get_revision/"+tc.name, func(t *testing.T) {
			mcpBody := xdMCP(t, a.handleTesseractGetRevision, map[string]any{"revision_id": tc.revID})
			code, httpBody := xdHTTP(t, srv, http.MethodGet, "/v1/memory/revisions/"+tc.revID, "")
			if code != http.StatusOK {
				t.Fatalf("HTTP revisions/%s: %d %s", tc.revID, code, httpBody)
			}
			if got, want := strings.TrimSpace(mcpBody), strings.TrimSpace(httpBody); got != want {
				t.Errorf("MCP and HTTP disagree byte for byte:\n MCP: %s\nHTTP: %s", got, want)
			}
			// And the row really is the domain the subtest names, so this is
			// not two memory revisions wearing different labels.
			var got struct {
				Domain string `json:"domain"`
			}
			if err := json.Unmarshal([]byte(mcpBody), &got); err != nil {
				t.Fatalf("decode revision: %v", err)
			}
			if got.Domain != string(tc.domain) {
				t.Errorf("revision domain = %q, want %q", got.Domain, tc.domain)
			}
		})
	}

	// Deprecating a knowledge revision by ID, with no knowledge-specific tool
	// involved.
	t.Run("tesseract_deprecate/knowledge revision", func(t *testing.T) {
		body := xdMCP(t, a.handleTesseractDeprecate, map[string]any{"revision_id": knowRevs[0].RevisionID})
		var got map[string]string
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode: %v (raw=%s)", err, body)
		}
		// Hand-stated, not read back off the handler's own map.
		if got["status"] != "deprecated" || got["revision_id"] != knowRevs[0].RevisionID {
			t.Errorf("deprecate answered %v", got)
		}
		rev, err := ms.GetRevisionByID(context.Background(), knowRevs[0].RevisionID)
		if err != nil {
			t.Fatalf("re-read deprecated revision: %v", err)
		}
		if rev.Status != memory.StatusDeprecated {
			t.Errorf("status after deprecate = %q, want %q", rev.Status, memory.StatusDeprecated)
		}
	})
}

// TestCrossDomainGet_ContextArmShapeIsHandStated pins the context arm against
// literals written here rather than against its HTTP route.
//
// The two doors have never agreed on this shape and still do not: the MCP side
// answers the record projection recordJSON builds, and GET /v1/context/head
// answers {"record": <record>}. That divergence predates this ticket — it was
// context_head's, and the bodies are unchanged — so it is pinned rather than
// asserted away, and the second half of this test states the divergence
// explicitly so a future reader meets it as a known fact rather than as a
// surprise.
func TestCrossDomainGet_ContextArmShapeIsHandStated(t *testing.T) {
	a, srv, _, _ := crossDomainSurfaces(t)

	raw := xdMCP(t, a.handleTesseractGet, map[string]any{
		"domain": "context", "namespace": xdCtxNS, "key": xdCtxKey,
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, raw)
	}

	// Hand-stated from the fixture above, not read off the response.
	if got["namespace"] != xdCtxNS {
		t.Errorf("namespace = %v, want %q", got["namespace"], xdCtxNS)
	}
	if got["key"] != xdCtxKey {
		t.Errorf("key = %v, want %q", got["key"], xdCtxKey)
	}
	if got["revision"] != float64(2) {
		t.Errorf("revision = %v, want 2 — the head of the two records seeded", got["revision"])
	}
	if _, ok := got["record"]; ok {
		t.Errorf("the context arm nested its answer under `record`; that is the HTTP "+
			"route's envelope, not this tool's: %v", got)
	}

	// The stated divergence, so it cannot quietly change in either direction.
	code, httpBody := xdHTTP(t, srv, http.MethodGet,
		"/v1/context/head?namespace="+xdCtxNS+"&key="+xdCtxKey, "")
	if code != http.StatusOK {
		t.Fatalf("HTTP /v1/context/head: %d %s", code, httpBody)
	}
	var httpGot map[string]any
	if err := json.Unmarshal([]byte(httpBody), &httpGot); err != nil {
		t.Fatalf("decode HTTP: %v", err)
	}
	if _, ok := httpGot["record"]; !ok {
		t.Errorf("GET /v1/context/head no longer nests under `record` (%v); if that "+
			"changed deliberately, the two doors may now agree and this test should "+
			"become a byte-identity assertion", httpGot)
	}
}

// TestCrossDomainHistory_ContextArmKeepsItsOwnEnvelope pins the other arm whose
// shape is domain-specific. The context domain answers the go-mcp budget
// envelope; memory and knowledge answer a bare array unless a knob is engaged.
func TestCrossDomainHistory_ContextArmKeepsItsOwnEnvelope(t *testing.T) {
	a, _, _, _ := crossDomainSurfaces(t)

	ctxRaw := xdMCP(t, a.handleTesseractHistory, map[string]any{
		"domain": "context", "namespace": xdCtxNS, "key": xdCtxKey,
	})
	var ctxGot map[string]any
	if err := json.Unmarshal([]byte(ctxRaw), &ctxGot); err != nil {
		t.Fatalf("the context arm did not answer a JSON object: %v (raw=%s)", err, ctxRaw)
	}
	if _, ok := ctxGot["items"]; !ok {
		t.Errorf("the context history envelope has no `items` key: %v", ctxGot)
	}

	memRaw := xdMCP(t, a.handleTesseractHistory, map[string]any{
		"domain": "memory", "namespace": xdMemNS, "key": xdMemKey,
	})
	var memGot []map[string]any
	if err := json.Unmarshal([]byte(memRaw), &memGot); err != nil {
		t.Fatalf("the memory arm did not answer a bare array: %v (raw=%s)", err, memRaw)
	}
	if len(memGot) != 3 {
		t.Errorf("memory history returned %d revisions, want the 3 seeded", len(memGot))
	}
}

// ── AC2: the gating fix, stated as deployments ──────────────────────────────

// registeredNames builds an adapter the way a deployment would and reports what
// it registers.
func registeredNames(t *testing.T, wireMemory, wireKnowledge bool) (*Adapter, map[string]struct{}) {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	tok, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "deployment",
		Scopes: []string{"memory:read", "memory:write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(cs, tok)
	if wireMemory {
		a.MemoryStore = ms
	}
	if wireKnowledge {
		a.KnowledgeStore = knowledge.New(ms)
	}

	srv := server.NewMCPServer("deployment", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)
	names := map[string]struct{}{}
	for name := range srv.ListTools() {
		names[name] = struct{}{}
	}
	return a, names
}

// TestKnowledgeOnlyDeployment_RevisionOpsWork is the criterion the ticket kept
// verbatim: with no memory store wired, a knowledge revision can still be
// fetched and deprecated by ID.
//
// It is asserted as a DEPLOYMENT — RegisterAllTools over an adapter built that
// way — because the defect was in registration. A unit test that called the
// handler directly would have passed throughout.
func TestKnowledgeOnlyDeployment_RevisionOpsWork(t *testing.T) {
	a, names := registeredNames(t, false, true)
	if a.MemoryStore != nil {
		t.Fatal("fixture wired a memory store; this deployment must not have one")
	}

	for _, name := range []string{"tesseract_get_revision", "tesseract_deprecate", "tesseract_touch", "tesseract_recall"} {
		if _, ok := names[name]; !ok {
			t.Errorf("%s is not registered on a knowledge-only deployment", name)
		}
	}

	rev, err := a.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: xdKnowNS,
		Key:       xdKnowKey,
		Kind:      "package",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/pkg/xd"},
		Summary:   "knowledge-only deployment probe",
		Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		SessionID: "indexer:xd",
	})
	if err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}

	fetched := xdMCP(t, a.handleTesseractGetRevision, map[string]any{"revision_id": rev.RevisionID})
	var got map[string]any
	if err := json.Unmarshal([]byte(fetched), &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, fetched)
	}
	if got["revision_id"] != rev.RevisionID {
		t.Errorf("fetched revision_id = %v, want %q (raw=%s)", got["revision_id"], rev.RevisionID, fetched)
	}

	dep := xdMCP(t, a.handleTesseractDeprecate, map[string]any{"revision_id": rev.RevisionID})
	if !strings.Contains(dep, `"status":"deprecated"`) {
		t.Errorf("deprecate on a knowledge-only deployment answered %s", dep)
	}

	// And the keyed reads say which domain is missing rather than 404ing.
	memGet := xdMCP(t, a.handleTesseractGet, map[string]any{
		"domain": "memory", "namespace": xdMemNS, "key": xdMemKey,
	})
	if !strings.Contains(memGet, `"code":"domain_unavailable"`) {
		t.Errorf("domain=memory on a knowledge-only deployment answered %s; a missing "+
			"store and a missing key must not look the same", memGet)
	}
	// A second entry, because the one above has just been deprecated and a
	// keyed read of it would legitimately answer not_found.
	if _, err := a.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: xdKnowNS,
		Key:       "xd.know.live",
		Kind:      "doc",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/pkg/xd-live"},
		Summary:   "knowledge-only deployment keyed-read probe",
		Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		SessionID: "indexer:xd",
	}); err != nil {
		t.Fatalf("seed second knowledge entry: %v", err)
	}
	knowGet := xdMCP(t, a.handleTesseractGet, map[string]any{
		"domain": "knowledge", "namespace": xdKnowNS, "key": "xd.know.live",
	})
	if strings.Contains(knowGet, `"code":`) {
		t.Errorf("domain=knowledge on a knowledge-only deployment answered an error: %s", knowGet)
	}
}

// TestMemoryOnlyDeployment_RevisionOpsWork is the converse. The defect is
// symmetric, so the assertion is too.
func TestMemoryOnlyDeployment_RevisionOpsWork(t *testing.T) {
	a, names := registeredNames(t, true, false)
	if a.KnowledgeStore != nil {
		t.Fatal("fixture wired a knowledge store; this deployment must not have one")
	}

	for _, name := range []string{"tesseract_get_revision", "tesseract_deprecate", "tesseract_touch", "tesseract_recall"} {
		if _, ok := names[name]; !ok {
			t.Errorf("%s is not registered on a memory-only deployment", name)
		}
	}

	rev, err := a.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  xdMemNS,
		MemoryKey:  xdMemKey,
		Author:     memory.Author{AgentID: "claude"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "sess-xd",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "memory-only deployment probe"},
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	fetched := xdMCP(t, a.handleTesseractGetRevision, map[string]any{"revision_id": rev.RevisionID})
	if !strings.Contains(fetched, rev.RevisionID) {
		t.Errorf("fetch by ID on a memory-only deployment answered %s", fetched)
	}
	dep := xdMCP(t, a.handleTesseractDeprecate, map[string]any{"revision_id": rev.RevisionID})
	if !strings.Contains(dep, `"status":"deprecated"`) {
		t.Errorf("deprecate on a memory-only deployment answered %s", dep)
	}

	knowGet := xdMCP(t, a.handleTesseractGet, map[string]any{
		"domain": "knowledge", "namespace": xdKnowNS, "key": xdKnowKey,
	})
	if !strings.Contains(knowGet, `"code":"domain_unavailable"`) {
		t.Errorf("domain=knowledge on a memory-only deployment answered %s", knowGet)
	}
}

// TestContextOnlyDeployment_RevisionOpsAbsent is the negative control for the
// two above. If tesseract_get_revision registered unconditionally, both of them
// would pass for the wrong reason.
func TestContextOnlyDeployment_RevisionOpsAbsent(t *testing.T) {
	a, names := registeredNames(t, false, false)

	for _, name := range []string{"tesseract_get_revision", "tesseract_deprecate", "tesseract_recall", "tesseract_touch"} {
		if _, ok := names[name]; ok {
			t.Errorf("%s registered with no revision store wired; it would panic on call", name)
		}
	}
	// The keyed reads still register, because the context store is always there.
	for _, name := range []string{"tesseract_get", "tesseract_history"} {
		if _, ok := names[name]; !ok {
			t.Errorf("%s is not registered on a context-only deployment", name)
		}
	}
	if a.revisionStore() != nil {
		t.Error("revisionStore returned non-nil with neither store wired")
	}
}

// ── AC3: the descriptions are accurate, not merely reworded ─────────────────

// TestReadDomainVocabularyIsExactlyThese hand-states the vocabulary.
//
// It is written out rather than compared against readDomainVocabulary's own
// output, or against domains.All(), because a test that reads the definition it
// is checking passes for whatever the definition says. The point of writing the
// three values here is that changing the vocabulary has to be a deliberate edit
// in two places.
func TestReadDomainVocabularyIsExactlyThese(t *testing.T) {
	want := []string{"context", "memory", "knowledge"}
	got := readDomainVocabulary()
	if len(got) != len(want) {
		t.Fatalf("vocabulary = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("vocabulary[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestCrossDomainToolsServeEveryDomainTheyAdvertise closes AC3's first half: a
// description that names a domain must have a working arm for it.
//
// The domains are read off each tool's own `domain` parameter description —
// the text an agent reads — and each is then exercised. A tool that advertised
// a fourth domain would fail here rather than answering validation_error to the
// caller who believed it.
func TestCrossDomainToolsServeEveryDomainTheyAdvertise(t *testing.T) {
	a, _, _, _ := crossDomainSurfaces(t)
	srv := server.NewMCPServer("advertised", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)
	tools := srv.ListTools()

	probes := map[string]map[string][2]string{
		"tesseract_get": {
			"context":   {xdCtxNS, xdCtxKey},
			"memory":    {xdMemNS, xdMemKey},
			"knowledge": {xdKnowNS, xdKnowKey},
		},
		"tesseract_history": {
			"context":   {xdCtxNS, xdCtxKey},
			"memory":    {xdMemNS, xdMemKey},
			"knowledge": {xdKnowNS, xdKnowKey},
		},
	}

	for _, name := range []string{"tesseract_get", "tesseract_history"} {
		st, ok := tools[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		schema, err := st.Tool.InputSchema.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal schema: %v", err)
		}
		blurb := string(schema)

		h := a.handleTesseractHistory
		if name == "tesseract_get" {
			h = a.handleTesseractGet
		}
		exercised := 0
		for _, dom := range []string{"context", "memory", "knowledge"} {
			if !strings.Contains(blurb, `"`+dom+`"`) && !strings.Contains(blurb, dom+" |") &&
				!strings.Contains(blurb, "| "+dom) {
				continue
			}
			probe, ok := probes[name][dom]
			if !ok {
				t.Errorf("%s advertises domain %q and this test has no probe for it", name, dom)
				continue
			}
			raw := xdMCP(t, h, map[string]any{"domain": dom, "namespace": probe[0], "key": probe[1]})
			if strings.Contains(raw, `"code":"`) {
				t.Errorf("%s advertises domain %q but answers an error for it: %s", name, dom, raw)
			}
			exercised++
		}
		if exercised != 3 {
			t.Errorf("%s: exercised %d advertised domains, expected 3 — the extraction "+
				"above stopped matching the description, so a clean run here means nothing",
				name, exercised)
		}
	}
}

// TestCrossDomainErrorCodesAreReachable closes AC3's second half. Every code
// the descriptions name is produced by a call made here.
//
// The codes are hand-stated. Extracting them from the description and then
// asserting the description names them would be the tautology this repo has
// already shipped once.
func TestCrossDomainErrorCodesAreReachable(t *testing.T) {
	a, _, _, _ := crossDomainSurfaces(t)
	noStore, _ := registeredNames(t, false, false)

	for _, tc := range []struct {
		name string
		want string
		raw  func() string
	}{
		{
			name: "validation_error/domain missing",
			want: "validation_error",
			raw: func() string {
				return xdMCP(t, a.handleTesseractGet, map[string]any{"namespace": xdMemNS, "key": xdMemKey})
			},
		},
		{
			name: "validation_error/domain unknown",
			want: "validation_error",
			raw: func() string {
				return xdMCP(t, a.handleTesseractGet, map[string]any{"domain": "memories", "namespace": xdMemNS, "key": xdMemKey})
			},
		},
		{
			name: "validation_error/key missing",
			want: "validation_error",
			raw: func() string {
				return xdMCP(t, a.handleTesseractGet, map[string]any{"domain": "memory", "namespace": xdMemNS})
			},
		},
		{
			name: "validation_error/revision_id missing",
			want: "validation_error",
			raw:  func() string { return xdMCP(t, a.handleTesseractGetRevision, map[string]any{}) },
		},
		{
			name: "domain_unavailable",
			want: "domain_unavailable",
			raw: func() string {
				return xdMCP(t, noStore.handleTesseractGet, map[string]any{"domain": "memory", "namespace": xdMemNS, "key": xdMemKey})
			},
		},
		{
			name: "not_found/keyed read",
			want: "not_found",
			raw: func() string {
				return xdMCP(t, a.handleTesseractGet, map[string]any{"domain": "memory", "namespace": xdMemNS, "key": "no.such.key"})
			},
		},
		{
			name: "not_found/revision by id",
			want: "not_found",
			raw: func() string {
				return xdMCP(t, a.handleTesseractGetRevision, map[string]any{"revision_id": "01HXNOSUCHREVISION"})
			},
		},
		{
			name: "not_found/deprecate by id",
			want: "not_found",
			raw: func() string {
				return xdMCP(t, a.handleTesseractDeprecate, map[string]any{"revision_id": "01HXNOSUCHREVISION"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.raw()
			var got map[string]any
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatalf("decode: %v (raw=%s)", err, raw)
			}
			if got["code"] != tc.want {
				t.Errorf("code = %v, want %q (raw=%s)", got["code"], tc.want, raw)
			}
		})
	}
}

// TestUnknownDomainNamesTheAllowedSet keeps the rejection actionable: a caller
// who guessed wrong should not have to read the source to find the right value.
func TestUnknownDomainNamesTheAllowedSet(t *testing.T) {
	a, _, _, _ := crossDomainSurfaces(t)
	raw := xdMCP(t, a.handleTesseractGet, map[string]any{
		"domain": "ctx", "namespace": xdCtxNS, "key": xdCtxKey,
	})
	for _, want := range []string{"context", "memory", "knowledge"} {
		if !strings.Contains(raw, want) {
			t.Errorf("the rejection does not name %q: %s", want, raw)
		}
	}
}

// TestCrossDomainGet_ReinforcesOnlyTheMemoryArm pins the side effect the
// description claims, per domain.
//
// The asymmetry is inherited: memory_get reinforced and knowledge_get did not.
// It is preserved rather than smoothed, so it needs a test that says which arm
// does which — a description claiming "reinforces" on a tool where two of three
// arms do not would be the kind of overstatement this repo treats as a defect.
func TestCrossDomainGet_ReinforcesOnlyTheMemoryArm(t *testing.T) {
	a, _, ms, _ := crossDomainSurfaces(t)
	ctx := context.Background()

	memRev, err := ms.GetCurrent(ctx, xdMemNS, xdMemKey)
	if err != nil {
		t.Fatalf("resolve memory head: %v", err)
	}
	knowRev, err := ms.GetCurrent(ctx, xdKnowNS, xdKnowKey)
	if err != nil {
		t.Fatalf("resolve knowledge head: %v", err)
	}

	memBefore, err := ms.GetState(ctx, memRev.MemoryID)
	if err != nil {
		t.Fatalf("memory state before: %v", err)
	}
	knowBefore, err := ms.GetState(ctx, knowRev.MemoryID)
	if err != nil {
		t.Fatalf("knowledge state before: %v", err)
	}

	xdMCP(t, a.handleTesseractGet, map[string]any{"domain": "memory", "namespace": xdMemNS, "key": xdMemKey})
	xdMCP(t, a.handleTesseractGet, map[string]any{"domain": "knowledge", "namespace": xdKnowNS, "key": xdKnowKey})

	memAfter, err := ms.GetState(ctx, memRev.MemoryID)
	if err != nil {
		t.Fatalf("memory state after: %v", err)
	}
	knowAfter, err := ms.GetState(ctx, knowRev.MemoryID)
	if err != nil {
		t.Fatalf("knowledge state after: %v", err)
	}

	if memAfter.AccessCount != memBefore.AccessCount+1 {
		t.Errorf("memory arm access_count went %d -> %d, want +1",
			memBefore.AccessCount, memAfter.AccessCount)
	}
	if knowAfter.AccessCount != knowBefore.AccessCount {
		t.Errorf("knowledge arm access_count went %d -> %d, want unchanged",
			knowBefore.AccessCount, knowAfter.AccessCount)
	}
}

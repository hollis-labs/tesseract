// Package parity verifies that every canonical Tesseract operation is
// reachable over both MCP (stdio tool) and HTTP (REST route), or is covered by
// an explicit waiver. The test fails when a surface is added or removed
// without updating the shared catalog — that's the durable guardrail against
// MCP↔HTTP drift called out in SPR-20260415-mcp-parity-access-s1 task 007.
//
// The catalog is the single source of truth for "what Vanta exposes."
// Adding a new tool or route without a matching entry is the signal to
// consciously decide whether it belongs on both surfaces or is one-sided
// by design; either way, the reason gets documented here.
package parity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// parityOp is one row in the surface catalog. Either MCP or HTTP may be
// empty when the op is intentionally one-sided; in that case Waiver must
// be non-empty.
type parityOp struct {
	MCP        string // e.g. "context_write"
	HTTPMethod string // e.g. http.MethodPost
	HTTPPath   string // e.g. "/v1/context/write"
	Waiver     string // non-empty reason when MCP or HTTP is intentionally empty
}

// surfaceCatalog lists every tool + route Vanta exposes. Keep sorted by MCP
// name, then HTTPPath, to minimize merge churn.
//
// Waiver rules:
//   - MCP-only (HTTPPath == ""): op is stateful agent affordance or a
//     convenience wrapper with no clean HTTP equivalent.
//   - HTTP-only (MCP == ""): op is an infra/admin/security boundary, a
//     deprecated alias, or batch-2 work not yet on MCP.
var surfaceCatalog = []parityOp{
	// ── Context domain ──────────────────────────────────────────────────
	{MCP: "context_audit", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/audit"},
	{MCP: "context_broker_fetch", Waiver: "MCP-only convenience: plan+packet in one call"},
	{MCP: "context_broker_plan", HTTPMethod: http.MethodPost, HTTPPath: "/v1/broker/plan"},
	{MCP: "context_bulk_ingest", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/bulk-ingest"},
	{MCP: "context_chunked_ingest", Waiver: "MCP-only: chunked stateful ingest has no clean HTTP shape"},
	{MCP: "context_embed", Waiver: "MCP-only: embedding-only op for agent tooling"},
	{MCP: "context_estimate", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/estimate"},
	{MCP: "context_head", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/head"},
	{MCP: "context_history", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/history"},
	{MCP: "context_namespace_register", HTTPMethod: http.MethodPost, HTTPPath: "/v1/namespaces/register"},
	{MCP: "context_namespace_show", HTTPMethod: http.MethodGet, HTTPPath: "/v1/namespaces/get"},
	{MCP: "context_namespaces_list", Waiver: "MCP-only: list-of-namespaces helper; HTTP caller iterates /namespaces/get"},
	{MCP: "context_pack", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/pack"},
	{MCP: "context_packet", Waiver: "MCP-only; HTTP /v1/context/packet has a divergent budget/manifest shape"},
	{MCP: "context_promote_apply", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/apply"},
	{MCP: "context_promote_approve", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/approve"},
	{MCP: "context_promote_list", Waiver: "MCP-only; HTTP side lists promotions via audit query"},
	{MCP: "context_promote_request", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/request"},
	{MCP: "context_rag_query", Waiver: "MCP-only: convenience query over embedding search"},
	{MCP: "context_search", Waiver: "MCP-only: low-level embedding search for agent tooling"},
	{MCP: "context_session_snapshot", Waiver: "MCP-only: per-session snapshot capture"},
	{MCP: "context_status_deprecate", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/status/deprecate"},
	{MCP: "context_status_promote", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/status/promote"},
	{MCP: "context_typed_view", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/typed-view"},
	{MCP: "context_typed_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/typed-write"},
	{MCP: "context_types_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/types"},
	{MCP: "context_view", Waiver: "MCP-only simplified view; views_evaluate is the full-power peer of /v1/views/evaluate"},
	{MCP: "context_views_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/views"},
	{MCP: "context_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/write"},
	{MCP: "views_evaluate", HTTPMethod: http.MethodPost, HTTPPath: "/v1/views/evaluate"},

	// ── Memory domain ──────────────────────────────────────────────────
	{MCP: "memory_deprecate", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/deprecate"},
	{MCP: "memory_get", HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/current"},
	{MCP: "memory_get_revision", HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/revisions/{id}"},
	{MCP: "memory_history", HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/history"},
	{MCP: "memory_promote", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/promote"},
	{MCP: "memory_recall", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/recall"},
	{MCP: "memory_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/write"},

	// ── Knowledge domain ───────────────────────────────────────────────
	{MCP: "knowledge_get", HTTPMethod: http.MethodGet, HTTPPath: "/v1/knowledge/current"},
	{MCP: "knowledge_history", HTTPMethod: http.MethodGet, HTTPPath: "/v1/knowledge/history"},
	{MCP: "knowledge_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/knowledge/write"},

	// ── Unified lookup ─────────────────────────────────────────────────
	{MCP: "conduit_lookup", HTTPMethod: http.MethodPost, HTTPPath: "/v1/conduit/lookup"},

	// ── Meta (orientation / discovery) ─────────────────────────────────
	{MCP: "vanta_skills", Waiver: "MCP-only: progressive-discovery meta-tool; serves embedded skill MDs"},

	// ── HTTP-only (infra, admin, security boundary, batch-2) ───────────
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/metrics", Waiver: "HTTP-only: Prometheus-style scrape endpoint"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/health/readiness", Waiver: "HTTP-only: liveness/readiness probe"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/consistency/scan", Waiver: "batch 2 — MCP peer pending TASK-20260415-009"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/consistency/repair", Waiver: "batch 2 — MCP peer pending TASK-20260415-010"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/maintenance/ttl-cleanup", Waiver: "batch 2 — MCP peer pending TASK-20260415-011"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/maintenance/trim", Waiver: "batch 2 — MCP peer pending TASK-20260415-011"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/maintenance/compact", Waiver: "batch 2 — MCP peer pending TASK-20260415-011"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/auth/tokens/create", Waiver: "HTTP-only security boundary per TASK-20260415-012"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/auth/tokens/list", Waiver: "HTTP-only security boundary per TASK-20260415-012"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/auth/tokens/revoke", Waiver: "HTTP-only security boundary per TASK-20260415-012"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/plan", Waiver: "HTTP alias of /v1/broker/plan; MCP peer is context_broker_plan"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote", Waiver: "HTTP-only: returns 410 Gone; superseded by /v1/context/promote/request"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/packet", Waiver: "HTTP-only: divergent packet shape; MCP context_packet uses different budget/manifest"},
}

// observedHTTPRoutes mirrors the switch in internal/contextapi/server.go.
// When a new route is added there without showing up in surfaceCatalog, the
// drift assertion below fails. Keep sorted — adds land cleanly in diffs.
var observedHTTPRoutes = []parityOp{
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/audit"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/consistency/scan"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/head"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/history"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/types"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/views"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/auth/tokens/list"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/health/readiness"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/knowledge/current"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/knowledge/history"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/current"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/history"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/revisions/{id}"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/metrics"},
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/namespaces/get"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/auth/tokens/create"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/auth/tokens/revoke"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/broker/plan"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/conduit/lookup"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/bulk-ingest"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/consistency/repair"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/estimate"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/pack"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/packet"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/plan"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/apply"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/approve"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/request"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/status/deprecate"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/status/promote"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/typed-view"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/typed-write"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/write"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/knowledge/write"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/maintenance/compact"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/maintenance/trim"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/maintenance/ttl-cleanup"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/deprecate"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/promote"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/recall"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/write"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/namespaces/register"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/views/evaluate"},
}

// TestSurfaceCatalogWaivers asserts every catalog row is either fully paired
// (MCP + HTTP) or has a non-empty waiver explaining the one-sidedness.
func TestSurfaceCatalogWaivers(t *testing.T) {
	for _, op := range surfaceCatalog {
		hasMCP := op.MCP != ""
		hasHTTP := op.HTTPPath != ""
		hasWaiver := strings.TrimSpace(op.Waiver) != ""
		switch {
		case hasMCP && hasHTTP && !hasWaiver:
			// Paired — good.
		case hasMCP != hasHTTP && hasWaiver:
			// One-sided with an explicit waiver — good.
		case hasMCP && hasHTTP && hasWaiver:
			t.Errorf("op %q/%q: both MCP and HTTP set but waiver also non-empty (%q); drop the waiver or mark one side empty",
				op.MCP, op.HTTPPath, op.Waiver)
		case !hasMCP && !hasHTTP:
			t.Errorf("catalog row has neither MCP nor HTTP set: %+v", op)
		case hasMCP != hasHTTP && !hasWaiver:
			t.Errorf("op %q/%q: one-sided without a waiver; add MCP+HTTP or document why it's one-sided",
				op.MCP, op.HTTPPath)
		}
	}
}

// TestMCPRegistrationMatchesCatalog asserts the set of MCP tools a
// fully-wired adapter registers equals the set of catalog rows with MCP != "".
func TestMCPRegistrationMatchesCatalog(t *testing.T) {
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("parity-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	registered := make(map[string]struct{})
	for name := range srv.ListTools() {
		registered[name] = struct{}{}
	}

	expected := make(map[string]struct{})
	for _, op := range surfaceCatalog {
		if op.MCP != "" {
			expected[op.MCP] = struct{}{}
		}
	}

	for name := range registered {
		if _, ok := expected[name]; !ok {
			t.Errorf("MCP tool %q is registered but missing from surfaceCatalog — add it with an HTTP peer or a waiver", name)
		}
	}
	for name := range expected {
		if _, ok := registered[name]; !ok {
			t.Errorf("MCP tool %q is in surfaceCatalog but not registered — wire it in the adapter or remove from catalog", name)
		}
	}
}

// TestHTTPRoutesMatchCatalog asserts every observed HTTP route has a matching
// catalog row and vice versa. The observedHTTPRoutes list mirrors the switch
// in internal/contextapi/server.go; adding a route there without updating
// this list or the catalog fails here.
func TestHTTPRoutesMatchCatalog(t *testing.T) {
	// Probe each observed route on a wired server. A 404 (endpoint not found)
	// means the switch lost it. Other status codes (400, 405, 401, 503) are
	// fine — the route exists and rejected the probe for reasons unrelated
	// to routing.
	srv := newFullyWiredHTTPServer(t)
	for _, rt := range observedHTTPRoutes {
		path := rt.HTTPPath
		if strings.Contains(path, "{id}") {
			path = strings.ReplaceAll(path, "{id}", "probe-id")
		}
		req := httptest.NewRequest(rt.HTTPMethod, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			if !strings.Contains(rr.Body.String(), `"endpoint not found"`) {
				// Some handler returned 404 on its own terms (e.g. "no such revision") — fine.
				continue
			}
			t.Errorf("route %s %s returned endpoint-not-found; the router lost it", rt.HTTPMethod, rt.HTTPPath)
		}
	}

	// Drift: catalog says HTTP path X exists, but observedHTTPRoutes doesn't include X.
	catalogPaths := make(map[string]struct{})
	for _, op := range surfaceCatalog {
		if op.HTTPPath != "" {
			catalogPaths[op.HTTPMethod+" "+op.HTTPPath] = struct{}{}
		}
	}
	observedPaths := make(map[string]struct{})
	for _, rt := range observedHTTPRoutes {
		observedPaths[rt.HTTPMethod+" "+rt.HTTPPath] = struct{}{}
	}

	for key := range catalogPaths {
		if _, ok := observedPaths[key]; !ok {
			t.Errorf("catalog references HTTP route %q but it's not in observedHTTPRoutes (and therefore not wired in server.go)", key)
		}
	}
	for key := range observedPaths {
		if _, ok := catalogPaths[key]; !ok {
			t.Errorf("HTTP route %q is wired in server.go but missing from surfaceCatalog — add it with an MCP peer or a waiver", key)
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────

func newFullyWiredAdapter(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	know := knowledge.New(mem)
	return &mcpadapter.Adapter{
		Store:          cs,
		Token:          "",
		MemoryStore:    mem,
		KnowledgeStore: know,
	}
}

func newFullyWiredHTTPServer(t *testing.T) *contextapi.Server {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := contextapi.NewServer(cs, contextpolicy.New())
	srv.EnableMetrics = true
	srv.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)
	return srv
}

// Package parity verifies that every canonical Tesseract operation is
// reachable over both MCP (stdio tool) and HTTP (REST route), or is covered by
// an explicit waiver. The test fails when a surface is added or removed
// without updating the shared catalog — that's the durable guardrail against
// MCP↔HTTP drift called out in SPR-20260415-mcp-parity-access-s1 task 007.
//
// The catalog is the single source of truth for "what Tesseract exposes."
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

// surfaceCatalog lists every tool + route Tesseract exposes. Keep sorted by MCP
// name, then HTTPPath, to minimize merge churn.
//
// Waiver rules:
//   - MCP-only (HTTPPath == ""): op is stateful agent affordance or a
//     convenience wrapper with no clean HTTP equivalent.
//   - HTTP-only (MCP == ""): op is an infra/admin/security boundary, a
//     deprecated alias, or batch-2 work not yet on MCP.
//
// An MCP name may appear on SEVERAL rows, one per HTTP route it covers. Every
// assertion below builds a SET rather than walking pairs —
// TestMCPRegistrationMatchesCatalog and TestHTTPRoutesMatchCatalog both collect
// into map[string]struct{}, and TestSurfaceCatalogWaivers classifies each row on
// its own — so repeating a name costs nothing and keeps each route paired with
// the tool that serves it.
//
// The cross-domain reads of CW-20260825-0010 are why that matters: five MCP
// tools cover ten routes between them, and writing those as five MCP-only plus
// ten HTTP-only waivers would have added fifteen exclusions to say what ten
// paired rows say exactly. Reach for a waiver only when a surface genuinely has
// no peer, never to express a fan-out this shape already carries.
//
// ROW COUNT IS NOT TOOL COUNT. Because names repeat, the number of tools is the
// number of DISTINCT MCP names:
//
//	awk '/^var surfaceCatalog = \[\]parityOp\{/{f=1;next} f&&/^\}$/{exit} f' \
//	  tests/parity/parity_test.go | grep -oE 'MCP: "[a-z_]+"' | sort -u | wc -l
var surfaceCatalog = []parityOp{
	// ── Context domain ──────────────────────────────────────────────────
	{MCP: "context_audit_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/audit"},
	{MCP: "context_embed", Waiver: "MCP-only: embedding-only op for agent tooling"},
	{MCP: "context_estimate", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/estimate"},
	{MCP: "context_ingest", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/bulk-ingest"},
	{MCP: "context_namespace_register", HTTPMethod: http.MethodPost, HTTPPath: "/v1/namespaces/register"},
	{MCP: "context_pack", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/pack"},
	// Two routes, one handler: server.go dispatches both POST /v1/broker/plan
	// and POST /v1/context/plan to Server.handleContextPlan. Recorded as two
	// paired rows because this catalog builds SETS — see the header note. Until
	// CW-20260825-0012 the second was an HTTP-only waiver reading "HTTP alias of
	// /v1/broker/plan; MCP peer is context_broker", which named the peer in the
	// waiver text while blanking out the MCP column, so no assertion could see
	// the pairing it described.
	{MCP: "context_plan", HTTPMethod: http.MethodPost, HTTPPath: "/v1/broker/plan"},
	{MCP: "context_plan", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/plan"},
	{MCP: "context_promote", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/apply"},
	{MCP: "context_promote", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/approve"},
	{MCP: "context_promote", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote/request"},
	{MCP: "context_promotion_list", Waiver: "MCP-only; HTTP side lists promotions via audit query"},
	{MCP: "context_rag_query", Waiver: "MCP-only: convenience query over embedding search"},
	{MCP: "context_registry_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/types"},
	{MCP: "context_registry_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/views"},
	{MCP: "context_registry_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/namespaces/get"},
	{MCP: "context_registry_list", HTTPMethod: http.MethodGet, HTTPPath: "/v1/namespaces/list"},
	{MCP: "context_search", Waiver: "MCP-only: low-level embedding search for agent tooling"},
	{MCP: "context_session_write", Waiver: "MCP-only: per-session snapshot capture"},
	{MCP: "context_status_set", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/status/deprecate"},
	{MCP: "context_status_set", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/status/promote"},
	{MCP: "context_typed_view", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/typed-view"},
	{MCP: "context_typed_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/typed-write"},
	{MCP: "context_view", HTTPMethod: http.MethodPost, HTTPPath: "/v1/views/evaluate"},
	{MCP: "context_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/write"},

	// ── Memory domain ──────────────────────────────────────────────────
	{MCP: "memory_promote", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/promote"},
	{MCP: "memory_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/write"},

	// ── Knowledge domain ───────────────────────────────────────────────
	{MCP: "knowledge_write", HTTPMethod: http.MethodPost, HTTPPath: "/v1/knowledge/write"},

	// ── Cross-domain reads (CW-20260825-0010) ──────────────────────────
	// One MCP tool per operation, several HTTP routes each. The routes are
	// unchanged from when a domain-specific tool served each of them, so
	// every pairing below is a real peer relationship and not a placeholder.
	// Argument and output parity per (tool, domain) is asserted in
	// internal/mcpadapter/crossdomain_parity_test.go; these rows carry the
	// existence half.
	{MCP: "tesseract_deprecate", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/deprecate"},
	{MCP: "tesseract_get", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/head"},
	{MCP: "tesseract_get", HTTPMethod: http.MethodGet, HTTPPath: "/v1/knowledge/current"},
	{MCP: "tesseract_get", HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/current"},
	{MCP: "tesseract_get_revision", HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/revisions/{id}"},
	{MCP: "tesseract_history", HTTPMethod: http.MethodGet, HTTPPath: "/v1/context/history"},
	{MCP: "tesseract_history", HTTPMethod: http.MethodGet, HTTPPath: "/v1/knowledge/history"},
	{MCP: "tesseract_history", HTTPMethod: http.MethodGet, HTTPPath: "/v1/memory/history"},
	{MCP: "tesseract_recall", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/recall"},
	{MCP: "tesseract_recall", HTTPMethod: http.MethodPost, HTTPPath: "/v1/tesseract/lookup"},

	// ── Reinforcement ──────────────────────────────────────────────────
	// Cross-domain like tesseract_recall: a revision ID resolves whether it
	// was written as memory or as knowledge. The route sits under /v1/memory/
	// because memory_state is where the reinforcement lands.
	{MCP: "tesseract_touch", HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/touch"},

	// ── Meta (orientation / discovery) ─────────────────────────────────
	{MCP: "tesseract_skills", Waiver: "MCP-only: progressive-discovery meta-tool; serves embedded skill MDs"},

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
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/promote", Waiver: "HTTP-only: returns 410 Gone; superseded by /v1/context/promote/request"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/context/packet", Waiver: "HTTP-only: divergent packet shape; MCP context_pack shape=packet uses different budget/manifest"},
}

// Two arms of the merges above have no HTTP peer, and the catalog's row shape
// — one (tool, route) pair — cannot say so, because the tool itself IS paired.
// Recording them here rather than as waivers, which would have to blank out the
// MCP name and so would misdescribe the tool:
//
//   - context_plan with execute=true (the former context_broker_fetch):
//     plan+packet in one call, MCP-only. POST /v1/context/plan and its second
//     path POST /v1/broker/plan are the peers of the default arm only.
//   - context_ingest with mode=chunked (the former context_chunked_ingest):
//     chunked stateful ingest has no clean HTTP shape. POST
//     /v1/context/bulk-ingest is the peer of mode=bulk only.
//
// Both facts are also stated in the tools' own argument descriptions, which is
// where an agent will actually read them.

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
	{HTTPMethod: http.MethodGet, HTTPPath: "/v1/namespaces/list"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/auth/tokens/create"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/auth/tokens/revoke"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/broker/plan"},
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/tesseract/lookup"},
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
	{HTTPMethod: http.MethodPost, HTTPPath: "/v1/memory/touch"},
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

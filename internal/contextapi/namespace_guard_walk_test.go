package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// CW-20261016-0205: a token limited by namespace_globs must not read or change
// data outside them through ANY authenticated route. This test walks every
// authRequired route in apiRoutes with a restricted token against fixture data
// in a namespace the token does not cover, and fails for any route that is not
// declared below, so a new route must say how it handles namespaces.

const (
	guardOutNS    = "project/fixture-out/data"
	guardInNS     = "project/fixture-in/data"
	guardOutMark  = "OUT-OF-GLOB-MARKER"
	guardInMark   = "IN-GLOB-MARKER"
	guardMemOutNS = "project/fixture-out/memory/notes"
)

type guardCase struct {
	method, path string
	body         any
	// denied: the route must answer 403 for the out-of-glob ask. Otherwise the
	// route answers normally but must not carry any out-of-glob data.
	denied bool
	// notFound: a by-id route must answer 404 for an out-of-glob id, the same
	// as a missing one, so ids cannot be probed.
	notFound bool
	// echoesInput: the response repeats the caller's own namespace argument.
	echoesInput bool
	// teeth: an unrestricted token gets data from the out-of-glob namespace from this same
	// request, proving the case would catch a leak.
	teeth bool
}

type guardFixture struct {
	srv            *Server
	restricted     map[string]string
	unrestricted   map[string]string
	outRevisionID  string
	outItemID      string
	promoteRequest string
}

func newGuardFixture(t *testing.T) *guardFixture {
	t.Helper()
	srv := referenceHTTPServer(t)
	ctx := context.Background()
	view, ok := typeregistry.Default().GetView("briefing")
	if !ok || len(view.Types) == 0 {
		t.Fatal("briefing view fixture missing")
	}
	for ns, mark := range map[string]string{guardOutNS: guardOutMark, guardInNS: guardInMark} {
		for _, in := range []contextstore.AppendInput{
			{Namespace: ns, Key: "doc", Actor: "fixture", Payload: json.RawMessage(`{"marker":"` + mark + `"}`)},
			{Namespace: ns, Key: "typed", Actor: "fixture", Payload: json.RawMessage(`{"marker":"` + mark + `"}`),
				RecordType: view.Types[0], Status: "canonical"},
		} {
			if _, err := srv.Store.AppendRecord(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
	}
	rev, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{
		Domain: domains.Memory, Actor: "user", Namespace: guardMemOutNS, MemoryKey: "fx.secret",
		Summary: guardOutMark, Author: memory.Author{AgentID: "test"}, SessionID: "guard",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}

	mk := func(label string, globs []string) map[string]string {
		raw, _, err := srv.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
			Label: label, TTL: time.Hour, NamespaceGlobs: globs,
			Scopes: []string{"read", "write", "memory:read", "memory:write", "promote.request", "promote.approve", "promote.apply", "packet", "repair", "namespace.register"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + raw}
	}
	srv.ManagedAuth = true
	f := &guardFixture{
		srv:           srv,
		restricted:    mk("guard-restricted", []string{"project/fixture-in/*"}),
		unrestricted:  mk("guard-unrestricted", []string{"*"}),
		outRevisionID: rev.RevisionID,
		outItemID:     rev.ItemID,
	}
	// A promote request whose source is out of the restricted token's globs,
	// created by the unrestricted token, for the approve/apply routes.
	res := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/promote/request", map[string]any{
		"actor": "fixture", "client_id": "fixture",
		"source_namespace": guardOutNS, "source_key": "doc",
		"target_namespace": guardOutNS + "/promoted", "target_key": "doc",
	}, f.unrestricted)
	if res.Code != http.StatusOK {
		t.Fatalf("seed promote request: %d %s", res.Code, res.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	for _, k := range []string{"request_id", "id"} {
		if v, ok := created[k].(string); ok {
			f.promoteRequest = v
		}
	}
	if f.promoteRequest == "" {
		t.Fatalf("promote request id missing in %s", res.Body.String())
	}
	return f
}

func (f *guardFixture) cases() (map[routeKey][]guardCase, map[routeKey]string) {
	get := func(path string, denied, teeth bool) []guardCase {
		return []guardCase{{method: http.MethodGet, path: path, denied: denied, teeth: teeth}}
	}
	post := func(path string, body any, denied, teeth bool) []guardCase {
		return []guardCase{{method: http.MethodPost, path: path, body: body, denied: denied, teeth: teeth}}
	}
	out := "namespace=" + guardOutNS + "&key=doc"
	memOut := "namespace=" + guardMemOutNS + "&key=fx.secret"
	selOnly := func(ns ...string) map[string]any {
		s := map[string]any{"revision_scope": "all"}
		if len(ns) > 0 {
			s["namespaces"] = ns
		}
		return map[string]any{"selector": s}
	}
	sel := func(ns ...string) map[string]any {
		m := selOnly(ns...)
		m["include_payload"] = true
		return m
	}
	byID := func(method, path string, body any) []guardCase {
		return []guardCase{{method: method, path: path, body: body, notFound: true}}
	}
	both := func(a, b []guardCase) []guardCase { return append(a, b...) }

	expect := map[routeKey][]guardCase{
		{http.MethodGet, "/v1/context/head"}:    get("/v1/context/head?"+out, true, true),
		{http.MethodGet, "/v1/context/history"}: get("/v1/context/history?"+out, true, true),
		{http.MethodGet, "/v1/context/audit"}: both(
			get("/v1/context/audit?namespace="+guardOutNS, true, false),
			get("/v1/context/audit", false, true)),
		{http.MethodGet, "/v1/context/consistency/scan"}:    get("/v1/context/consistency/scan", false, false),
		{http.MethodPost, "/v1/context/consistency/repair"}: post("/v1/context/consistency/repair", map[string]any{}, true, false),
		{http.MethodPost, "/v1/views/evaluate"}: both(
			post("/v1/views/evaluate", sel(guardOutNS), true, true),
			post("/v1/views/evaluate", sel(), false, true)),
		{http.MethodPost, "/v1/context/estimate"}: both(
			post("/v1/context/estimate", selOnly(guardOutNS), true, false),
			post("/v1/context/estimate", selOnly(), false, false)),
		{http.MethodPost, "/v1/context/packet"}: both(
			post("/v1/context/packet", map[string]any{"selector": map[string]any{"namespaces": []string{guardOutNS}, "revision_scope": "head"}, "assembly": map[string]any{"shape": map[string]any{"include_payload": true}}}, true, true),
			post("/v1/context/packet", map[string]any{"selector": map[string]any{"revision_scope": "head"}, "assembly": map[string]any{"include_pins": true, "shape": map[string]any{"include_payload": true}}}, false, true)),
		{http.MethodPost, "/v1/context/pack"}: both(
			post("/v1/context/pack", map[string]any{"view_id": "briefing", "namespaces": []string{guardOutNS}}, true, true),
			post("/v1/context/pack", map[string]any{"view_id": "briefing"}, false, true)),
		{http.MethodPost, "/v1/context/typed-view"}: both(
			post("/v1/context/typed-view", map[string]any{"view_id": "briefing", "namespaces": []string{guardOutNS}, "include_payload": true}, true, true),
			post("/v1/context/typed-view", map[string]any{"view_id": "briefing", "include_payload": true}, false, true)),
		{http.MethodPost, "/v1/context/plan"}: post("/v1/context/plan", map[string]any{
			"intent": "custom", "constraints": map[string]any{"namespaces": []string{guardOutNS}}}, true, false),
		{http.MethodPost, "/v1/broker/plan"}: post("/v1/broker/plan", map[string]any{
			"intent": "custom", "constraints": map[string]any{"namespaces": []string{guardOutNS}}}, true, false),

		{http.MethodPost, "/v1/context/status/promote"}:   post("/v1/context/status/promote", map[string]any{"namespace": guardOutNS, "key": "doc"}, true, false),
		{http.MethodPost, "/v1/context/status/deprecate"}: post("/v1/context/status/deprecate", map[string]any{"namespace": guardOutNS, "key": "doc"}, true, false),
		{http.MethodPost, "/v1/context/promote/request"}: post("/v1/context/promote/request", map[string]any{
			"source_namespace": guardOutNS, "source_key": "doc", "target_namespace": guardInNS, "target_key": "x"}, true, false),
		{http.MethodPost, "/v1/context/promote/approve"}: byID(http.MethodPost, "/v1/context/promote/approve", map[string]any{"request_id": f.promoteRequest}),
		{http.MethodPost, "/v1/context/promote/apply"}:   byID(http.MethodPost, "/v1/context/promote/apply", map[string]any{"request_id": f.promoteRequest}),

		{http.MethodGet, "/v1/memory/revisions/"}: byID(http.MethodGet, "/v1/memory/revisions/"+f.outRevisionID, nil),
		{http.MethodPost, "/v1/memory/deprecate"}: byID(http.MethodPost, "/v1/memory/deprecate", map[string]any{"revision_id": f.outRevisionID}),
		{http.MethodGet, "/v1/memory/current"}:    get("/v1/memory/current?"+memOut, true, true),
		{http.MethodGet, "/v1/memory/history"}:    get("/v1/memory/history?"+memOut, true, true),
		{http.MethodPost, "/v1/memory/recall"}:    post("/v1/memory/recall", map[string]any{"namespaces": []string{guardMemOutNS}, "query": "x"}, true, false),
		{http.MethodGet, "/v1/recall"}:            get("/v1/recall?namespace="+guardMemOutNS+"&query=x", true, false),

		{http.MethodGet, "/v1/namespaces/get"}:  get("/v1/namespaces/get?namespace="+guardOutNS, true, false),
		{http.MethodGet, "/v1/namespaces/list"}: get("/v1/namespaces/list", false, false),
		{http.MethodPost, "/v1/namespaces/register"}: post("/v1/namespaces/register", map[string]any{
			"namespace": guardOutNS, "owner_type": "user", "owner_id": "x", "policy": map[string]any{}}, true, false),
		{http.MethodPost, "/v1/admin/namespaces/preview"}: post("/v1/admin/namespaces/preview", map[string]any{
			"namespace": guardOutNS, "owner_type": "user", "owner_id": "x", "policy": map[string]any{}}, true, false),
		{http.MethodPost, "/v1/admin/namespaces/update"}: post("/v1/admin/namespaces/update", map[string]any{
			"namespace": guardOutNS, "owner_type": "user", "owner_id": "x", "policy": map[string]any{}}, true, false),
		{http.MethodGet, "/v1/admin/namespaces/history"}: []guardCase{{method: http.MethodGet, path: "/v1/admin/namespaces/history?namespace=" + guardOutNS, echoesInput: true}},

		{http.MethodPost, "/v1/maintenance/trim"}:        post("/v1/maintenance/trim", map[string]any{"namespace_pattern": "project/fixture-out/%", "dry_run": true}, true, false),
		{http.MethodPost, "/v1/maintenance/compact"}:     post("/v1/maintenance/compact", map[string]any{"namespace_pattern": "project/fixture-out/%", "dry_run": true}, true, false),
		{http.MethodPost, "/v1/maintenance/ttl-cleanup"}: post("/v1/maintenance/ttl-cleanup", map[string]any{}, true, false),

		{http.MethodPost, "/v1/context/write"}:       post("/v1/context/write", map[string]any{"actor": "x", "namespace": guardOutNS, "key": "k", "payload": map[string]any{}}, true, false),
		{http.MethodPost, "/v1/context/typed-write"}: post("/v1/context/typed-write", map[string]any{"actor": "x", "namespace": guardOutNS, "key": "k", "payload": map[string]any{}}, true, false),
		{http.MethodPost, "/v1/context/bulk-ingest"}: post("/v1/context/bulk-ingest", map[string]any{"actor": "x", "items": []map[string]any{{"namespace": guardOutNS, "key": "k", "payload": map[string]any{}}}}, true, false),
		{http.MethodPost, "/v1/memory/write"}:        post("/v1/memory/write", map[string]any{"domain": "memory", "namespace": guardMemOutNS, "memory_key": "fx.new", "summary": "x"}, true, false),
		{http.MethodPost, "/v1/memory/touch"}:        post("/v1/memory/touch", map[string]any{"revision_ids": []string{f.outRevisionID}}, false, false),
		{http.MethodPost, "/v1/refs/resolve"}:        post("/v1/refs/resolve", map[string]any{"revision_id": f.outRevisionID}, true, false),
		{http.MethodPost, "/v1/tesseract/lookup"}:    post("/v1/tesseract/lookup", map[string]any{"namespaces": []string{guardMemOutNS}, "query": "x"}, true, false),
		{http.MethodGet, "/v1/items/"}:               get("/v1/items/"+f.outItemID, true, false),
	}

	const noNS = "no namespaced data; gated by scope only"
	exempt := map[routeKey]string{
		{http.MethodPost, "/v1/context/promote"}:           "retired route; always answers 410",
		{http.MethodGet, "/v1/context/types"}:              "type registry; no stored data",
		{http.MethodGet, "/v1/context/views"}:              "view registry; no stored data",
		{http.MethodPost, "/v1/synthesis/ask"}:             "needs an LLM provider; guarded per namespace before recall (requireNamespaceSelectorAccess)",
		{http.MethodPost, "/v1/workspace/write"}:           "guarded by requireNamespaceAccess; needs workspace fixtures",
		{http.MethodPost, "/v1/workspace/delete"}:          "guarded by requireNamespaceAccess; needs workspace fixtures",
		{http.MethodGet, "/v1/workspace/current"}:          "guarded by requireNamespaceAccess; needs workspace fixtures",
		{http.MethodPost, "/v1/workspace/promote/request"}: "authorizeWorkspacePromotion",
		{http.MethodPost, "/v1/workspace/promote/approve"}: "authorizeWorkspacePromotion",
		{http.MethodPost, "/v1/workspace/promote/apply"}:   "authorizeWorkspacePromotion",
		{http.MethodPost, "/v1/memory/promote"}:            "guarded by requireNamespaceAccess on source and target",
		{http.MethodPost, "/v1/knowledge/write"}:           "guarded by requireNamespaceAccess",
		{http.MethodGet, "/v1/knowledge/current"}:          "guarded by requireNamespaceAccess",
		{http.MethodGet, "/v1/knowledge/history"}:          "guarded by requireNamespaceAccess",
		{http.MethodPost, "/v1/event/write"}:               "guarded by requireNamespaceAccess",
		{http.MethodGet, "/v1/event/log"}:                  "guarded by requireNamespaceAccess per namespace",
	}
	for _, k := range []routeKey{
		{http.MethodGet, "/v1/admin/setup"}, {http.MethodGet, "/v1/admin/settings"},
		{http.MethodPost, "/v1/admin/settings/preview"}, {http.MethodPost, "/v1/admin/settings/apply"},
		{http.MethodGet, "/v1/admin/config/backups"}, {http.MethodPost, "/v1/admin/config/backup"},
		{http.MethodPost, "/v1/admin/config/restore"}, {http.MethodGet, "/v1/admin/queue"},
		{http.MethodGet, "/v1/admin/queue/failures"}, {http.MethodPost, "/v1/admin/queue/retry-failed"},
		{http.MethodPost, "/v1/admin/queue/backfill"}, {http.MethodGet, "/v1/admin/storage"},
		{http.MethodPost, "/v1/auth/tokens/create"}, {http.MethodPost, "/v1/auth/tokens/rotate"},
		{http.MethodGet, "/v1/auth/tokens/list"}, {http.MethodPost, "/v1/auth/tokens/revoke"},
		{http.MethodGet, "/v1/auth/whoami"}, {http.MethodGet, "/v1/auth/summary"},
	} {
		exempt[k] = noNS
	}
	return expect, exempt
}

func TestEveryAuthRequiredRouteDeclaresNamespaceHandling(t *testing.T) {
	f := &guardFixture{promoteRequest: "x", outRevisionID: "x"}
	expect, exempt := f.cases()
	for _, rt := range apiRoutes {
		if rt.auth != authRequired {
			continue
		}
		key := routeKey{method: rt.method, path: rt.path}
		_, inExpect := expect[key]
		_, inExempt := exempt[key]
		if !inExpect && !inExempt {
			t.Errorf("%s %s has no namespace-handling declaration: add it to cases() in namespace_guard_walk_test.go (a case with a restricted token, or an exemption with a reason)", rt.method, rt.path)
		}
		if inExpect && inExempt {
			t.Errorf("%s %s is both expected and exempt", rt.method, rt.path)
		}
	}
}

func TestRestrictedTokenSeesNothingOutsideItsNamespaceGlobs(t *testing.T) {
	f := newGuardFixture(t)
	expect, _ := f.cases()
	for key, cases := range expect {
		for i, c := range cases {
			t.Run(key.method+" "+key.path+"#"+string(rune('a'+i)), func(t *testing.T) {
				res := performJSONWithHeaders(t, f.srv, c.method, c.path, c.body, f.restricted)
				body := res.Body.String()
				if strings.Contains(body, guardOutMark) {
					t.Fatalf("restricted token received out-of-glob data: %d %s", res.Code, body)
				}
				if c.notFound {
					if res.Code != http.StatusNotFound {
						t.Fatalf("status = %d, want 404 for an out-of-glob id: %s", res.Code, body)
					}
				} else if c.denied {
					if res.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403 for an out-of-glob request: %s", res.Code, body)
					}
				} else {
					if res.Code >= 500 {
						t.Fatalf("status = %d: %s", res.Code, body)
					}
					if !c.echoesInput && strings.Contains(body, "fixture-out") {
						t.Fatalf("response names an out-of-glob namespace: %s", body)
					}
				}
				if c.teeth {
					full := performJSONWithHeaders(t, f.srv, c.method, c.path, c.body, f.unrestricted)
					if !strings.Contains(full.Body.String(), "fixture-out") {
						t.Fatalf("control: an unrestricted token does not receive the marker (%d %s); this case proves nothing", full.Code, full.Body.String())
					}
				}
			})
		}
	}
}

func TestRestrictedTokenStillReadsItsOwnNamespaces(t *testing.T) {
	f := newGuardFixture(t)
	in := "namespace=" + guardInNS + "&key=doc"
	res := performJSONWithHeaders(t, f.srv, http.MethodGet, "/v1/context/head?"+in, nil, f.restricted)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), guardInMark) {
		t.Fatalf("head in-glob = %d %s", res.Code, res.Body.String())
	}
	res = performJSONWithHeaders(t, f.srv, http.MethodPost, "/v1/context/packet", map[string]any{
		"selector": map[string]any{"revision_scope": "head"},
		"assembly": map[string]any{"shape": map[string]any{"include_payload": true}},
	}, f.restricted)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), guardInMark) {
		t.Fatalf("empty-selector packet must still return in-glob records: %d %s", res.Code, res.Body.String())
	}
}

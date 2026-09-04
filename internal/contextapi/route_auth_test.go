package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// publicRoutes is the complete set of routes that may answer without
// credentials while a token mode is configured.
//
// This list is the security contract. Adding to it is a deliberate act:
// TestRouteTableAuthCoverage fails if the table and this list disagree in
// either direction, so a route cannot become public by accident and cannot be
// declared public here without actually being classified that way.
var publicRoutes = map[routeKey]string{
	{method: http.MethodGet, path: "/v1/health/readiness"}: "liveness probing must work before a token is issued",
	{method: http.MethodGet, path: "/v1/metrics"}:          "scrape target; already gated behind --metrics",
}

// TestRouteTableClassifiesEveryRoute is the guard that makes forgetting to
// classify a new route a build-breaking mistake rather than a silent hole. A
// table entry written without an auth field gets authUnclassified, and fails
// here.
func TestRouteTableClassifiesEveryRoute(t *testing.T) {
	if len(apiRoutes) == 0 {
		t.Fatal("route table is empty")
	}
	seen := map[routeKey]bool{}
	for _, rt := range apiRoutes {
		key := routeKey{method: rt.method, path: rt.path}
		if rt.auth == authUnclassified {
			t.Errorf("route %s %s has no authorization classification — set authPublic or authRequired", rt.method, rt.path)
		}
		if rt.auth != authPublic && rt.auth != authRequired {
			t.Errorf("route %s %s has unknown authorization classification %d", rt.method, rt.path, rt.auth)
		}
		if rt.handler == nil {
			t.Errorf("route %s %s has no handler", rt.method, rt.path)
		}
		if !strings.HasPrefix(rt.path, "/v1/") {
			t.Errorf("route %s %s is not under /v1/", rt.method, rt.path)
		}
		if seen[key] {
			t.Errorf("route %s %s is declared twice", rt.method, rt.path)
		}
		seen[key] = true
	}
}

// TestRouteTablePublicSetIsExactlyDeclared pins the public surface. It fails
// both when a route is made public without updating publicRoutes and when
// publicRoutes claims a route that is not actually public.
func TestRouteTablePublicSetIsExactlyDeclared(t *testing.T) {
	actual := map[routeKey]bool{}
	for _, rt := range apiRoutes {
		if rt.auth == authPublic {
			actual[routeKey{method: rt.method, path: rt.path}] = true
		}
	}
	for key := range actual {
		if _, ok := publicRoutes[key]; !ok {
			t.Errorf("route %s %s is public in the route table but is not declared in publicRoutes; "+
				"if this is intentional, add it there with the reason", key.method, key.path)
		}
	}
	for key := range publicRoutes {
		if !actual[key] {
			t.Errorf("publicRoutes declares %s %s public but the route table does not", key.method, key.path)
		}
	}
}

// TestRouteTableAuthCoverage walks every route in the dispatcher and asserts
// that, under each configured token mode, an anonymous request is rejected —
// except for the declared public set. This is the route-table coverage test:
// a new route is exercised the moment it lands in apiRoutes.
func TestRouteTableAuthCoverage(t *testing.T) {
	modes := []struct {
		name  string
		apply func(*testing.T, *Server)
	}{
		{
			name: "static-token",
			apply: func(t *testing.T, srv *Server) {
				srv.AuthToken = "coverage-secret"
			},
		},
		{
			name: "managed-auth",
			apply: func(t *testing.T, srv *Server) {
				srv.ManagedAuth = true
			},
		},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			srv := newTestServer(t)
			// Metrics on, so /v1/metrics resolves to its handler instead of
			// the 404 the flag gate would otherwise produce — the point here
			// is that it is not a 401.
			srv.EnableMetrics = true
			mode.apply(t, srv)

			for _, rt := range apiRoutes {
				rt := rt
				name := rt.method + " " + rt.path
				t.Run(name, func(t *testing.T) {
					res := callRoute(t, srv, rt, nil)
					_, public := publicRoutes[routeKey{method: rt.method, path: rt.path}]
					if public {
						if res.Code == http.StatusUnauthorized {
							t.Fatalf("public route %s returned 401; body=%s", name, res.Body.String())
						}
						return
					}
					if res.Code != http.StatusUnauthorized {
						t.Fatalf("route %s answered an anonymous request with %d (want 401); body=%s",
							name, res.Code, res.Body.String())
					}
					var envelope map[string]any
					if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
						t.Fatalf("route %s: unmarshal error envelope: %v", name, err)
					}
					if envelope["code"] != "auth_required" {
						t.Fatalf("route %s: expected code=auth_required, got %v", name, envelope["code"])
					}
				})
			}
		})
	}
}

// TestRouteTableAcceptsValidCredentials is the other half of the coverage
// test: a protected route must not be a 401 for a caller that does hold
// credentials. It only asserts "not 401" — the routes disagree wildly about
// what a valid response looks like, and that is not what is under test here.
func TestRouteTableAcceptsValidCredentials(t *testing.T) {
	srv := newTestServer(t)
	srv.EnableMetrics = true
	srv.AuthToken = "coverage-secret"
	headers := map[string]string{"Authorization": "Bearer coverage-secret"}

	for _, rt := range apiRoutes {
		rt := rt
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			res := callRoute(t, srv, rt, headers)
			if res.Code == http.StatusUnauthorized {
				t.Fatalf("route %s %s rejected a valid static token: %s", rt.method, rt.path, res.Body.String())
			}
		})
	}
}

// TestReadRoutesRequireCredentialsUnderTokenMode spot-checks the reads that
// used to answer anonymously, including the two that leaked the most: the
// token inventory and the admin introspection GETs.
func TestReadRoutesRequireCredentialsUnderTokenMode(t *testing.T) {
	previouslyOpen := []string{
		"/v1/auth/tokens/list",
		"/v1/admin/setup",
		"/v1/admin/settings",
		"/v1/admin/storage",
		"/v1/admin/queue",
		"/v1/admin/queue/failures",
		"/v1/admin/config/backups",
		"/v1/admin/namespaces/history",
		"/v1/namespaces/list",
		"/v1/context/audit",
		"/v1/context/head",
		"/v1/recall",
	}

	srv := newTestServer(t)
	srv.AuthToken = "reads-secret"
	for _, path := range previouslyOpen {
		res := performJSON(t, srv, http.MethodGet, path, nil)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("GET %s answered %d without credentials (want 401): %s", path, res.Code, res.Body.String())
		}
	}
}

// TestUnknownRouteStillNotFound keeps the dispatcher's fallthrough intact: an
// unrouted path is a 404, not a 401, and does not become a credential oracle.
func TestUnknownRouteStillNotFound(t *testing.T) {
	srv := newTestServer(t)
	srv.AuthToken = "secret"
	res := performJSON(t, srv, http.MethodGet, "/v1/does-not-exist", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown route, got %d: %s", res.Code, res.Body.String())
	}
	// Wrong method on a real path is equally unrouted.
	res = performJSON(t, srv, http.MethodDelete, "/v1/context/head", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unrouted method, got %d: %s", res.Code, res.Body.String())
	}
}

// TestStaticTokenScopeGuardsFailClosed covers the fail-open bug: with a token
// mode configured, a scope guard must be able to say no. The static token
// carries the default scope set, which does not include "admin", so the admin
// mutations reject it — previously they accepted anything, because a request
// with no claims was treated as fully privileged.
func TestStaticTokenScopeGuardsFailClosed(t *testing.T) {
	srv := newTestServer(t)
	srv.AuthToken = "static-secret"
	headers := map[string]string{"Authorization": "Bearer static-secret"}

	adminMutations := []string{
		"/v1/admin/settings/apply",
		"/v1/admin/config/restore",
		"/v1/admin/settings/preview",
		"/v1/admin/config/backup",
	}
	for _, path := range adminMutations {
		res := performJSONWithHeaders(t, srv, http.MethodPost, path, map[string]any{}, headers)
		if res.Code != http.StatusForbidden {
			t.Errorf("POST %s with the static token returned %d (want 403 insufficient_scope): %s",
				path, res.Code, res.Body.String())
		}
	}

	// The scopes the static token does hold keep working — this mode has to
	// stay usable for the writes it was created to protect.
	write := performJSONWithHeaders(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	}, headers)
	if write.Code != http.StatusOK {
		t.Fatalf("static token should retain the write scope, got %d: %s", write.Code, write.Body.String())
	}
}

// TestScopeGuardsPassWithNoAuthModeConfigured pins the local-first default:
// with no token mode at all there are no claims to check, and the guards must
// not start refusing the unauthenticated loopback workflow.
func TestScopeGuardsPassWithNoAuthModeConfigured(t *testing.T) {
	srv := newTestServer(t)

	write := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"v": 1},
	})
	if write.Code != http.StatusOK {
		t.Fatalf("unauthenticated loopback write should succeed, got %d: %s", write.Code, write.Body.String())
	}
	head := performJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil)
	if head.Code != http.StatusOK {
		t.Fatalf("unauthenticated loopback read should succeed, got %d: %s", head.Code, head.Body.String())
	}
}

// TestStaticTokenClaimsMatchStoreDefaults guards the scope list duplicated
// into staticTokenClaims against drift in contextstore's own default. If
// contextstore changes what a token created without explicit scopes receives,
// the static token must follow.
func TestStaticTokenClaimsMatchStoreDefaults(t *testing.T) {
	srv := newTestServer(t)
	raw, meta, err := srv.Store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "default-scopes",
		TTL:   time.Hour,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	_ = raw
	want := slices.Clone(meta.Scopes)
	got := slices.Clone(staticTokenClaims.Scopes)
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		t.Fatalf("staticTokenClaims.Scopes drifted from contextstore's default token scopes:\n  store:  %v\n  static: %v",
			want, got)
	}
	if slices.Contains(got, "admin") {
		t.Fatal("the static token must not carry the admin scope — that is what makes the admin guards real in this mode")
	}
	if !slices.Equal(staticTokenClaims.NamespaceGlobs, []string{"*"}) {
		t.Fatalf("staticTokenClaims should permit all namespaces, got %v", staticTokenClaims.NamespaceGlobs)
	}
}

// TestStaticTokenComparisonRejectsPrefixAndSuffix checks the constant-time
// comparison behaves like an equality test for the cases that matter.
func TestStaticTokenComparisonRejectsPrefixAndSuffix(t *testing.T) {
	srv := newTestServer(t)
	srv.AuthToken = "static-secret"

	// Note: the extracted bearer value is trimmed before comparison (existing
	// behaviour), so surrounding whitespace is not a mismatch.
	for _, token := range []string{"", "static-secre", "static-secrets", "STATIC-SECRET", "static-secretx"} {
		res := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/namespaces/list", nil,
			map[string]string{"Authorization": "Bearer " + token})
		if res.Code != http.StatusUnauthorized {
			t.Errorf("token %q was accepted (status %d)", token, res.Code)
		}
	}

	ok := performJSONWithHeaders(t, srv, http.MethodGet, "/v1/namespaces/list", nil,
		map[string]string{"Authorization": "Bearer static-secret"})
	if ok.Code != http.StatusOK {
		t.Fatalf("exact token rejected: %d %s", ok.Code, ok.Body.String())
	}
}

// callRoute issues an anonymous-or-credentialed request against one table
// entry. Prefix routes get a plausible path segment appended.
func callRoute(t *testing.T, srv *Server, rt apiRoute, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	path := rt.path
	if rt.prefix {
		path += "rev-1"
	}
	var body []byte
	if rt.method == http.MethodPost {
		body = []byte(`{}`)
	}
	req := httptest.NewRequest(rt.method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)
	return res
}

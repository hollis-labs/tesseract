package contextapi

import (
	"net/http"
	"testing"

	"github.com/hollis-labs/tesseract/internal/authscope"
)

// TestEveryAuthRequiredRouteHasAScope is the deny-by-default gate: a route
// added to apiRoutes without a tesseract.<verb> would be refused with
// route_not_mapped under enforce, so the build fails here instead.
func TestEveryAuthRequiredRouteHasAScope(t *testing.T) {
	for _, rt := range apiRoutes {
		key := routeKey{method: rt.method, path: rt.path}
		scope, has := routeScopes[key]
		switch rt.auth {
		case authRequired:
			if !has {
				t.Errorf("route %s %s has no entry in routeScopes (it would be denied as route_not_mapped under enforce)", rt.method, rt.path)
			}
		case authPublic:
			if has {
				t.Errorf("public route %s %s must not carry a scope", rt.method, rt.path)
			}
		}
		if has && !validScope(scope) {
			t.Errorf("route %s %s maps to unknown scope %q", rt.method, rt.path, scope)
		}
	}
}

// TestRouteScopesHaveNoStaleEntries keeps the table honest the other way: an
// entry for a route that no longer exists is dead policy.
func TestRouteScopesHaveNoStaleEntries(t *testing.T) {
	routes := map[routeKey]bool{}
	for _, rt := range apiRoutes {
		routes[routeKey{method: rt.method, path: rt.path}] = true
	}
	for key := range routeScopes {
		if !routes[key] {
			t.Errorf("routeScopes has %s %s but apiRoutes does not", key.method, key.path)
		}
	}
}

func TestRouteScopeTableResolvesPrefixAndExactRoutes(t *testing.T) {
	tbl := RouteScopeTable()
	cases := []struct {
		method, path, want string
		known              bool
	}{
		{http.MethodGet, "/v1/recall", authscope.Read, true},
		{http.MethodPost, "/v1/memory/write", authscope.Write, true},
		{http.MethodGet, "/v1/items/abc123", authscope.Read, true},
		{http.MethodGet, "/v1/items/abc123/history", authscope.Read, true},
		{http.MethodGet, "/v1/memory/revisions/rev-1", authscope.Read, true},
		{http.MethodPost, "/v1/admin/settings/apply", authscope.Admin, true},
		{http.MethodPost, "/v1/auth/tokens/create", authscope.Admin, true},
		{http.MethodPost, "/v1/context/promote/apply", authscope.Promote, true},
		{http.MethodPost, "/v1/maintenance/compact", authscope.Maintain, true},
		{http.MethodGet, "/v1/not/a/route", "", false},
		{http.MethodDelete, "/v1/recall", "", false}, // wrong method is unmapped, not read
	}
	for _, tc := range cases {
		got, known := tbl.ForRoute(tc.method, tc.path)
		if known != tc.known || got != tc.want {
			t.Errorf("%s %s = (%q, %v), want (%q, %v)", tc.method, tc.path, got, known, tc.want, tc.known)
		}
	}
}

func validScope(s string) bool {
	for _, c := range authscope.Canonical() {
		if c == s {
			return true
		}
	}
	return false
}

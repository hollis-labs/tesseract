package contextapi

import (
	"net/http"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/authscope"
)

// routeScopes is the go-auth route-to-scope table: the COARSE decision made by
// the guard before a handler runs. It is deny-by-default. A route with no entry
// here is not registered with the guard, so in enforce mode the guard answers
// 403 route_not_mapped; TestEveryAuthRequiredRouteHasAScope fails the build
// before that can happen to a real route.
//
// The fine decision (requireScope on legacy scope names, namespace globs)
// stays in the handlers and is untouched; see package authscope for how the
// two vocabularies join. Where a handler has never checked a scope, the coarse
// scope here is the class of the operation, chosen so that every token minted
// before go-auth (default scope set, or the memory:read/write sets in use on
// the hub) still reaches the same routes:
//
//   - reads of any kind: tesseract.read, which every managed token holds;
//   - HTTP writes to memory/knowledge/event/workspace: tesseract.write, which
//     both "write" and "memory:write" imply. This is a deliberate tightening
//     for a token holding neither (it could write memory over HTTP before);
//   - admin GETs stay tesseract.read, as today. Candidate for hardening to
//     tesseract.maintain once the UI sends a scoped bearer.
//   - token lifecycle routes require tesseract.admin. Before go-auth any valid
//     token could mint or list tokens; that was the DEC-103 admin gap.
var routeScopes = map[routeKey]string{
	// namespaces
	{http.MethodPost, "/v1/namespaces/register"}: authscope.Maintain,
	{http.MethodGet, "/v1/namespaces/list"}:      authscope.Read,
	{http.MethodGet, "/v1/namespaces/get"}:       authscope.Read,

	// context records
	{http.MethodPost, "/v1/context/write"}:              authscope.Write,
	{http.MethodPost, "/v1/context/promote"}:            authscope.Promote,
	{http.MethodPost, "/v1/context/promote/request"}:    authscope.Promote,
	{http.MethodPost, "/v1/context/promote/approve"}:    authscope.Promote,
	{http.MethodPost, "/v1/context/promote/apply"}:      authscope.Promote,
	{http.MethodGet, "/v1/context/head"}:                authscope.Read,
	{http.MethodGet, "/v1/context/history"}:             authscope.Read,
	{http.MethodGet, "/v1/context/audit"}:               authscope.Read,
	{http.MethodGet, "/v1/context/consistency/scan"}:    authscope.Read,
	{http.MethodPost, "/v1/context/consistency/repair"}: authscope.Maintain,
	{http.MethodPost, "/v1/context/typed-write"}:        authscope.Write,
	{http.MethodPost, "/v1/context/bulk-ingest"}:        authscope.Write,
	{http.MethodPost, "/v1/context/status/promote"}:     authscope.Write,
	{http.MethodPost, "/v1/context/status/deprecate"}:   authscope.Write,
	{http.MethodPost, "/v1/context/typed-view"}:         authscope.Read,
	{http.MethodGet, "/v1/context/types"}:               authscope.Read,
	{http.MethodGet, "/v1/context/views"}:               authscope.Read,
	{http.MethodPost, "/v1/context/pack"}:               authscope.Read,
	{http.MethodPost, "/v1/context/plan"}:               authscope.Read,
	{http.MethodPost, "/v1/context/packet"}:             authscope.Read,
	{http.MethodPost, "/v1/context/estimate"}:           authscope.Read,
	{http.MethodPost, "/v1/broker/plan"}:                authscope.Read,
	{http.MethodPost, "/v1/views/evaluate"}:             authscope.Read,
	{http.MethodPost, "/v1/maintenance/trim"}:           authscope.Maintain,
	{http.MethodPost, "/v1/maintenance/compact"}:        authscope.Maintain,
	{http.MethodPost, "/v1/maintenance/ttl-cleanup"}:    authscope.Maintain,

	// admin
	{http.MethodGet, "/v1/admin/setup"}:               authscope.Read,
	{http.MethodGet, "/v1/admin/settings"}:            authscope.Read,
	{http.MethodPost, "/v1/admin/settings/preview"}:   authscope.Admin,
	{http.MethodPost, "/v1/admin/settings/apply"}:     authscope.Admin,
	{http.MethodGet, "/v1/admin/config/backups"}:      authscope.Read,
	{http.MethodPost, "/v1/admin/config/backup"}:      authscope.Admin,
	{http.MethodPost, "/v1/admin/config/restore"}:     authscope.Admin,
	{http.MethodGet, "/v1/admin/queue"}:               authscope.Read,
	{http.MethodGet, "/v1/admin/queue/failures"}:      authscope.Read,
	{http.MethodPost, "/v1/admin/queue/retry-failed"}: authscope.Maintain,
	{http.MethodPost, "/v1/admin/queue/backfill"}:     authscope.Maintain,
	{http.MethodGet, "/v1/admin/storage"}:             authscope.Read,
	{http.MethodPost, "/v1/admin/namespaces/preview"}: authscope.Maintain,
	{http.MethodPost, "/v1/admin/namespaces/update"}:  authscope.Maintain,
	{http.MethodGet, "/v1/admin/namespaces/history"}:  authscope.Read,

	// auth
	{http.MethodPost, "/v1/auth/tokens/create"}: authscope.Admin,
	{http.MethodGet, "/v1/auth/tokens/list"}:    authscope.Admin,
	{http.MethodPost, "/v1/auth/tokens/revoke"}: authscope.Admin,
	{http.MethodGet, "/v1/auth/whoami"}:         authscope.Read,
	{http.MethodGet, "/v1/auth/summary"}:        authscope.Admin,

	// memory
	{http.MethodPost, "/v1/memory/write"}:     authscope.Write,
	{http.MethodPost, "/v1/memory/recall"}:    authscope.Read,
	{http.MethodGet, "/v1/memory/revisions/"}: authscope.Read,
	{http.MethodGet, "/v1/memory/current"}:    authscope.Read,
	{http.MethodGet, "/v1/memory/history"}:    authscope.Read,
	{http.MethodPost, "/v1/memory/touch"}:     authscope.Write,
	{http.MethodPost, "/v1/memory/deprecate"}: authscope.Write,
	{http.MethodPost, "/v1/memory/promote"}:   authscope.Write,

	// workspace
	{http.MethodPost, "/v1/workspace/write"}:           authscope.Write,
	{http.MethodPost, "/v1/workspace/delete"}:          authscope.Write,
	{http.MethodGet, "/v1/workspace/current"}:          authscope.Read,
	{http.MethodPost, "/v1/workspace/promote/request"}: authscope.Promote,
	{http.MethodPost, "/v1/workspace/promote/approve"}: authscope.Promote,
	{http.MethodPost, "/v1/workspace/promote/apply"}:   authscope.Promote,

	// items, refs, knowledge, event, retrieval
	{http.MethodGet, "/v1/items/"}:            authscope.Read,
	{http.MethodPost, "/v1/refs/resolve"}:     authscope.Read,
	{http.MethodPost, "/v1/knowledge/write"}:  authscope.Write,
	{http.MethodGet, "/v1/knowledge/current"}: authscope.Read,
	{http.MethodGet, "/v1/knowledge/history"}: authscope.Read,
	{http.MethodPost, "/v1/event/write"}:      authscope.Write,
	{http.MethodGet, "/v1/event/log"}:         authscope.Read,
	{http.MethodPost, "/v1/tesseract/lookup"}: authscope.Read,
	{http.MethodGet, "/v1/recall"}:            authscope.Read,
	{http.MethodPost, "/v1/synthesis/ask"}:    authscope.Read,
}

// RouteScopeTable builds the guard's route table from apiRoutes. Only
// authRequired routes with a scope entry are registered; everything else is
// unmapped and therefore denied by the guard in enforce mode. Public routes
// never reach the guard (ServeHTTP skips authorization for them).
func RouteScopeTable() *auth.ScopeTable {
	t := auth.NewScopeTable()
	for _, rt := range apiRoutes {
		if rt.auth != authRequired {
			continue
		}
		scope, ok := routeScopes[routeKey{method: rt.method, path: rt.path}]
		if !ok {
			continue
		}
		pattern := rt.path
		if rt.prefix {
			pattern += "*"
		}
		t.Route(rt.method, pattern, scope)
	}
	return t
}

// RoutePattern returns the route-table path for a request ("/v1/items/" for
// any item id) so callers can aggregate by route without path-parameter
// cardinality. Unknown requests return "(unmapped)".
func RoutePattern(method, urlPath string) string {
	if rt, ok := lookupRoute(method, urlPath); ok {
		return rt.path
	}
	return "(unmapped)"
}

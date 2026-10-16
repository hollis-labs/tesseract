package contextapi

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/tesseract/internal/nsglob"
)

// namespacePermit resolves the namespace restriction a request's token places
// on a read that may span namespaces.
//
// requested is what the caller asked for (selector namespaces; may be empty).
// A pattern the token's globs do not contain is refused with 403
// namespace_not_permitted, the same answer recall gives. The returned predicate
// is what the caller sets on the store selector (Selector.Permit) so that even
// a permitted-but-broad or empty selector never returns an out-of-glob record.
//
// A nil predicate means unrestricted: no token mode configured (loopback dev)
// or a "*" glob. Absent claims under a configured token mode fail closed.
func namespacePermit(w http.ResponseWriter, r *http.Request, requested []string) (func(string) bool, bool) {
	claims, ok := getTokenClaims(r)
	if !ok {
		if authModeConfigured(r) {
			writeError(w, http.StatusForbidden, "namespace_not_permitted",
				"request carries no token claims", nil)
			return nil, false
		}
		return nil, true
	}
	globs := claims.NamespaceGlobs
	if nsglob.Unrestricted(globs) {
		return nil, true
	}
	for _, ns := range requested {
		if strings.TrimSpace(ns) == "" {
			continue
		}
		if !nsglob.PatternWithin(globs, ns) {
			writeError(w, http.StatusForbidden, "namespace_not_permitted",
				"token is not permitted to access this namespace selector", map[string]any{
					"namespace": ns, "token_globs": globs,
				})
			return nil, false
		}
	}
	return func(namespace string) bool { return nsglob.Permitted(globs, namespace) }, true
}

// namespaceVisible reports whether the request's token may see one concrete
// namespace, without writing a response. By-id routes use it to answer an
// out-of-glob id exactly like a missing one (404), so ids cannot be probed.
func namespaceVisible(r *http.Request, namespace string) bool {
	claims, ok := getTokenClaims(r)
	if !ok {
		return !authModeConfigured(r)
	}
	return nsglob.Permitted(claims.NamespaceGlobs, namespace) || nsglob.Unrestricted(claims.NamespaceGlobs)
}

// requireUnrestrictedNamespaces gates operations that act on the whole store
// and cannot be narrowed to a token's globs (TTL cleanup, head rebuild). A
// token limited to some namespaces is refused.
func requireUnrestrictedNamespaces(w http.ResponseWriter, r *http.Request) bool {
	permit, ok := namespacePermit(w, r, nil)
	if !ok {
		return false
	}
	if permit != nil {
		writeError(w, http.StatusForbidden, "namespace_not_permitted",
			"this operation spans every namespace and needs a token without namespace restrictions", nil)
		return false
	}
	return true
}

// requireNamespacePatternAccess authorizes a maintenance-style SQL LIKE
// pattern ("%" wildcard): every namespace it could match must lie inside the
// token's globs.
func requireNamespacePatternAccess(w http.ResponseWriter, r *http.Request, likePattern string) bool {
	_, ok := namespacePermit(w, r, []string{strings.ReplaceAll(likePattern, "%", "*")})
	return ok
}

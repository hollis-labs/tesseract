package memory

import "strings"

// buildNamespaceClause produces a WHERE fragment + bind args matching any of
// the supplied namespaces. Each entry is matched as either an exact value or
// a prefix (LIKE 'ns/%') depending on its shape.
//
// Prefix forms (CW-20260519-0030):
//   - `user/{id}/memory`                       (legacy flat / "all my user memory")
//   - `user/{id}/project/{pid}/memory`         (legacy flat / "all my project memory")
//   - `user/{id}/session/{sid}/memory`         (legacy flat / "all my session memory")
//   - any of the above with a trailing `/*` (explicit wildcard)
//
// Exact forms:
//   - a fully-typed memory namespace `user/{id}/memory/{type}` etc.
//   - non-memory namespaces (knowledge, etc.) — passed through unchanged.
//
// Returns a single fragment of the form `(... OR ...)` for `len > 0`; returns
// `1=0` (matches nothing) for an empty list so callers don't accidentally
// short-circuit to "everything".
func buildNamespaceClause(namespaces []string) (string, []interface{}) {
	if len(namespaces) == 0 {
		return "1=0", nil
	}
	conds := make([]string, 0, len(namespaces))
	args := make([]interface{}, 0, len(namespaces))
	for _, ns := range namespaces {
		if pfx, ok := memoryPrefix(ns); ok {
			conds = append(conds, "r.namespace LIKE ?")
			args = append(args, pfx+"/%")
		} else {
			conds = append(conds, "r.namespace = ?")
			args = append(args, ns)
		}
	}
	return "(" + strings.Join(conds, " OR ") + ")", args
}

// memoryPrefix returns (prefix-without-trailing-slash, true) if ns is a
// memory-shaped prefix request — either ending in `/memory` (legacy flat
// form, now interpreted as "any type") or ending in `/memory/*` (explicit
// wildcard). Otherwise returns ("", false).
//
// Intentionally lenient: any namespace ending in `/memory` is treated as a
// prefix, including non-canonical shapes — the SQL prefix match returns
// nothing for malformed inputs, which is the right outcome (graceful no-op
// rather than parser errors at recall time).
func memoryPrefix(ns string) (string, bool) {
	if strings.HasSuffix(ns, "/memory/*") {
		return strings.TrimSuffix(ns, "/*"), true
	}
	if strings.HasSuffix(ns, "/memory") {
		return ns, true
	}
	return "", false
}

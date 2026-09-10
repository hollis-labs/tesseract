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
//   - the same four shapes with `event` in place of `memory` (CW-20260909-0035)
//   - any of the above with a trailing `/*` (explicit wildcard)
//
// Exact forms:
//   - a fully-typed namespace `user/{id}/memory/{type}`, `user/{id}/event/{type}` etc.
//   - namespaces of the other grammars (knowledge) — passed through unchanged.
//
// Returns one parenthesized fragment for `len > 0`. Its shape depends on what
// the input contained, and callers must not assume an OR chain:
//
//	(r.namespace = ?)                            one exact namespace
//	(r.namespace IN (?,?,?))                     several exact, no prefixes
//	(r.namespace LIKE ?)                         one prefix
//	(r.namespace IN (?,?) OR r.namespace LIKE ?) mixed
//	((a OR b) OR (c OR d))                       many prefixes, balanced
//
// An empty list returns `1=0` (matches nothing) rather than an empty string,
// so callers don't accidentally short-circuit to "everything".
//
// Shape is chosen to keep the parsed expression SHALLOW, not just correct.
// SQLite rejects any expression whose tree is deeper than
// SQLITE_MAX_EXPR_DEPTH (1000 by default) at prepare time, and a flat
// `a OR b OR ... ` chain is one level per term. A caller recalling across
// every registered namespace — the memory review queue does exactly that —
// used to reach that ceiling and fail the whole query with "Expression tree
// is too large". Two things keep the depth bounded: exact matches collapse
// into one `IN (...)` list regardless of count, and the remaining terms are
// OR'd as a balanced tree whose height grows as log2(n).
func buildNamespaceClause(namespaces []string) (string, []interface{}) {
	if len(namespaces) == 0 {
		return "1=0", nil
	}

	exact := make([]string, 0, len(namespaces))
	prefixes := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		if pfx, ok := scopedPrefix(ns); ok {
			prefixes = append(prefixes, pfx+"/%")
		} else {
			exact = append(exact, ns)
		}
	}

	conds := make([]string, 0, len(prefixes)+1)
	args := make([]interface{}, 0, len(namespaces))

	// One exact namespace stays `= ?` rather than a one-element IN list: it is
	// the shape every ordinary single-namespace recall emits, and there is no
	// reason to hand the planner a different one.
	switch len(exact) {
	case 0:
	case 1:
		conds = append(conds, "r.namespace = ?")
		args = append(args, exact[0])
	default:
		conds = append(conds, "r.namespace IN ("+placeholders(len(exact))+")")
		for _, ns := range exact {
			args = append(args, ns)
		}
	}

	for _, pfx := range prefixes {
		conds = append(conds, "r.namespace LIKE ?")
		args = append(args, pfx)
	}

	if len(conds) == 1 {
		return "(" + conds[0] + ")", args
	}
	return orTree(conds), args
}

// orTree joins conditions with OR, parenthesized as a balanced binary tree so
// the parsed expression's height is log2(n) rather than n. OR is associative,
// so the grouping changes only the parse depth, never the result.
//
// Callers must pass at least one condition.
func orTree(conds []string) string {
	if len(conds) == 1 {
		return conds[0]
	}
	mid := len(conds) / 2
	return "(" + orTree(conds[:mid]) + " OR " + orTree(conds[mid:]) + ")"
}

// scopedPrefix returns (prefix-without-trailing-slash, true) if ns is a
// prefix request under one of the scoped shallow-faceted grammars — either
// ending in the bare domain segment (legacy flat form, now interpreted as "any
// type") or ending in `/{segment}/*` (explicit wildcard). Otherwise returns
// ("", false).
//
// It covers `/memory` and `/event`, the two grammars that carry a {type}
// segment (see scopedNamespaceSegments). Knowledge is excluded on purpose: its
// namespaces have free depth, so `user/x/knowledge/foo` is an exact namespace
// somebody writes to, and reading any `/knowledge`-suffixed string as a prefix
// would reinterpret real namespaces rather than add a shorthand.
//
// Intentionally lenient about shape: any namespace ending in one of those
// segments is treated as a prefix, including non-canonical forms — the SQL
// prefix match returns nothing for malformed inputs, which is the right
// outcome (graceful no-op rather than parser errors at recall time).
func scopedPrefix(ns string) (string, bool) {
	for _, seg := range scopedNamespaceSegments {
		if strings.HasSuffix(ns, "/"+seg+"/*") {
			return strings.TrimSuffix(ns, "/*"), true
		}
		if strings.HasSuffix(ns, "/"+seg) {
			return ns, true
		}
	}
	return "", false
}

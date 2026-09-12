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

// scopedPrefix returns (prefix-without-trailing-slash, true) if ns is a prefix
// REQUEST, and ("", false) if it names an exact namespace.
//
// Two ways to ask, and the asymmetry between them is the decision
// (CW-20260912-0078):
//
//   - An EXPLICIT trailing `/*` is a prefix request at ANY tier and any depth.
//     `project/*`, `project/tether/*`, `project/tether/knowledge/*`,
//     `user/chrispian/memory/*`. This is the universal rule, and the one an
//     agent should learn.
//   - A BARE form ending in `/memory` or `/event` is ALSO read as a prefix.
//     This is inference, it is grandfathered, and it is not extended to
//     anything else — see below.
//
// WHY INFERENCE WAS GRANDFATHERED RATHER THAN EXTENDED.
//
// The scope-type-rooted grammar gives knowledge a fixed-depth head, which made
// it look as though `{scope}/{id}/knowledge` could now be inferred as a prefix
// the way `/memory` is. It cannot, and the reason is measured rather than
// theoretical. Two populations in the live store sit at exactly the boundary
// any such inference would claim:
//
//   - `user/chrispian/knowledge` holds a canonical knowledge record
//     (`cerberus.v2.setup_guide.node_launchd_absolute_path`, revision
//     01KQ8C32HNABJSEF9RHSWD1EE9). Inferring a prefix there turns a read that
//     returns ONE record into a read across the 74 knowledge namespaces
//     beneath it.
//   - 64 records across 9 namespaces sit at exactly two segments — `app/mentat`
//     (35), `user/memory` (10), `app/volon` (5), `global/system` (5),
//     `app/conduit` (3), `app/cortex` (3), `agentrc/docs`, `app/hadron`,
//     `mentat/epics`. Every one of those IS a scope head under the new
//     grammar, so head inference would make all 64 unreachable by exact match.
//
// Memory's bare form is not the same case, and the difference is what makes
// this a decision rather than a consistency failure: `user/{id}/memory` is a
// shape the PARSER REJECTS, so it is a dead spelling that nothing can write to
// and reinterpreting it costs nothing. `{scope}/{id}/knowledge` is live and
// writable today.
//
// What the original exclusion comment was protecting against — inference
// reinterpreting namespaces that are legal exact namespaces — is therefore
// still true, and this honors it rather than overriding it. The fix is to stop
// inferring at the new tiers, not to bound the inference.
//
// Do not "simplify" this into one uniform rule in either direction. Extending
// inference silently widens 65 reads; withdrawing it from `/memory` and
// `/event` breaks callers that rely on a documented shorthand. The root cause
// is that registration is a side effect of writing, so the registry holds
// namespaces at every depth — which N4 mitigates but does not remove.
//
// Intentionally lenient about shape: a malformed prefix returns nothing from
// the SQL match, which is the right outcome — a graceful no-op rather than a
// parser error at recall time.
func scopedPrefix(ns string) (string, bool) {
	if strings.HasSuffix(ns, "/*") {
		return strings.TrimSuffix(ns, "/*"), true
	}
	for _, seg := range scopedNamespaceSegments {
		if strings.HasSuffix(ns, "/"+seg) {
			return ns, true
		}
	}
	return "", false
}

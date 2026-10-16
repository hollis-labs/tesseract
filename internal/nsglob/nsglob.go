// Package nsglob is the one place a token's namespace_globs are matched against
// namespaces. The HTTP layer (contextapi) and the MCP adapter both import it so
// the two doors cannot drift (CW-20261016-0205).
package nsglob

import (
	"path"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// Permitted reports whether the globs authorize one concrete stored namespace.
// A glob ending in "/*" covers every descendant ("app/test/*" matches
// "app/test/session/task-001"); "*" covers everything. A namespace that itself
// contains a wildcard is treated as literal data here.
func Permitted(globs []string, namespace string) bool {
	for _, glob := range globs {
		glob = strings.TrimSpace(glob)
		if !validGlob(glob) {
			continue
		}
		if glob == "*" || glob == namespace {
			return true
		}
		if matched, _ := path.Match(glob, namespace); matched {
			return true
		}
		if descendantPrefix(glob) != "" && strings.HasPrefix(namespace, descendantPrefix(glob)) {
			return true
		}
	}
	return false
}

// validGlob fails closed on a malformed glob: one path.Match cannot parse
// grants nothing, even where a prefix fallback would otherwise match ("[/*"
// must not grant "[/x").
func validGlob(glob string) bool {
	_, err := path.Match(glob, "")
	return err == nil
}

// descendantPrefix returns the literal prefix a "/*" glob covers ("app/test/"
// for "app/test/*"), or "" when the glob is not of that form or its prefix has
// a wildcard of its own. An interior-wildcard grant ("project/*/memory/*") is
// matched by path.Match only, one segment at a time, and never widened to
// descendants by string prefix.
func descendantPrefix(glob string) string {
	if !strings.HasSuffix(glob, "/*") {
		return ""
	}
	prefix := strings.TrimSuffix(glob, "*")
	if strings.ContainsAny(prefix, "*?[") {
		return ""
	}
	return prefix
}

// PatternWithin reports whether every namespace a selector pattern could match
// lies inside one glob. Use it for selectors, which may carry wildcards; use
// Permitted for a concrete namespace.
func PatternWithin(globs []string, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	wild := strings.ContainsAny(pattern, "*?[")
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if !validGlob(g) {
			continue
		}
		if g == "*" || g == pattern {
			return true
		}
		if descendantPrefix(g) != "" && strings.HasPrefix(pattern, descendantPrefix(g)) {
			return true
		}
		if !wild {
			if ok, _ := path.Match(g, pattern); ok {
				return true
			}
		}
	}
	return false
}

// Unrestricted reports whether the globs include "*".
func Unrestricted(globs []string) bool {
	for _, g := range globs {
		if strings.TrimSpace(g) == "*" {
			return true
		}
	}
	return false
}

// SelectorPermitted authorizes one recall selector, which may be a prefix
// ("project/x/memory/*" style, resolved against the domains in play by
// memory.RecallNamespacePrefix). A concrete selector is checked like Permitted;
// a prefix selector is allowed only when one "/*" glob contains every
// namespace it could match, so a broad prefix is never authorized by a narrower
// glob.
func SelectorPermitted(globs []string, selector string, domainNames []string) bool {
	prefix, isPrefix := memory.RecallNamespacePrefix(selector, domainNames)
	if !isPrefix {
		return Permitted(globs, selector)
	}
	for _, glob := range globs {
		glob = strings.TrimSpace(glob)
		if glob == "*" {
			return true
		}
		if !strings.HasSuffix(glob, "/*") {
			continue
		}
		allowedPrefix := strings.TrimSuffix(glob, "/*")
		if strings.ContainsAny(allowedPrefix, "*?[") {
			continue
		}
		if prefix == allowedPrefix || strings.HasPrefix(prefix, allowedPrefix+"/") {
			return true
		}
	}
	return false
}

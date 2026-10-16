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
		if glob == "*" || glob == namespace {
			return true
		}
		if matched, _ := path.Match(glob, namespace); matched {
			return true
		}
		if strings.HasSuffix(glob, "/*") && strings.HasPrefix(namespace, strings.TrimSuffix(glob, "*")) {
			return true
		}
	}
	return false
}

// PatternWithin reports whether every namespace a selector pattern could match
// lies inside one glob. Use it for selectors, which may carry wildcards; use
// Permitted for a concrete namespace.
func PatternWithin(globs []string, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	wild := strings.ContainsAny(pattern, "*?[")
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "*" || g == pattern {
			return true
		}
		if strings.HasSuffix(g, "/*") && strings.HasPrefix(pattern, strings.TrimSuffix(g, "*")) {
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
		for _, glob := range globs {
			glob = strings.TrimSpace(glob)
			if glob == "*" || glob == selector {
				return true
			}
			if matched, err := path.Match(glob, selector); err == nil && matched {
				return true
			}
			if strings.HasSuffix(glob, "/*") && strings.HasPrefix(selector, strings.TrimSuffix(glob, "*")) {
				return true
			}
		}
		return false
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

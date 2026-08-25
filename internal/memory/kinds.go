package memory

import (
	"sort"
	"strings"
)

// The canonical knowledge `facet_kind` vocabulary.
//
// This is a CLOSED set and the enforcement authority: `knowledge.Store.Write`
// rejects any kind outside it. The vocabulary lives in this package rather
// than in `internal/knowledge` because `internal/knowledge` imports
// `internal/memory` and not the reverse, and because `facet_kind` is a column
// this package owns.
//
// The set is the taxonomy locked 2026-05-14 (nine kinds) plus two promoted
// 2026-08-25 because a shipped producer emits each systematically:
// `mcp_server` and `investigation`. Naming rule: canonical kinds are
// snake_case — the one hyphenated kind that reached the corpus (`mcp-server`)
// arrived from guidance that has since been corrected.
//
// Three kinds are canonical but unpopulated — `playbook`, `learning`, and
// `handoff` have no entries in the corpus. They are deliberately writable: a
// vocabulary that named only what already exists could never be written into,
// and refusing them would recreate, for those kinds, the readable-but-
// unwritable trap that the taxonomy normalization existed to remove.
//
// Adding a kind is a governed change: the vocabulary is revised here and in
// the taxonomy record together. Code is the enforcement authority; the
// Tesseract `kinds_taxonomy` memory carries the rationale. On divergence the
// code wins.
var canonicalKnowledgeKinds = map[string]struct{}{
	"doc":               {},
	"handoff":           {},
	"investigation":     {},
	"learning":          {},
	"mcp_server":        {},
	"note":              {},
	"package":           {},
	"playbook":          {},
	"pointer":           {},
	"project_canonical": {},
	"session_close":     {},
}

// KnowledgeKindVocabulary returns the canonical knowledge kinds, sorted.
func KnowledgeKindVocabulary() []string { return sortedKeys(canonicalKnowledgeKinds) }

// IsCanonicalKnowledgeKind reports whether kind is in the closed vocabulary.
func IsCanonicalKnowledgeKind(kind string) bool {
	_, ok := canonicalKnowledgeKinds[kind]
	return ok
}

// KnowledgeKindList renders the vocabulary for an error message, so a
// rejection can name the allowed set rather than only refusing.
func KnowledgeKindList() string {
	kinds := KnowledgeKindVocabulary()
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}

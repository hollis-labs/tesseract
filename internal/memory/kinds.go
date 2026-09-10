package memory

import (
	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// The canonical knowledge `facet_kind` vocabulary — now a lookup, not a list.
//
// It used to be a closed Go map right here. CW-20260909-0034 folded it into the
// type registry (`knowledge.facet_kind`), where the set is DECLARED closed
// rather than closed because someone reached for a map. See
// [[tesseract_vocabularies_fold_into_registry]].
//
// What did NOT move is enforcement. `Store.WriteRevision` is still the
// persistence boundary that rejects an off-vocabulary kind, through
// knowledgePolicy.ValidateFacets in domainpolicy.go, and it still names the
// allowed set in the error. Under [[config_is_policy_code_is_engine]] the
// engine keeps enforcing and the operator owns the list: what moved is WHICH
// VALUES ARE ALLOWED, not whether anything checks.
//
// These three functions stay in this package rather than becoming registry
// calls at every site for two reasons. `internal/knowledge` imports
// `internal/memory` and not the reverse, and `facet_kind` is a column this
// package owns — so this is where knowledge-kind questions have always been
// asked. And keeping the seam means the next vocabulary move is one file, not
// a sweep.
//
// Adding a kind is still a governed change: the registry declaration and the
// [[kinds_taxonomy]] record are revised together, as one change. What changed
// on 2026-09-09 is the substrate, not the rule — the record is no longer the
// mere rationale behind an authoritative Go map, because the declaration it
// governs is now a file an operator can edit.

// KnowledgeKindVocabulary returns the canonical knowledge kinds, sorted.
func KnowledgeKindVocabulary() []string {
	return typeregistry.Default().Values(typeregistry.VocabKnowledgeFacetKind)
}

// IsCanonicalKnowledgeKind reports whether kind is in the closed vocabulary.
func IsCanonicalKnowledgeKind(kind string) bool {
	return typeregistry.Default().Allows(typeregistry.VocabKnowledgeFacetKind, kind)
}

// KnowledgeKindList renders the vocabulary for an error message, so a
// rejection can name the allowed set rather than only refusing.
func KnowledgeKindList() string {
	return typeregistry.Default().List(typeregistry.VocabKnowledgeFacetKind)
}

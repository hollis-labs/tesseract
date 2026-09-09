package memory

import (
	"strings"

	"github.com/hollis-labs/tesseract/internal/memorylinks"
)

// buildRelatedClause produces the WHERE fragment + bind args for the `related`
// recall expansion: narrow results to entries adjacent to one of the anchor
// keys in the link graph (CW-20260825-0017).
//
// # Why adjacency is undirected
//
// A link is written from one end, but relatedness is not directional in the
// way a citation is. If a followup cites a decision, the decision is related
// to the followup and the followup is related to the decision — asking "what
// is related to this decision" and getting back only the four records it
// happened to cite, while the eleven records citing IT stay invisible, is the
// answer to a different question than the one anyone asks. The corpus makes
// this concrete: followups → decisions is the single densest edge direction,
// so a forward-only expansion would leave every decision looking unreferenced.
//
// Both directions are indexed for exactly this reason — idx_memory_links_from_memory
// and idx_memory_links_to_memory in internal/memorylinks.
//
// # Grain
//
// Anchors are memory KEYS because that is what a link target is; results are
// filtered by memory_id because that is what an edge resolves to. A key
// present in more than one namespace anchors on all of them, which is the same
// permissiveness the resolution rule has when it declines to guess between
// them.
//
// # The self-adjacency of lineage
//
// Under relation=supersedes the expansion returns the anchor's own entry and
// nothing else, because every supersedes edge is intra-entry (see
// internal/memorylinks for why that is enforced rather than incidental). That
// is not a degenerate case to work around: combined with
// revision_scope=timeline it is how you ask for an entry's lineage through the
// graph. Excluding self-adjacency to make the default answer tidier would
// silently delete that, so the clause does not special-case it.
//
// # Unresolved edges
//
// An edge whose target resolved to nothing has no far end to traverse, so it
// contributes to neither direction. It stays in the table as a record of what
// was written — and becomes traversable the moment its target key is written,
// once the edge is re-resolved.
//
// Returns ("", nil) when there are no anchors, so the caller adds no fragment.
func buildRelatedClause(anchors, relations []string) (string, []interface{}) {
	if len(anchors) == 0 {
		return "", nil
	}

	// The two arms are UNIONed inside one IN (...) subquery rather than OR'd
	// as two EXISTS clauses so each arm gets its own index (from_memory,
	// to_memory) instead of the planner picking one for a disjunction.
	var b strings.Builder
	args := make([]interface{}, 0, 2*(len(anchors)+len(relations)))

	relationFilter := ""
	if len(relations) > 0 {
		relationFilter = " AND l.relation IN (" + placeholders(len(relations)) + ")"
	}
	anchorFilter := "anchor.memory_key IN (" + placeholders(len(anchors)) + ")"

	appendArgs := func() {
		for _, a := range anchors {
			args = append(args, a)
		}
		for _, rel := range relations {
			args = append(args, rel)
		}
	}

	b.WriteString("r.memory_id IN (\n")
	// Outbound: entries the anchor links TO.
	b.WriteString("    SELECT l.to_memory_id FROM memory_links l\n")
	b.WriteString("    INNER JOIN memory_state anchor ON anchor.memory_id = l.from_memory_id\n")
	b.WriteString("    WHERE " + anchorFilter + " AND l.to_memory_id IS NOT NULL" + relationFilter + "\n")
	appendArgs()
	b.WriteString("    UNION\n")
	// Inbound: entries that link TO the anchor.
	b.WriteString("    SELECT l.from_memory_id FROM memory_links l\n")
	b.WriteString("    INNER JOIN memory_state anchor ON anchor.memory_id = l.to_memory_id\n")
	b.WriteString("    WHERE " + anchorFilter + relationFilter + "\n")
	appendArgs()
	b.WriteString(")")

	return b.String(), args
}

// ── Relation vocabulary, re-exported ─────────────────────────────────────────
//
// The vocabulary is defined in internal/memorylinks, next to the CHECK
// constraint that enforces it in storage. It is re-exported here because
// internal/memory is the door every recall surface already goes through for
// query vocabularies (SearchMode, PointerHealthStatus), and a surface reaching
// past it into the storage package to validate one argument would be the only
// place that did.

// LinkRelation is an edge type in the link graph.
type LinkRelation = memorylinks.Relation

// The two members of the relation vocabulary.
const (
	LinkRelationReferences = memorylinks.RelationReferences
	LinkRelationSupersedes = memorylinks.RelationSupersedes
)

// LinkRelationVocabulary returns the accepted relation values in stable order,
// for surfaces that render it into help text or validate an argument against
// it. Rendered rather than restated, so a surface cannot advertise a relation
// the filter does not accept.
var LinkRelationVocabulary = memorylinks.Vocabulary

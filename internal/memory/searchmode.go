package memory

// SearchMode selects WHICH retrieval signal answers a query. It is a knob on
// ranking=relevance, not a ranking of its own (D6): the machinery is the same
// BM25 and cosine arms relevance already runs — this chooses how many of them
// fire and whether their output is fused.
//
//	hybrid    both arms, fused by RRF and weighted by the activation-style
//	          modifiers. The default, and the pre-existing behavior.
//	lexical   the BM25 arm alone, in bm25() order. Exact-match retrieval:
//	          identifiers, symbols, namespaces, error strings.
//	semantic  the cosine arm alone, in similarity order. Meaning-match
//	          retrieval when the caller knows the words will not line up.
//
// lexical and semantic deliberately do NOT apply the status/origin/confidence/
// recency/activation modifiers hybrid multiplies into its fused score. Those
// modifiers exist to arbitrate between two arms that disagree; applied to a
// single arm they only move an exact match away from the top, which is the
// failure this knob exists to remove. A caller asking for lexical is asking
// for the lexical ordering, not for a re-weighted approximation of it.
type SearchMode string

const (
	// SearchModeHybrid fuses the BM25 and cosine arms via RRF.
	SearchModeHybrid SearchMode = "hybrid"
	// SearchModeLexical runs the BM25 arm alone, ordered by bm25().
	SearchModeLexical SearchMode = "lexical"
	// SearchModeSemantic runs the cosine arm alone, ordered by similarity.
	SearchModeSemantic SearchMode = "semantic"
)

// DefaultSearchMode is what an omitted search_mode resolves to. It is hybrid
// because hybrid is what every caller got before this knob existed: omitting
// the argument must keep producing byte-identical responses.
//
// This default is a constant, not a config key. A server-side default would
// let a deployment silently turn every existing caller's hybrid recall into
// lexical — the same class of change as seeding a budget from config, which
// flipped a response envelope for callers passing nothing. A caller who wants
// a different mode says so per call.
const DefaultSearchMode = SearchModeHybrid

// Valid reports whether m is one of the three canonical search modes.
func (m SearchMode) Valid() bool {
	switch m {
	case SearchModeHybrid, SearchModeLexical, SearchModeSemantic:
		return true
	}
	return false
}

// SearchModeVocabulary returns the accepted values in a stable order, so
// surfaces can render their argument descriptions and error messages from the
// vocabulary rather than restating it and drifting from it.
func SearchModeVocabulary() []string {
	return []string{
		string(SearchModeHybrid),
		string(SearchModeLexical),
		string(SearchModeSemantic),
	}
}

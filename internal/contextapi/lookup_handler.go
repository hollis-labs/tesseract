package contextapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hollis-labs/vanta-conduit/domains"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// conduitLookupRequest is the unified search payload across memory +
// knowledge domains. Thin wrapper over memory.RecallInput that makes the
// cross-domain filters explicit in JSON.
type conduitLookupRequest struct {
	Namespaces    []string             `json:"namespaces"`
	RevisionScope memory.RevisionScope `json:"revision_scope,omitempty"`
	Ranking       memory.Ranking       `json:"ranking,omitempty"`
	Query         string               `json:"query,omitempty"`
	Limit         int                  `json:"limit,omitempty"`

	Domains      []domains.Domain `json:"domains,omitempty"`
	FacetKinds   []string         `json:"facet_kinds,omitempty"`
	FacetSources []string         `json:"facet_sources,omitempty"`

	Origins       []memory.Origin  `json:"origins,omitempty"`
	Statuses      []memory.Status  `json:"statuses,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	ConfidenceMin float64          `json:"confidence_min,omitempty"`
	Since         *time.Time       `json:"since,omitempty"`
	Until         *time.Time       `json:"until,omitempty"`
}

// conduitLookupResponse wraps the recall results with a simple facet
// histogram computed client-side from the result set. The histogram is
// best-effort: it reflects only returned rows, not the full match set.
type conduitLookupResponse struct {
	Results []memory.RecallResult `json:"results"`
	Facets  lookupFacets          `json:"facets"`
}

type lookupFacets struct {
	Domains map[string]int `json:"domains,omitempty"`
	Kinds   map[string]int `json:"kinds,omitempty"`
	Sources map[string]int `json:"sources,omitempty"`
}

func (s *Server) handleConduitLookup(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req conduitLookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON: "+err.Error(), nil)
		return
	}
	for _, ns := range req.Namespaces {
		if !requireNamespaceAccess(w, r, ns) {
			return
		}
	}

	in := memory.RecallInput{
		Namespaces:    req.Namespaces,
		RevisionScope: req.RevisionScope,
		Ranking:       req.Ranking,
		Query:         req.Query,
		Limit:         req.Limit,
		Filters: memory.RecallFilters{
			Origins:       req.Origins,
			Statuses:      req.Statuses,
			Tags:          req.Tags,
			ConfidenceMin: req.ConfidenceMin,
			Since:         req.Since,
			Until:         req.Until,
			Domains:       req.Domains,
			FacetKinds:    req.FacetKinds,
			FacetSources:  req.FacetSources,
		},
	}
	results, err := s.MemoryStore.Recall(r.Context(), in)
	if err != nil {
		if errors.Is(err, memory.ErrSimilarityUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "similarity_unavailable", err.Error(), nil)
			return
		}
		if errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, conduitLookupResponse{
		Results: results,
		Facets:  buildFacets(results),
	})
}

func buildFacets(results []memory.RecallResult) lookupFacets {
	out := lookupFacets{
		Domains: map[string]int{},
		Kinds:   map[string]int{},
		Sources: map[string]int{},
	}
	for _, r := range results {
		if d := string(r.Revision.Domain); d != "" {
			out.Domains[d]++
		}
		if k := r.Revision.Facets.Kind; k != "" {
			out.Kinds[k]++
		}
		if s := r.Revision.Facets.Source; s != "" {
			out.Sources[s]++
		}
	}
	return out
}

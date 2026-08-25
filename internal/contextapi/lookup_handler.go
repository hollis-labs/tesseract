package contextapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// tesseractLookupRequest is the unified search payload across memory +
// knowledge domains. Thin wrapper over memory.RecallInput that makes the
// cross-domain filters explicit in JSON.
type tesseractLookupRequest struct {
	Namespaces    []string             `json:"namespaces"`
	RevisionScope memory.RevisionScope `json:"revision_scope,omitempty"`
	Ranking       memory.Ranking       `json:"ranking,omitempty"`
	Query         string               `json:"query,omitempty"`
	Limit         int                  `json:"limit,omitempty"`

	Domains      []domains.Domain `json:"domains,omitempty"`
	FacetKinds   []string         `json:"facet_kinds,omitempty"`
	FacetSources []string         `json:"facet_sources,omitempty"`

	// PointerHealth filters knowledge results by verification state. Peer of
	// the MCP tesseract_lookup argument of the same name — same vocabulary,
	// same semantics, same SQL-before-limit application.
	PointerHealth []string `json:"pointer_health,omitempty"`

	Origins       []memory.Origin `json:"origins,omitempty"`
	Statuses      []memory.Status `json:"statuses,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	ConfidenceMin float64         `json:"confidence_min,omitempty"`
	Since         *time.Time      `json:"since,omitempty"`
	Until         *time.Time      `json:"until,omitempty"`

	// PayloadMode projects each result: keys|summary|full. Empty means the
	// server default (read.payload_mode). Peer of the MCP tesseract_lookup
	// argument of the same name — same vocabulary, same semantics.
	//
	// A caller that intends to EDIT what it reads must pass "full"
	// explicitly. Under a projected mode payload.body is withheld, and
	// because Payload.Body carries `omitempty` a withheld body is
	// indistinguishable from an empty one by shape alone — results carry
	// `payload_mode` for exactly that reason.
	PayloadMode memory.PayloadMode `json:"payload_mode,omitempty"`
}

// tesseractLookupResponse wraps the recall results with a simple facet
// histogram computed client-side from the result set. The histogram is
// best-effort: it reflects only returned rows, not the full match set.
// Results is `any` because its shape depends on payload_mode: full mode
// serializes []memory.RecallResult unchanged, while keys and summary
// serialize []memory.ProjectedResult.
type tesseractLookupResponse struct {
	Results any          `json:"results"`
	Facets  lookupFacets `json:"facets"`
}

type lookupFacets struct {
	Domains map[string]int `json:"domains,omitempty"`
	Kinds   map[string]int `json:"kinds,omitempty"`
	Sources map[string]int `json:"sources,omitempty"`
}

func (s *Server) handleTesseractLookup(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req tesseractLookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
		return
	}
	for _, ns := range req.Namespaces {
		if !requireNamespaceAccess(w, r, ns) {
			return
		}
	}

	payloadMode := s.defaultPayloadMode()
	if req.PayloadMode != "" {
		if !req.PayloadMode.Valid() {
			writeError(w, http.StatusBadRequest, "validation_error",
				"payload_mode must be one of keys|summary|full, got "+string(req.PayloadMode), nil)
			return
		}
		payloadMode = req.PayloadMode
	}

	// Reject an unknown status rather than letting it silently match nothing:
	// an empty result set from a typo is indistinguishable from a clean corpus.
	for _, h := range req.PointerHealth {
		if !memory.PointerHealthStatus(h).Valid() {
			writeError(w, http.StatusBadRequest, "validation_error",
				"pointer_health must be one of "+strings.Join(memory.PointerHealthStatusVocabulary(), ", ")+", got "+h, nil)
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
			PointerHealth: req.PointerHealth,
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
	// Facets come from the unprojected results: the histogram describes what
	// matched, not what was serialized.
	writeJSON(w, http.StatusOK, tesseractLookupResponse{
		Results: memory.ProjectResults(results, payloadMode),
		Facets:  buildFacets(results),
	})
}

// defaultPayloadMode resolves the server-configured recall projection,
// falling back to memory.DefaultPayloadMode when config is unset or carries
// an unrecognized value.
func (s *Server) defaultPayloadMode() memory.PayloadMode {
	mode := memory.PayloadMode(s.RuntimeConfig.Read.PayloadMode)
	if !mode.Valid() {
		return memory.DefaultPayloadMode
	}
	return mode
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

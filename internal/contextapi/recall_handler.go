package contextapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// recallBriefItem is the condensed representation returned when format=brief.
// It exposes only the fields an agent needs for triage: id, namespace,
// domain, tags, confidence, summary, and created_at. The full Revision is
// available via /v1/memory/revisions/{id} when needed.
type recallBriefItem struct {
	RevisionID string   `json:"revision_id"`
	MemoryID   string   `json:"memory_id"`
	Domain     string   `json:"domain"`
	Namespace  string   `json:"namespace"`
	MemoryKey  string   `json:"memory_key,omitempty"`
	Tags       []string `json:"tags"`
	Confidence float64  `json:"confidence"`
	Summary    string   `json:"summary"`
	CreatedAt  string   `json:"created_at"`
}

// recallResponse is the envelope returned by GET /v1/recall.
type recallResponse struct {
	Results any          `json:"results"`
	Facets  lookupFacets `json:"facets"`
	Meta    recallMeta   `json:"meta"`
}

type recallMeta struct {
	Namespace string `json:"namespace"`
	Limit     int    `json:"limit"`
	Returned  int    `json:"returned"`
	Format    string `json:"format"`
}

// handleRecall serves GET /v1/recall — a read-only, query-param-driven recall
// endpoint that spans all three domains (memory, knowledge) within a single
// namespace. Mirrors POST /v1/conduit/lookup but optimised for scripted/agent
// consumption where a GET + URL params is more convenient than a JSON body.
//
// Query parameters:
//
//	namespace  (required) — the single namespace to query, e.g.
//	             user/chrispian/memory
//	             user/chrispian/knowledge/session-close/nanite
//	tags       (optional) — comma-separated tag filter; any tag match is
//	             sufficient (OR semantics, consistent with POST recall).
//	limit      (optional) — max results; defaults to 15, capped at 500.
//	format     (optional) — "brief" (default) returns condensed items;
//	             "full" returns the complete RecallResult including score
//	             and State.
func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}

	q := r.URL.Query()

	namespace := strings.TrimSpace(q.Get("namespace"))
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace is required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, namespace) {
		return
	}

	// Parse tags — comma-separated, skip blanks.
	var tags []string
	if raw := strings.TrimSpace(q.Get("tags")); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}

	// Parse limit.
	limit := 15
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "validation_error", "limit must be a positive integer", nil)
			return
		}
		limit = n
	}

	// Parse format.
	format := "brief"
	if raw := strings.ToLower(strings.TrimSpace(q.Get("format"))); raw == "full" {
		format = "full"
	}

	in := memory.RecallInput{
		Namespaces:    []string{namespace},
		RevisionScope: memory.RevisionScopeCurrent,
		Limit:         limit,
		Filters: memory.RecallFilters{
			Tags: tags,
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
		writeError(w, http.StatusInternalServerError, "recall_failed", err.Error(), nil)
		return
	}

	facets := buildFacets(results)

	var items any
	if format == "brief" {
		brief := make([]recallBriefItem, 0, len(results))
		for _, rr := range results {
			rev := rr.Revision
			brief = append(brief, recallBriefItem{
				RevisionID: rev.RevisionID,
				MemoryID:   rev.MemoryID,
				Domain:     string(rev.Domain),
				Namespace:  rev.Namespace,
				MemoryKey:  rev.MemoryKey,
				Tags:       rev.Tags,
				Confidence: rev.Confidence,
				Summary:    rev.Payload.Summary,
				CreatedAt:  rev.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			})
		}
		items = brief
	} else {
		items = results
	}

	writeJSON(w, http.StatusOK, recallResponse{
		Results: items,
		Facets:  facets,
		Meta: recallMeta{
			Namespace: namespace,
			Limit:     limit,
			Returned:  len(results),
			Format:    format,
		},
	})
}

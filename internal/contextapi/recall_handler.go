package contextapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// recallBriefItem is the condensed representation returned when format=brief.
// It exposes only the fields an agent needs for triage: id, namespace,
// domain, tags, confidence, summary, and created_at. The full Revision is
// available via /v1/memory/revisions/{id} when needed.
type recallBriefItem struct {
	RevisionID   string             `json:"revision_id"`
	ItemID       string             `json:"item_id"`
	MemoryID     string             `json:"memory_id"`
	Domain       string             `json:"domain"`
	Namespace    string             `json:"namespace"`
	MemoryKey    string             `json:"memory_key,omitempty"`
	WorkstreamID string             `json:"workstream_id,omitempty"`
	Provenance   *memory.Provenance `json:"provenance,omitempty"`
	Tags         []string           `json:"tags"`
	Confidence   float64            `json:"confidence"`
	Summary      string             `json:"summary"`
	CreatedAt    string             `json:"created_at"`
}

type recallBriefWorkspaceItem struct {
	ItemID       string             `json:"item_id"`
	Domain       string             `json:"domain"`
	Namespace    string             `json:"namespace"`
	Key          string             `json:"key,omitempty"`
	WorkstreamID string             `json:"workstream_id,omitempty"`
	Provenance   *memory.Provenance `json:"provenance,omitempty"`
	Tags         []string           `json:"tags"`
	Summary      string             `json:"summary"`
	UpdatedAt    string             `json:"updated_at"`
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
// endpoint that spans revisioned domains and opt-in workspace within a single
// namespace. Mirrors POST /v1/tesseract/lookup but optimized for scripted/agent
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
//	domains    (optional) — comma-separated domain filters; workspace and event
//	             remain opt-in.
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
	// Parse tags — comma-separated, skip blanks.
	var tags []string
	if raw := strings.TrimSpace(q.Get("tags")); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	var requestedDomains []domains.Domain
	if raw := strings.TrimSpace(q.Get("domains")); raw != "" {
		for _, d := range strings.Split(raw, ",") {
			if d = strings.TrimSpace(d); d != "" {
				requestedDomains = append(requestedDomains, domains.Domain(d))
			}
		}
	}
	domainNames := make([]string, len(requestedDomains))
	for i, domain := range requestedDomains {
		domainNames[i] = string(domain)
	}
	if !requireNamespaceSelectorAccess(w, r, namespace, domainNames...) {
		return
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
			Tags: tags, Domains: requestedDomains, WorkstreamID: q.Get("workstream_id"),
		},
	}

	if !recallIncludesWorkspace(requestedDomains) {
		// Preserve the original GET contract for revision-only reads. This route
		// has no cursor, and both brief and full historically accepted up to the
		// revision store's 500-row ceiling; the additive workspace pager's full
		// projection cap must not shorten that legacy response.
		results, err := s.itemRevisionStore().Recall(r.Context(), in)
		if err != nil {
			if errors.Is(err, memory.ErrEmbedderUnavailable) {
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
		var items any = results
		if format == "brief" {
			brief := make([]recallBriefItem, 0, len(results))
			for _, rr := range results {
				rev := rr.Revision
				brief = append(brief, recallBriefItem{
					RevisionID: rev.RevisionID, ItemID: rev.MemoryID, MemoryID: rev.MemoryID,
					Domain: string(rev.Domain), Namespace: rev.Namespace, MemoryKey: rev.MemoryKey,
					WorkstreamID: rev.WorkstreamID, Provenance: rev.Provenance,
					Tags: rev.Tags, Confidence: rev.Confidence, Summary: rev.Payload.Summary,
					CreatedAt: rev.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
				})
			}
			items = brief
		}
		writeJSON(w, http.StatusOK, recallResponse{
			Results: items, Facets: facets,
			Meta: recallMeta{Namespace: namespace, Limit: limit, Returned: len(results), Format: format},
		})
		return
	}

	page, err := s.itemService().RecallPaged(r.Context(), in, memory.PageRequest{Limit: limit, PayloadMode: memory.PayloadModeFull})
	if err != nil {
		if errors.Is(err, memory.ErrEmbedderUnavailable) {
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

	facets := buildItemFacets(page.Kept)

	var items any
	if format == "brief" {
		brief := make([]any, 0, len(page.Kept))
		for _, rr := range page.Kept {
			if rr.Item != nil {
				brief = append(brief, recallBriefWorkspaceItem{ItemID: rr.Item.ItemID, Domain: rr.Item.Domain, Namespace: rr.Item.Namespace, Key: rr.Item.Key, WorkstreamID: rr.Item.WorkstreamID, Provenance: rr.Item.Provenance, Tags: rr.Item.Tags, Summary: rr.Item.Payload.Summary, UpdatedAt: rr.Item.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")})
				continue
			}
			rev := *rr.Revision
			brief = append(brief, recallBriefItem{
				RevisionID:   rev.RevisionID,
				ItemID:       rev.MemoryID,
				MemoryID:     rev.MemoryID,
				Domain:       string(rev.Domain),
				Namespace:    rev.Namespace,
				MemoryKey:    rev.MemoryKey,
				WorkstreamID: rev.WorkstreamID,
				Provenance:   rev.Provenance,
				Tags:         rev.Tags,
				Confidence:   rev.Confidence,
				Summary:      rev.Payload.Summary,
				CreatedAt:    rev.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			})
		}
		items = brief
	} else {
		items = page.Results
	}

	writeJSON(w, http.StatusOK, recallResponse{
		Results: items,
		Facets:  facets,
		Meta: recallMeta{
			Namespace: namespace,
			Limit:     limit,
			Returned:  len(page.Kept),
			Format:    format,
		},
	})
}

func recallIncludesWorkspace(requested []domains.Domain) bool {
	for _, domain := range requested {
		if string(domain) == "workspace" {
			return true
		}
	}
	return false
}

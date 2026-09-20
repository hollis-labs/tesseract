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

	// Total, Truncated, TruncationReason and PagedRoute say whether Returned is
	// everything or a window (CW-20260919-0020). They are additive: the four
	// fields above are exactly what they were, because the boot profiles in the
	// mux catalog build their slot URLs on this route and read them.
	//
	// Total and Truncated are always present, and never omitted when zero or
	// false: `truncated: false` is the statement that Returned is everything, and
	// a caller must be able to read it rather than infer it from an absence.
	//
	// Total counts every row that matched before Limit was applied. This route
	// takes no query, so it ranks by activation or chronology and Total is exact,
	// not the fused-arm bound it is under ranking=relevance.
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`

	// TruncationReason is set only when Truncated is: `limit` (the caller's own
	// limit cut the set; raising it helps), `ceiling` (the caller asked for more
	// than this route's 500-row ceiling; raising it does not help), or
	// `payload_mode_limit_cap` (the workspace path's projection cap).
	TruncationReason string `json:"truncation_reason,omitempty"`

	// PagedRoute names the route that pages by cursor, set only when Truncated is.
	// This one has no cursor by design of its original contract, and ignores a
	// `cursor` parameter rather than refusing it, so a caller who sends one gets
	// the first page again. Nothing on this response can be resumed.
	PagedRoute string `json:"paged_route,omitempty"`
}

// recallTruncatedByCeiling is recallMeta.TruncationReason when the caller asked
// for more rows than this route will ever return. It is this route's own reason:
// the manifest vocabulary has no name for the legacy 500-row clamp, and
// payload_mode_limit_cap would say something false, since format=full is not
// capped here.
const recallTruncatedByCeiling = "ceiling"

// recallPagedRoute is where a caller goes when this route cut its result short.
// POST /v1/memory/recall is its sibling and runs the same engine; the lookup
// route is named here because its request is the flat, snake_case shape a caller
// moving off this route's query parameters already has. See docs/SPECS/API.md.
const recallPagedRoute = "POST /v1/tesseract/lookup"

// newRecallMeta builds the response meta, and is the one place the additive
// fields are derived, so the two paths below cannot disagree about them.
func newRecallMeta(namespace string, limit int, format string, returned, total int, truncated bool, reason string) recallMeta {
	meta := recallMeta{
		Namespace: namespace, Limit: limit, Returned: returned, Format: format,
		Total: total, Truncated: truncated,
	}
	if truncated {
		meta.TruncationReason = reason
		meta.PagedRoute = recallPagedRoute
	}
	return meta
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
	workstreamID := q.Get("workstream_id")

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
	if q.Has("workstream_id") {
		if err := memory.ValidateWorkstreamFilter(workstreamID); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
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
			Tags: tags, Domains: requestedDomains, WorkstreamID: workstreamID,
		},
	}

	if !recallIncludesWorkspace(requestedDomains) {
		// Preserve the original GET contract for revision-only reads. This route
		// has no cursor, and both brief and full historically accepted up to the
		// revision store's 500-row ceiling; the additive workspace pager's full
		// projection cap must not shorten that legacy response.
		// RecallPage rather than Recall: Recall is RecallPage's first page with
		// Total discarded, and Total is what lets meta say whether the response was
		// cut short. The window is the same rows.
		recalled, err := s.itemRevisionStore().RecallPage(r.Context(), in)
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
		results := recalled.Results

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
		truncated := recalled.Total > len(results)
		reason := memory.TruncationLimit
		if limit > memory.MaxRecallLimit {
			// The store clamps silently to its ceiling. Raising limit cannot help,
			// and saying `limit` would send the caller to try.
			reason = recallTruncatedByCeiling
		}
		writeJSON(w, http.StatusOK, recallResponse{
			Results: items, Facets: facets,
			Meta: newRecallMeta(namespace, limit, format, len(results), recalled.Total, truncated, reason),
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

	// The paged engine already computed all of this. The workspace path reports it
	// as the manifest does, including payload_mode_limit_cap, its projection cap.
	writeJSON(w, http.StatusOK, recallResponse{
		Results: items,
		Facets:  facets,
		Meta: newRecallMeta(namespace, limit, format, len(page.Kept),
			page.Manifest.ResultsTotal, page.Manifest.Truncated, page.Manifest.TruncationReason),
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

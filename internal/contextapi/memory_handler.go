package contextapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// memoryStoreUnavailable writes a 503 when MemoryStore is not configured
// (e.g. serve mode without memory wiring).
func (s *Server) memoryStoreUnavailable(w http.ResponseWriter) bool {
	if s.MemoryStore == nil {
		writeError(w, http.StatusServiceUnavailable, "memory_unavailable",
			"memory subsystem not wired into this server", nil)
		return true
	}
	return false
}

// memoryWriteRequest mirrors memory.WriteInput with JSON-friendly fields.
//
// Nested, like every other body on this surface: `author`, `payload` and
// `facets` are objects. The MCP memory_write tool takes the same facts flat
// (author_agent_id, author_version, payload_summary, payload_body) because MCP
// tool schemas favour flat scalar parameters. decodeRequestBody rejects a body
// in the other surface's shape by name rather than decoding it into a
// zero-valued struct — see the rationale on knowledgeWriteRequest.
type memoryWriteRequest struct {
	Domain         domains.Domain `json:"domain,omitempty"`
	Namespace      string         `json:"namespace"`
	MemoryKey      string         `json:"memory_key,omitempty"`
	Supersedes     string         `json:"supersedes,omitempty"`
	Status         memory.Status  `json:"status,omitempty"`
	Author         memory.Author  `json:"author"`
	Trigger        memory.Trigger `json:"trigger"`
	SessionID      string         `json:"session_id"`
	Origin         memory.Origin  `json:"origin"`
	Confidence     float64        `json:"confidence"`
	Tags           []string       `json:"tags,omitempty"`
	TTLSeconds     int64          `json:"ttl_seconds,omitempty"`
	Payload        memory.Payload `json:"payload"`
	Facets         memory.Facets  `json:"facets,omitempty"`
	Dedup          string         `json:"dedup,omitempty"`
	DedupThreshold float64        `json:"dedup_threshold,omitempty"`
}

func (s *Server) handleMemoryWrite(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryWriteRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	// This endpoint is memory-domain only. Knowledge writes must go through
	// POST /v1/knowledge/write for its knowledge-specific request shape. The
	// shared revision store remains authoritative for domain/facet invariants.
	if req.Domain != "" && req.Domain != domains.Memory {
		writeError(w, http.StatusBadRequest, "wrong_endpoint",
			"domain must be empty or 'memory' on /v1/memory/write; use /v1/knowledge/write for knowledge revisions",
			map[string]any{"domain": string(req.Domain)})
		return
	}
	req.Domain = domains.Memory
	if !requireNamespaceAccess(w, r, req.Namespace) {
		return
	}

	in := memory.WriteInput{
		Domain:         req.Domain,
		Namespace:      req.Namespace,
		MemoryKey:      req.MemoryKey,
		Supersedes:     req.Supersedes,
		Status:         req.Status,
		Author:         req.Author,
		Trigger:        req.Trigger,
		SessionID:      req.SessionID,
		Origin:         req.Origin,
		Confidence:     req.Confidence,
		Tags:           req.Tags,
		TTL:            time.Duration(req.TTLSeconds) * time.Second,
		Payload:        req.Payload,
		Facets:         req.Facets,
		Dedup:          req.Dedup,
		DedupThreshold: req.DedupThreshold,
	}

	rev, err := s.MemoryStore.WriteRevision(r.Context(), in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "memory_write_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

type memoryRecallRequest struct {
	Namespaces    []string             `json:"namespaces"`
	RevisionScope memory.RevisionScope `json:"revision_scope,omitempty"`
	Ranking       memory.Ranking       `json:"ranking,omitempty"`
	Query         string               `json:"query,omitempty"`
	Filters       memory.RecallFilters `json:"filters,omitempty"`
	Limit         int                  `json:"limit,omitempty"`

	// SearchMode selects the retrieval arms under ranking=relevance:
	// hybrid|lexical|semantic. Empty means hybrid. Peer of the MCP
	// tesseract_recall argument of the same name — same vocabulary, same
	// default, same validation, because both hand the value to
	// RecallPaged unmodified and it is RecallPaged that rejects it.
	SearchMode memory.SearchMode `json:"search_mode,omitempty"`

	// PayloadMode projects each result: keys|summary|full. Empty means the
	// server default (read.payload_mode). Peer of the MCP tesseract_recall
	// argument of the same name.
	//
	// parity_test.go pairs memory_recall with this route and carries no
	// waiver, so the two must agree on both the default and the accepted
	// vocabulary. The parity harness only asserts that the route exists —
	// argument parity is on us.
	PayloadMode memory.PayloadMode `json:"payload_mode,omitempty"`

	// SimilarityMin is the cosine floor. Peer of the MCP tesseract_recall argument
	// of the same name — same range, same ranking/search_mode restriction, same
	// error, because both hand the value to RecallPaged unmodified.
	//
	// It is declared FLAT here, next to search_mode and payload_mode, rather
	// than left to ride inside `filters`. The MCP argument is spelled
	// similarity_min, and this route's `filters` object decodes
	// memory.RecallFilters with Go's default field names (Origins, Tags,
	// ConfidenceMin, ...), so a nested spelling would be `SimilarityMin` and the
	// two doors would disagree on the name of the same knob. Flat keeps the
	// vocabulary identical across all four surfaces.
	//
	// When set it wins over anything decoded into Filters.SimilarityMin. The
	// two cannot collide in practice — encoding/json matches field names
	// case-insensitively but not across underscores, so `filters.similarity_min`
	// does not decode into that field at all — but the precedence is stated
	// rather than left to be discovered.
	//
	// Note what that means for the nested object: memory.RecallFilters carries
	// no struct tags, so its keys are its Go field names (Origins, Statuses,
	// Tags, ConfidenceMin, ...), matched case-insensitively and NOT across
	// underscores. `filters` is the one place on this surface that is not
	// snake_case. Under decodeRequestBody a snake_case child now returns a 400
	// naming it rather than being dropped in silence, which is the point — but
	// the leaf name is all encoding/json reports, so the error cannot say which
	// object it came from.
	//
	// A pointer for the reason memory.RecallFilters.SimilarityMin documents:
	// 0.0 is a legal floor and `omitempty` on a bare float64 would drop it.
	SimilarityMin *float64 `json:"similarity_min,omitempty"`

	// Cursor, BudgetBytes, BudgetTokens and EstimateOnly: peers of the MCP
	// tesseract_recall arguments of the same name. See pageArgs.
	pageArgs
}

func (s *Server) handleMemoryRecall(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryRecallRequest
	if !decodeRequestBody(w, r, &req) {
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
	pr, ok := s.pageRequest(w, req.pageArgs, payloadMode, req.Limit)
	if !ok {
		return
	}

	filters := req.Filters
	if req.SimilarityMin != nil {
		filters.SimilarityMin = req.SimilarityMin
	}
	in := memory.RecallInput{
		Namespaces:    req.Namespaces,
		RevisionScope: req.RevisionScope,
		Ranking:       req.Ranking,
		SearchMode:    req.SearchMode,
		Query:         req.Query,
		Filters:       filters,
	}
	page, err := s.MemoryStore.RecallPaged(r.Context(), in, pr)
	if err != nil {
		writeRecallError(w, err, "recall_failed")
		return
	}
	// The same page.Manifest the full response below carries — this route has
	// no facet histogram, so its estimate carries none either.
	if pr.EstimateOnly {
		writeJSON(w, http.StatusOK, estimateResponse{
			Manifest:     page.Manifest,
			EstimateOnly: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleMemoryGetRevision serves GET /v1/memory/revisions/{id}.
func (s *Server) handleMemoryGetRevision(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/memory/revisions/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "validation_error", "revision id required", nil)
		return
	}
	// Deliberate read: reinforce the parent memory's activation.
	rev, err := s.MemoryStore.GetRevisionByIDReinforced(r.Context(), id)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// handleMemoryGetCurrent serves GET /v1/memory/current?namespace=...&memory_key=...
func (s *Server) handleMemoryGetCurrent(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	ns := r.URL.Query().Get("namespace")
	key := r.URL.Query().Get("memory_key")
	if ns == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and memory_key are required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, ns) {
		return
	}
	// Deliberate read: reinforce the resolved memory's activation — but only
	// after the domain check inside GetCurrentInDomainReinforced. This route
	// resolved (namespace, memory_key) unfiltered until CW-20260825-0010, so a
	// knowledge namespace asked of it returned the knowledge revision and
	// reinforced it, contradicting the contract knowledge/store.go states.
	rev, err := s.MemoryStore.GetCurrentInDomainReinforced(r.Context(), domains.Memory, ns, key)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// handleMemoryHistory serves GET /v1/memory/history?namespace=...&memory_key=...
func (s *Server) handleMemoryHistory(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	ns := r.URL.Query().Get("namespace")
	key := r.URL.Query().Get("memory_key")
	if ns == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and memory_key are required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, ns) {
		return
	}
	pr, ok := s.historyPageRequest(w, r)
	if !ok {
		return
	}
	revs, err := s.MemoryStore.GetHistoryInDomain(r.Context(), domains.Memory, ns, key)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeHistoryPage(w, revs, pr, memory.HistoryOrderingFingerprint(string(domains.Memory), ns, key))
}

type memoryDeprecateRequest struct {
	RevisionID string `json:"revision_id"`
}

func (s *Server) handleMemoryDeprecate(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryDeprecateRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if req.RevisionID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "revision_id is required", nil)
		return
	}
	if err := s.MemoryStore.Deprecate(r.Context(), req.RevisionID); err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "deprecate_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "deprecated",
		"revision_id": req.RevisionID,
	})
}

// memoryTouchRequest is the body of POST /v1/memory/touch, the HTTP peer of the
// tesseract_touch MCP tool. Field name and semantics are the tool's argument;
// the two are asserted equal in tests/parity.
type memoryTouchRequest struct {
	RevisionIDs []string `json:"revision_ids"`
}

// handleMemoryTouch serves POST /v1/memory/touch: the caller reporting which
// recalled revisions actually informed its work.
//
// The batch cap and the dedup rules live in the store, not here, so this door and
// the MCP tool cannot drift on either — both surface memory.ErrInvalidInput as a
// validation_error with the store's own message.
//
// No namespace check: the request names revision IDs, not namespaces, exactly as
// GET /v1/memory/revisions/{id} does — the other route that reinforces by
// revision ID. It is gated by authorizeRequest in the router because it is a
// POST that changes state; that gate authenticates rather than checking a
// write scope, so a read-only caller can still close the loop.
func (s *Server) handleMemoryTouch(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryTouchRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	res, err := s.MemoryStore.TouchRevisions(r.Context(), req.RevisionIDs)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "touch_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type memoryPromoteRequest struct {
	SourceNamespace string `json:"source_namespace"`
	SourceMemoryID  string `json:"source_memory_id"`
	TargetNamespace string `json:"target_namespace"`
	ActorAgentID    string `json:"actor_agent_id"`
	ActorVersion    string `json:"actor_version,omitempty"`
}

func (s *Server) handleMemoryPromote(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryPromoteRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if !requireNamespaceAccess(w, r, req.SourceNamespace) {
		return
	}
	if !requireNamespaceAccess(w, r, req.TargetNamespace) {
		return
	}
	in := memory.PromoteInput{
		SourceNamespace: req.SourceNamespace,
		SourceMemoryID:  req.SourceMemoryID,
		TargetNamespace: req.TargetNamespace,
		ActorAgentID:    req.ActorAgentID,
		ActorVersion:    req.ActorVersion,
	}
	promoted, err := s.MemoryStore.Promote(r.Context(), in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "promote_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, promoted)
}

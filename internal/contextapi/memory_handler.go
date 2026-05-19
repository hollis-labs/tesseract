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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
		return
	}
	// This endpoint is memory-domain only. Knowledge writes must go through
	// POST /v1/knowledge/write so facet invariants (kind/source/pointer) are
	// enforced by knowledge.Store.
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
}

func (s *Server) handleMemoryRecall(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryRecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
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
		Filters:       req.Filters,
		Limit:         req.Limit,
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
	writeJSON(w, http.StatusOK, results)
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
	// Deliberate read: reinforce the resolved memory's activation.
	rev, err := s.MemoryStore.GetCurrentReinforced(r.Context(), ns, key)
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
	revs, err := s.MemoryStore.GetHistory(r.Context(), ns, key)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, revs)
}

type memoryDeprecateRequest struct {
	RevisionID string `json:"revision_id"`
}

func (s *Server) handleMemoryDeprecate(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req memoryDeprecateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
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

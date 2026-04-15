package contextapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

type knowledgeWriteRequest struct {
	Namespace  string         `json:"namespace"`
	Key        string         `json:"key,omitempty"`
	Kind       string         `json:"kind"`
	Source     string         `json:"source"`
	Pointer    memory.Pointer `json:"pointer"`
	Summary    string         `json:"summary"`
	Body       string         `json:"body,omitempty"`
	Author     memory.Author  `json:"author"`
	SessionID  string         `json:"session_id"`
	Tags       []string       `json:"tags,omitempty"`
	TTLSeconds int64          `json:"ttl_seconds,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Supersedes string         `json:"supersedes,omitempty"`
}

func (s *Server) handleKnowledgeWrite(w http.ResponseWriter, r *http.Request) {
	if s.KnowledgeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "knowledge_unavailable",
			"knowledge subsystem not wired into this server", nil)
		return
	}
	var req knowledgeWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON: "+err.Error(), nil)
		return
	}
	if !requireNamespaceAccess(w, r, req.Namespace) {
		return
	}

	rev, err := s.KnowledgeStore.Write(r.Context(), knowledge.WriteInput{
		Namespace:  req.Namespace,
		Key:        req.Key,
		Kind:       req.Kind,
		Source:     req.Source,
		Pointer:    req.Pointer,
		Summary:    req.Summary,
		Body:       req.Body,
		Author:     req.Author,
		SessionID:  req.SessionID,
		Tags:       req.Tags,
		TTL:        time.Duration(req.TTLSeconds) * time.Second,
		Confidence: req.Confidence,
		Supersedes: req.Supersedes,
	})
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "knowledge_write_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

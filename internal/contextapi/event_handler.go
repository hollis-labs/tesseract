package contextapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// The event domain's HTTP surface (CW-20260909-0035): a write and a log read.
//
// Two routes, not five. Event gets no /v1/event/current or /v1/event/history
// peers for tesseract_get and tesseract_history, and that is a decision rather
// than a gap: those tools serve the event domain over MCP, but almost every
// event is written KEYLESS, so a (namespace, key) fetch answers not_found for
// the overwhelming majority of the log. Shipping the routes would advertise a
// door that mostly does not open. The keyed minority is reachable over MCP; the
// log itself is reachable here, which is the read that actually exists.

// eventWriteRequest is the POST /v1/event/write body.
//
// Nested `author`, flat everything else — the same shape
// knowledgeWriteRequest uses, for the same reason: this surface nests its
// sub-objects and MCP flattens them, and making one endpoint disagree with the
// rest of the HTTP API to match another protocol's ergonomics would be a worse
// inconsistency than the one it removed.
type eventWriteRequest struct {
	Namespace  string         `json:"namespace"`
	Key        string         `json:"key,omitempty"`
	Summary    string         `json:"summary"`
	Body       string         `json:"body,omitempty"`
	Author     memory.Author  `json:"author"`
	SessionID  string         `json:"session_id"`
	Tags       []string       `json:"tags,omitempty"`
	TTLSeconds int64          `json:"ttl_seconds,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Origin     memory.Origin  `json:"origin,omitempty"`
	Trigger    memory.Trigger `json:"trigger,omitempty"`
	Supersedes string         `json:"supersedes,omitempty"`
}

// eventStoreUnavailable writes a 503 when EventStore is not configured.
func (s *Server) eventStoreUnavailable(w http.ResponseWriter) bool {
	if s.EventStore == nil {
		writeError(w, http.StatusServiceUnavailable, "event_unavailable",
			"event subsystem not wired into this server", nil)
		return true
	}
	return false
}

// handleEventWrite serves POST /v1/event/write.
func (s *Server) handleEventWrite(w http.ResponseWriter, r *http.Request) {
	if s.eventStoreUnavailable(w) {
		return
	}
	var req eventWriteRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}
	if !requireNamespaceAccess(w, r, req.Namespace) {
		return
	}

	rev, err := s.EventStore.Write(r.Context(), event.WriteInput{
		Namespace:  req.Namespace,
		Key:        req.Key,
		Summary:    req.Summary,
		Body:       req.Body,
		Author:     req.Author,
		SessionID:  req.SessionID,
		Tags:       req.Tags,
		TTL:        time.Duration(req.TTLSeconds) * time.Second,
		Confidence: req.Confidence,
		Origin:     req.Origin,
		Trigger:    req.Trigger,
		Supersedes: req.Supersedes,
	})
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "event_write_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// handleEventLog serves GET /v1/event/log.
//
// A GET with query parameters rather than a POST with a body, matching
// /v1/memory/history and /v1/context/audit: it is a read with no side effects,
// and a cursor round-trips through a URL fine because it is already base64url.
//
//	?namespace=user/chrispian/event/journal   (repeatable)
//	&direction=newest_first|oldest_first
//	&since=RFC3339&until=RFC3339
//	&limit=50&cursor=...&payload_mode=summary
//
// `namespace` is repeatable rather than comma-separated because a namespace is
// a path and commas in a path would be ambiguous the first time somebody used
// one.
func (s *Server) handleEventLog(w http.ResponseWriter, r *http.Request) {
	if s.eventStoreUnavailable(w) {
		return
	}
	q := r.URL.Query()

	namespaces := q["namespace"]
	if len(namespaces) == 0 {
		writeError(w, http.StatusBadRequest, "validation_error",
			"at least one `namespace` query parameter is required", nil)
		return
	}
	for _, ns := range namespaces {
		if strings.TrimSpace(ns) == "" {
			writeError(w, http.StatusBadRequest, "validation_error",
				"`namespace` entries must be non-empty", nil)
			return
		}
		// Every namespace is authorized, not just the first. A read that
		// checked one and served several would let a token widen its own reach
		// by appending parameters.
		if !requireNamespaceAccess(w, r, ns) {
			return
		}
	}

	in := memory.EventLogInput{
		Namespaces: namespaces,
		Direction:  memory.LogDirection(q.Get("direction")),
		Cursor:     q.Get("cursor"),
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error",
				"limit must be an integer: "+err.Error(), nil)
			return
		}
		in.Limit = n
	}
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error",
				"since must be RFC3339: "+err.Error(), nil)
			return
		}
		in.Since = &t
	}
	if raw := q.Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error",
				"until must be RFC3339: "+err.Error(), nil)
			return
		}
		in.Until = &t
	}

	mode := memory.DefaultPayloadMode
	if raw := q.Get("payload_mode"); raw != "" {
		mode = memory.PayloadMode(raw)
		if !mode.Valid() {
			writeError(w, http.StatusBadRequest, "validation_error",
				"payload_mode must be one of keys|summary|full, got "+raw, nil)
			return
		}
	}
	// Same payload-weight clamp the MCP tool applies; see handleEventList.
	if effective, _ := memory.ClampRecallLimit(in.Limit, mode); in.Limit > 0 {
		in.Limit = effective
	}

	page, err := s.EventStore.ReadLog(r.Context(), in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidCursor) || errors.Is(err, memory.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "event_log_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, memory.ProjectEventLogPage(page, mode))
}

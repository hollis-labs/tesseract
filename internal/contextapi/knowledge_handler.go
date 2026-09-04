package contextapi

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// --- Strict request-body decoding ------------------------------------------

// decodeRequestBody reads a JSON request body into dst, or writes the 400 and
// returns false. It is what the handlers in this file and in
// memory_handler.go, lookup_handler.go and synthesis_handler.go use instead of
// a bare json.NewDecoder(r.Body).
//
// It delegates to decodeJSON, the package chokepoint that sets
// DisallowUnknownFields and caps the body, and adds one thing on top: it turns
// the decoder's terse unknown-field error into a message that names the field
// and, where we recognise it, the shape this surface actually wants.
//
// Strictness is the whole point. These routes used to decode leniently, so a
// body shaped for a different surface decoded "successfully" into a
// zero-valued struct and then failed downstream as a complaint about missing
// content that never mentioned a single field the caller had sent. Rejecting
// the request while the offending field name is still in hand is the
// difference between fixing a payload in one attempt and not being able to
// tell what went wrong at all.
func decodeRequestBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := decodeJSON(r, dst); err != nil {
		writeDecodeError(w, err, dst)
		return false
	}
	return true
}

// mcpFlatFieldHints maps the flat argument spellings of the MCP tool surface
// onto the nested location an HTTP body carries the same fact in.
//
// These are exactly the pairs a caller reaches for after reading an MCP tool
// schema — or docs/MCP_TOOLS.md, which pairs the tools with these routes
// without restating the body shape — and assuming both doors take the same
// parameters. Note author_version: the MCP argument is NOT spelled
// author_agent_version, so both plausible flattenings are mapped.
//
// A hint is only offered when the target struct actually declares the parent
// object, so a route with no `author` never advises a caller to nest one.
var mcpFlatFieldHints = map[string]string{
	"pointer_scheme":       "pointer.scheme",
	"pointer_locator":      "pointer.locator",
	"pointer_resolved_at":  "pointer.resolved_at",
	"author_agent_id":      "author.agent_id",
	"author_version":       "author.agent_version",
	"author_agent_version": "author.agent_version",
	"payload_summary":      "payload.summary",
	"payload_body":         "payload.body",
}

// unknownFieldPrefix opens the error encoding/json returns under
// DisallowUnknownFields. There is no typed error to match on — the decoder
// builds it with fmt.Errorf — so the field name is recovered from the text.
const unknownFieldPrefix = "json: unknown field "

// writeDecodeError renders a decode failure as the package's standard
// validation_error envelope. An unknown field gets the useful version: the
// field by name, the nested equivalent when this is a known MCP-vs-HTTP pair,
// and the list of keys the endpoint does accept — the canonical shape is
// otherwise undiscoverable from the error alone.
func writeDecodeError(w http.ResponseWriter, err error, dst any) {
	field, ok := strings.CutPrefix(err.Error(), unknownFieldPrefix)
	if !ok {
		// A syntax error, a type mismatch, or an over-limit body — decodeJSON
		// has already phrased that last one in terms of the limit.
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	field = strings.Trim(field, `"`)

	accepted := jsonFieldNames(dst)
	details := map[string]any{
		"unknown_field":   field,
		"accepted_fields": accepted,
	}
	message := "unknown field " + quoteJSON(field) + " in request body"
	if nested, hinted := mcpFlatFieldHints[field]; hinted {
		if parent, _, _ := strings.Cut(nested, "."); slices.Contains(accepted, parent) {
			details["expected_field"] = nested
			message += "; this endpoint nests it as " + nested +
				" — the flat spelling is an MCP tool argument, not an HTTP body field"
		}
	}
	// Top-level, deliberately: encoding/json reports an unknown field nested
	// inside an object by its leaf name alone, with no path, so this list is
	// the one thing that can be stated without guessing where the key sat.
	if len(accepted) > 0 {
		message += ". Accepted top-level fields: " + strings.Join(accepted, ", ")
	}
	writeError(w, http.StatusBadRequest, "validation_error", message, details)
}

// jsonFieldNames lists the JSON keys a request struct accepts, in declaration
// order, so an error can say what the door does take rather than only what it
// does not. Embedded structs (pageArgs) are flattened the way encoding/json
// promotes them, including when the embedded type is unexported.
func jsonFieldNames(dst any) []string {
	t := reflect.TypeOf(dst)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			names = append(names, jsonFieldNames(reflect.New(f.Type).Interface())...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// knowledgeWriteRequest is the body of POST /v1/knowledge/write.
//
// It is NESTED — `pointer` and `author` are objects — while the MCP
// knowledge_write tool takes the same facts FLAT (pointer_scheme,
// pointer_locator, author_agent_id, author_version). Both shapes are
// deliberate, and this door keeps the nesting:
//
//   - The HTTP surface is internally consistent. POST /v1/memory/write nests
//     `author`, `payload` and `facets` for the same reason. Flattening only
//     knowledge would make the HTTP API inconsistent with itself in order to
//     match another protocol's ergonomics.
//   - MCP flattens because MCP tool schemas favour flat scalar parameters.
//     That is a property of that surface, not a contract this one adopts.
//
// The defect this endpoint actually had was never the nesting: it was that a
// flat body was accepted in silence, leaving Pointer zero-valued and failing
// later with a validation error about missing pointer facets that named none
// of the fields the caller had sent. decodeRequestBody is the fix.
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
	if !decodeRequestBody(w, r, &req) {
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

// knowledgeStoreUnavailable writes a 503 when KnowledgeStore is not configured.
func (s *Server) knowledgeStoreUnavailable(w http.ResponseWriter) bool {
	if s.KnowledgeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "knowledge_unavailable",
			"knowledge subsystem not wired into this server", nil)
		return true
	}
	return false
}

// handleKnowledgeGetCurrent serves GET /v1/knowledge/current?namespace=...&memory_key=...
//
// Param name `memory_key` matches the equivalent /v1/memory/current handler so
// callers can target either store with a single normalized identifier. The
// underlying KnowledgeStore.GetCurrent call still takes the bare key string.
func (s *Server) handleKnowledgeGetCurrent(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeStoreUnavailable(w) {
		return
	}
	namespace := r.URL.Query().Get("namespace")
	key := r.URL.Query().Get("memory_key")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and memory_key are required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, namespace) {
		return
	}
	rev, err := s.KnowledgeStore.GetCurrent(r.Context(), namespace, key)
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

// handleKnowledgeGetHistory serves GET /v1/knowledge/history?namespace=...&memory_key=...
func (s *Server) handleKnowledgeGetHistory(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeStoreUnavailable(w) {
		return
	}
	namespace := r.URL.Query().Get("namespace")
	key := r.URL.Query().Get("memory_key")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and memory_key are required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, namespace) {
		return
	}
	pr, ok := s.historyPageRequest(w, r)
	if !ok {
		return
	}
	revs, err := s.KnowledgeStore.GetHistory(r.Context(), namespace, key)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeHistoryPage(w, revs, pr, memory.HistoryOrderingFingerprint(string(domains.Knowledge), namespace, key))
}

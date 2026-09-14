package contextapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/surfacefields"
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
// and, where we recognize it, the shape this surface actually wants.
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

// requestDoors names the surfacefields door each strict request struct belongs
// to, so a decode failure can ask the shared table what this route calls a fact
// the caller named the MCP way.
//
// This replaces a literal map of flat-to-nested pairs. That map was correct
// when written and went stale on 2026-09-12, when `payload_data` landed on all
// three write tools and nothing updated it — leaving the one field
// CW-20260912-0048 was filed about as the one field the translation could not
// translate. Nothing tied the table to the surfaces it described; a door name
// does, and TestDoorsMatchTheSurfaces fails when either surface moves.
//
// It also removes a limit the literal had by construction. Its values were
// dotted paths, so it could only ever say "nest it" — it had no way to tell a
// caller that /v1/knowledge/write takes the same fact flat, under `data`.
var requestDoors = map[reflect.Type]string{
	reflect.TypeOf(memoryWriteRequest{}):            "memory.write",
	reflect.TypeOf(knowledgeWriteRequest{}):         "knowledge.write",
	reflect.TypeOf(eventWriteRequest{}):             "event.write",
	reflect.TypeOf(workspaceWriteRequest{}):         "workspace.write",
	reflect.TypeOf(workspacePromotionRequestHTTP{}): "workspace.promote",
	reflect.TypeOf(referenceResolveRequest{}):       "reference.resolve",
}

// doorFor resolves the door a decode target belongs to, or false for a request
// struct no door covers — every route that does not take the record's own
// fields, which is most of them.
func doorFor(dst any) (surfacefields.Door, bool) {
	t := reflect.TypeOf(dst)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return surfacefields.Door{}, false
	}
	name, ok := requestDoors[t]
	if !ok {
		return surfacefields.Door{}, false
	}
	return surfacefields.DoorByName(name)
}

// crossSurfaceHint renders the sentence an unknown field earns when the shared
// table knows what this door calls the same fact, plus the spelling itself for
// the details bag.
//
// The phrasing follows the answer rather than assuming it: a dotted path is
// something to nest, a bare name is something to spell differently. The old
// message said "nests it as" unconditionally, which would have been wrong for
// every flat answer it could not produce anyway.
func crossSurfaceHint(spelling string) string {
	if strings.Contains(spelling, ".") {
		return "; this endpoint nests it as " + spelling +
			" — the flat spelling is an MCP tool argument, not an HTTP body field"
	}
	return "; this endpoint takes it flat as " + spelling
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
// retiredFieldHints names fields this API used to accept under a different
// spelling, so an unknown-field rejection can say where the value went.
//
// The rejection itself is free — decodeJSON runs DisallowUnknownFields, so an
// HTTP caller sending a retired name already fails closed rather than having
// its value dropped. What is NOT free is the caller learning the new name: a
// bare "unknown field \"origin\"" tells a migrating client that its field is
// wrong without telling it what is right, which costs a round trip at best and
// a guess at worst. This is the HTTP half of the MCP surface's
// rejectRetiredArg.
var retiredFieldHints = map[string]string{
	"origin": "`origin` was renamed to `derived_from` on 2026-09-12. The five values are " +
		"unchanged (user, feedback, project, reference, observation) — only the name moved, " +
		"because `origin` read as *who originated this* and was being filled in as an " +
		"authorship claim.",
	"origins": "`origins` was renamed to `derived_from` on 2026-09-12 and still takes an array " +
		"of the same five values. Only the name moved.",
}

// rejectRetiredReadKey checks presence, including an empty value and requests
// carrying both spellings, before a read can select or reinforce an entry.
func rejectRetiredReadKey(w http.ResponseWriter, r *http.Request) bool {
	if !r.URL.Query().Has("memory_key") {
		return false
	}
	writeError(w, http.StatusBadRequest, "validation_error",
		"query parameter memory_key was renamed to key; send key only. Stored revisions and read responses still use memory_key",
		map[string]any{"unknown_field": "memory_key", "renamed_to": "key", "expected_field": "key"})
	return true
}

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
	// A retired name is checked first: it is a strictly better explanation than
	// the flat/nested hint, and a field can plausibly be both.
	if _, memoryWrite := dst.(*memoryWriteRequest); memoryWrite && field == "payload" {
		details["expected_fields"] = []string{"summary", "body", "data", "data_schema_hash"}
		message += "; memory write content is now flat: move payload.summary, payload.body, payload.data and payload.data_schema_hash to top-level summary, body, data and data_schema_hash. Send only the flat fields; read responses still use payload"
	} else if _, memoryRecall := dst.(*memoryRecallRequest); memoryRecall && field == "workstream_id" {
		details["expected_field"] = "workstream_id"
		message += "; move filters.workstream_id to the top-level workstream_id field and send only the top-level selector"
	} else if retired, hinted := retiredFieldHints[field]; hinted {
		details["renamed_to"] = "derived_from"
		message += "; " + retired
	} else if door, known := doorFor(dst); known {
		if spelling, hinted := door.HTTPSpellingFor(field); hinted {
			// The table says what this door calls the fact; `accepted` says what
			// the struct in hand actually declares. They agree unless one has
			// drifted, and the check that would have caught that runs in CI, not
			// here — so stay silent rather than advise a field the body cannot
			// take. Top-level, because that is the granularity `accepted` has.
			top, _, _ := strings.Cut(spelling, ".")
			if slices.Contains(accepted, top) {
				details["expected_field"] = spelling
				message += crossSurfaceHint(spelling)
			}
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
// Content is flat, matching the library and the other HTTP write routes.
// Pointer and author remain objects; Tesseract's MCP API chooses flat names
// such as pointer_scheme and author_agent_id for these same facts.
// decodeRequestBody refuses the other surface's spelling with a useful hint,
// rather than dropping it and reporting a missing facet later.
type knowledgeWriteRequest struct {
	Namespace    string         `json:"namespace"`
	Actor        string         `json:"actor,omitempty"`
	Key          string         `json:"key,omitempty"`
	WorkstreamID presentString  `json:"workstream_id,omitempty"`
	Kind         string         `json:"kind"`
	Source       string         `json:"source"`
	Pointer      memory.Pointer `json:"pointer"`
	Summary      string         `json:"summary"`
	Body         string         `json:"body,omitempty"`
	// The record's own fields, stored verbatim and never interpreted. Nested
	// here where MCP takes a JSON-encoded string, the way `tags` already
	// differs. See internal/memory/payloaddata.go.
	Data           json.RawMessage `json:"data,omitempty"`
	DataSchemaHash string          `json:"data_schema_hash,omitempty"`
	Author         memory.Author   `json:"author"`
	SessionID      string          `json:"session_id"`
	Tags           []string        `json:"tags,omitempty"`
	TTLSeconds     int64           `json:"ttl_seconds,omitempty"`
	Confidence     float64         `json:"confidence,omitempty"`
	Supersedes     string          `json:"supersedes,omitempty"`

	// ConsumerState is the caller's operational JSON bag (CW-20260909-0036).
	ConsumerState json.RawMessage `json:"consumer_state,omitempty"`
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

	actor := req.Actor
	if actor == "" {
		actor = "agent"
	}
	var clientID string
	if claims, ok := getTokenClaims(r); ok {
		clientID = claims.ClientID
	}
	if err := s.Policy.CanWrite(clientID, actor, req.Namespace); err != nil {
		writeError(w, http.StatusForbidden, "policy_denied", err.Error(), nil)
		return
	}

	rev, err := s.KnowledgeStore.Write(r.Context(), knowledge.WriteInput{
		Namespace:      req.Namespace,
		Actor:          actor,
		ClientID:       clientID,
		Key:            req.Key,
		WorkstreamID:   req.WorkstreamID.Pointer(),
		Kind:           req.Kind,
		Source:         req.Source,
		Pointer:        req.Pointer,
		Summary:        req.Summary,
		Body:           req.Body,
		Data:           req.Data,
		DataSchemaHash: req.DataSchemaHash,
		Author:         req.Author,
		SessionID:      req.SessionID,
		Tags:           req.Tags,
		TTL:            time.Duration(req.TTLSeconds) * time.Second,
		Confidence:     req.Confidence,
		Supersedes:     req.Supersedes,
		ConsumerState:  req.ConsumerState,
	})
	if err != nil {
		var spv *contextpolicy.ScopePolicyViolation
		if errors.As(err, &spv) {
			writeError(w, http.StatusForbidden, "policy_denied", err.Error(), nil)
			return
		}
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

// handleKnowledgeGetCurrent serves GET /v1/knowledge/current?namespace=...&key=...
//
// Param name `key` matches the equivalent /v1/memory/current handler so
// callers can target either store with a single normalized identifier. The
// underlying KnowledgeStore.GetCurrentReinforced call still takes the bare key
// string.
//
// This is a deliberate read and reinforces, matching /v1/memory/current. It did
// not until CW-20260910-0021: this route and tesseract_get's knowledge arm were
// the two call sites that reached for the plain getter, which is how knowledge
// came to decay with nothing lifting it.
func (s *Server) handleKnowledgeGetCurrent(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeStoreUnavailable(w) {
		return
	}
	namespace := r.URL.Query().Get("namespace")
	if rejectRetiredReadKey(w, r) {
		return
	}
	key := r.URL.Query().Get("key")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
		return
	}
	if !requireNamespaceAccess(w, r, namespace) {
		return
	}
	rev, err := s.KnowledgeStore.GetCurrentReinforced(r.Context(), namespace, key)
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

// handleKnowledgeGetHistory serves GET /v1/knowledge/history?namespace=...&key=...
func (s *Server) handleKnowledgeGetHistory(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeStoreUnavailable(w) {
		return
	}
	namespace := r.URL.Query().Get("namespace")
	if rejectRetiredReadKey(w, r) {
		return
	}
	key := r.URL.Query().Get("key")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and key are required", nil)
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

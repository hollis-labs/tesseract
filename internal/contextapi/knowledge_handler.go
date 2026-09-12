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
	reflect.TypeOf(memoryWriteRequest{}):    "memory.write",
	reflect.TypeOf(knowledgeWriteRequest{}): "knowledge.write",
	reflect.TypeOf(eventWriteRequest{}):     "event.write",
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
	return "; this endpoint takes it flat as " + spelling +
		" — the prefixed spelling is an MCP tool argument, not an HTTP body field"
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
	if retired, hinted := retiredFieldHints[field]; hinted {
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
// It is NESTED — `pointer` and `author` are objects — while the MCP
// knowledge_write tool takes the same facts FLAT (pointer_scheme,
// pointer_locator, author_agent_id, author_version). Both shapes are
// deliberate, and this door keeps the nesting:
//
//   - The HTTP surface is internally consistent. POST /v1/memory/write nests
//     `author`, `payload` and `facets` for the same reason. Flattening only
//     knowledge would make the HTTP API inconsistent with itself in order to
//     match another protocol's ergonomics.
//   - MCP flattens because MCP tool schemas favor flat scalar parameters.
//     That is a property of that surface, not a contract this one adopts.
//
// The defect this endpoint actually had was never the nesting: it was that a
// flat body was accepted in silence, leaving Pointer zero-valued and failing
// later with a validation error about missing pointer facets that named none
// of the fields the caller had sent. decodeRequestBody is the fix.
type knowledgeWriteRequest struct {
	Namespace string         `json:"namespace"`
	Key       string         `json:"key,omitempty"`
	Kind      string         `json:"kind"`
	Source    string         `json:"source"`
	Pointer   memory.Pointer `json:"pointer"`
	Summary   string         `json:"summary"`
	Body      string         `json:"body,omitempty"`
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

	rev, err := s.KnowledgeStore.Write(r.Context(), knowledge.WriteInput{
		Namespace:      req.Namespace,
		Key:            req.Key,
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
	key := r.URL.Query().Get("memory_key")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "namespace and memory_key are required", nil)
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

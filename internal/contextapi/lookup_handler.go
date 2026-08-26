package contextapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// tesseractLookupRequest is the unified search payload across memory +
// knowledge domains. Thin wrapper over memory.RecallInput that makes the
// cross-domain filters explicit in JSON.
type tesseractLookupRequest struct {
	Namespaces    []string             `json:"namespaces"`
	RevisionScope memory.RevisionScope `json:"revision_scope,omitempty"`
	Ranking       memory.Ranking       `json:"ranking,omitempty"`
	Query         string               `json:"query,omitempty"`
	Limit         int                  `json:"limit,omitempty"`

	// SearchMode selects the retrieval arms under ranking=relevance:
	// hybrid|lexical|semantic. Empty means hybrid. Peer of the MCP
	// tesseract_recall argument of the same name — same vocabulary, same
	// default, same validation, because both hand the value to RecallPaged
	// unmodified and it is RecallPaged that rejects it.
	SearchMode memory.SearchMode `json:"search_mode,omitempty"`

	Domains      []domains.Domain `json:"domains,omitempty"`
	FacetKinds   []string         `json:"facet_kinds,omitempty"`
	FacetSources []string         `json:"facet_sources,omitempty"`

	// PointerHealth filters knowledge results by verification state. Peer of
	// the MCP tesseract_recall argument of the same name — same vocabulary,
	// same semantics, same SQL-before-limit application.
	PointerHealth []string `json:"pointer_health,omitempty"`

	Origins       []memory.Origin `json:"origins,omitempty"`
	Statuses      []memory.Status `json:"statuses,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	ConfidenceMin float64         `json:"confidence_min,omitempty"`

	// SimilarityMin is the cosine floor. Peer of the MCP tesseract_recall
	// argument of the same name — same range, same ranking/search_mode
	// restriction, same error, because both hand the value to RecallPaged
	// unmodified and it is RecallPaged that rejects it.
	//
	// A pointer for the reason memory.RecallFilters.SimilarityMin documents:
	// 0.0 is a legal floor that selects a different set from an absent one, and
	// `omitempty` on a bare float64 would erase exactly that case on the wire.
	SimilarityMin *float64 `json:"similarity_min,omitempty"`

	Since *time.Time `json:"since,omitempty"`
	Until *time.Time `json:"until,omitempty"`

	// PayloadMode projects each result: keys|summary|full. Empty means the
	// server default (read.payload_mode). Peer of the MCP tesseract_recall
	// argument of the same name — same vocabulary, same semantics.
	//
	// A caller that intends to EDIT what it reads must pass "full"
	// explicitly. Under a projected mode payload.body is withheld, and
	// because Payload.Body carries `omitempty` a withheld body is
	// indistinguishable from an empty one by shape alone — results carry
	// `payload_mode` for exactly that reason.
	PayloadMode memory.PayloadMode `json:"payload_mode,omitempty"`

	// Cursor, BudgetBytes and BudgetTokens: peers of the MCP tesseract_recall
	// arguments of the same name. See pageArgs.
	pageArgs
}

// tesseractLookupResponse wraps the recall results with a simple facet
// histogram computed client-side from the result set. The histogram is
// best-effort: it reflects only returned rows, not the full match set —
// Manifest.ResultsTotal is the number to read for that.
// Results is `any` because its shape depends on payload_mode: full mode
// serializes []memory.RecallResult unchanged, while keys and summary
// serialize []memory.ProjectedResult.
type tesseractLookupResponse struct {
	Results  any             `json:"results"`
	Facets   lookupFacets    `json:"facets"`
	Manifest memory.Manifest `json:"manifest"`
}

type lookupFacets struct {
	Domains map[string]int `json:"domains,omitempty"`
	Kinds   map[string]int `json:"kinds,omitempty"`
	Sources map[string]int `json:"sources,omitempty"`
}

func (s *Server) handleTesseractLookup(w http.ResponseWriter, r *http.Request) {
	if s.memoryStoreUnavailable(w) {
		return
	}
	var req tesseractLookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "malformed JSON: "+err.Error(), nil)
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

	// Reject an unknown status rather than letting it silently match nothing:
	// an empty result set from a typo is indistinguishable from a clean corpus.
	for _, h := range req.PointerHealth {
		if !memory.PointerHealthStatus(h).Valid() {
			writeError(w, http.StatusBadRequest, "validation_error",
				"pointer_health must be one of "+strings.Join(memory.PointerHealthStatusVocabulary(), ", ")+", got "+h, nil)
			return
		}
	}

	in := memory.RecallInput{
		Namespaces:    req.Namespaces,
		RevisionScope: req.RevisionScope,
		Ranking:       req.Ranking,
		SearchMode:    req.SearchMode,
		Query:         req.Query,
		Filters: memory.RecallFilters{
			Origins:       req.Origins,
			Statuses:      req.Statuses,
			Tags:          req.Tags,
			ConfidenceMin: req.ConfidenceMin,
			SimilarityMin: req.SimilarityMin,
			Since:         req.Since,
			Until:         req.Until,
			Domains:       req.Domains,
			FacetKinds:    req.FacetKinds,
			FacetSources:  req.FacetSources,
			PointerHealth: req.PointerHealth,
		},
	}
	page, err := s.MemoryStore.RecallPaged(r.Context(), in, pr)
	if err != nil {
		writeRecallError(w, err, "lookup_failed")
		return
	}
	// Facets come from the unprojected results: the histogram describes what
	// this page returned, not what was serialized.
	facets := buildFacets(page.Kept)
	// Built from the same facets value and the same manifest the full response
	// below carries, so an estimate cannot report different numbers than the
	// read it is estimating.
	if pr.EstimateOnly {
		writeJSON(w, http.StatusOK, estimateResponse{
			Facets:       &facets,
			Manifest:     page.Manifest,
			EstimateOnly: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, tesseractLookupResponse{
		Results:  page.Results,
		Facets:   facets,
		Manifest: page.Manifest,
	})
}

// estimateResponse is the wire shape of an estimate_only read on either
// route: the envelope the route would have returned, minus the rows.
//
// `results` is absent rather than empty for the same reason the MCP peer omits
// it — an empty array reads as "nothing matched", and the whole value of a
// pre-flight is telling those two apart. EstimateOnly is the positive marker.
//
// Facets is a pointer so the field disappears entirely on the recall route,
// which has no facet histogram in its normal response either. An estimate must
// not advertise a number the read it estimates cannot return; that would be the
// opposite of the identity the knob exists to provide.
type estimateResponse struct {
	Facets       *lookupFacets   `json:"facets,omitempty"`
	Manifest     memory.Manifest `json:"manifest"`
	EstimateOnly bool            `json:"estimate_only"`
}

// defaultPayloadMode resolves the server-configured recall projection,
// falling back to memory.DefaultPayloadMode when config is unset or carries
// an unrecognized value.
func (s *Server) defaultPayloadMode() memory.PayloadMode {
	mode := memory.PayloadMode(s.RuntimeConfig.Read.PayloadMode)
	if !mode.Valid() {
		return memory.DefaultPayloadMode
	}
	return mode
}

// pageArgs is the budget/cursor half of a read request body, shared verbatim
// by POST /v1/memory/recall and POST /v1/tesseract/lookup so the two cannot
// drift from each other or from their MCP peers.
//
// BudgetBytes / BudgetTokens are *int rather than int for the reason
// memory.Budget documents: 0 sits inside the type's range but outside its
// meaning, and `omitempty`-style conflation of "unset" with "zero" is the
// defect that has already been fixed three times in this domain
// (RecallResult.Score, synthesisSource.Score, Payload.Confidence). Absent
// means "use the server default"; an explicit 0 or negative is a validation
// error, matching the MCP side exactly.
type pageArgs struct {
	Cursor       string `json:"cursor,omitempty"`
	BudgetBytes  *int   `json:"budget_bytes,omitempty"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`

	// EstimateOnly withholds the result rows and returns only the envelope
	// describing them. Peer of the MCP argument of the same name on both
	// tesseract_recall.
	//
	// A plain bool rather than a pointer, unlike the budgets beside it: false
	// is both the zero value and the meaning of an absent field, so there is no
	// third state for a pointer to preserve. The budgets need one because 0
	// sits inside their range but outside their meaning.
	EstimateOnly bool `json:"estimate_only,omitempty"`
}

// pageRequest resolves the shared knobs against server config and the
// caller's payload mode. It writes its own 400 and returns ok=false on an
// invalid budget.
func (s *Server) pageRequest(w http.ResponseWriter, a pageArgs, mode memory.PayloadMode, limit int) (memory.PageRequest, bool) {
	pr := memory.PageRequest{
		Cursor:       a.Cursor,
		PayloadMode:  mode,
		Limit:        limit,
		EstimateOnly: a.EstimateOnly,
		Budget: memory.Budget{
			Bytes:  s.RuntimeConfig.Read.BudgetBytes,
			Tokens: s.RuntimeConfig.Read.BudgetTokens,
		},
	}
	for _, knob := range []struct {
		name string
		got  *int
		dst  *int
	}{
		{"budget_bytes", a.BudgetBytes, &pr.Budget.Bytes},
		{"budget_tokens", a.BudgetTokens, &pr.Budget.Tokens},
	} {
		if knob.got == nil {
			continue
		}
		if *knob.got <= 0 {
			writeError(w, http.StatusBadRequest, "validation_error",
				knob.name+" must be greater than 0; omit it for no ceiling", nil)
			return memory.PageRequest{}, false
		}
		*knob.dst = *knob.got
	}
	return pr, true
}

// historyPageRequest builds the paging/budget half of a history read from
// query parameters, mirroring the MCP history tools' arguments name for name.
//
// Presence is read with Query().Has rather than by comparing against a zero
// default, so `?budget_bytes=0` is rejected on this surface exactly as an
// explicit 0 is rejected on MCP.
//
// The server-configured budget is deliberately NOT applied here, matching
// resolveHistoryPageRequest on the MCP side. History answers with a bare array
// unless the caller engages a knob, and a bare array has nowhere to report
// truncation — so a deployment-level ceiling could only either flip the shape
// for every caller (breaking frontend/src/api/client.ts and the fenced
// internal/webui/dist bundle) or silently drop revisions. read.budget_bytes
// and read.budget_tokens are a recall/lookup ceiling only.
//
// It writes its own 400 and returns ok=false on a malformed value.
func (s *Server) historyPageRequest(w http.ResponseWriter, r *http.Request) (memory.PageRequest, bool) {
	q := r.URL.Query()
	pr := memory.PageRequest{
		Cursor: q.Get("cursor"),
		// History serializes bare Revisions; full is what the byte
		// accounting must measure.
		PayloadMode: memory.PayloadModeFull,
	}
	// limit and the budgets differ deliberately on what a non-positive value
	// means, and the two surfaces agree on the difference.
	//
	// limit ≤ 0 is "unspecified" and resolves to the default. That is the
	// meaning RecallInput.Limit and ClampHistoryLimit have always had, and
	// the one views_evaluate advertises ("0 = use selector or default"); a
	// budget has no such precedent, and a zero budget can only produce an
	// empty page, so there it is a caller mistake worth naming.
	for _, knob := range []struct {
		name         string
		dst          *int
		rejectNonPos bool
	}{
		{"limit", &pr.Limit, false},
		{"budget_bytes", &pr.Budget.Bytes, true},
		{"budget_tokens", &pr.Budget.Tokens, true},
	} {
		if !q.Has(knob.name) {
			continue
		}
		v, err := strconv.Atoi(q.Get(knob.name))
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error",
				knob.name+" must be a whole number", nil)
			return memory.PageRequest{}, false
		}
		if knob.rejectNonPos && v <= 0 {
			writeError(w, http.StatusBadRequest, "validation_error",
				knob.name+" must be greater than 0; omit it for no ceiling", nil)
			return memory.PageRequest{}, false
		}
		if v < 0 {
			v = 0
		}
		*knob.dst = v
	}
	return pr, true
}

// writeHistoryPage serves one revision-history response on either history
// route. When the caller engaged no paging knob it writes the bare array the
// route has always returned — the shipped web UI parses both
// GET /v1/memory/history and GET /v1/knowledge/history as arrays and its
// bundle is not rebuilt here. Otherwise it writes the {results, manifest}
// envelope.
func writeHistoryPage(w http.ResponseWriter, revs []memory.Revision, pr memory.PageRequest, fingerprint string) {
	if !pr.Engaged() {
		writeJSON(w, http.StatusOK, revs)
		return
	}
	page, err := memory.PageRevisions(revs, pr, fingerprint)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// writeRecallError maps a store error from a paged read onto an HTTP status.
// Shared so that an invalid cursor is a 400 with the same code on both the
// recall and lookup routes, and on their MCP peers.
func writeRecallError(w http.ResponseWriter, err error, failCode string) {
	switch {
	case errors.Is(err, memory.ErrInvalidCursor):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
	// ErrEmbedderUnavailable, not the ErrSimilarityUnavailable alias: the two
	// are the same value, but the alias is deprecated and this is new code.
	// Renaming the alias itself is fenced to CW-20260825-0020.
	case errors.Is(err, memory.ErrEmbedderUnavailable):
		writeError(w, http.StatusServiceUnavailable, "similarity_unavailable", err.Error(), nil)
	case errors.Is(err, memory.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, failCode, err.Error(), nil)
	}
}

func buildFacets(results []memory.RecallResult) lookupFacets {
	out := lookupFacets{
		Domains: map[string]int{},
		Kinds:   map[string]int{},
		Sources: map[string]int{},
	}
	for _, r := range results {
		if d := string(r.Revision.Domain); d != "" {
			out.Domains[d]++
		}
		if k := r.Revision.Facets.Kind; k != "" {
			out.Kinds[k]++
		}
		if s := r.Revision.Facets.Source; s != "" {
			out.Sources[s]++
		}
	}
	return out
}

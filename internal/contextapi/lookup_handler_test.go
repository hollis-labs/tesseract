package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newLookupServer(t *testing.T) *Server {
	t.Helper()
	srv := newMemoryTestServer(t)
	srv.KnowledgeStore = knowledge.New(srv.MemoryStore)
	return srv
}

func seedMemory(t *testing.T, srv *Server) {
	t.Helper()
	_, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "prefs.terse",
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "terse output"},
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}
}

func seedKnowledge(t *testing.T, srv *Server) {
	t.Helper()
	_, err := srv.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: "user/chrispian/knowledge/framework",
		Key:       "framework.go-providers",
		Kind:      "package",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/pkg/go-providers"},
		Summary:   "go-providers multi-provider adapter",
		Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		SessionID: "indexer:01HX",
	})
	if err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}
}

// lookupTestResponse decodes a lookup response for assertions.
//
// It deliberately does NOT reuse tesseractLookupResponse: that struct now
// carries Results as `any`, and round-tripping the server's own type would
// assert nothing about the wire shape anyway. The fields below are the ones
// present under every payload_mode, so these tests read the same paths
// regardless of projection.
type lookupTestResponse struct {
	Results []lookupTestResult `json:"results"`
	Facets  lookupFacets       `json:"facets"`
}

type lookupTestResult struct {
	Revision struct {
		RevisionID string         `json:"revision_id"`
		MemoryID   string         `json:"memory_id"`
		Domain     domains.Domain `json:"domain"`
		Namespace  string         `json:"namespace"`
		MemoryKey  string         `json:"memory_key"`
		Status     string         `json:"status"`
		Confidence float64        `json:"confidence"`
		Payload    *struct {
			Summary string `json:"summary"`
			Body    string `json:"body"`
		} `json:"payload"`
	} `json:"revision"`
	Score       *float64        `json:"score"`
	PayloadMode string          `json:"payload_mode"`
	State       json.RawMessage `json:"state"`
}

func decodeLookup(t *testing.T, raw []byte) lookupTestResponse {
	t.Helper()
	var resp lookupTestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode (raw=%s): %v", raw, err)
	}
	return resp
}

func TestTesseractLookup_UnifiedAcrossDomains(t *testing.T) {
	srv := newLookupServer(t)
	seedMemory(t, srv)
	seedKnowledge(t, srv)

	body := `{
		"namespaces":["user/chrispian/memory/notes","user/chrispian/knowledge/framework"],
		"ranking":"chronological"
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tesseract/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	resp := decodeLookup(t, rr.Body.Bytes())
	if len(resp.Results) != 2 {
		t.Fatalf("want 2 results across domains, got %d", len(resp.Results))
	}
	if resp.Facets.Domains["memory"] != 1 || resp.Facets.Domains["knowledge"] != 1 {
		t.Errorf("facets.Domains = %+v, want memory:1 knowledge:1", resp.Facets.Domains)
	}
	if resp.Facets.Kinds["package"] != 1 {
		t.Errorf("facets.Kinds = %+v, want package:1", resp.Facets.Kinds)
	}
}

func TestTesseractLookup_DomainFilter(t *testing.T) {
	srv := newLookupServer(t)
	seedMemory(t, srv)
	seedKnowledge(t, srv)

	body := `{
		"namespaces":["user/chrispian/memory/notes","user/chrispian/knowledge/framework"],
		"ranking":"chronological",
		"domains":["knowledge"]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tesseract/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	resp := decodeLookup(t, rr.Body.Bytes())
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 knowledge result, got %d", len(resp.Results))
	}
	if resp.Results[0].Revision.Domain != domains.Knowledge {
		t.Errorf("Domain = %q, want knowledge", resp.Results[0].Revision.Domain)
	}
}

func TestTesseractLookup_FacetKindFilter(t *testing.T) {
	srv := newLookupServer(t)
	seedKnowledge(t, srv)

	body := `{
		"namespaces":["user/chrispian/knowledge/framework"],
		"ranking":"chronological",
		"facet_kinds":["package"]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tesseract/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	resp := decodeLookup(t, rr.Body.Bytes())
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result for kind=package, got %d", len(resp.Results))
	}
}

// ── payload_mode (CW-20260825-0003) ──────────────────────────────────────

const lookupBodySentinel = "BODY-SENTINEL rest body that projection must drop"

func seedMemoryWithBody(t *testing.T, srv *Server) {
	t.Helper()
	_, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "prefs.body",
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HY",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "has a body", Body: lookupBodySentinel},
	})
	if err != nil {
		t.Fatalf("seed memory with body: %v", err)
	}
}

func postLookup(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/tesseract/lookup", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// The HTTP peer honors the same payload_mode vocabulary as the MCP tool.
// The body sentinel is what proves projection ran: a no-op projection that
// merely renamed fields would still leak it.
func TestTesseractLookup_PayloadModeProjection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		wantBody bool
	}{
		{"keys", `,"payload_mode":"keys"`, false},
		{"summary", `,"payload_mode":"summary"`, false},
		{"full", `,"payload_mode":"full"`, true},
		{"server default", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLookupServer(t)
			seedMemoryWithBody(t, srv)

			rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological"`+tc.mode+`}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
			}
			if got := strings.Contains(rr.Body.String(), lookupBodySentinel); got != tc.wantBody {
				t.Errorf("body sentinel present = %v, want %v; raw=%s", got, tc.wantBody, rr.Body.String())
			}

			resp := decodeLookup(t, rr.Body.Bytes())
			if len(resp.Results) != 1 {
				t.Fatalf("want 1 result, got %d", len(resp.Results))
			}
			// revision_id must survive every mode so the caller can hydrate.
			if resp.Results[0].Revision.RevisionID == "" {
				t.Error("revision_id absent; hydration by ID is impossible")
			}
		})
	}
}

// An HTTP caller that pins full gets the body even when the server default
// is a projected mode — this is the escape hatch the web UI's editor uses.
func TestTesseractLookup_PayloadModeFullOverridesServerDefault(t *testing.T) {
	srv := newLookupServer(t)
	srv.RuntimeConfig = config.Defaults() // read.payload_mode = summary
	seedMemoryWithBody(t, srv)

	rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"full"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), lookupBodySentinel) {
		t.Errorf("payload_mode=full did not override the server default; raw=%s", rr.Body.String())
	}
}

// A projected HTTP response is self-describing: payload_mode rides on each
// result so a caller can tell a withheld body from an empty one.
func TestTesseractLookup_ProjectedResultsCarryMarker(t *testing.T) {
	srv := newLookupServer(t)
	seedMemoryWithBody(t, srv)

	rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`)
	resp := decodeLookup(t, rr.Body.Bytes())
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].PayloadMode != "summary" {
		t.Errorf("payload_mode marker = %q, want summary", resp.Results[0].PayloadMode)
	}

	rrFull := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"full"}`)
	full := decodeLookup(t, rrFull.Body.Bytes())
	if full.Results[0].PayloadMode != "" {
		t.Errorf("full-mode result carries a payload_mode marker (%q); full must stay byte-identical to its pre-projection shape",
			full.Results[0].PayloadMode)
	}
}

func TestTesseractLookup_InvalidPayloadModeIsValidationError(t *testing.T) {
	srv := newLookupServer(t)
	seedMemory(t, srv)

	rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"payload_mode":"brief"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "keys|summary|full") {
		t.Errorf("error does not state the accepted vocabulary; body = %s", rr.Body.String())
	}
}

// ── POST /v1/memory/recall is memory_recall's declared HTTP peer ─────────
//
// parity_test.go pairs them with no waiver, so they must agree on the
// payload_mode vocabulary AND on the default. The parity harness only
// asserts route existence, so argument parity needs its own guard: without
// these, an HTTP caller asking for keys silently receives full bodies.

func postMemoryRecall(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/recall", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func TestMemoryRecallHTTP_PayloadModeProjection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		wantBody bool
	}{
		{"keys", `,"payload_mode":"keys"`, false},
		{"summary", `,"payload_mode":"summary"`, false},
		{"full", `,"payload_mode":"full"`, true},
		{"server default", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLookupServer(t)
			seedMemoryWithBody(t, srv)

			rr := postMemoryRecall(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological"`+tc.mode+`}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
			}
			if got := strings.Contains(rr.Body.String(), lookupBodySentinel); got != tc.wantBody {
				t.Errorf("body sentinel present = %v, want %v; raw=%s", got, tc.wantBody, rr.Body.String())
			}

			var results []lookupTestResult
			if err := json.Unmarshal(httpRecallResultsJSON(t, rr.Body.Bytes()), &results); err != nil {
				t.Fatalf("decode (raw=%s): %v", rr.Body.String(), err)
			}
			if len(results) != 1 {
				t.Fatalf("want 1 result, got %d", len(results))
			}
			if results[0].Revision.RevisionID == "" {
				t.Error("revision_id absent; hydration by ID is impossible")
			}
			wantMarker := ""
			if !tc.wantBody {
				wantMarker = "summary"
				if tc.mode == `,"payload_mode":"keys"` {
					wantMarker = "keys"
				}
			}
			if results[0].PayloadMode != wantMarker {
				t.Errorf("payload_mode marker = %q, want %q", results[0].PayloadMode, wantMarker)
			}
		})
	}
}

// The HTTP peer must reject an unknown mode rather than decode it away.
// encoding/json is non-strict by default, so an unvalidated field silently
// becomes "" and the caller gets a projection it did not ask for.
func TestMemoryRecallHTTP_InvalidPayloadModeIsValidationError(t *testing.T) {
	srv := newLookupServer(t)
	seedMemory(t, srv)

	rr := postMemoryRecall(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"payload_mode":"bogus"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "keys|summary|full") {
		t.Errorf("error does not state the accepted vocabulary; body = %s", rr.Body.String())
	}
}

// The declared peer pair must share a default, not just a vocabulary. A
// server whose config says "keys" must project HTTP recall to keys too.
func TestMemoryRecallHTTP_HonorsConfiguredDefault(t *testing.T) {
	srv := newLookupServer(t)
	srv.RuntimeConfig = config.Defaults()
	srv.RuntimeConfig.Read.PayloadMode = "keys"
	seedMemoryWithBody(t, srv)

	rr := postMemoryRecall(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological"}`)
	var results []lookupTestResult
	if err := json.Unmarshal(httpRecallResultsJSON(t, rr.Body.Bytes()), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if results[0].PayloadMode != "keys" {
		t.Errorf("payload_mode marker = %q, want keys (config default not honored)", results[0].PayloadMode)
	}
	if results[0].Revision.Payload != nil {
		t.Errorf("keys mode carried a payload object: %+v", results[0].Revision.Payload)
	}
}

// Facets count the RETURNED rows, not the full match set — recall truncates
// to limit before the handler sees the results. This pins the documented
// behavior so the tool description cannot drift back to claiming otherwise.
func TestTesseractLookup_FacetsCountReturnedRowsOnly(t *testing.T) {
	srv := newLookupServer(t)
	for i := 0; i < 5; i++ {
		_, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
			Namespace:  "user/chrispian/memory/notes",
			MemoryKey:  "facet.probe." + string(rune('a'+i)),
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "manual:01HZ",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload:    memory.Payload{Summary: "facet probe"},
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","limit":2}`)
	resp := decodeLookup(t, rr.Body.Bytes())
	if len(resp.Results) != 2 {
		t.Fatalf("want 2 results, got %d", len(resp.Results))
	}
	if resp.Facets.Domains["memory"] != 2 {
		t.Errorf("facets.domains.memory = %d, want 2 (facets must count returned rows, not the 5 that matched)",
			resp.Facets.Domains["memory"])
	}
}

// Facets describe what was returned, and projection must not change them.
func TestTesseractLookup_FacetsUnaffectedByProjection(t *testing.T) {
	var counts []map[string]int
	for _, mode := range []string{"keys", "summary", "full"} {
		srv := newLookupServer(t)
		seedMemory(t, srv)
		seedKnowledge(t, srv)

		rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes","user/chrispian/knowledge/framework"],"ranking":"chronological","payload_mode":"`+mode+`"}`)
		resp := decodeLookup(t, rr.Body.Bytes())
		counts = append(counts, resp.Facets.Domains)
	}
	for i := 1; i < len(counts); i++ {
		if counts[i]["memory"] != counts[0]["memory"] || counts[i]["knowledge"] != counts[0]["knowledge"] {
			t.Errorf("facets differ across payload_mode: %v vs %v", counts[0], counts[i])
		}
	}
}

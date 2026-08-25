package contextapi

// HTTP-surface tests for CW-20260825-0004's budget/cursor knobs.
//
// tests/parity/parity_test.go asserts that a declared pair's ROUTE exists. It
// says nothing about arguments, and CW-20260825-0003 shipped payload_mode on
// MCP memory_recall while POST /v1/memory/recall silently dropped it — a
// non-strict json.Decoder discarded the field with no error, so the same call
// got a different projection depending on which door it came in. Nothing
// structural catches that.
//
// The both-doors guard for this ticket's knobs is
// TestBudgetCursorParity_MCPvsHTTP in internal/mcpadapter: it lives there
// because it must call the unexported MCP handlers, and contextapi.Server's
// fields are all exported so it can build the HTTP half from outside.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// httpRecallResultsJSON extracts the results array from a recall envelope.
// POST /v1/memory/recall returns {results, manifest} as of this ticket.
func httpRecallResultsJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var env struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("recall response is not an envelope (raw=%s): %v", raw, err)
	}
	if len(env.Results) == 0 {
		t.Fatalf("recall response carries no results key; raw=%s", raw)
	}
	return env.Results
}

func httpManifest(t *testing.T, raw []byte) memory.Manifest {
	t.Helper()
	var env struct {
		Manifest *memory.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode manifest (raw=%s): %v", raw, err)
	}
	if env.Manifest == nil {
		t.Fatalf("response carries no manifest; raw=%s", raw)
	}
	return *env.Manifest
}

// seedBudgetRows writes n unkeyed memory revisions, each with a body, so a
// byte budget has something to bind against.
func seedBudgetRows(t *testing.T, srv *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
			Namespace:  "user/chrispian/memory/notes",
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "sess-budget",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload: memory.Payload{
				Summary: "budget probe row",
				Body:    strings.Repeat("x", 200),
			},
		}); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
}

// ── The envelope on HTTP ─────────────────────────────────────────────────────

func TestMemoryRecallHTTP_ManifestCarriesZeroValues(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 2)

	rr := postMemoryRecall(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{
		`"truncated":false`, `"truncation_reason":""`, `"next_cursor":null`,
		`"results_total":2`, `"results_returned":2`,
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("manifest does not carry %s; body = %s", want, rr.Body.String())
		}
	}
}

func TestTesseractLookupHTTP_CarriesManifestAlongsideFacets(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 3)

	rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
	}
	m := httpManifest(t, rr.Body.Bytes())
	if m.ResultsTotal != 3 {
		t.Errorf("results_total = %d, want 3", m.ResultsTotal)
	}
	if !strings.Contains(rr.Body.String(), `"facets"`) {
		t.Errorf("facets did not survive the envelope; body = %s", rr.Body.String())
	}
}

// ── Budget on HTTP ───────────────────────────────────────────────────────────

func TestMemoryRecallHTTP_BudgetBytesTruncates(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 8)

	base := httpManifest(t, postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`).Body.Bytes())

	rr := postMemoryRecall(t, srv, `{"namespaces":["user/chrispian/memory/notes"],`+
		`"ranking":"chronological","payload_mode":"summary","budget_bytes":`+
		itoa(base.BytesReturned/3)+`}`)
	m := httpManifest(t, rr.Body.Bytes())
	if !m.Truncated {
		t.Fatalf("budget did not truncate: %+v", m)
	}
	if m.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("truncation_reason = %q, want %q", m.TruncationReason, memory.TruncationBudgetBytes)
	}
	if m.NextCursor == nil {
		t.Error("truncated response carries no next_cursor")
	}
}

// A budget field the decoder drops is the CW-20260825-0003 failure repeating.
// This asserts the field is actually read, not just accepted.
func TestMemoryRecallHTTP_ZeroBudgetIsValidationError(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 2)

	for _, knob := range []string{"budget_bytes", "budget_tokens"} {
		for _, v := range []string{"0", "-1"} {
			t.Run(knob+"="+v, func(t *testing.T) {
				rr := postMemoryRecall(t, srv,
					`{"namespaces":["user/chrispian/memory/notes"],"`+knob+`":`+v+`}`)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
				}
				if !strings.Contains(rr.Body.String(), knob) {
					t.Errorf("error does not name the offending knob; body = %s", rr.Body.String())
				}
			})
		}
	}
}

func TestTesseractLookupHTTP_ZeroBudgetIsValidationError(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 2)

	rr := postLookup(t, srv, `{"namespaces":["user/chrispian/memory/notes"],"budget_tokens":0}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

// The declared peer pair must share a default, not just a vocabulary — and
// both must read it from the same config field.
func TestMemoryRecallHTTP_BudgetDefaultComesFromConfig(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 8)

	unbounded := httpManifest(t, postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`).Body.Bytes())
	if unbounded.Truncated {
		t.Fatalf("default config must mean no ceiling: %+v", unbounded)
	}

	srv.RuntimeConfig = config.Defaults()
	srv.RuntimeConfig.Read.BudgetBytes = unbounded.BytesReturned / 3
	bounded := httpManifest(t, postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`).Body.Bytes())
	if !bounded.Truncated || bounded.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("read.budget_bytes not honored on HTTP: %+v", bounded)
	}
}

// config.Defaults() must ship no ceiling. A non-zero default would start
// truncating every already-deployed caller on the next release.
func TestConfigDefaultBudgetIsUnbounded(t *testing.T) {
	d := config.Defaults()
	if d.Read.BudgetBytes != 0 || d.Read.BudgetTokens != 0 {
		t.Errorf("Defaults().Read budget = (%d, %d), want (0, 0)",
			d.Read.BudgetBytes, d.Read.BudgetTokens)
	}
}

// ── Cursor on HTTP ───────────────────────────────────────────────────────────

func TestMemoryRecallHTTP_CursorPagesAndRejectsAChangedSort(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 6)

	first := httpManifest(t, postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","limit":2}`).Body.Bytes())
	if first.NextCursor == nil {
		t.Fatalf("no cursor issued: %+v", first)
	}
	cursor, err := json.Marshal(*first.NextCursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}

	same := postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","limit":2,"cursor":`+string(cursor)+`}`)
	if same.Code != http.StatusOK {
		t.Fatalf("resuming the same sort = %d; body = %s", same.Code, same.Body.String())
	}
	if m := httpManifest(t, same.Body.Bytes()); m.ResultsReturned != 2 {
		t.Errorf("resume returned %d results, want 2", m.ResultsReturned)
	}

	changed := postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"activation","limit":2,"cursor":`+string(cursor)+`}`)
	if changed.Code != http.StatusBadRequest {
		t.Fatalf("resuming after a changed sort = %d, want 400; body = %s",
			changed.Code, changed.Body.String())
	}
	if !strings.Contains(changed.Body.String(), "different query") {
		t.Errorf("error does not explain the mismatch; body = %s", changed.Body.String())
	}
}

func TestMemoryRecallHTTP_MalformedCursorIs400(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 2)

	rr := postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"cursor":"!!!not-a-cursor!!!"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

// ── History on HTTP ──────────────────────────────────────────────────────────

func seedHistory(t *testing.T, srv *Server, n int) {
	t.Helper()
	var last string
	for i := 0; i < n; i++ {
		rev, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
			Namespace:  "user/chrispian/memory/notes",
			MemoryKey:  "hist.key",
			Supersedes: last,
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "sess-hist",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload:    memory.Payload{Summary: "history probe"},
		})
		if err != nil {
			t.Fatalf("seed history %d: %v", i, err)
		}
		last = rev.RevisionID
	}
}

func getHistory(t *testing.T, srv *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/memory/history?"+query, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// The shipped web UI decodes GET /v1/memory/history as MemoryRevision[] and
// its bundle (internal/webui/dist) is not regenerated by this ticket, so the
// unmodified request must keep returning a bare array. This test is the
// contract that keeps the UI working.
func TestMemoryHistoryHTTP_BareArrayUntilAKnobIsPassed(t *testing.T) {
	srv := newLookupServer(t)
	seedHistory(t, srv, 4)

	bare := getHistory(t, srv, "namespace=user/chrispian/memory/notes&memory_key=hist.key")
	if bare.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", bare.Code, bare.Body.String())
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(bare.Body.Bytes(), &arr); err != nil {
		t.Fatalf("default history must stay a bare array: %v (body=%s)", err, bare.Body.String())
	}
	if len(arr) != 4 {
		t.Errorf("bare history returned %d revisions, want 4", len(arr))
	}

	paged := getHistory(t, srv, "namespace=user/chrispian/memory/notes&memory_key=hist.key&limit=2")
	m := httpManifest(t, paged.Body.Bytes())
	if m.ResultsTotal != 4 || m.ResultsReturned != 2 {
		t.Errorf("paged history manifest = %+v, want total 4 returned 2", m)
	}
	if m.NextCursor == nil {
		t.Fatal("paged history issued no cursor")
	}

	// And it pages.
	rest := getHistory(t, srv,
		"namespace=user/chrispian/memory/notes&memory_key=hist.key&limit=2&cursor="+*m.NextCursor)
	restM := httpManifest(t, rest.Body.Bytes())
	if restM.ResultsReturned != 2 || restM.NextCursor != nil {
		t.Errorf("second page manifest = %+v, want 2 results and no further cursor", restM)
	}
}

func TestKnowledgeHistoryHTTP_BareArrayUntilAKnobIsPassed(t *testing.T) {
	srv := newLookupServer(t)
	var last string
	for i := 0; i < 3; i++ {
		rev, err := srv.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
			Namespace:  "user/chrispian/knowledge/framework",
			Key:        "k.hist",
			Kind:       "doc",
			Source:     "filesystem",
			Pointer:    memory.Pointer{Scheme: "file", Locator: "/tmp/x.md"},
			Summary:    "knowledge history probe",
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			SessionID:  "sess-hist",
			Confidence: 0.9,
			Supersedes: last,
		})
		if err != nil {
			t.Fatalf("seed knowledge %d: %v", i, err)
		}
		last = rev.RevisionID
	}

	do := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/knowledge/history?"+query, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr
	}

	bare := do("namespace=user/chrispian/knowledge/framework&memory_key=k.hist")
	var arr []json.RawMessage
	if err := json.Unmarshal(bare.Body.Bytes(), &arr); err != nil {
		t.Fatalf("default knowledge history must stay a bare array: %v (body=%s)",
			err, bare.Body.String())
	}
	if len(arr) != 3 {
		t.Errorf("bare knowledge history returned %d revisions, want 3", len(arr))
	}

	paged := do("namespace=user/chrispian/knowledge/framework&memory_key=k.hist&limit=1")
	if m := httpManifest(t, paged.Body.Bytes()); m.ResultsTotal != 3 || m.ResultsReturned != 1 {
		t.Errorf("paged knowledge history manifest = %+v, want total 3 returned 1", m)
	}
}

// A configured budget must leave history's response SHAPE untouched.
//
// read.budget_bytes is a deployment-level setting this ticket introduced. When
// historyPageRequest seeded PageRequest.Budget from it, Budget.Set() made
// PageRequest.Engaged() true for every caller — including ones passing no
// knobs — flipping both history routes from a bare array to {results,
// manifest}. That is a pure shape break with no rows withheld, and it breaks
// frontend/src/api/client.ts:544 and :564 plus the fenced internal/webui/dist
// bundle that consumes them.
//
// The budget below is 10 MB against a 4-revision history, chosen so it can
// withhold nothing: any change in the response is the config's shape effect
// and nothing else.
func TestMemoryHistoryHTTP_ConfiguredBudgetDoesNotChangeShape(t *testing.T) {
	srv := newLookupServer(t)
	seedHistory(t, srv, 4)

	var arr []json.RawMessage
	base := getHistory(t, srv, "namespace=user/chrispian/memory/notes&memory_key=hist.key")
	if err := json.Unmarshal(base.Body.Bytes(), &arr); err != nil {
		t.Fatalf("baseline is not a bare array: %v", err)
	}
	baseline := base.Body.String()

	srv.RuntimeConfig = config.Defaults()
	srv.RuntimeConfig.Read.BudgetBytes = 10 << 20
	srv.RuntimeConfig.Read.BudgetTokens = 10 << 20

	withBudget := getHistory(t, srv, "namespace=user/chrispian/memory/notes&memory_key=hist.key")
	if withBudget.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", withBudget.Code, withBudget.Body.String())
	}
	if err := json.Unmarshal(withBudget.Body.Bytes(), &arr); err != nil {
		t.Fatalf("a configured budget flipped history's shape: %v\nbody=%s",
			err, withBudget.Body.String())
	}
	if withBudget.Body.String() != baseline {
		t.Errorf("a configured budget changed the response byte-for-byte:\n before: %s\n  after: %s",
			baseline, withBudget.Body.String())
	}

	// Knowledge history is the same route family and the same UI consumer.
	seedKnowledge(t, srv)
	kreq := httptest.NewRequest(http.MethodGet,
		"/v1/knowledge/history?namespace=user/chrispian/knowledge/framework&memory_key=go-providers", nil)
	krr := httptest.NewRecorder()
	srv.ServeHTTP(krr, kreq)
	if krr.Code == http.StatusOK {
		if err := json.Unmarshal(krr.Body.Bytes(), &arr); err != nil {
			t.Errorf("a configured budget flipped knowledge history's shape: %v\nbody=%s",
				err, krr.Body.String())
		}
	}

	// A PER-CALL budget still engages the envelope — the knob works, it is
	// only the deployment-level default that must not change shape.
	perCall := getHistory(t, srv,
		"namespace=user/chrispian/memory/notes&memory_key=hist.key&budget_bytes=400")
	if _, ok := tryHTTPManifest(perCall.Body.Bytes()); !ok {
		t.Errorf("a per-call budget did not engage the envelope; body = %s", perCall.Body.String())
	}
}

// The recall/lookup side must keep honoring the configured budget — the fix
// above narrowed it to those two surfaces, it did not disable it.
func TestConfiguredBudgetStillAppliesToRecallAndLookup(t *testing.T) {
	srv := newLookupServer(t)
	seedBudgetRows(t, srv, 8)

	base := httpManifest(t, postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`).Body.Bytes())

	srv.RuntimeConfig = config.Defaults()
	srv.RuntimeConfig.Read.BudgetBytes = base.BytesReturned / 3

	recall := httpManifest(t, postMemoryRecall(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`).Body.Bytes())
	if !recall.Truncated || recall.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("recall stopped honoring read.budget_bytes: %+v", recall)
	}

	lookup := httpManifest(t, postLookup(t, srv,
		`{"namespaces":["user/chrispian/memory/notes"],"ranking":"chronological","payload_mode":"summary"}`).Body.Bytes())
	if !lookup.Truncated || lookup.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("lookup stopped honoring read.budget_bytes: %+v", lookup)
	}
}

// tryHTTPManifest reports whether a response is an envelope at all.
func tryHTTPManifest(raw []byte) (memory.Manifest, bool) {
	var env struct {
		Manifest *memory.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Manifest == nil {
		return memory.Manifest{}, false
	}
	return *env.Manifest, true
}

func TestMemoryHistoryHTTP_MalformedKnobsAre400(t *testing.T) {
	srv := newLookupServer(t)
	seedHistory(t, srv, 2)

	// limit=0 is NOT here: ≤ 0 means "unspecified" for limit, matching
	// RecallInput.Limit and ClampHistoryLimit, and matching the MCP peer.
	// TestHistoryBudgetCursorParity_MCPvsHTTP pins that both doors agree.
	for _, q := range []string{
		"limit=abc", "budget_bytes=0", "budget_bytes=-3", "budget_tokens=nope",
	} {
		t.Run(q, func(t *testing.T) {
			rr := getHistory(t, srv,
				"namespace=user/chrispian/memory/notes&memory_key=hist.key&"+q)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400; body = %s", q, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestMemoryHistoryHTTP_CursorRejectsADifferentSeries(t *testing.T) {
	srv := newLookupServer(t)
	seedHistory(t, srv, 4)
	seedBudgetRows(t, srv, 1)

	paged := getHistory(t, srv, "namespace=user/chrispian/memory/notes&memory_key=hist.key&limit=2")
	m := httpManifest(t, paged.Body.Bytes())
	if m.NextCursor == nil {
		t.Fatal("no cursor issued")
	}

	// Same cursor, different key: the series it names no longer matches.
	rr := getHistory(t, srv,
		"namespace=user/chrispian/memory/notes&memory_key=prefs.terse&cursor="+*m.NextCursor)
	if rr.Code == http.StatusOK {
		t.Errorf("a cursor from another series was accepted; body = %s", rr.Body.String())
	}
}

// itoa avoids pulling strconv into the string-building above for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

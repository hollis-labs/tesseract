package mcpadapter

// MCP <-> HTTP argument parity for CW-20260825-0004's budget/cursor knobs.
//
// tests/parity/parity_test.go pairs memory_recall with POST /v1/memory/recall
// and carries no waiver, but it only asserts the ROUTE exists. Argument parity
// has no structural guard, and its absence has already shipped a defect:
// CW-20260825-0003 put payload_mode on the MCP tool while the HTTP peer's
// non-strict json.Decoder discarded the field with no error, so the same call
// returned a different projection depending on which door it came in, and an
// invalid value was a validation error on one surface and a 200 on the other.
//
// This file drives BOTH surfaces over the SAME stores, so any difference it
// reports is the surface's and not the data's. It lives in this package
// because it must call the unexported MCP handlers; contextapi.Server's fields
// are exported, so the HTTP half can be built from here.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

// bothSurfaces wires one contextstore to an MCP adapter and an HTTP server so
// the two can be compared directly.
func bothSurfaces(t *testing.T, rows int) (*Adapter, *contextapi.Server) {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	tok, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "parity",
		Scopes: []string{"memory:read", "memory:write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	a := New(cs, tok)
	a.MemoryStore = ms
	a.KnowledgeStore = ks

	srv := contextapi.NewServer(cs, contextpolicy.New())
	srv.MemoryStore = ms
	srv.KnowledgeStore = ks

	for i := 0; i < rows; i++ {
		if _, err := ms.WriteRevision(context.Background(), memory.WriteInput{
			Namespace:  "user/chrispian/memory/notes",
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "sess-parity",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusCanonical,
			Payload: memory.Payload{
				Summary: "parity probe row",
				Body:    strings.Repeat("x", 200),
			},
		}); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	return a, srv
}

func TestBudgetCursorParity_MCPvsHTTP(t *testing.T) {
	a, srv := bothSurfaces(t, 8)
	const ns = `["user/chrispian/memory/notes"]`

	// callMCP returns the raw tool text plus its manifest. On an error result
	// the manifest is zero and the raw text carries the error code.
	callMCP := func(t *testing.T, args map[string]any) (string, memory.Manifest) {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		res, err := a.handleMemoryRecall(context.Background(), req)
		if err != nil {
			t.Fatalf("MCP recall: %v", err)
		}
		raw := res.Content[0].(mcp.TextContent).Text
		m, _ := tryManifest([]byte(raw))
		return raw, m
	}
	callHTTP := func(t *testing.T, body string) (int, string, memory.Manifest) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/memory/recall", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			return rr.Code, rr.Body.String(), memory.Manifest{}
		}
		return rr.Code, rr.Body.String(), decodeManifest(t, rr.Body.Bytes())
	}

	// 1. Unbounded: the two doors must produce identical manifests.
	_, mcpBase := callMCP(t, map[string]any{
		"namespaces": ns, "ranking": "chronological", "payload_mode": "summary",
	})
	_, _, httpBase := callHTTP(t,
		`{"namespaces":`+ns+`,"ranking":"chronological","payload_mode":"summary"}`)
	sameManifest(t, "unbounded", mcpBase, httpBase)
	if mcpBase.ResultsTotal != 8 {
		t.Fatalf("seed did not land: results_total = %d", mcpBase.ResultsTotal)
	}

	// 2. budget_bytes must bind identically.
	budget := mcpBase.BytesReturned / 3
	_, mcpBudget := callMCP(t, map[string]any{
		"namespaces": ns, "ranking": "chronological", "payload_mode": "summary",
		"budget_bytes": float64(budget),
	})
	_, _, httpBudget := callHTTP(t, `{"namespaces":`+ns+
		`,"ranking":"chronological","payload_mode":"summary","budget_bytes":`+itoa(budget)+`}`)
	sameManifest(t, "budget_bytes", mcpBudget, httpBudget)
	// Without this the comparison above would pass on two identical
	// unbounded responses and prove nothing.
	if !mcpBudget.Truncated || mcpBudget.ResultsReturned >= mcpBase.ResultsReturned {
		t.Errorf("budget_bytes=%d did not bind — the parity comparison is vacuous: %+v",
			budget, mcpBudget)
	}

	// 3. budget_tokens.
	tokens := mcpBase.TokensEstimate / 3
	_, mcpTok := callMCP(t, map[string]any{
		"namespaces": ns, "ranking": "chronological", "payload_mode": "summary",
		"budget_tokens": float64(tokens),
	})
	_, _, httpTok := callHTTP(t, `{"namespaces":`+ns+
		`,"ranking":"chronological","payload_mode":"summary","budget_tokens":`+itoa(tokens)+`}`)
	sameManifest(t, "budget_tokens", mcpTok, httpTok)
	if !mcpTok.Truncated {
		t.Errorf("budget_tokens=%d did not bind — comparison is vacuous: %+v", tokens, mcpTok)
	}

	// 4. limit.
	_, mcpLim := callMCP(t, map[string]any{
		"namespaces": ns, "ranking": "chronological", "payload_mode": "summary",
		"limit": float64(3),
	})
	_, _, httpLim := callHTTP(t, `{"namespaces":`+ns+
		`,"ranking":"chronological","payload_mode":"summary","limit":3}`)
	sameManifest(t, "limit", mcpLim, httpLim)
	if mcpLim.NextCursor == nil || httpLim.NextCursor == nil {
		t.Fatalf("limit=3 of 8 issued no cursor on one surface: MCP=%+v HTTP=%+v", mcpLim, httpLim)
	}

	// 5. A cursor issued by one surface must resume on the other. This is the
	// strongest available statement that both doors compute the same ordering
	// fingerprint: if they disagreed at all, a cross-surface resume would be
	// rejected as a changed query.
	crossed, mcpResume := callMCP(t, map[string]any{
		"namespaces": ns, "ranking": "chronological", "payload_mode": "summary",
		"limit": float64(3), "cursor": *httpLim.NextCursor,
	})
	if isToolError(crossed) {
		t.Fatalf("an HTTP-issued cursor was rejected by MCP: %s", crossed)
	}
	cursorJSON, err := json.Marshal(*mcpLim.NextCursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}
	code, body, httpResume := callHTTP(t, `{"namespaces":`+ns+
		`,"ranking":"chronological","payload_mode":"summary","limit":3,"cursor":`+string(cursorJSON)+`}`)
	if code != http.StatusOK {
		t.Fatalf("an MCP-issued cursor was rejected by HTTP (%d): %s", code, body)
	}
	sameManifest(t, "cross-surface resume", mcpResume, httpResume)
	if mcpResume.ResultsReturned != 3 {
		t.Errorf("resume returned %d results, want 3", mcpResume.ResultsReturned)
	}

	// 6. Invalid values must be errors on BOTH, not a 200 on one. This is the
	// exact shape of the CW-20260825-0003 defect.
	for _, tc := range []struct {
		name string
		args map[string]any
		body string
	}{
		{"budget_bytes=0",
			map[string]any{"namespaces": ns, "budget_bytes": float64(0)},
			`{"namespaces":` + ns + `,"budget_bytes":0}`},
		{"budget_bytes negative",
			map[string]any{"namespaces": ns, "budget_bytes": float64(-1)},
			`{"namespaces":` + ns + `,"budget_bytes":-1}`},
		{"budget_tokens=0",
			map[string]any{"namespaces": ns, "budget_tokens": float64(0)},
			`{"namespaces":` + ns + `,"budget_tokens":0}`},
		{"malformed cursor",
			map[string]any{"namespaces": ns, "cursor": "!!!"},
			`{"namespaces":` + ns + `,"cursor":"!!!"}`},
		{"cursor across a changed sort", nil, ""}, // filled in below
	} {
		if tc.args == nil {
			// A cursor from the chronological ordering, replayed under
			// activation: wrong on both surfaces or wrong on neither.
			tc.args = map[string]any{
				"namespaces": ns, "ranking": "activation", "limit": float64(3),
				"cursor": *mcpLim.NextCursor,
			}
			tc.body = `{"namespaces":` + ns + `,"ranking":"activation","limit":3,"cursor":` +
				string(cursorJSON) + `}`
		}
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := callMCP(t, tc.args)
			if !strings.Contains(raw, "validation_error") {
				t.Errorf("MCP accepted %s; raw=%s", tc.name, raw)
			}
			code, body, _ := callHTTP(t, tc.body)
			if code != http.StatusBadRequest {
				t.Errorf("HTTP accepted %s: status=%d body=%s", tc.name, code, body)
			}
		})
	}
}

// The history routes are declared peers too, and their knobs arrive as query
// parameters rather than a JSON body — a second decoding path, and so a second
// chance to diverge.
func TestHistoryBudgetCursorParity_MCPvsHTTP(t *testing.T) {
	a, srv := bothSurfaces(t, 0)

	var last string
	for i := 0; i < 5; i++ {
		rev, err := a.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
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

	mcpHistory := func(t *testing.T, args map[string]any) (string, memory.Manifest) {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		res, err := a.handleMemoryHistory(context.Background(), req)
		if err != nil {
			t.Fatalf("MCP history: %v", err)
		}
		raw := res.Content[0].(mcp.TextContent).Text
		m, _ := tryManifest([]byte(raw))
		return raw, m
	}
	// Non-fatal on a missing manifest: the history routes legitimately answer
	// with a bare array when no paging knob was engaged.
	httpHistory := func(t *testing.T, query string) (int, string, memory.Manifest) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/memory/history?"+query, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		m, _ := tryManifest(rr.Body.Bytes())
		return rr.Code, rr.Body.String(), m
	}

	const q = "namespace=user/chrispian/memory/notes&memory_key=hist.key"
	base := map[string]any{
		"namespace":  "user/chrispian/memory/notes",
		"memory_key": "hist.key",
	}

	// Both surfaces must page the same way.
	args := map[string]any{}
	for k, v := range base {
		args[k] = v
	}
	args["limit"] = float64(2)
	_, mcpM := mcpHistory(t, args)
	_, _, httpM := httpHistory(t, q+"&limit=2")
	sameManifest(t, "history limit", mcpM, httpM)
	if mcpM.NextCursor == nil {
		t.Fatalf("history limit=2 of 5 issued no cursor: %+v", mcpM)
	}

	// And a cursor from one door resumes on the other.
	args["cursor"] = *httpM.NextCursor
	raw, mcpResume := mcpHistory(t, args)
	if isToolError(raw) {
		t.Fatalf("an HTTP-issued history cursor was rejected by MCP: %s", raw)
	}
	code, body, httpResume := httpHistory(t, q+"&limit=2&cursor="+*mcpM.NextCursor)
	if code != http.StatusOK {
		t.Fatalf("an MCP-issued history cursor was rejected by HTTP (%d): %s", code, body)
	}
	sameManifest(t, "history resume", mcpResume, httpResume)

	// limit and the budgets differ on what a non-positive value means, and
	// both surfaces must agree on the difference.
	//
	// limit ≤ 0 is "unspecified" — the meaning RecallInput.Limit and
	// ClampHistoryLimit have always had — so it engages nothing and both
	// doors return the historical bare array. A budget of 0 has no such
	// precedent and can only produce an empty page, so it is a named error.
	// This asymmetry is deliberate; the assertions below pin both halves so
	// neither surface can drift to the other's rule.
	t.Run("limit=0 is unspecified on both", func(t *testing.T) {
		call := map[string]any{}
		for k, v := range base {
			call[k] = v
		}
		call["limit"] = float64(0)
		raw, _ := mcpHistory(t, call)
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			t.Errorf("MCP history with limit=0 did not return a bare array: %v (raw=%s)", err, raw)
		}
		code, body, _ := httpHistory(t, q+"&limit=0")
		if code != http.StatusOK {
			t.Fatalf("HTTP history with limit=0 = %d, want 200: %s", code, body)
		}
		if err := json.Unmarshal([]byte(body), &arr); err != nil {
			t.Errorf("HTTP history with limit=0 did not return a bare array: %v (body=%s)", err, body)
		}
	})

	// Invalid values are errors on both.
	for _, tc := range []struct {
		name  string
		args  map[string]any
		query string
	}{
		{"budget_bytes=0", map[string]any{"budget_bytes": float64(0)}, q + "&budget_bytes=0"},
		{"budget_tokens negative", map[string]any{"budget_tokens": float64(-2)}, q + "&budget_tokens=-2"},
		{"malformed cursor", map[string]any{"cursor": "!!!"}, q + "&cursor=!!!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := map[string]any{}
			for k, v := range base {
				call[k] = v
			}
			for k, v := range tc.args {
				call[k] = v
			}
			raw, _ := mcpHistory(t, call)
			if !strings.Contains(raw, "validation_error") {
				t.Errorf("MCP history accepted %s; raw=%s", tc.name, raw)
			}
			code, body, _ := httpHistory(t, tc.query)
			if code != http.StatusBadRequest {
				t.Errorf("HTTP history accepted %s: status=%d body=%s", tc.name, code, body)
			}
		})
	}
}

// isToolError reports whether an MCP tool result is toolError's shape.
//
// toolError emits {"code": ..., "message": ...} — NOT {"error": ...}. Matching
// on the wrong key here would make every "was it rejected?" check silently
// pass, so it is worth naming once rather than inlining a substring.
func isToolError(raw string) bool {
	var probe struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return false
	}
	return probe.Code != "" && probe.Message != ""
}

// tryManifest pulls the manifest out of a response, reporting whether the
// response was an envelope at all.
//
// It must not fail the test on a miss: these helpers are used on error
// responses and on the history routes' bare-array form, both of which are
// legitimate answers that carry no manifest. The callers assert.
func tryManifest(raw []byte) (memory.Manifest, bool) {
	var env struct {
		Manifest *memory.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Manifest == nil {
		return memory.Manifest{}, false
	}
	return *env.Manifest, true
}

// decodeManifest is tryManifest for the places where a missing manifest is a
// test failure.
func decodeManifest(t *testing.T, raw []byte) memory.Manifest {
	t.Helper()
	m, ok := tryManifest(raw)
	if !ok {
		t.Fatalf("response carries no manifest; raw=%s", raw)
	}
	return m
}

// manifestKey renders a manifest for comparison across surfaces.
//
// Manifest is not comparable with == in a meaningful way: NextCursor is a
// *string, so two responses carrying the SAME cursor compare unequal because
// the pointers differ. Comparing the rendered JSON compares the wire, which is
// the thing parity is actually about.
func manifestKey(t *testing.T, m memory.Manifest) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(raw)
}

// sameManifest fails when two surfaces disagree about what they served.
func sameManifest(t *testing.T, label string, mcpM, httpM memory.Manifest) {
	t.Helper()
	got, want := manifestKey(t, mcpM), manifestKey(t, httpM)
	if got != want {
		t.Errorf("%s manifests differ:\n MCP: %s\nHTTP: %s", label, got, want)
	}
}

// itoa keeps the JSON body building above free of a strconv import.
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

package mcpadapter

// MCP-surface tests for CW-20260825-0004's budget/cursor knobs.
//
// The paging semantics themselves live in internal/memory/paging_test.go.
// What is tested here is the surface contract: that the arguments exist under
// the names the HTTP peers use, that they validate the same way, and that the
// envelope reaches the caller intact.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// recallResultsJSON extracts the results array from a memory_recall envelope.
//
// memory_recall returns {results, manifest} as of CW-20260825-0004; it
// returned a bare array before. Tests that only care about the elements go
// through here so the envelope shape lives in exactly one place, and so a
// response that stops carrying `results` fails loudly instead of decoding to
// an empty slice.
func recallResultsJSON(t *testing.T, raw string) []byte {
	t.Helper()
	var env struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("recall response is not an envelope (raw=%s): %v", raw, err)
	}
	if len(env.Results) == 0 {
		t.Fatalf("recall response carries no results key; raw=%s", raw)
	}
	return env.Results
}

// recallManifest extracts the manifest from a recall or lookup envelope.
func recallManifest(t *testing.T, raw string) memory.Manifest {
	t.Helper()
	var env struct {
		Manifest *memory.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode manifest (raw=%s): %v", raw, err)
	}
	if env.Manifest == nil {
		t.Fatalf("response carries no manifest; raw=%s", raw)
	}
	return *env.Manifest
}

// registeredToolSchemas returns each registered tool's declared input
// properties. An argument a handler reads but the schema does not advertise
// is invisible to an agent reading the tool list, so it does not exist.
func registeredToolSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = knowledge.New(ms)

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	out := map[string]map[string]any{}
	for name, st := range srv.ListTools() {
		out[name] = st.Tool.InputSchema.Properties
	}
	return out
}

func budgetAdapter(t *testing.T, n int) *Adapter {
	t.Helper()
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	for i := 0; i < n; i++ {
		writeViaHandler(t, a, map[string]any{
			"namespace":       "user/chrispian/memory/notes",
			"author_agent_id": "claude",
			"trigger":         "explicit",
			"session_id":      "sess-budget",
			"origin":          "user",
			"confidence":      0.9,
			"payload_summary": "budget probe row",
			"payload_body":    strings.Repeat("x", 200),
		})
	}
	return a
}

func callTool(t *testing.T, fn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := fn(context.Background(), req)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return text.Text
}

// ── The envelope reaches the caller ──────────────────────────────────────────

// Every zero-valued manifest field must survive serialization through the
// tool result. This is the assertion that catches an `omitempty` creeping
// back onto truncated or next_cursor — the exact defect this domain has
// already fixed three times on other fields.
func TestMCPRecall_ManifestCarriesZeroValues(t *testing.T) {
	a := budgetAdapter(t, 2)
	raw := callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
	})
	for _, want := range []string{
		`"truncated":false`, `"truncation_reason":""`, `"next_cursor":null`,
		`"results_total":2`, `"results_returned":2`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("manifest does not carry %s; raw=%s", want, raw)
		}
	}
}

func TestMCPLookup_CarriesManifestAlongsideFacets(t *testing.T) {
	a := budgetAdapter(t, 3)
	raw := callTool(t, a.handleTesseractLookup, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
	})
	var env struct {
		Results  []json.RawMessage         `json:"results"`
		Facets   map[string]map[string]int `json:"facets"`
		Manifest *memory.Manifest          `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, raw)
	}
	if env.Manifest == nil {
		t.Fatalf("lookup carries no manifest; raw=%s", raw)
	}
	if env.Facets["domains"]["memory"] != 3 {
		t.Errorf("facets did not survive the envelope: %+v", env.Facets)
	}
	if env.Manifest.ResultsReturned != len(env.Results) {
		t.Errorf("results_returned=%d but %d results", env.Manifest.ResultsReturned, len(env.Results))
	}
}

// ── Budget arguments ─────────────────────────────────────────────────────────

func TestMCPRecall_BudgetBytesTruncatesWithReasonAndCursor(t *testing.T) {
	a := budgetAdapter(t, 8)
	base := recallManifest(t, callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces":   `["user/chrispian/memory/notes"]`,
		"ranking":      "chronological",
		"payload_mode": "summary",
	}))

	raw := callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces":   `["user/chrispian/memory/notes"]`,
		"ranking":      "chronological",
		"payload_mode": "summary",
		"budget_bytes": float64(base.BytesReturned / 3),
	})
	m := recallManifest(t, raw)
	if !m.Truncated {
		t.Fatalf("budget did not truncate: %+v", m)
	}
	if m.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("truncation_reason = %q, want %q", m.TruncationReason, memory.TruncationBudgetBytes)
	}
	if m.NextCursor == nil {
		t.Error("truncated response carries no next_cursor")
	}
	if m.BytesReturned > base.BytesReturned/3 {
		t.Errorf("bytes_returned %d exceeds budget %d", m.BytesReturned, base.BytesReturned/3)
	}
}

// 0 is inside the type's range and outside its meaning. Absent means "no
// ceiling"; an explicit 0 is a caller mistake and must be named, not silently
// treated as either.
func TestMCPRecall_ZeroBudgetIsValidationError(t *testing.T) {
	a := budgetAdapter(t, 2)
	for _, knob := range []string{"budget_bytes", "budget_tokens"} {
		for _, v := range []float64{0, -1} {
			t.Run(knob, func(t *testing.T) {
				raw := callTool(t, a.handleMemoryRecall, map[string]any{
					"namespaces": `["user/chrispian/memory/notes"]`,
					knob:         v,
				})
				if !strings.Contains(raw, "validation_error") {
					t.Errorf("%s=%v was accepted; raw=%s", knob, v, raw)
				}
				if !strings.Contains(raw, knob) {
					t.Errorf("error does not name the offending knob; raw=%s", raw)
				}
			})
		}
	}
}

// A fractional budget is rejected rather than truncated toward zero, matching
// wholeNumberArg's contract on the other numeric arguments.
func TestMCPRecall_FractionalBudgetIsValidationError(t *testing.T) {
	a := budgetAdapter(t, 2)
	raw := callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces":   `["user/chrispian/memory/notes"]`,
		"budget_bytes": 2.5,
	})
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("fractional budget accepted; raw=%s", raw)
	}
}

// An absent budget must leave the response unbounded even when the adapter
// carries no configured default, and must pick up the configured default when
// it does.
func TestMCPRecall_BudgetDefaultComesFromConfig(t *testing.T) {
	a := budgetAdapter(t, 8)
	unbounded := recallManifest(t, callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces":   `["user/chrispian/memory/notes"]`,
		"ranking":      "chronological",
		"payload_mode": "summary",
	}))
	if unbounded.Truncated {
		t.Fatalf("no configured budget must mean no ceiling: %+v", unbounded)
	}

	a.DefaultBudget = memory.Budget{Bytes: unbounded.BytesReturned / 3}
	bounded := recallManifest(t, callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces":   `["user/chrispian/memory/notes"]`,
		"ranking":      "chronological",
		"payload_mode": "summary",
	}))
	if !bounded.Truncated || bounded.TruncationReason != memory.TruncationBudgetBytes {
		t.Errorf("configured default budget not applied: %+v", bounded)
	}

	// A per-call argument overrides the configured default upward.
	override := recallManifest(t, callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces":   `["user/chrispian/memory/notes"]`,
		"ranking":      "chronological",
		"payload_mode": "summary",
		"budget_bytes": float64(unbounded.BytesReturned * 2),
	}))
	if override.Truncated {
		t.Errorf("per-call budget did not override the config default: %+v", override)
	}
}

// ── Cursor arguments ─────────────────────────────────────────────────────────

func TestMCPRecall_CursorPagesAndRejectsAChangedSort(t *testing.T) {
	a := budgetAdapter(t, 6)
	first := recallManifest(t, callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
		"limit":      float64(2),
	}))
	if first.NextCursor == nil {
		t.Fatalf("no cursor issued: %+v", first)
	}

	// Same sort resumes.
	second := recallManifest(t, callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
		"limit":      float64(2),
		"cursor":     *first.NextCursor,
	}))
	if second.ResultsReturned != 2 {
		t.Errorf("resume returned %d results, want 2", second.ResultsReturned)
	}

	// Changed sort errors.
	raw := callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "activation",
		"limit":      float64(2),
		"cursor":     *first.NextCursor,
	})
	if !strings.Contains(raw, "validation_error") {
		t.Fatalf("resuming a cursor after changing ranking was accepted; raw=%s", raw)
	}
	if !strings.Contains(raw, "different query") {
		t.Errorf("error does not explain the mismatch; raw=%s", raw)
	}
}

func TestMCPLookup_CursorRejectsAChangedSort(t *testing.T) {
	a := budgetAdapter(t, 6)
	first := recallManifest(t, callTool(t, a.handleTesseractLookup, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
		"limit":      float64(2),
	}))
	if first.NextCursor == nil {
		t.Fatalf("no cursor issued: %+v", first)
	}
	raw := callTool(t, a.handleTesseractLookup, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
		"limit":      float64(2),
		"cursor":     *first.NextCursor,
		"statuses":   `["canonical"]`, // a filter change reorders the set
	})
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("lookup accepted a cursor across a changed filter; raw=%s", raw)
	}
}

func TestMCPRecall_MalformedCursorIsValidationError(t *testing.T) {
	a := budgetAdapter(t, 2)
	raw := callTool(t, a.handleMemoryRecall, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"cursor":     "!!!not-a-cursor!!!",
	})
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("malformed cursor accepted; raw=%s", raw)
	}
}

// ── History ──────────────────────────────────────────────────────────────────

// GET-shaped history keeps its bare array unless the caller engages a knob.
// The shipped web UI parses both history routes as arrays and its bundle is
// not rebuilt here, so the default response must not move.
func TestMCPHistory_BareArrayUntilAKnobIsPassed(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	var last string
	for i := 0; i < 4; i++ {
		res := writeViaHandler(t, a, map[string]any{
			"namespace":       "user/chrispian/memory/notes",
			"memory_key":      "hist.key",
			"supersedes":      last,
			"author_agent_id": "claude",
			"trigger":         "explicit",
			"session_id":      "sess-hist",
			"origin":          "user",
			"confidence":      0.9,
			"payload_summary": "history probe",
		})
		last, _ = res["revision_id"].(string)
	}

	bare := callTool(t, a.handleMemoryHistory, map[string]any{
		"namespace":  "user/chrispian/memory/notes",
		"memory_key": "hist.key",
	})
	var arr []map[string]any
	if err := json.Unmarshal([]byte(bare), &arr); err != nil {
		t.Fatalf("default history must stay a bare array: %v (raw=%s)", err, bare)
	}
	if len(arr) != 4 {
		t.Errorf("bare history returned %d revisions, want 4", len(arr))
	}

	paged := callTool(t, a.handleMemoryHistory, map[string]any{
		"namespace":  "user/chrispian/memory/notes",
		"memory_key": "hist.key",
		"limit":      float64(2),
	})
	m := recallManifest(t, paged)
	if m.ResultsTotal != 4 || m.ResultsReturned != 2 {
		t.Errorf("paged history manifest = %+v, want total 4 returned 2", m)
	}
	if m.NextCursor == nil {
		t.Error("paged history issued no cursor")
	}
	if !m.Truncated || m.TruncationReason != memory.TruncationLimit {
		t.Errorf("paged history manifest = %+v, want truncated with reason %q",
			m, memory.TruncationLimit)
	}
}

func TestMCPHistory_ZeroBudgetIsValidationError(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "hist.key",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-hist",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "history probe",
	})
	raw := callTool(t, a.handleMemoryHistory, map[string]any{
		"namespace":    "user/chrispian/memory/notes",
		"memory_key":   "hist.key",
		"budget_bytes": float64(0),
	})
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("history accepted budget_bytes=0; raw=%s", raw)
	}
}

// ── Argument surface ─────────────────────────────────────────────────────────

// Every knob this ticket adds must be declared on the tools that accept it.
// A handler that reads an argument the schema does not advertise is invisible
// to an agent reading the tool list.
func TestBudgetCursorArgumentsAreDeclared(t *testing.T) {
	tools := registeredToolSchemas(t)
	for _, tc := range []struct {
		tool string
		args []string
	}{
		{"memory_recall", []string{"cursor", "budget_bytes", "budget_tokens", "limit"}},
		{"tesseract_lookup", []string{"cursor", "budget_bytes", "budget_tokens", "limit"}},
		{"memory_history", []string{"cursor", "budget_bytes", "budget_tokens", "limit"}},
		{"knowledge_history", []string{"cursor", "budget_bytes", "budget_tokens", "limit"}},
	} {
		props, ok := tools[tc.tool]
		if !ok {
			t.Errorf("tool %s is not registered", tc.tool)
			continue
		}
		for _, arg := range tc.args {
			if _, ok := props[arg]; !ok {
				t.Errorf("%s does not declare %q", tc.tool, arg)
			}
		}
	}
}

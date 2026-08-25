package mcpadapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// CW-20260825-0005 guard. memory_recall and tesseract_lookup serialize
// memory.RecallResult straight to the wire. That struct carried no JSON
// tags, so every live response came back PascalCase —
// {"Revision":…,"Score":…,"State":…} — against snake_case everywhere else
// in the API. These tests lock the corrected shape at the MCP boundary,
// which is where callers actually see it.

// recallShapeAdapter seeds one memory and returns the adapter.
func recallShapeAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "shape.one",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "shape probe",
	})
	return a
}

// callRecall runs memory_recall and decodes the bare result array.
func callRecall(t *testing.T, a *Adapter, args map[string]any) []map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := a.handleMemoryRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryRecall: %v", err)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal recall (raw=%s): %v", text.Text, err)
	}
	if len(out) == 0 {
		t.Fatalf("expected at least one result, raw=%s", text.Text)
	}
	return out
}

// assertSnakeCaseResult checks one result object's key set.
//
// CW-20260825-0003 added payload_mode projection, so `state` now rides only
// on full-mode results. wantState carries that: the snake_case assertions
// below still apply in every mode, and the extra key-set assertions are in
// assertProjectedResult / the payload_mode tests rather than relaxed here.
func assertSnakeCaseResult(t *testing.T, item map[string]any, wantScore, wantState bool) {
	t.Helper()
	for _, bad := range []string{"Revision", "Score", "State", "PayloadMode"} {
		if _, present := item[bad]; present {
			t.Errorf("result carries PascalCase key %q; keys=%v", bad, keysOf(item))
		}
	}
	if _, present := item["revision"]; !present {
		t.Errorf("result missing snake_case key \"revision\"; keys=%v", keysOf(item))
	}
	if _, present := item["state"]; present != wantState {
		t.Errorf("result state present = %v, want %v; keys=%v", present, wantState, keysOf(item))
	}
	if _, present := item["score"]; present != wantScore {
		t.Errorf("result score present = %v, want %v; keys=%v", present, wantScore, keysOf(item))
	}
	// The nested revision must already be snake_case — it has tags today,
	// and this catches a regression that strips them.
	rev, ok := item["revision"].(map[string]any)
	if !ok {
		t.Fatalf("revision is %T, want object", item["revision"])
	}
	if rev["memory_key"] != "shape.one" {
		t.Errorf("revision.memory_key = %v, want shape.one", rev["memory_key"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Snake-case is asserted in every payload_mode, not just the default: the
// projected result is a different Go struct from RecallResult, so it can
// regress to PascalCase independently.
func TestMemoryRecall_ResultKeysAreSnakeCase(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		wantState bool
	}{
		{"keys", false},
		{"summary", false},
		{"full", true},
		{"", false}, // config default — summary
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			a := recallShapeAdapter(t)
			args := map[string]any{
				"namespaces": `["user/chrispian/memory/notes"]`,
				"ranking":    "activation",
			}
			if tc.mode != "" {
				args["payload_mode"] = tc.mode
			}
			results := callRecall(t, a, args)
			assertSnakeCaseResult(t, results[0], true, tc.wantState)
		})
	}
}

// Under chronological ranking `score` is absent rather than carrying a raw
// CreatedAt.UnixNano(). Ordering is carried by array order plus
// revision.created_at; a score there would restate the sort key in units no
// other ranking mode uses.
func TestMemoryRecall_ChronologicalOmitsScore(t *testing.T) {
	a := recallShapeAdapter(t)
	results := callRecall(t, a, map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "chronological",
	})
	assertSnakeCaseResult(t, results[0], false, false)
}

// The nil-score contract must survive projection: projected results copy
// Score as a pointer, so a full-mode-only guard would miss a regression that
// materializes a zero score in the projected path.
func TestMemoryRecall_ChronologicalOmitsScore_EveryMode(t *testing.T) {
	for _, mode := range []string{"keys", "summary", "full"} {
		t.Run("mode="+mode, func(t *testing.T) {
			a := recallShapeAdapter(t)
			results := callRecall(t, a, map[string]any{
				"namespaces":   `["user/chrispian/memory/notes"]`,
				"ranking":      "chronological",
				"payload_mode": mode,
			})
			if _, present := results[0]["score"]; present {
				t.Errorf("mode=%s: score present under chronological; keys=%v", mode, keysOf(results[0]))
			}
		})
	}
}

func TestTesseractLookup_ResultKeysAreSnakeCase(t *testing.T) {
	a := recallShapeAdapter(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "activation",
	}
	res, err := a.handleTesseractLookup(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractLookup: %v", err)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var envelope struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(text.Text), &envelope); err != nil {
		t.Fatalf("unmarshal lookup (raw=%s): %v", text.Text, err)
	}
	if len(envelope.Results) == 0 {
		t.Fatalf("expected at least one result, raw=%s", text.Text)
	}
	// No payload_mode argument — this exercises the config default, which is
	// summary, so state is projected away.
	assertSnakeCaseResult(t, envelope.Results[0], true, false)
}

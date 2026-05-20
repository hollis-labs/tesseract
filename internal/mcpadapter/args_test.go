package mcpadapter

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestMemoryWrite_TagsAcceptBothForms covers CW-20260519-0039 — clients that
// send `tags` as a native JSON array (e.g. via the mux MCP proxy) had their
// tags silently dropped because `req.GetString` returns "" for non-string args.
// The fix accepts native []any, []string, and stringified JSON array.
func TestMemoryWrite_TagsAcceptBothForms(t *testing.T) {
	cases := []struct {
		name string
		tags any
		want []string
	}{
		{"native_any_array", []any{"alpha", "beta"}, []string{"alpha", "beta"}},
		{"native_string_array", []string{"alpha", "beta"}, []string{"alpha", "beta"}},
		{"stringified_array", `["alpha","beta"]`, []string{"alpha", "beta"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newMemoryAdapter(t, "memory:write", "memory:read")
			body := writeViaHandler(t, a, map[string]any{
				"namespace":       "user/chrispian/memory",
				"memory_key":      "user.prefs." + tc.name,
				"author_agent_id": "claude",
				"trigger":         "explicit",
				"session_id":      "sess-001",
				"origin":          "user",
				"confidence":      0.9,
				"payload_summary": "Tags persistence regression test",
				"tags":            tc.tags,
			})
			if body["code"] != nil {
				t.Fatalf("write failed: %v", body)
			}
			gotRaw, ok := body["tags"]
			if !ok {
				t.Fatalf("response missing tags field: %v", body)
			}
			gotAny, ok := gotRaw.([]any)
			if !ok {
				t.Fatalf("tags not an array in response: %T %v", gotRaw, gotRaw)
			}
			got := make([]string, 0, len(gotAny))
			for _, v := range gotAny {
				got = append(got, v.(string))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("tags = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMemoryWrite_TagsInvalidShape rejects clearly-wrong shapes instead of
// silently dropping (the previous failure mode).
func TestMemoryWrite_TagsInvalidShape(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	body := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "user.prefs.invalid",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "tags invalid shape",
		"tags":            42, // number, not string or array
	})
	if body["code"] != "validation_error" {
		t.Errorf("expected validation_error for non-array tags, got %v", body)
	}
}

// TestMemoryRecall_TagFilterNativeArray covers the same bug in memory_recall's
// `tags` / `namespaces` filter args.
func TestMemoryRecall_TagFilterNativeArray(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")

	// Seed one tagged + one untagged memory.
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "tagged.one",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "tagged",
		"tags":            []any{"decision"},
	})
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory",
		"memory_key":      "untagged.one",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "untagged",
	})

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"namespaces": []any{"user/chrispian/memory"},
		"tags":       []any{"decision"},
	}
	res, err := a.handleMemoryRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryRecall: %v", err)
	}
	textContent := res.Content[0].(mcp.TextContent)
	var results []map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &results); err != nil {
		t.Fatalf("unmarshal results (raw=%s): %v", textContent.Text, err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 tagged result, got %d", len(results))
	}
	rev, _ := results[0]["Revision"].(map[string]any)
	if rev["memory_key"] != "tagged.one" {
		t.Errorf("filter returned wrong row: %v", results[0])
	}
}

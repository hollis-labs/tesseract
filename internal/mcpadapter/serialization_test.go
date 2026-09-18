package mcpadapter

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestToolJSONIsAPassThrough pins toolJSON's current contract under go-mcp:
// unlike the mark3labs-era version, it does not marshal anything itself
// (and so cannot fail to) — it hands the value back unchanged for go-mcp's
// own ToolHandler dispatch to JSON-marshal into StructuredContent and a
// mirrored text block. A value that WOULD fail to marshal (e.g. malformed
// json.RawMessage) is go-mcp's problem now, reported as a protocol-level
// error (ErrCodeInternal) rather than folded into tool result content —
// see server.adaptHandler in go-mcp/server/server.go. That boundary is
// go-mcp's own and has no equivalent to assert from this package.
func TestToolJSONIsAPassThrough(t *testing.T) {
	v := map[string]any{"payload": json.RawMessage(`{"unfinished":`)}
	if got := toolJSON(v); !reflect.DeepEqual(got, v) {
		t.Fatalf("toolJSON(v) = %#v, want v unchanged: %#v", got, v)
	}
}

func TestToolJSONSuccessStillUsesOrdinaryResultShape(t *testing.T) {
	body := textOf(t, toolJSON(map[string]any{"ok": true, "count": 2}))
	var result map[string]any
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("success result is not valid JSON: %q: %v", body, err)
	}
	if result["ok"] != true || result["count"] != float64(2) {
		t.Fatalf("unexpected result: %v", result)
	}
}

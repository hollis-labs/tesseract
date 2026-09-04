package mcpadapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolJSONMarshalFailureIsExplicit(t *testing.T) {
	result := toolJSON(map[string]any{
		"payload": json.RawMessage(`{"unfinished":`),
	})
	body := textOf(t, result)
	if body == "" {
		t.Fatal("marshal failure returned an empty successful-looking tool result")
	}

	var failure struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatalf("marshal failure result is not valid JSON: %q: %v", body, err)
	}
	if failure.Code != string(codeInternalError) {
		t.Fatalf("code = %q, want %q (body=%s)", failure.Code, codeInternalError, body)
	}
	if !strings.Contains(failure.Message, "failed to serialize MCP tool result") {
		t.Fatalf("message does not identify serialization failure: %q", failure.Message)
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

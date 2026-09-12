package mcpadapter

import (
	"encoding/json"
	"testing"
)

// The MCP door for payload.data (CW-20260912-0036).
//
// Two shapes have to work, because MCP clients are inconsistent about it and
// `consumer_state` already accepts both: a JSON-encoded STRING (the flat-scalar
// convention these schemas favor, matching `tags`) and a native object. Neither
// is normalized at the door — the store is the single authority on whether the
// bytes are an object, so a malformed bag produces the same message whichever
// surface it arrived at.

func payloadDataOf(t *testing.T, body map[string]any) string {
	t.Helper()
	payload, ok := body["payload"].(map[string]any)
	if !ok {
		t.Fatalf("no payload in result: %v", body)
	}
	raw, err := json.Marshal(payload["data"])
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	return string(raw)
}

func TestPayloadDataAcceptsBothArgumentShapes(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")

	base := func(key string) map[string]any {
		return map[string]any{
			"namespace":       "user/chrispian/memory/notes",
			"memory_key":      key,
			"author_agent_id": "test",
			"trigger":         "explicit",
			"session_id":      "s-1",
			"derived_from":    "observation",
			"confidence":      0.9,
			"payload_summary": "a record with fields",
		}
	}

	t.Run("JSON-encoded string", func(t *testing.T) {
		args := base("data.string")
		args["payload_data"] = `{"component":"auth","severity":3}`
		body := writeViaHandler(t, a, args)
		if isRefusal(body) {
			t.Fatalf("refused a JSON-encoded payload_data: %v", body)
		}
		if got := payloadDataOf(t, body); got == "null" {
			t.Errorf("payload.data was dropped: %v", body)
		}
	})

	t.Run("native object", func(t *testing.T) {
		args := base("data.native")
		args["payload_data"] = map[string]any{"component": "auth", "severity": 3}
		body := writeViaHandler(t, a, args)
		if isRefusal(body) {
			t.Fatalf("refused a native payload_data object: %v", body)
		}
		if got := payloadDataOf(t, body); got == "null" {
			t.Errorf("payload.data was dropped: %v", body)
		}
	})

	// The refusal has to arrive as a tool RESULT carrying a code, not as a Go
	// error — a test that looked for the latter would read every rejection as
	// a success.
	t.Run("a non-object is refused", func(t *testing.T) {
		args := base("data.bad")
		args["payload_data"] = `[1,2,3]`
		body := writeViaHandler(t, a, args)
		if !isRefusal(body) {
			t.Errorf("accepted an array as payload_data: %v", body)
		}
	})

	// The claim is opt-in and is never filled in for the caller.
	t.Run("no schema claim stores none", func(t *testing.T) {
		args := base("data.noclaim")
		args["payload_data"] = `{"a":1}`
		body := writeViaHandler(t, a, args)
		if isRefusal(body) {
			t.Fatalf("refused: %v", body)
		}
		payload, _ := body["payload"].(map[string]any)
		if v, present := payload["data_schema_hash"]; present {
			t.Errorf("data_schema_hash = %v for a write that claimed nothing; it is never defaulted", v)
		}
	})
}

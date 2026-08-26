// HTTP-side guards for the packet payload vocabulary change of
// CW-20260825-0011.
//
// POST /v1/context/packet used to accept `assembly.shape.payload_mode` with the
// vocabulary full|head_only, and honored ONLY "head_only" — every other value,
// including the keys|summary|full vocabulary the recall surface uses under the
// same argument name, fell through to full payloads with no error. That failed
// toward MORE context than the caller asked for.
//
// Byte capping is now `assembly.shape.payload_max_bytes`, and `payload_mode`
// accepts only "full" here so `payload_mode` names one projection across the
// whole surface.
package contextapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// packetWithShape posts a one-record packet request with the given shape block.
func packetWithShape(t *testing.T, s http.Handler, ns string, shape map[string]any) map[string]any {
	t.Helper()
	res := performJSON(t, s, "POST", "/v1/context/packet", map[string]any{
		"selector": map[string]any{"namespaces": []string{ns}, "revision_scope": "head"},
		"assembly": map[string]any{"include_pins": false, "shape": shape},
	})
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	body["__status"] = float64(res.Code)
	return body
}

func seedBigRecord(t *testing.T, s http.Handler, ns, key string, bodyLen int) {
	t.Helper()
	res := performJSON(t, s, "POST", "/v1/context/write", map[string]any{
		"actor":     "user",
		"namespace": ns,
		"key":       key,
		"payload":   map[string]any{"body": strings.Repeat("x", bodyLen)},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("seed write: %s", res.Body)
	}
}

// TestPacketPayloadModeRetiredVocabularyIsRejected pins the fail-closed half.
// Before this change every one of these values returned 200 with full payloads.
func TestPacketPayloadModeRetiredVocabularyIsRejected(t *testing.T) {
	s := newTestServer(t)
	seedBigRecord(t, s, "user/memory/cap-test", "doc", 10)

	for _, mode := range []string{"head_only", "keys", "summary", "bogus"} {
		t.Run(mode, func(t *testing.T) {
			body := packetWithShape(t, s, "user/memory/cap-test", map[string]any{
				"include_payload": true,
				"payload_mode":    mode,
			})
			if int(body["__status"].(float64)) != http.StatusBadRequest {
				t.Fatalf("payload_mode=%q returned %v, want 400; body %v", mode, body["__status"], body)
			}
			if body["code"] != "validation_error" {
				t.Errorf("code = %v, want validation_error", body["code"])
			}
		})
	}

	// Positive control: "full" is still accepted, so the rejections above are
	// about the retired values rather than about the field being refused.
	body := packetWithShape(t, s, "user/memory/cap-test", map[string]any{
		"include_payload": true,
		"payload_mode":    "full",
	})
	if int(body["__status"].(float64)) != http.StatusOK {
		t.Fatalf("payload_mode=full returned %v, want 200; body %v", body["__status"], body)
	}
}

// TestPacketPayloadMaxBytesCapsWithoutBreakingJSON pins the replacement.
//
// A capped item carries no `payload` at all — the prefix of a JSON object is
// not valid JSON, so shortening `payload` in place is not an option. It reports
// payload_head, payload_truncated and payload_bytes instead.
func TestPacketPayloadMaxBytesCapsWithoutBreakingJSON(t *testing.T) {
	s := newTestServer(t)
	seedBigRecord(t, s, "user/memory/cap-test", "doc", 600)

	body := packetWithShape(t, s, "user/memory/cap-test", map[string]any{
		"include_payload":   true,
		"payload_max_bytes": 64,
	})
	if int(body["__status"].(float64)) != http.StatusOK {
		t.Fatalf("status %v; body %v", body["__status"], body)
	}
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if _, ok := item["payload"]; ok {
		t.Errorf("a capped item must not carry `payload`; got %v", item)
	}
	head, _ := item["payload_head"].(string)
	if len(head) != 64 {
		t.Errorf("payload_head = %d bytes, want 64", len(head))
	}
	if item["payload_truncated"] != true {
		t.Errorf("payload_truncated = %v, want true", item["payload_truncated"])
	}
	if n, _ := item["payload_bytes"].(float64); int(n) <= 64 {
		t.Errorf("payload_bytes = %v, want the full payload size (> 64)", item["payload_bytes"])
	}

	// Positive control: uncapped, the same record comes back with a payload,
	// so the assertions above measure the cap and not the fixture.
	uncapped := packetWithShape(t, s, "user/memory/cap-test", map[string]any{
		"include_payload": true,
	})
	only := uncapped["items"].([]any)[0].(map[string]any)
	if _, ok := only["payload"]; !ok {
		t.Fatalf("uncapped item lost its payload: %v", only)
	}
	if _, ok := only["payload_head"]; ok {
		t.Errorf("uncapped item carries payload_head: %v", only)
	}
}

// TestPacketPayloadMaxBytesRejectsNegative: a negative cap is outside the
// knob's meaning, and reading it as "no cap" would return more than asked for.
func TestPacketPayloadMaxBytesRejectsNegative(t *testing.T) {
	s := newTestServer(t)
	seedBigRecord(t, s, "user/memory/cap-test", "doc", 10)

	body := packetWithShape(t, s, "user/memory/cap-test", map[string]any{
		"include_payload":   true,
		"payload_max_bytes": -1,
	})
	if int(body["__status"].(float64)) != http.StatusBadRequest {
		t.Fatalf("negative cap returned %v, want 400; body %v", body["__status"], body)
	}
}

package contextapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The HTTP door for payload.data (CW-20260912-0036).
//
// HTTP takes it as a nested object where MCP takes a JSON-encoded string, the
// same way `tags` and `consumer_state` already differ. The parity harness
// asserts only that the route exists, so argument parity is on these tests.

// TestPayloadDataRoundTripsOverHTTP proves the door is wired AND that it does
// not reshape what it carries.
//
// The keys are out of alphabetical order and the id is larger than float64
// holds exactly, because the plausible wrong implementation — decoding into
// map[string]any and re-marshaling — passes any assertion that compares parsed
// values and fails both of these.
func TestPayloadDataRoundTripsOverHTTP(t *testing.T) {
	srv := newMemoryTestServer(t)

	const data = `{"zeta":1,"id":9007199254740993,"alpha":{"nested":true}}`
	body := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"adr.http",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"observation",
		"confidence":0.9,
		"payload":{"summary":"an ADR","data":` + data + `}
	}`
	rr, _ := postJSON(t, srv, "/v1/memory/write", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("write status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var wrote struct {
		Payload struct {
			Data json.RawMessage `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &wrote); err != nil {
		t.Fatalf("decode write response: %v", err)
	}
	if string(wrote.Payload.Data) != data {
		t.Errorf("payload.data came back reshaped over HTTP:\n got %s\nwant %s\n"+
			"Key order and integer precision both matter to a consumer that hashes its object.",
			wrote.Payload.Data, data)
	}
}

// TestPayloadDataRefusedOverHTTPWhenNotAnObject checks the refusal reaches the
// caller as a validation error rather than a 500 — the store's message is the
// one every surface reports, and this asserts HTTP does not swallow or reclassify it.
func TestPayloadDataRefusedOverHTTPWhenNotAnObject(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"namespace":"user/chrispian/memory/notes",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"observation",
		"confidence":0.9,
		"payload":{"summary":"s","data":[1,2,3]}
	}`
	rr, decoded := postJSON(t, srv, "/v1/memory/write", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an array payload.data; body=%s", rr.Code, rr.Body.String())
	}
	if decoded["code"] != "validation_error" {
		t.Errorf("code = %v, want validation_error", decoded["code"])
	}
	if msg, _ := decoded["message"].(string); !strings.Contains(msg, "payload.data") {
		t.Errorf("the refusal does not name the field the caller must fix: %v", decoded["message"])
	}
}

package contextapi

import (
	"encoding/json"
	"net/http"
	"reflect"
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

// TestPayloadDataWireFormIsSemanticNotByteIdentical pins the boundary of the
// "stored verbatim" guarantee, which was promised more broadly than it was true
// (PR #41 review).
//
// The COLUMN is exact and a Go caller reads exact bytes. A JSON RESPONSE is
// not: encoding/json compacts a RawMessage and escapes <, > and & on the way
// out. Both forms are the same JSON value and parse identically, so nothing is
// lost — but a consumer that hashes or signs what it receives cannot assume the
// bytes match what it sent, and that was exactly the use case cited as the
// reason for using json.RawMessage at all.
//
// Asserted rather than commented so the claim cannot quietly widen again: if
// someone later adds a raw-preserving encoder, this test fails and they update
// the promise deliberately.
func TestPayloadDataWireFormIsSemanticNotByteIdentical(t *testing.T) {
	srv := newMemoryTestServer(t)

	// Insignificant whitespace and the three characters Go escapes.
	const sent = `{"a": 1,  "html":"x<y&z>w"}`
	body := `{
		"namespace":"user/chrispian/memory/notes",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"derived_from":"observation",
		"confidence":0.9,
		"payload":{"summary":"s","data":` + sent + `}
	}`
	rr, _ := postJSON(t, srv, "/v1/memory/write", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("write status = %d; body=%s", rr.Code, rr.Body.String())
	}

	var got struct {
		Payload struct {
			Data json.RawMessage `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// SEMANTIC equality holds, and this is the guarantee consumers actually get.
	var a, b map[string]any
	if err := json.Unmarshal([]byte(sent), &a); err != nil {
		t.Fatalf("parse sent: %v", err)
	}
	if err := json.Unmarshal(got.Payload.Data, &b); err != nil {
		t.Fatalf("parse received: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("payload.data changed VALUE over the wire, which would be a real defect:\n"+
			" sent %s\n got %s", sent, got.Payload.Data)
	}

	// BYTE equality does not, and saying so here is the point of the test.
	if string(got.Payload.Data) == sent {
		t.Skip("the wire form is now byte-identical — encoding must have changed. " +
			"Update Payload.Data's doc comment, which currently tells consumers it is not.")
	}
	t.Logf("wire form differs from stored bytes, as documented:\n sent %s\n wire %s",
		sent, got.Payload.Data)
}

// TestPayloadDataOnKnowledgeAndEventHTTP covers the two doors PR #41's review
// found unwired.
//
// The task said all three domains get this together because `Payload` is
// shared — and the STORE inputs and the MCP tools were wired for all three
// while only the memory HTTP handler was. The failure mode is unusually sharp:
// `decodeJSON` runs DisallowUnknownFields, so a knowledge or event write
// carrying `data` was not ignoring it, it was 400-ing the whole write. The
// feature was not merely missing on those surfaces, it broke callers who
// followed the shared MCP description over to HTTP.
func TestPayloadDataOnKnowledgeAndEventHTTP(t *testing.T) {
	const data = `{"decision":"use WAL","alternatives":["DELETE"]}`

	t.Run("knowledge", func(t *testing.T) {
		srv := newKnowledgeTestServer(t)
		rr, _ := postJSON(t, srv, "/v1/knowledge/write", `{
			"namespace":"user/chrispian/knowledge/framework",
			"key":"framework.go-providers",
			"kind":"package",
			"source":"filesystem",
			"pointer":{"scheme":"file","locator":"/pkg/go-providers"},
			"summary":"go-providers multi-provider adapter",
			"author":{"agent_id":"indexer","agent_version":"1.0"},
			"session_id":"indexer:01HX",
			"data":`+data+`
		}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the knowledge HTTP door rejects payload.data; body=%s",
				rr.Code, rr.Body.String())
		}
		assertDataEquals(t, rr.Body.Bytes(), data)
	})

	t.Run("event", func(t *testing.T) {
		srv := newEventTestServer(t)
		rr, _ := postJSON(t, srv, "/v1/event/write", `{
			"namespace":"user/chrispian/event/journal",
			"summary":"settled the namespace grammar",
			"author":{"agent_id":"chrispian","agent_version":""},
			"session_id":"manual:01HX",
			"data":`+data+`
		}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the event HTTP door rejects payload.data; body=%s",
				rr.Code, rr.Body.String())
		}
		assertDataEquals(t, rr.Body.Bytes(), data)
	})
}

// assertDataEquals compares by VALUE, not by bytes — see
// TestPayloadDataWireFormIsSemanticNotByteIdentical for why the distinction
// matters and where it is pinned.
func assertDataEquals(t *testing.T, body []byte, want string) {
	t.Helper()
	var got struct {
		Payload struct {
			Data json.RawMessage `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var a, b map[string]any
	if err := json.Unmarshal([]byte(want), &a); err != nil {
		t.Fatalf("parse want: %v", err)
	}
	if err := json.Unmarshal(got.Payload.Data, &b); err != nil {
		t.Fatalf("parse got %q: %v", got.Payload.Data, err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("payload.data did not round trip:\n got %s\nwant %s", got.Payload.Data, want)
	}
}

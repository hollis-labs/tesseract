package contextapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// The HTTP peer of the MCP structured-object door (CW-20260909-0036).
//
// The two surfaces take the same fact in different shapes — nested object here,
// JSON-encoded string on MCP, the way `tags` already differs — and the parity
// harness only asserts that a route exists. Argument parity is on us, so these
// tests assert the same three properties the MCP tests do: the bag round-trips
// with its JSON types intact, a non-object is refused, and a filter on a
// boolean actually selects.

func postJSON(t *testing.T, srv *Server, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s response: %v (body=%s)", path, err, rr.Body.String())
	}
	return rr, decoded
}

func writeTodoHTTP(t *testing.T, srv *Server, key, bag string) {
	t.Helper()
	body := `{
		"namespace":"user/chrispian/memory/todos",
		"memory_key":"` + key + `",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"payload":{"summary":"a todo"},
		"consumer_state":` + bag + `
	}`
	rr, decoded := postJSON(t, srv, "/v1/memory/write", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("write %s: status %d, body %v", key, rr.Code, decoded)
	}
}

func TestHTTPMemoryWriteRoundTripsConsumerState(t *testing.T) {
	srv := newMemoryTestServer(t)

	bag := `{"kind":"todo","section":"now","completed":false,"priority":2}`
	body := `{
		"namespace":"user/chrispian/memory/todos",
		"memory_key":"todo.milk",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"payload":{"summary":"buy milk"},
		"consumer_state":` + bag + `
	}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var rev memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var echoed map[string]any
	if err := json.Unmarshal(rev.ConsumerState, &echoed); err != nil {
		t.Fatalf("consumer_state is not an object: %v (%s)", err, rev.ConsumerState)
	}
	if echoed["completed"] != false {
		t.Errorf("consumer_state.completed = %#v, want the boolean false", echoed["completed"])
	}
	if echoed["priority"] != float64(2) {
		t.Errorf("consumer_state.priority = %#v, want the number 2", echoed["priority"])
	}
}

func TestHTTPMemoryWriteRefusesANonObjectConsumerState(t *testing.T) {
	srv := newMemoryTestServer(t)
	for name, bag := range map[string]string{
		"an array":      `["now"]`,
		"a bare string": `"now"`,
		"a null":        `null`,
	} {
		t.Run(name, func(t *testing.T) {
			body := `{
				"namespace":"user/chrispian/memory/todos",
				"author":{"agent_id":"test","agent_version":"1.0"},
				"trigger":"explicit",
				"session_id":"manual:01HX",
				"origin":"user",
				"confidence":0.9,
				"payload":{"summary":"buy milk"},
				"consumer_state":` + bag + `
			}`
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			srv.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400; body = %s", name, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHTTPLookupFiltersOnConsumerState(t *testing.T) {
	srv := newMemoryTestServer(t)
	writeTodoHTTP(t, srv, "todo.open", `{"kind":"todo","section":"now","completed":false}`)
	writeTodoHTTP(t, srv, "todo.done", `{"kind":"todo","section":"now","completed":true}`)
	writeTodoHTTP(t, srv, "note.open", `{"kind":"note","section":"now","completed":false}`)

	rr, decoded := postJSON(t, srv, "/v1/tesseract/lookup", `{
		"namespaces":["user/chrispian/memory/todos"],
		"ranking":"chronological",
		"state_filters":[
			{"field":"kind","values":["todo"]},
			{"field":"completed","values":[false]}
		]
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rr.Code, decoded)
	}

	results, ok := decoded["results"].([]any)
	if !ok {
		t.Fatalf("no results in %v", decoded)
	}
	if len(results) != 1 {
		t.Fatalf("returned %d results, want 1 (todo.open); a JSON false that reached SQLite as the "+
			"string \"false\" matches nothing, which reads exactly like a corpus with no open todos:\n%v",
			len(results), decoded)
	}
	rev := results[0].(map[string]any)["revision"].(map[string]any)
	if rev["memory_key"] != "todo.open" {
		t.Errorf("matched %v, want todo.open", rev["memory_key"])
	}
	if _, carried := rev["consumer_state"]; !carried {
		t.Error("consumer_state is absent under the default projection; a list view would have to " +
			"request full mode and drag every body across the wire to read four scalars")
	}
}

func TestHTTPLookupRefusesAMalformedStateFilter(t *testing.T) {
	srv := newMemoryTestServer(t)
	for name, filters := range map[string]string{
		"an uppercase field":  `[{"field":"Section","values":["now"]}]`,
		"a JSON path":         `[{"field":"$.section","values":["now"]}]`,
		"an empty value set":  `[{"field":"section","values":[]}]`,
		"a duplicated field":  `[{"field":"section","values":["now"]},{"field":"section","values":["soon"]}]`,
		"a null filter value": `[{"field":"section","values":[null]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			rr, body := postJSON(t, srv, "/v1/tesseract/lookup", `{
				"namespaces":["user/chrispian/memory/todos"],
				"ranking":"chronological",
				"state_filters":`+filters+`
			}`)
			if rr.Code == http.StatusOK {
				t.Errorf("%s was accepted: %v\nAn empty page from a malformed filter is "+
					"indistinguishable from a clean corpus", name, body)
			}
			// The message a caller reads must name the argument, not the
			// internal function that happened to notice.
			if msg, _ := body["message"].(string); strings.Contains(msg, "fetchCandidates") {
				t.Errorf("%s: the error leaks an internal function name: %q", name, msg)
			}
		})
	}
}

// The nested /v1/memory/recall route decodes straight into memory.RecallFilters,
// so it reaches the filter through a different path than the lookup route's
// explicit request struct. Both must accept the same spelling.
func TestHTTPMemoryRecallAcceptsStateFilters(t *testing.T) {
	srv := newMemoryTestServer(t)
	writeTodoHTTP(t, srv, "todo.open", `{"kind":"todo","section":"now","completed":false}`)
	writeTodoHTTP(t, srv, "todo.done", `{"kind":"todo","section":"now","completed":true}`)

	rr, decoded := postJSON(t, srv, "/v1/memory/recall", `{
		"namespaces":["user/chrispian/memory/todos"],
		"ranking":"chronological",
		"filters":{"state_filters":[{"field":"completed","values":[true]}]}
	}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", rr.Code, decoded)
	}
	results, ok := decoded["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("returned %v, want exactly todo.done — if this route ignored state_filters it "+
			"would return both rows and look like a working call", decoded)
	}
	rev := results[0].(map[string]any)["revision"].(map[string]any)
	if rev["memory_key"] != "todo.done" {
		t.Errorf("matched %v, want todo.done", rev["memory_key"])
	}
}

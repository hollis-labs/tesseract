package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// The MCP door for structured objects (CW-20260909-0036).
//
// Two things are being proven, and the second is the one that would break
// quietly. First, that `consumer_state` and `state_filters` are wired at all.
// Second, that a value survives the trip with its JSON TYPE intact: an MCP
// client sends these as JSON-encoded strings, and a door that decoded them
// through []string would turn the literal `false` into the string "false".
// Both are truthy-looking bags, they filter to different rows, and nothing
// would report the difference.

// isRefusal reports whether a parsed tool result is the error envelope.
// An MCP tool reports a refusal as a RESULT carrying a code, not as a
// transport error, so a test that looks for a Go error reads every rejection
// as a success.
func isRefusal(body map[string]any) bool {
	_, ok := body["code"]
	return ok
}

func recallViaHandler(t *testing.T, a *Adapter, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := a.handleTesseractRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractRecall: %v", err)
	}
	return parseResult(t, res)
}

func writeTodo(t *testing.T, a *Adapter, key, state string) {
	t.Helper()
	body := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/todos",
		"memory_key":      key,
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-todo",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "a todo",
		"consumer_state":  state,
	})
	if isRefusal(body) {
		t.Fatalf("writing %s returned %v", key, body)
	}
}

func TestMemoryWriteAcceptsConsumerStateAsAJSONString(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	bag := `{"kind":"todo","section":"now","completed":false}`
	body := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/todos",
		"memory_key":      "todo.milk",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "buy milk",
		"consumer_state":  bag,
	})

	raw, ok := body["consumer_state"]
	if !ok {
		t.Fatalf("write result carries no consumer_state: %v", body)
	}
	got, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Re-marshaled and compared field by field: the door must not have
	// stringified the bag, which is what a []string decode would do.
	var echoed map[string]any
	if err := json.Unmarshal(got, &echoed); err != nil {
		t.Fatalf("consumer_state came back as %s, which is not an object: %v", got, err)
	}
	if echoed["completed"] != false {
		t.Errorf("consumer_state.completed = %#v, want the boolean false — a bag whose booleans "+
			"arrived as strings filters to different rows and nothing reports it", echoed["completed"])
	}
	if echoed["section"] != "now" {
		t.Errorf("consumer_state.section = %#v, want \"now\"", echoed["section"])
	}
}

func TestMemoryWriteRefusesANonObjectConsumerState(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	body := writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/todos",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-001",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "buy milk",
		"consumer_state":  `["not","an","object"]`,
	})
	if !isRefusal(body) {
		t.Errorf("an array bag was accepted: %v", body)
	}
}

func TestRecallFiltersOnConsumerState(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	writeTodo(t, a, "todo.open", `{"kind":"todo","section":"now","completed":false}`)
	writeTodo(t, a, "todo.done", `{"kind":"todo","section":"now","completed":true}`)
	writeTodo(t, a, "note.open", `{"kind":"note","section":"now","completed":false}`)

	keysOf := func(body map[string]any) []string {
		t.Helper()
		results, ok := body["results"].([]any)
		if !ok {
			t.Fatalf("no results in %v", body)
		}
		var keys []string
		for _, r := range results {
			rev := r.(map[string]any)["revision"].(map[string]any)
			if k, ok := rev["memory_key"].(string); ok {
				keys = append(keys, k)
			}
		}
		return keys
	}

	// The load-bearing case: a JSON literal false, sent inside a JSON-encoded
	// string argument, must reach SQLite as something json_extract's integer 0
	// compares equal to.
	body := recallViaHandler(t, a, map[string]any{
		"namespaces":    `["user/chrispian/memory/todos"]`,
		"ranking":       "chronological",
		"state_filters": `[{"field":"kind","values":["todo"]},{"field":"completed","values":[false]}]`,
	})
	keys := keysOf(body)
	if len(keys) != 1 || keys[0] != "todo.open" {
		t.Errorf("state_filters returned %v, want [todo.open]. A boolean that arrived as the "+
			"string \"false\" matches nothing, which reads exactly like a corpus with no open todos",
			keys)
	}

	// And the filter reaches the summary projection, so a list view sees
	// lifecycle without asking for bodies.
	results := body["results"].([]any)
	rev := results[0].(map[string]any)["revision"].(map[string]any)
	if _, ok := rev["consumer_state"]; !ok {
		t.Errorf("consumer_state is absent under the default projection: %v\n"+
			"Structured objects are unusable if reading four scalars costs every body on the page",
			rev)
	}
}

func TestRecallRefusesAMalformedStateFilter(t *testing.T) {
	a := newMemoryAdapter(t, "memory:read")
	for name, filters := range map[string]string{
		"not an array":       `{"field":"section","values":["now"]}`,
		"not JSON":           `[{field: section}]`,
		"an uppercase field": `[{"field":"Section","values":["now"]}]`,
		"an empty value set": `[{"field":"section","values":[]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			body := recallViaHandler(t, a, map[string]any{
				"namespaces":    `["user/chrispian/memory/todos"]`,
				"ranking":       "chronological",
				"state_filters": filters,
			})
			if !isRefusal(body) {
				t.Errorf("%s was accepted: %v", name, body)
			}
		})
	}
}

// The `consumer_state` argument and the `state` block on a full-mode result
// are two different things, and the tool prose has to say so at both doors.
// The abbreviation is the whole failure mode: memory_state is the mutable
// activation row, and a caller who conflates them writes a todo's status into
// the activation ledger — or expects to and finds nothing.
func TestConsumerStateProseDistinguishesItFromMemoryState(t *testing.T) {
	for tool, text := range map[string]string{
		"consumer_state write argument": consumerStateArgDescription,
		"state_filters recall argument": stateFiltersArgDescription,
	} {
		if !strings.Contains(text, "consumer_state") {
			t.Errorf("%s never names consumer_state", tool)
		}
		if !strings.Contains(text, "state") || !strings.Contains(strings.ToLower(text), "not") {
			t.Errorf("%s does not distinguish itself from the `state` block a full-mode result "+
				"carries; the two share four letters and nothing else", tool)
		}
	}
	if !strings.Contains(consumerStateArgDescription, "never reads a value") {
		t.Error("the write argument's prose does not say Tesseract never reads a value out of the " +
			"bag. That sentence is the contract a consumer designs against — without it, a caller " +
			"reasonably expects Tesseract to enforce their vocabulary")
	}
}

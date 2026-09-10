package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func newEventAdapter(t *testing.T) (*Adapter, *memory.Store) {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	tok, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label:  "event",
		Scopes: []string{"memory:read", "memory:write", "write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	a := New(cs, tok)
	a.MemoryStore = ms
	a.EventStore = event.New(ms)
	return a, ms
}

func callEventTool(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return res.Content[0].(mcp.TextContent).Text
}

// TestEventWriteToolDescribesTheClosedTypeVocabulary. Same reasoning as the
// knowledge_write guard: an agent reads the tool description before it reads a
// skill, so a stale vocabulary here is the first thing it sees and the
// rejection is the last. The description renders memory.EventTypeList() rather
// than restating it.
func TestEventWriteToolDescribesTheClosedTypeVocabulary(t *testing.T) {
	a, _ := newEventAdapter(t)
	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	st, ok := srv.ListTools()["event_write"]
	if !ok {
		t.Fatal("event_write not registered")
	}
	schema, err := st.Tool.InputSchema.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	desc := string(schema)
	for _, typ := range memory.EventTypeAllowlist() {
		if !strings.Contains(desc, typ) {
			t.Errorf("event_write does not advertise the %q namespace type", typ)
		}
	}
	// The one claim the domain most needs an agent to read before writing.
	if !strings.Contains(st.Tool.Description, "NOT telemetry") {
		t.Error("event_write's description does not say it is not telemetry, which is the " +
			"single distinction that decides whether an agent is using the right system")
	}
}

func TestEventWriteAppendsAndEventListReadsBack(t *testing.T) {
	a, _ := newEventAdapter(t)

	for _, summary := range []string{"first thought", "second thought"} {
		raw := callEventTool(t, a.handleEventWrite, map[string]any{
			"namespace":       "user/chrispian/event/reasoning",
			"summary":         summary,
			"body":            "why: " + summary,
			"author_agent_id": "claude-code",
			"session_id":      "sess-1",
		})
		if strings.Contains(raw, `"code":"`) {
			t.Fatalf("event_write failed: %s", raw)
		}
		var rev memory.Revision
		if err := json.Unmarshal([]byte(raw), &rev); err != nil {
			t.Fatalf("unmarshal revision: %v (raw=%s)", err, raw)
		}
		if rev.MemoryKey != "" {
			t.Errorf("a keyless event write produced memory_key %q", rev.MemoryKey)
		}
		if rev.Status != memory.StatusCanonical {
			t.Errorf("status = %q, want canonical — a log entry makes no claim to settle", rev.Status)
		}
	}

	raw := callEventTool(t, a.handleEventList, map[string]any{
		"namespaces":   `["user/chrispian/event/reasoning"]`,
		"payload_mode": "full",
	})
	if strings.Contains(raw, `"code":"`) {
		t.Fatalf("event_list failed: %s", raw)
	}
	var got struct {
		Entries  []memory.Revision `json:"entries"`
		Manifest struct {
			Returned    int     `json:"returned"`
			Direction   string  `json:"direction"`
			HasMore     bool    `json:"has_more"`
			NextCursor  *string `json:"next_cursor"`
			PayloadMode string  `json:"payload_mode"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal log page: %v (raw=%s)", err, raw)
	}
	if got.Manifest.Returned != 2 || len(got.Entries) != 2 {
		t.Fatalf("returned %d entries, want 2: %s", len(got.Entries), raw)
	}
	if got.Entries[0].Payload.Summary != "second thought" {
		t.Errorf("first entry = %q, want the newest", got.Entries[0].Payload.Summary)
	}
	if got.Manifest.Direction != string(memory.LogNewestFirst) {
		t.Errorf("direction = %q, want %q", got.Manifest.Direction, memory.LogNewestFirst)
	}
	if got.Manifest.PayloadMode != string(memory.PayloadModeFull) {
		t.Errorf("payload_mode = %q, want full", got.Manifest.PayloadMode)
	}
	if got.Manifest.NextCursor != nil {
		t.Errorf("next_cursor = %q on a complete page, want null", *got.Manifest.NextCursor)
	}

	// The manifest must never grow a total; see EventLogManifest's doc.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(envelope["manifest"], &manifest); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"total", "results_total", "count"} {
		if _, present := manifest[forbidden]; present {
			t.Errorf("the log manifest carries %q; counting a log is a scan of the whole "+
				"partition on every page, which is the cost this read exists to avoid", forbidden)
		}
	}
}

func TestEventListValidatesItsArguments(t *testing.T) {
	a, _ := newEventAdapter(t)

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"no namespaces", map[string]any{}},
		{"bad direction", map[string]any{
			"namespaces": `["user/chrispian/event/reasoning"]`, "direction": "sideways"}},
		{"bad since", map[string]any{
			"namespaces": `["user/chrispian/event/reasoning"]`, "since": "yesterday"}},
		{"bad payload_mode", map[string]any{
			"namespaces": `["user/chrispian/event/reasoning"]`, "payload_mode": "everything"}},
		{"garbage cursor", map[string]any{
			"namespaces": `["user/chrispian/event/reasoning"]`, "cursor": "nonsense"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := callEventTool(t, a.handleEventList, tc.args)
			if !strings.Contains(raw, `"code":"validation_error"`) {
				t.Errorf("answered %s; want a validation_error", raw)
			}
		})
	}
}

// TestEventWriteRejectsAMemoryNamespace: the domain policy reaches the tool.
func TestEventWriteRejectsAMemoryNamespace(t *testing.T) {
	a, _ := newEventAdapter(t)
	raw := callEventTool(t, a.handleEventWrite, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"summary":         "wrong door",
		"author_agent_id": "claude-code",
		"session_id":      "sess-1",
	})
	if !strings.Contains(raw, `"code":"validation_error"`) {
		t.Errorf("answered %s; want a validation_error naming the namespace shape", raw)
	}
}

// TestEventToolsAreGatedOnTheirStore mirrors how memory_write and
// knowledge_write are gated: an adapter with no event store does not advertise
// tools that cannot work.
func TestEventToolsAreGatedOnTheirStore(t *testing.T) {
	cs := newTestStore(t)
	a := New(cs, "")
	a.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)
	tools := srv.ListTools()
	for _, name := range []string{"event_write", "event_list"} {
		if _, ok := tools[name]; ok {
			t.Errorf("%s is registered on an adapter with no event store", name)
		}
	}
}

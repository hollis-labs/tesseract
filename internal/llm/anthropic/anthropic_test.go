package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New("test-key", option.WithBaseURL(srv.URL+"/"), option.WithHTTPClient(srv.Client()))
}

func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal body: %v\nraw: %s", err, body)
	}
	return out
}

// messageResponse returns a minimal Messages API body. Caller passes the text
// payload to splice in.
func messageResponse(text string) string {
	return `{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[{"type":"text","text":"` + text + `"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`
}

func TestComplete_SystemPromptAndUserRole(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(messageResponse("the answer")))
	})

	answer, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:        "claude-test",
		SystemPrompt: "be concise",
		MaxTokens:    256,
		Messages:     []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/v1/messages") {
		t.Errorf("path = %q, want suffix /v1/messages", gotPath)
	}
	if answer != "the answer" {
		t.Errorf("answer = %q, want 'the answer'", answer)
	}
	if gotBody["model"] != "claude-test" {
		t.Errorf("model = %v, want claude-test", gotBody["model"])
	}
	// max_tokens propagated as a top-level field (anthropic requires it).
	if mt, ok := gotBody["max_tokens"]; !ok || mt.(float64) != 256 {
		t.Errorf("max_tokens = %v, want 256", gotBody["max_tokens"])
	}
	system, ok := gotBody["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("system = %v, want 1-entry text-block array", gotBody["system"])
	}
	if first := system[0].(map[string]any); first["text"] != "be concise" {
		t.Errorf("system[0].text = %v, want 'be concise'", first)
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %v, want 1 entry", gotBody["messages"])
	}
}

func TestComplete_DefaultMaxTokensWhenZero(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		if mt, ok := body["max_tokens"]; !ok || int(mt.(float64)) != DefaultMaxTokens {
			t.Errorf("max_tokens = %v, want DefaultMaxTokens (%d)", body["max_tokens"], DefaultMaxTokens)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(messageResponse("ok")))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "claude-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestComplete_AssistantRoleSupported(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		msgs := body["messages"].([]any)
		if len(msgs) != 2 {
			t.Errorf("len(messages) = %d, want 2", len(msgs))
		}
		if msgs[0].(map[string]any)["role"] != "user" {
			t.Errorf("messages[0].role = %v, want user", msgs[0])
		}
		if msgs[1].(map[string]any)["role"] != "assistant" {
			t.Errorf("messages[1].role = %v, want assistant", msgs[1])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(messageResponse("ok")))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model: "claude-test",
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "q"},
			{Role: "assistant", Content: "a"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestComplete_UnsupportedRoleReturnsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("HTTP must not be called for unsupported role")
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "claude-test",
		Messages: []llmtypes.ChatMessage{{Role: "system", Content: "no"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported role") {
		t.Errorf("err = %v, want unsupported-role error", err)
	}
}

func TestComplete_TextBlockExtraction(t *testing.T) {
	// Multiple text blocks plus a non-text block — only text should
	// concatenate into the answer string.
	body := `{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[
			{"type":"text","text":"part1 "},
			{"type":"thinking","thinking":"ignored"},
			{"type":"text","text":"part2"}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	answer, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "claude-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if answer != "part1 part2" {
		t.Errorf("answer = %q, want 'part1 part2'", answer)
	}
}

func TestComplete_NoTextBlocksReturnsError(t *testing.T) {
	body := `{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "claude-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no text blocks") {
		t.Errorf("err = %v, want no-text-blocks error", err)
	}
}

func TestStreamChat_NotImplemented(t *testing.T) {
	c := New("test-key")
	ch, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{Model: "claude"})
	if ch != nil || err == nil {
		t.Fatalf("StreamChat: got ch=%v err=%v, want nil/error", ch, err)
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("err = %v, want 'not implemented'", err)
	}
}

func TestCapabilities_HasReasonableDefaults(t *testing.T) {
	c := New("test-key")
	caps := c.Capabilities()
	if !caps.SupportsToolCalling {
		t.Error("SupportsToolCalling should be true")
	}
	if !caps.SupportsSystemPromptCaching {
		t.Error("SupportsSystemPromptCaching should be true")
	}
}

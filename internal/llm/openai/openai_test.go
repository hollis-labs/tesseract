package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/openai/openai-go/option"
)

// newTestClient wires a Client to an httptest.Server. The handler receives
// the raw request and writes a canned response. apiKey is set to a dummy
// value so the SDK doesn't reject startup.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New("test-key", option.WithBaseURL(srv.URL+"/"), option.WithHTTPClient(srv.Client()))
}

// readJSON drains and decodes the request body for assertion.
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

func TestEmbed_RequestShapeAndResponse(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],
			"model":"text-embedding-3-small",
			"usage":{"prompt_tokens":5,"total_tokens":5}
		}`))
	})

	res, err := c.Embed(context.Background(), "hello", "text-embedding-3-small")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/embeddings") {
		t.Errorf("expected path to end in /embeddings, got %q", gotPath)
	}
	if gotBody["model"] != "text-embedding-3-small" {
		t.Errorf("model = %v, want text-embedding-3-small", gotBody["model"])
	}
	if gotBody["input"] != "hello" {
		t.Errorf("input = %v, want hello", gotBody["input"])
	}
	if got := res.Embedding; len(got) != 3 || got[0] != 0.1 || got[2] != 0.3 {
		t.Errorf("Embedding = %v, want [0.1 0.2 0.3]", got)
	}
	if res.TokenCount != 5 {
		t.Errorf("TokenCount = %d, want 5", res.TokenCount)
	}
}

func TestEmbed_EmptyModelReturnsError(t *testing.T) {
	c := New("test-key")
	_, err := c.Embed(context.Background(), "hello", "")
	if err == nil {
		t.Fatal("expected error for empty model, got nil")
	}
}

func TestEmbedBatch_ArrayInputAndPerRowTokens(t *testing.T) {
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = readJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[
				{"object":"embedding","index":0,"embedding":[0.1,0.2]},
				{"object":"embedding","index":1,"embedding":[0.3,0.4]}
			],
			"model":"m",
			"usage":{"prompt_tokens":10,"total_tokens":10}
		}`))
	})

	res, err := c.EmbedBatch(context.Background(), []string{"a", "b"}, "m")
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	input, ok := gotBody["input"].([]any)
	if !ok || len(input) != 2 || input[0] != "a" || input[1] != "b" {
		t.Errorf("input batch = %v, want [a b]", gotBody["input"])
	}
	if len(res) != 2 {
		t.Fatalf("len(res) = %d, want 2", len(res))
	}
	// 10 total tokens / 2 rows = 5 per row.
	if res[0].TokenCount != 5 || res[1].TokenCount != 5 {
		t.Errorf("per-row tokens = %d/%d, want 5/5", res[0].TokenCount, res[1].TokenCount)
	}
}

func TestEmbedBatch_EmptySlice(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("HTTP must not be called for empty input")
	})
	res, err := c.EmbedBatch(context.Background(), nil, "m")
	if err != nil || res != nil {
		t.Errorf("empty input: got res=%v err=%v, want nil/nil", res, err)
	}
}

// completionsResponse is a minimal canned chat-completion body — just enough
// to exercise the fields Complete() reads.
const completionsResponse = `{
	"id":"cmpl_test",
	"object":"chat.completion",
	"created":1,
	"model":"gpt-test",
	"choices":[{"index":0,"message":{"role":"assistant","content":"answer-text"},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
}`

func TestComplete_SystemPromptAndUserRole(t *testing.T) {
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = readJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionsResponse))
	})

	answer, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:        "gpt-test",
		SystemPrompt: "you are concise",
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if answer != "answer-text" {
		t.Errorf("answer = %q, want answer-text", answer)
	}
	if gotBody["model"] != "gpt-test" {
		t.Errorf("model = %v, want gpt-test", gotBody["model"])
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v, want 2 entries (system+user)", gotBody["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "you are concise" {
		t.Errorf("system message = %v, want system/you-are-concise", first)
	}
	second := msgs[1].(map[string]any)
	if second["role"] != "user" || second["content"] != "hi" {
		t.Errorf("user message = %v, want user/hi", second)
	}
}

func TestComplete_AssistantRoleSupported(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		msgs := body["messages"].([]any)
		// User then assistant — both must be in the request, no system since none was set.
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
		_, _ = w.Write([]byte(completionsResponse))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model: "gpt-test",
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
		Model:    "gpt-test",
		Messages: []llmtypes.ChatMessage{{Role: "system", Content: "no"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported role") {
		t.Errorf("err = %v, want unsupported-role error", err)
	}
}

func TestComplete_MaxTokensPropagated(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		mc, ok := body["max_completion_tokens"]
		if !ok {
			t.Errorf("max_completion_tokens missing from request body: %v", body)
		}
		// JSON numbers decode as float64 by default.
		if mc.(float64) != 256 {
			t.Errorf("max_completion_tokens = %v, want 256", mc)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionsResponse))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:     "gpt-test",
		MaxTokens: 256,
		Messages:  []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestComplete_MaxTokensZeroOmittedFromRequest(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		if _, ok := body["max_completion_tokens"]; ok {
			t.Errorf("max_completion_tokens should be absent when ChatRequest.MaxTokens is 0; body=%v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionsResponse))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "gpt-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestComplete_EmptyChoicesReturnsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[]}`))
	})
	_, err := c.Complete(context.Background(), llmtypes.ChatRequest{
		Model:    "gpt-test",
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("err = %v, want empty-response error", err)
	}
}

func TestStreamChat_NotImplemented(t *testing.T) {
	c := New("test-key")
	ch, err := c.StreamChat(context.Background(), llmtypes.ChatRequest{Model: "gpt"})
	if ch != nil || err == nil {
		t.Fatalf("StreamChat: got ch=%v err=%v, want nil/error", ch, err)
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("err = %v, want 'not implemented'", err)
	}
}

func TestEmbeddingDimensions_ReturnsZero(t *testing.T) {
	c := New("test-key")
	if d := c.EmbeddingDimensions("text-embedding-3-large"); d != 0 {
		t.Errorf("EmbeddingDimensions = %d, want 0 (synchronous lookup intentionally unknown)", d)
	}
}

func TestCapabilities_HasReasonableDefaults(t *testing.T) {
	c := New("test-key")
	caps := c.Capabilities()
	if !caps.SupportsToolCalling {
		t.Error("SupportsToolCalling should be true")
	}
	if !caps.SupportsEmbedding {
		t.Error("SupportsEmbedding should be true")
	}
	if caps.DefaultEmbeddingModel == "" {
		t.Error("DefaultEmbeddingModel should be non-empty")
	}
}

// Package openai is a thin SDK-backed wrapper that satisfies
// embedcontracts.Embedder and the Complete/Capabilities portion of
// llmcontracts.Provider for vanta-conduit's embedding + synthesis paths.
//
// StreamChat is intentionally not implemented: vanta-conduit's synthesis
// route (POST /v1/synthesis/ask) is non-streaming, and embedding callers
// use the Embedder methods. Tools or future-streaming consumers should
// import an SDK-backed wrapper from a sibling module instead.
package openai

import (
	"context"
	"errors"
	"fmt"
	"os"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// Client wraps openai-go to expose both embedding and chat-completion paths
// through the portfolio's contract interfaces.
type Client struct {
	sdk sdk.Client
}

var (
	_ embedcontracts.Embedder = (*Client)(nil)
	_ llmcontracts.Provider   = (*Client)(nil)
)

// New builds a Client. apiKey defaults to OPENAI_API_KEY when empty.
// Additional SDK options are appended after the API-key option, allowing
// callers (notably tests) to override the base URL or HTTP client.
func New(apiKey string, opts ...option.RequestOption) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	base := []option.RequestOption{option.WithAPIKey(apiKey)}
	return &Client{sdk: sdk.NewClient(append(base, opts...)...)}
}

// Embed runs a single-input embedding request against the configured model.
func (c *Client) Embed(ctx context.Context, text, model string) (*embedcontracts.EmbeddingResult, error) {
	if model == "" {
		return nil, errors.New("openai embed: model is required")
	}
	resp, err := c.sdk.Embeddings.New(ctx, sdk.EmbeddingNewParams{
		Input: sdk.EmbeddingNewParamsInputUnion{OfString: sdk.String(text)},
		Model: sdk.EmbeddingModel(model),
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, errors.New("openai embed: empty response")
	}
	return &embedcontracts.EmbeddingResult{
		Embedding:  toFloat32(resp.Data[0].Embedding),
		TokenCount: int(resp.Usage.TotalTokens),
	}, nil
}

// EmbedBatch runs a batch embedding request in a single API call. Per-row
// token counts are not surfaced by the API, so total tokens are split
// evenly across rows.
func (c *Client) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	if model == "" {
		return nil, errors.New("openai embed-batch: model is required")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	resp, err := c.sdk.Embeddings.New(ctx, sdk.EmbeddingNewParams{
		Input: sdk.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model: sdk.EmbeddingModel(model),
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed-batch: %w", err)
	}
	out := make([]embedcontracts.EmbeddingResult, len(resp.Data))
	perRow := 0
	if len(resp.Data) > 0 {
		perRow = int(resp.Usage.TotalTokens) / len(resp.Data)
	}
	for i, e := range resp.Data {
		out[i] = embedcontracts.EmbeddingResult{
			Embedding:  toFloat32(e.Embedding),
			TokenCount: perRow,
		}
	}
	return out, nil
}

// EmbeddingDimensions returns 0 (unknown). The OpenAI API does not expose
// a synchronous dim lookup; vanta-conduit derives dimensions from the
// first embedding response.
func (c *Client) EmbeddingDimensions(_ string) int { return 0 }

// Complete runs a non-streaming chat completion. Used by /v1/synthesis/ask.
func (c *Client) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	msgs := make([]sdk.ChatCompletionMessageParamUnion, 0, 1+len(req.Messages))
	if sys := req.EffectiveSystemPrompt(); sys != "" {
		msgs = append(msgs, sdk.SystemMessage(sys))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			msgs = append(msgs, sdk.UserMessage(m.Content))
		case "assistant":
			msgs = append(msgs, sdk.AssistantMessage(m.Content))
		default:
			return "", fmt.Errorf("openai complete: unsupported role %q", m.Role)
		}
	}
	params := sdk.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = sdk.Int(int64(req.MaxTokens))
	}
	resp, err := c.sdk.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("openai complete: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("openai complete: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

// StreamChat is intentionally not implemented for vanta-conduit's
// synthesis path. See package doc.
func (c *Client) StreamChat(_ context.Context, _ llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return nil, errors.New("openai: StreamChat is not implemented in vanta-conduit's synthesis wrapper (Complete-only)")
}

// Capabilities reports a fixed default capability set tuned for the
// synthesis Complete + embedding paths.
func (c *Client) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{
		SupportsToolCalling:   true,
		SupportsImageInput:    true,
		SupportsEmbedding:     true,
		DefaultEmbeddingModel: "text-embedding-3-small",
	}
}

func toFloat32(vec []float64) []float32 {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return out
}

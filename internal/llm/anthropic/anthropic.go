// Package anthropic is a thin SDK-backed wrapper that satisfies the
// Complete/Capabilities portion of llmcontracts.Provider for vanta-conduit's
// synthesis path (POST /v1/synthesis/ask).
//
// StreamChat is intentionally not implemented: synthesis is non-streaming.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// DefaultMaxTokens is used when ChatRequest.MaxTokens is unset. Anthropic
// requires max_tokens; the synthesis route's typical answer fits well
// under this cap.
const DefaultMaxTokens = 4096

// Client wraps anthropic-sdk-go to expose the Complete + Capabilities
// portion of llmcontracts.Provider.
type Client struct {
	sdk sdk.Client
}

var _ llmcontracts.Provider = (*Client)(nil)

// New builds a Client. apiKey defaults to ANTHROPIC_API_KEY when empty.
func New(apiKey string) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	return &Client{sdk: sdk.NewClient(option.WithAPIKey(apiKey))}
}

// Complete runs a non-streaming Messages call.
func (c *Client) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	msgs := make([]sdk.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			msgs = append(msgs, sdk.NewUserMessage(sdk.NewTextBlock(m.Content)))
		case "assistant":
			msgs = append(msgs, sdk.NewAssistantMessage(sdk.NewTextBlock(m.Content)))
		default:
			return "", fmt.Errorf("anthropic complete: unsupported role %q", m.Role)
		}
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	params := sdk.MessageNewParams{
		Model:     req.Model,
		Messages:  msgs,
		MaxTokens: maxTokens,
	}
	if sys := req.EffectiveSystemPrompt(); sys != "" {
		params.System = []sdk.TextBlockParam{{Text: sys}}
	}
	resp, err := c.sdk.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("anthropic complete: %w", err)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("anthropic complete: response had no text blocks")
	}
	return b.String(), nil
}

// StreamChat is intentionally not implemented for vanta-conduit's
// synthesis path. See package doc.
func (c *Client) StreamChat(_ context.Context, _ llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return nil, errors.New("anthropic: StreamChat is not implemented in vanta-conduit's synthesis wrapper (Complete-only)")
}

// Capabilities reports a fixed default capability set.
func (c *Client) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{
		SupportsToolCalling:         true,
		SupportsImageInput:          true,
		SupportsSystemPromptCaching: true,
	}
}

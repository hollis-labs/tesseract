package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerParityTools adds MCP tools that mirror HTTP routes for agent parity.
//
// The view-evaluation peer of /v1/views/evaluate is no longer a tool of its
// own: it is the full_evaluation arm of context_view, registered in tools.go. Its
// handler still lives here, next to the other selector-shaped op.
func (a *Adapter) registerParityTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_estimate",
		mcp.WithDescription("Estimate record count, payload bytes, and rough token count for a selector without returning the records. Peer of HTTP /v1/context/estimate. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("selector", mcp.Required(), mcp.Description("JSON object matching contextstore.Selector (namespaces, keys, revision_scope, tags_any, types, statuses, limit)")),
	), a.handleContextEstimate)
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (a *Adapter) handleContextEstimate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := req.GetString("selector", "")
	if raw == "" {
		return toolError(codeValidationError, "selector is required"), nil
	}
	var sel contextstore.Selector
	if err := json.Unmarshal([]byte(raw), &sel); err != nil {
		return toolError(codeValidationError, "selector must be a JSON object: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	result, err := a.Store.Estimate(ctx, sel)
	if err != nil {
		return toolError(codeSelectorError, err.Error()), nil
	}
	return toolJSON(result), nil
}

// handleViewsEvaluate is the full_evaluation arm of context_view, and the exact
// peer of HTTP POST /v1/views/evaluate — including in what it does NOT do:
// neither surface filters results by the capability token's namespace globs.
// The default arm of context_view does. See viewFullEvaluationArgDescription.
//
// sel is resolved by handleContextView, which accepts both the JSON-selector
// and comma-separated-glob forms.
func (a *Adapter) handleViewsEvaluate(ctx context.Context, sel contextstore.Selector, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	includePayload := req.GetBool("include_payload", false)
	limit, err := wholeNumberArg(req, "limit", 0)
	if err != nil {
		return toolError(codeValidationError, err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	result, err := a.Store.Evaluate(ctx, sel, includePayload, limit)
	if err != nil {
		return toolError(codeSelectorError, err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	// Match the HTTP /v1/views/evaluate envelope: items + evaluation_meta.
	// Keeping the wire shape identical lets agents share parsers across both surfaces.
	return toolJSON(map[string]any{
		"items": result.Items,
		"evaluation_meta": map[string]any{
			"sort_keys":        result.SortKeys,
			"matched_count":    result.MatchedCount,
			"truncated":        result.Truncated,
			"normalized_scope": result.NormalizedScope,
		},
	}), nil
}

// wholeNumberArg reads an MCP number arg and rejects non-integer values
// (e.g. 2.5) instead of silently truncating. The HTTP peer decodes the
// matching field into Go's int and rejects fractions, so MCP behavior
// must match for parity.
func wholeNumberArg(req mcp.CallToolRequest, name string, def float64) (int, error) {
	v := req.GetFloat(name, def)
	if v != float64(int(v)) {
		return 0, fmt.Errorf("%s must be a whole number, got %v", name, v)
	}
	return int(v), nil
}

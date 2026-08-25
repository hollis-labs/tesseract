package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerParityTools adds MCP tools that mirror HTTP routes for agent parity:
// context_estimate, views_evaluate, memory_get_revision, and (when
// KnowledgeStore is present) knowledge_get + knowledge_history.
func (a *Adapter) registerParityTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_estimate",
		mcp.WithDescription("Estimate record count, payload bytes, and rough token count for a selector without returning the records. Peer of HTTP /v1/context/estimate. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("selector", mcp.Required(), mcp.Description("JSON object matching contextstore.Selector (namespaces, keys, revision_scope, tags_any, types, statuses, limit)")),
	), a.handleContextEstimate)

	a.addTool(s, mcp.NewTool("views_evaluate",
		mcp.WithDescription("Evaluate a view selector against the context store. Returns items plus evaluation metadata (sort keys, matched count, truncated flag, normalized scope). Peer of HTTP /v1/views/evaluate. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("selector", mcp.Required(), mcp.Description("JSON object matching contextstore.Selector")),
		mcp.WithBoolean("include_payload", mcp.Description("Include record payloads in the response (default false)")),
		mcp.WithNumber("limit", mcp.Description("Override selector.limit (0 = use selector or default)")),
	), a.handleViewsEvaluate)

	if a.MemoryStore != nil {
		a.addTool(s, mcp.NewTool("memory_get_revision",
			mcp.WithDescription(
				"**Fetch a memory revision by its revision_id.** Peer of HTTP /v1/memory/revisions/{id}.\n"+
					"• **Kind of content:** a single revision record, including body, facets, and lineage.\n"+
					"• **Scope:** `memory:read`.\n"+
					"• **Use this when:** a recall or history result referenced a revision_id and you want the full content.\n"+
					"• **Don't use this for:** resolving by (namespace, memory_key) — use `memory_get`.\n"+
					"• **Side effect:** reinforces the parent memory's activation/access_count — a deliberate read counts as use.\n"+
					"• **Deeper:** `tesseract_skills revisions`.",
			),
			mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to fetch (e.g. 01HX...)")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), a.handleMemoryGetRevision)
	}

	if a.KnowledgeStore != nil {
		a.addTool(s, mcp.NewTool("knowledge_get",
			mcp.WithDescription(
				"**Fetch the current knowledge revision** for `(namespace, memory_key)`. Peer of HTTP /v1/knowledge/current.\n"+
					"• **Kind of content:** the latest non-deprecated knowledge revision for this entry.\n"+
					"• **Scope:** `memory:read`.\n"+
					"• **Use this when:** you know the key and want the current pointer + summary + body.\n"+
					"• **Don't use this for:** full history (`knowledge_history`), cross-entry search (`tesseract_lookup`).\n"+
					"• **Deeper:** `tesseract_skills knowledge`.",
			),
			mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace (must contain 'knowledge' segment, e.g. user/chrispian/knowledge/framework)")),
			mcp.WithString("memory_key", mcp.Required(), mcp.Description("Knowledge key (e.g. framework.go-providers). Named memory_key for parity with memory tools — both stores share the Revision shape.")),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), a.handleKnowledgeGet)

		a.addTool(s, mcp.NewTool("knowledge_history",
			mcp.WithDescription(
				"**Fetch the revision history** for a knowledge entry, newest-first. Peer of HTTP /v1/knowledge/history.\n"+
					"• **Kind of content:** every revision under `(namespace, memory_key)`, including superseded.\n"+
					"• **Result shape:** a bare array by default. Pass `limit`, `cursor`, `budget_bytes` or `budget_tokens` and the response becomes `{results, manifest}`.\n"+
					"• **Scope:** `memory:read`.\n"+
					"• **Use this when:** you need to trace how a knowledge entry evolved (e.g. pointer churn, summary rewrites).\n"+
					"• **Don't use this for:** just the current value (`knowledge_get`).\n"+
					"• **Deeper:** `tesseract_skills revisions`.",
			),
			mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace")),
			mcp.WithString("memory_key", mcp.Required(), mcp.Description("Knowledge key. Named memory_key for parity with memory tools.")),
			mcp.WithNumber("limit", mcp.Description(historyLimitArgDescription)),
			mcp.WithString("cursor", mcp.Description(cursorArgDescription)),
			mcp.WithNumber("budget_bytes", mcp.Description(budgetBytesArgDescription)),
			mcp.WithNumber("budget_tokens", mcp.Description(budgetTokensArgDescription)),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		), a.handleKnowledgeHistory)
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (a *Adapter) handleContextEstimate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := req.GetString("selector", "")
	if raw == "" {
		return toolError("validation_error", "selector is required"), nil
	}
	var sel contextstore.Selector
	if err := json.Unmarshal([]byte(raw), &sel); err != nil {
		return toolError("validation_error", "selector must be a JSON object: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	result, err := a.Store.Estimate(ctx, sel)
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}
	return toolJSON(result), nil
}

func (a *Adapter) handleViewsEvaluate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := req.GetString("selector", "")
	if raw == "" {
		return toolError("validation_error", "selector is required"), nil
	}
	var sel contextstore.Selector
	if err := json.Unmarshal([]byte(raw), &sel); err != nil {
		return toolError("validation_error", "selector must be a JSON object: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	includePayload := req.GetBool("include_payload", false)
	limit, err := wholeNumberArg(req, "limit", 0)
	if err != nil {
		return toolError("validation_error", err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	result, err := a.Store.Evaluate(ctx, sel, includePayload, limit)
	if err != nil {
		return toolError("selector_error", err.Error()), nil //nolint:nilerr // MCP tool pattern
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

func (a *Adapter) handleMemoryGetRevision(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}
	revisionID := req.GetString("revision_id", "")
	if revisionID == "" {
		return toolError("validation_error", "revision_id is required"), nil
	}
	// Deliberate read: GetRevisionByIDReinforced bumps activation/access_count.
	rev, err := a.MemoryStore.GetRevisionByIDReinforced(ctx, revisionID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

func (a *Adapter) handleKnowledgeGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}
	namespace := req.GetString("namespace", "")
	key := req.GetString("memory_key", "")
	if namespace == "" || key == "" {
		return toolError("validation_error", "namespace and memory_key are required"), nil
	}
	rev, err := a.KnowledgeStore.GetCurrent(ctx, namespace, key)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

func (a *Adapter) handleKnowledgeHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}
	namespace := req.GetString("namespace", "")
	key := req.GetString("memory_key", "")
	if namespace == "" || key == "" {
		return toolError("validation_error", "namespace and memory_key are required"), nil
	}
	pr, errRes := a.resolvePageRequest(req, memory.PayloadModeFull)
	if errRes != nil {
		return errRes, nil
	}
	revs, err := a.KnowledgeStore.GetHistory(ctx, namespace, key)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	if !pr.Engaged() {
		return toolJSON(revs), nil
	}
	page, err := memory.PageRevisions(revs, pr,
		memory.HistoryOrderingFingerprint(string(domains.Knowledge), namespace, key))
	if err != nil {
		if errors.Is(err, memory.ErrInvalidCursor) {
			return toolError("validation_error", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(page), nil
}

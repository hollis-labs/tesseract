package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerKnowledgeTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("knowledge_write",
		mcp.WithDescription(
			"**Write a knowledge revision** — a pointer-first reference to external content.\n"+
				"• **Kind of content:** package / doc / note / pointer records with `kind`/`source`/`pointer` facets.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** you are cataloging something that lives outside Vanta (a file, URL, library, doc).\n"+
				"• **Don't use this for:** agent-authored content with no external source — use `memory_write`. Generic records — use `context_write`.\n"+
				"• **Deeper:** `vanta_skills knowledge` for patterns; `vanta_skills facets-and-kinds` for facet vocabulary.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace; must contain a 'knowledge' segment (e.g. user/chrispian/knowledge/framework)")),
		mcp.WithString("key", mcp.Description("Optional logical key (slug, path, id) — same key on re-write creates a new revision")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Facet: the kind of entry (e.g. package, doc, note, pointer)")),
		mcp.WithString("source", mcp.Required(), mcp.Description("Facet: where this knowledge came from (e.g. filesystem, obsidian, nil, web, manual)")),
		mcp.WithString("pointer_scheme", mcp.Required(), mcp.Description("Pointer scheme (e.g. file, http, https, obsidian, nil)")),
		mcp.WithString("pointer_locator", mcp.Required(), mcp.Description("Pointer locator: scheme-specific address (path, URL, vault id, ...)")),
		mcp.WithString("pointer_resolved_at", mcp.Description("Optional RFC3339 timestamp for when the pointer was last verified. Defaults to now.")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("Short summary text (feeds embeddings)")),
		mcp.WithString("body", mcp.Description("Optional longer body (feeds embeddings when present)")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the writer")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier")),
		mcp.WithString("tags", mcp.Description("Optional JSON array of string tags")),
		mcp.WithNumber("ttl_seconds", mcp.Description("Optional TTL in seconds (0 = no expiry)")),
		mcp.WithNumber("confidence", mcp.Description("Confidence score in [0, 1.0] (default 0.9)")),
		mcp.WithString("supersedes", mcp.Description("Optional revision_id this entry supersedes")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleKnowledgeWrite)
}

func (a *Adapter) handleKnowledgeWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	var tags []string
	if raw := req.GetString("tags", ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			return toolError("validation_error", "tags must be a JSON array of strings"), nil //nolint:nilerr // MCP tool pattern
		}
	}

	pointer := memory.Pointer{
		Scheme:  req.GetString("pointer_scheme", ""),
		Locator: req.GetString("pointer_locator", ""),
	}
	if raw := req.GetString("pointer_resolved_at", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError("validation_error", "pointer_resolved_at must be RFC3339: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
		}
		pointer.ResolvedAt = &t
	}

	ttlSeconds := int64(req.GetFloat("ttl_seconds", 0))

	in := knowledge.WriteInput{
		Namespace:  req.GetString("namespace", ""),
		Key:        req.GetString("key", ""),
		Kind:       req.GetString("kind", ""),
		Source:     req.GetString("source", ""),
		Pointer:    pointer,
		Summary:    req.GetString("summary", ""),
		Body:       req.GetString("body", ""),
		Author: memory.Author{
			AgentID:      req.GetString("author_agent_id", ""),
			AgentVersion: req.GetString("author_version", ""),
		},
		SessionID:  req.GetString("session_id", ""),
		Tags:       tags,
		TTL:        time.Duration(ttlSeconds) * time.Second,
		Confidence: req.GetFloat("confidence", 0),
		Supersedes: req.GetString("supersedes", ""),
	}

	rev, err := a.KnowledgeStore.Write(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError("validation_error", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

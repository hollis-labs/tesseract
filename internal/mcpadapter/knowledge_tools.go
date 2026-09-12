package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// domainBoundaryLine is the memory/knowledge fork, in the shortest form that
// decides a write. It is ONE string used by both write tools on purpose: the
// full statement lives in skills/start-here.md, and this is the only place the
// short form exists, so the two tools cannot drift apart from each other or
// from the skill.
//
// It replaces "agent-authored content with no external source — use
// memory_write", which was false for every populated knowledge kind that has an
// agent as its author: `investigation`, `session_close` and `project_canonical`
// are all agent-written with nothing outside Tesseract to point at. DerivedFrom
// never decided this; how the content gets found again does. See
// docs/knowledge-memory-boundary.md for the corpus test behind that.
const domainBoundaryLine = "• **Which domain:** **knowledge is content you go TO** — addressed by key, read whole, expected to stay true. " +
	"**Memory is content that comes TO you** — recall surfaces it while you are working on something nearby, dated, superseded rather than edited. " +
	"DerivedFrom does not decide this: both domains are mostly agent-authored, and a knowledge entry with no external source is normal (`pointer_scheme: \"nil\"`). " +
	"**\"I might look this up later\" is not the test** — nearly all memory is looked up eventually. The test is whether you could NAME it before you went looking. " +
	"Full statement, with what it does not cover: `tesseract_skills start-here`.\n"

func (a *Adapter) registerKnowledgeTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("knowledge_write",
		mcp.WithDescription(
			"**Write a knowledge revision** — reference content a later session will go looking for by name.\n"+
				"• **Read this first:** call `tesseract_skills knowledge` before composing a body. It carries the canonical request shape as a copy-pasteable payload on both this surface and HTTP (which nests `pointer` and `author` where this one takes them flat), and states what belongs in `body` versus `pointer_locator` — the single decision that determines whether the entry still carries anything once the pointer rots.\n"+
				"• **Kind of content:** records carrying `kind`/`source`/`pointer` facets. `kind` is a closed vocabulary — see the `kind` parameter.\n"+
				"• **Scope:** `memory:write`.\n"+
				domainBoundaryLine+
				"• **Use this when:** a project's canonical, a handoff, a playbook, an investigation dossier, a doc or package reference — something someone will come back for deliberately. Whether it points at anything outside Tesseract is a separate question: `pointer_scheme: \"nil\"` is a first-class answer.\n"+
				"• **Don't use this for:** content nobody would know to ask for — a decision and its rationale, a limitation, a deferred follow-up, what a session learned. Recall is how those get found, so they are `memory_write`. Generic records — use `context_write`.\n"+
				"• **Deeper:** `tesseract_skills facets-and-kinds` for facet vocabulary.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Knowledge namespace; must contain a 'knowledge' segment (e.g. user/chrispian/knowledge/framework)")),
		mcp.WithString("key", mcp.Description("Optional logical key (slug, path, id) — same key on re-write creates a new revision. "+
			"Free-form: knowledge keys are NOT held to the memory domain's lowercase dot-notation rule, so hyphens, slashes and mixed case from an external source are accepted as written.")),
		// The allowed set is rendered from the enforced vocabulary rather than
		// restated, so this description cannot advertise a set the write path
		// does not accept.
		mcp.WithString("kind", mcp.Required(), mcp.Description(
			"Facet: the kind of entry. Closed vocabulary — any other value is rejected. Allowed: "+
				memory.KnowledgeKindList())),
		mcp.WithString("source", mcp.Required(), mcp.Description("Facet: where this knowledge came from (e.g. filesystem, obsidian, nil, web, manual)")),
		mcp.WithString("pointer_scheme", mcp.Required(), mcp.Description("Pointer scheme (e.g. file, http, https, obsidian, nil)")),
		mcp.WithString("pointer_locator", mcp.Required(), mcp.Description("Pointer locator: scheme-specific address (path, URL, vault id, ...)")),
		mcp.WithString("pointer_resolved_at", mcp.Description("Optional RFC3339 timestamp for when the pointer was last verified. Defaults to now.")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("Short summary text (feeds embeddings)")),
		mcp.WithString("body", mcp.Description("Optional longer body (feeds embeddings when present)")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the writer")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier")),
		mcp.WithString("consumer_state", mcp.Description(consumerStateArgDescription)),
		mcp.WithString("payload_data", mcp.Description(payloadDataArgDescription)),
		mcp.WithString("payload_data_schema_hash", mcp.Description(payloadDataSchemaHashArgDescription)),
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

	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	pointer := memory.Pointer{
		Scheme:  req.GetString("pointer_scheme", ""),
		Locator: req.GetString("pointer_locator", ""),
	}
	if raw := req.GetString("pointer_resolved_at", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError(codeValidationError, "pointer_resolved_at must be RFC3339: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
		}
		pointer.ResolvedAt = &t
	}

	ttlSeconds := int64(req.GetFloat("ttl_seconds", 0))

	in := knowledge.WriteInput{
		Namespace:      req.GetString("namespace", ""),
		Key:            req.GetString("key", ""),
		Kind:           req.GetString("kind", ""),
		Source:         req.GetString("source", ""),
		Pointer:        pointer,
		Summary:        req.GetString("summary", ""),
		Body:           req.GetString("body", ""),
		Data:           payloadDataArg(req),
		DataSchemaHash: req.GetString("payload_data_schema_hash", ""),
		Author: memory.Author{
			AgentID:      req.GetString("author_agent_id", ""),
			AgentVersion: req.GetString("author_version", ""),
		},
		SessionID:     req.GetString("session_id", ""),
		Tags:          tags,
		TTL:           time.Duration(ttlSeconds) * time.Second,
		Confidence:    req.GetFloat("confidence", 0),
		Supersedes:    req.GetString("supersedes", ""),
		ConsumerState: consumerStateArg(req),
	}

	rev, err := a.KnowledgeStore.Write(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

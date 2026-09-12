package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerMemoryTools(s *server.MCPServer) {
	// ── memory_write ─────────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_write",
		mcp.WithDescription(
			"**Append an agent memory revision** under `(namespace, memory_key)`.\n"+
				"• **Read this first:** call `tesseract_skills memory` before composing a write. It carries the complete request shape as a copy-pasteable payload — for this surface AND for the HTTP peer, which nests the same fields differently. Seven arguments are required, and `trigger`, `derived_from` and the namespace `{type}` segment are closed vocabularies; the skill is faster than finding that out one validation_error at a time.\n"+
				"• **Kind of content:** agent observations, preferences, session notes — content you'll want to recall by similarity, activation, or chronological order.\n"+
				"• **Scope:** `memory:write`.\n"+
				domainBoundaryLine+
				"• **Use this when:** a decision and its rationale, a limitation, a deferred follow-up, feedback, an outcome, what a session learned — something a later session should meet while working nearby, without having known to ask for it.\n"+
				"• **Don't use this for:** content someone will come back for by name — a project's canonical, a handoff, a playbook, a doc or package reference. That is `knowledge_write`, whether or not it points at anything outside Tesseract. Generic revisioned records — `context_write`.\n"+
				"• **Deeper:** `tesseract_skills namespaces` for namespace rules.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Typed memory namespace {scope}/{id}/memory/{type} — e.g. project/tesseract/memory/decisions. "+
			"Scope is one of: "+memory.ScopeList()+"; `system` is a singleton and takes no id (system/memory/{type}). "+
			"Allowed types: "+memory.TypeList()+". `notes` is the deliberate catch-all when no stronger type fits.")),
		mcp.WithString("memory_key", mcp.Description("Optional logical key for keyed memories (e.g. user.prefs.style). "+
			"Dot-separated segments, each matching ^[a-z0-9_]+$ — lowercase letters, digits and underscore only. "+
			"At most 6 segments, 64 characters per segment, 256 characters total. "+
			"Hyphens, uppercase and spaces are REJECTED with a validation_error, not normalized away: `user.prefs-style` and `User.Prefs.Style` both fail. "+
			"This rule is memory-domain only; `knowledge_write` keys are free-form slugs.")),
		mcp.WithString("supersedes", mcp.Description("Revision ID this revision supersedes (e.g. 01HX...)")),
		mcp.WithString("status", mcp.Description("Status: draft|reviewed|canonical (default: draft)")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the author (e.g. claude, nanite)")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("trigger", mcp.Required(), mcp.Description("Trigger: explicit|post_compact|per_turn|promotion|manual")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier (e.g. 2026-04-19:backend)")),
		mcp.WithString("derived_from", mcp.Required(), mcp.Description("DerivedFrom: user|feedback|project|reference|observation")),
		mcp.WithNumber("confidence", mcp.Required(), mcp.Description("Confidence score in [0, 1.0] (e.g. 0.9)")),
		mcp.WithString("tags", mcp.Description("JSON array of string tags (e.g. [\"preference\",\"style\"])")),
		mcp.WithNumber("ttl_seconds", mcp.Description("Time-to-live in seconds (0 = no expiry)")),
		mcp.WithString("payload_summary", mcp.Required(), mcp.Description("Summary text for the memory payload")),
		mcp.WithString("payload_body", mcp.Description("Optional body text for the memory payload")),
		mcp.WithString("consumer_state", mcp.Description(consumerStateArgDescription)),
		mcp.WithString("payload_data", mcp.Description(payloadDataArgDescription)),
		mcp.WithString("payload_data_schema_hash", mcp.Description(payloadDataSchemaHashArgDescription)),
		mcp.WithString("dedup", mcp.Description("Dedup mode: none (default) or semantic")),
		mcp.WithNumber("dedup_threshold", mcp.Description("Similarity threshold override for semantic dedup (0 = use config default 0.85)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryWrite)

	// ── memory_promote ───────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_promote",
		mcp.WithDescription(
			"**Promote a session-scoped memory** to user or project scope.\n"+
				"• **Read this first:** call `tesseract_skills promotion` before promoting. It carries a worked call on both surfaces and the two invariants this tool enforces silently in the namespace strings — source must be session-scoped, and the `{type}` segment must be identical on both sides.\n"+
				"• **Kind of content:** a copy of the source memory revision, re-scoped to the target namespace.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** a session memory has proven durable and you want it to survive session boundaries.\n"+
				"• **Don't use this for:** cross-ownership promotion (app/* → user/*) — use the `context_promote` three-stage workflow.",
		),
		mcp.WithString("source_namespace", mcp.Required(), mcp.Description("Source session memory namespace user/{id}/session/{sid}/memory/{type} (e.g. user/chrispian/session/2026-04-19:backend/memory/decisions)")),
		mcp.WithString("source_memory_id", mcp.Required(), mcp.Description("Source memory ID to promote")),
		mcp.WithString("target_namespace", mcp.Required(), mcp.Description("Target user or project memory namespace; the {type} segment MUST match the source (e.g. user/chrispian/memory/decisions)")),
		mcp.WithString("actor_agent_id", mcp.Required(), mcp.Description("Agent ID performing the promotion")),
		mcp.WithString("actor_version", mcp.Description("Agent version string")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryPromote)

}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (a *Adapter) handleMemoryWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	// `origin` was this argument's name until 2026-09-12. Refused, not
	// ignored: mcp-go does not set additionalProperties:false, so an argument
	// the tool no longer declares still arrives at the handler with nothing
	// reading it. An ignored `origin` would leave `derived_from` empty, and a
	// caller who supplied a value would be told a required field is missing —
	// a confusing error about the wrong field. Worse, had this field carried a
	// default, the write would have SUCCEEDED at the wrong ranking weight.
	if errResult := rejectRetiredArg(req, "origin",
		"this field is now named `derived_from`. The five values are unchanged "+
			"(user, feedback, project, reference, observation) — only the name moved, "+
			"because `origin` read as *who originated this* and was being filled in as "+
			"an authorship claim. It is a recall ranking multiplier, so the value matters "+
			"beyond labeling; see `tesseract_skills memory`."); errResult != nil {
		return errResult, nil
	}

	// Parse tags — accept both native JSON array and JSON-encoded string.
	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	ttlSeconds := int64(req.GetFloat("ttl_seconds", 0))

	in := memory.WriteInput{
		// `memory_write` is the memory surface and declares no `domain`
		// argument, so the caller has nothing to fill in. The domain is a
		// property of the TOOL, and it is set here rather than defaulted in
		// the store — see errDomainRequired in internal/memory/write.go.
		Domain:     domains.Memory,
		Namespace:  req.GetString("namespace", ""),
		MemoryKey:  req.GetString("memory_key", ""),
		Supersedes: req.GetString("supersedes", ""),
		Status:     memory.Status(req.GetString("status", "")),
		Author: memory.Author{
			AgentID:      req.GetString("author_agent_id", ""),
			AgentVersion: req.GetString("author_version", ""),
		},
		Trigger:     memory.Trigger(req.GetString("trigger", "")),
		SessionID:   req.GetString("session_id", ""),
		DerivedFrom: memory.DerivedFrom(req.GetString("derived_from", "")),
		Confidence:  req.GetFloat("confidence", 0),
		Tags:        tags,
		TTL:         time.Duration(ttlSeconds) * time.Second,
		Payload: memory.Payload{
			Summary:        req.GetString("payload_summary", ""),
			Body:           req.GetString("payload_body", ""),
			Data:           payloadDataArg(req),
			DataSchemaHash: req.GetString("payload_data_schema_hash", ""),
		},
		Dedup:          req.GetString("dedup", ""),
		DedupThreshold: req.GetFloat("dedup_threshold", 0),
		ConsumerState:  consumerStateArg(req),
	}

	rev, err := a.MemoryStore.WriteRevision(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

// handleTesseractTouch backs the tesseract_touch tool. Scope is memory:read
// rather than memory:write on purpose — see the tool description.
//
// The batch cap is enforced by the store rather than restated here, so this door
// and its HTTP peer refuse the same size with the same message by construction.
func (a *Adapter) handleTesseractTouch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	revisionIDs, present, err := parseStringArrayArg(req, "revision_ids")
	if err != nil {
		return toolError(codeValidationError, "revision_ids "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	if !present {
		return toolError(codeValidationError, "revision_ids is required"), nil
	}

	res, err := a.revisionStore().TouchRevisions(ctx, revisionIDs)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(res), nil
}

func (a *Adapter) handleMemoryPromote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	in := memory.PromoteInput{
		SourceNamespace: req.GetString("source_namespace", ""),
		SourceMemoryID:  req.GetString("source_memory_id", ""),
		TargetNamespace: req.GetString("target_namespace", ""),
		ActorAgentID:    req.GetString("actor_agent_id", ""),
		ActorVersion:    req.GetString("actor_version", ""),
	}

	promoted, err := a.MemoryStore.Promote(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		if errors.Is(err, memory.ErrNotFound) {
			return toolError(codeNotFound, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(promoted), nil
}

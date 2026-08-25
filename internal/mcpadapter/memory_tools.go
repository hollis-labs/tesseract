package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerMemoryTools(s *server.MCPServer) {
	// ── memory_write ─────────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_write",
		mcp.WithDescription(
			"**Append an agent memory revision** under `(namespace, memory_key)`.\n"+
				"• **Kind of content:** agent observations, preferences, session notes — content you'll want to recall by similarity, activation, or chronological order.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** the content is authored by you or another agent and belongs to the memory domain.\n"+
				"• **Don't use this for:** pointer-to-external-content (`knowledge_write`) or generic revisioned records (`context_write`).\n"+
				"• **Deeper:** `tesseract_skills memory` for patterns; `tesseract_skills namespaces` for namespace rules.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Typed memory namespace user/{id}/memory/{type} (e.g. user/chrispian/memory/decisions). Allowed types: decisions, feedback, followups, learnings, limitations, notes, outcomes, references.")),
		mcp.WithString("memory_key", mcp.Description("Optional logical key for keyed memories (e.g. user.prefs.style)")),
		mcp.WithString("supersedes", mcp.Description("Revision ID this revision supersedes (e.g. 01HX...)")),
		mcp.WithString("status", mcp.Description("Status: draft|reviewed|canonical (default: draft)")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the author (e.g. claude, nanite)")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("trigger", mcp.Required(), mcp.Description("Trigger: explicit|post_compact|per_turn|promotion|manual")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier (e.g. 2026-04-19:backend)")),
		mcp.WithString("origin", mcp.Required(), mcp.Description("Origin: user|feedback|project|reference|observation")),
		mcp.WithNumber("confidence", mcp.Required(), mcp.Description("Confidence score in [0, 1.0] (e.g. 0.9)")),
		mcp.WithString("tags", mcp.Description("JSON array of string tags (e.g. [\"preference\",\"style\"])")),
		mcp.WithNumber("ttl_seconds", mcp.Description("Time-to-live in seconds (0 = no expiry)")),
		mcp.WithString("payload_summary", mcp.Required(), mcp.Description("Summary text for the memory payload")),
		mcp.WithString("payload_body", mcp.Description("Optional body text for the memory payload")),
		mcp.WithString("dedup", mcp.Description("Dedup mode: none (default) or semantic")),
		mcp.WithNumber("dedup_threshold", mcp.Description("Similarity threshold override for semantic dedup (0 = use config default 0.85)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryWrite)

	// ── memory_get ───────────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_get",
		mcp.WithDescription(
			"**Get the current (head) revision** for a keyed memory.\n"+
				"• **Kind of content:** the latest revision under `(namespace, memory_key)`, deprecations skipped.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you have a specific key and want its current value.\n"+
				"• **Don't use this for:** revision history (`memory_history`), ranked recall (`memory_recall`), or a specific revision by ID (`memory_get_revision`).\n"+
				"• **Side effect:** reinforces the memory's activation/access_count — a deliberate read counts as use, unlike `memory_recall`.\n"+
				"• **Deeper:** `tesseract_skills memory`.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Typed memory namespace user/{id}/memory/{type} (e.g. user/chrispian/memory/decisions). Allowed types: decisions, feedback, followups, learnings, limitations, notes, outcomes, references.")),
		mcp.WithString("memory_key", mcp.Required(), mcp.Description("Logical memory key")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryGet)

	// ── memory_history ───────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_history",
		mcp.WithDescription(
			"**Get the full revision history** for a keyed memory, newest-first.\n"+
				"• **Kind of content:** every revision under `(namespace, memory_key)`, including superseded and deprecated ones.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you need to trace how a memory evolved, or inspect superseded content.\n"+
				"• **Don't use this for:** just the current value (`memory_get`).\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Memory namespace")),
		mcp.WithString("memory_key", mcp.Required(), mcp.Description("Logical memory key")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryHistory)

	// ── memory_recall ────────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_recall",
		mcp.WithDescription(
			"**Ranked recall across namespaces.** Multi-knob: activation / chronological / similarity / relevance.\n"+
				"• **Kind of content:** ranked list of memory revisions matching namespaces + filters.\n"+
				"• **Result shape:** array of `{revision, score, state}`, best first.\n"+
				"• **`score`:** ranking-relative, comparable only within one response. `activation` → activation strength; `similarity` → cosine similarity (can be 0 or negative); `relevance` → RRF-fused BM25 + cosine. **Absent under `chronological`** — order is carried by array order plus `revision.created_at`.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you want the best-match memories for a query or the top-of-mind memories without a query.\n"+
				"• **Don't use this for:** cross-domain search — `tesseract_lookup` spans memory + knowledge. Deterministic selection — use `context_view` / `views_evaluate`.\n"+
				"• **Deeper:** `tesseract_skills recall-and-ranking` for ranking modes; `tesseract_skills memory` for patterns.",
		),
		mcp.WithString("namespaces", mcp.Required(), mcp.Description("JSON array of memory namespace strings. Use typed form user/{id}/memory/{type} (e.g. [\"user/chrispian/memory/decisions\"]) or the legacy/prefix form user/{id}/memory (e.g. [\"user/chrispian/memory\"]) to span every typed sub-namespace under that scope. Allowed types: decisions, feedback, followups, learnings, limitations, notes, outcomes, references.")),
		mcp.WithString("revision_scope", mcp.Description("current or timeline (default: current)")),
		mcp.WithString("ranking", mcp.Description("activation, chronological, similarity, or relevance (default: relevance when query is set, else activation)")),
		mcp.WithString("query", mcp.Description("Semantic query string (required for similarity or relevance ranking)")),
		mcp.WithString("origins", mcp.Description("JSON array of origin filter values")),
		mcp.WithString("statuses", mcp.Description("JSON array of status filter values")),
		mcp.WithString("tags", mcp.Description("JSON array of tag filter values")),
		mcp.WithNumber("confidence_min", mcp.Description("Minimum confidence threshold")),
		mcp.WithString("since", mcp.Description("RFC3339 timestamp lower bound")),
		mcp.WithString("until", mcp.Description("RFC3339 timestamp upper bound")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 30, max 500)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryRecall)

	// ── memory_promote ───────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_promote",
		mcp.WithDescription(
			"**Promote a session-scoped memory** to user or project scope.\n"+
				"• **Kind of content:** a copy of the source memory revision, re-scoped to the target namespace.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** a session memory has proven durable and you want it to survive session boundaries.\n"+
				"• **Don't use this for:** cross-ownership promotion (app/* → user/*) — use the `context_promote_*` three-stage workflow.\n"+
				"• **Deeper:** `tesseract_skills promotion`.",
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

	// ── memory_deprecate ─────────────────────────────────────────────────────
	a.addTool(s, mcp.NewTool("memory_deprecate",
		mcp.WithDescription(
			"**Soft-remove a memory revision** by revision ID.\n"+
				"• **Kind of content:** a deprecation event on a specific revision. Revision stays in history.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** a revision is wrong, outdated, or should no longer appear in current recall.\n"+
				"• **Don't use this for:** replacing content — write a new revision with `supersedes`. Hard deletes — not supported (history is canonical).\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to deprecate (e.g. 01HX...)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleMemoryDeprecate)
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (a *Adapter) handleMemoryWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	// Parse tags — accept both native JSON array and JSON-encoded string.
	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError("validation_error", "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	ttlSeconds := int64(req.GetFloat("ttl_seconds", 0))

	in := memory.WriteInput{
		Namespace:  req.GetString("namespace", ""),
		MemoryKey:  req.GetString("memory_key", ""),
		Supersedes: req.GetString("supersedes", ""),
		Status:     memory.Status(req.GetString("status", "")),
		Author: memory.Author{
			AgentID:      req.GetString("author_agent_id", ""),
			AgentVersion: req.GetString("author_version", ""),
		},
		Trigger:    memory.Trigger(req.GetString("trigger", "")),
		SessionID:  req.GetString("session_id", ""),
		Origin:     memory.Origin(req.GetString("origin", "")),
		Confidence: req.GetFloat("confidence", 0),
		Tags:       tags,
		TTL:        time.Duration(ttlSeconds) * time.Second,
		Payload: memory.Payload{
			Summary: req.GetString("payload_summary", ""),
			Body:    req.GetString("payload_body", ""),
		},
		Dedup:          req.GetString("dedup", ""),
		DedupThreshold: req.GetFloat("dedup_threshold", 0),
	}

	rev, err := a.MemoryStore.WriteRevision(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError("validation_error", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

func (a *Adapter) handleMemoryGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	namespace := req.GetString("namespace", "")
	memoryKey := req.GetString("memory_key", "")

	// Deliberate read: GetCurrentReinforced bumps activation/access_count.
	rev, err := a.MemoryStore.GetCurrentReinforced(ctx, namespace, memoryKey)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

func (a *Adapter) handleMemoryHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	namespace := req.GetString("namespace", "")
	memoryKey := req.GetString("memory_key", "")

	revs, err := a.MemoryStore.GetHistory(ctx, namespace, memoryKey)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(revs), nil
}

func (a *Adapter) handleMemoryRecall(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	// Parse namespaces — accept both native array and stringified.
	namespaces, _, err := parseStringArrayArg(req, "namespaces")
	if err != nil {
		return toolError("validation_error", "namespaces "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	originStrs, _, err := parseStringArrayArg(req, "origins")
	if err != nil {
		return toolError("validation_error", "origins "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	var origins []memory.Origin
	for _, s := range originStrs {
		origins = append(origins, memory.Origin(s))
	}

	statusStrs, _, err := parseStringArrayArg(req, "statuses")
	if err != nil {
		return toolError("validation_error", "statuses "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	var statuses []memory.Status
	for _, s := range statusStrs {
		statuses = append(statuses, memory.Status(s))
	}

	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError("validation_error", "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	var since *time.Time
	if raw := req.GetString("since", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError("validation_error", "since must be RFC3339: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
		}
		since = &t
	}

	var until *time.Time
	if raw := req.GetString("until", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError("validation_error", "until must be RFC3339: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
		}
		until = &t
	}

	in := memory.RecallInput{
		Namespaces:    namespaces,
		RevisionScope: memory.RevisionScope(req.GetString("revision_scope", "")),
		Ranking:       memory.Ranking(req.GetString("ranking", "")),
		Query:         req.GetString("query", ""),
		Filters: memory.RecallFilters{
			Origins:       origins,
			Statuses:      statuses,
			Tags:          tags,
			ConfidenceMin: req.GetFloat("confidence_min", 0),
			Since:         since,
			Until:         until,
		},
		Limit: int(req.GetFloat("limit", 0)),
	}

	results, err := a.MemoryStore.Recall(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrSimilarityUnavailable) {
			return toolError("similarity_unavailable", err.Error()), nil
		}
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError("validation_error", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(results), nil
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
			return toolError("validation_error", err.Error()), nil
		}
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(promoted), nil
}

func (a *Adapter) handleMemoryDeprecate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	revisionID := req.GetString("revision_id", "")
	if revisionID == "" {
		return toolError("validation_error", "revision_id is required"), nil
	}

	err := a.MemoryStore.Deprecate(ctx, revisionID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(map[string]string{"status": "deprecated", "revision_id": revisionID}), nil
}

package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerEventTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("event_write",
		mcp.WithDescription(
			"**Append one entry to the event log** — an agent's reasoning about what it is doing, or a personal log/journal entry.\n"+
				"• **Kind of content:** narrative prose. What you were trying, why you chose this over that, what you noticed. The value is the reasoning a trace throws away.\n"+
				"• **NOT telemetry.** No spans, no metrics, no structured timing. If what you are recording is a duration or a count, this is the wrong tool and Tesseract is the wrong system.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Append-only by default:** `key` is optional and usually omitted. A log entry records that something HAPPENED; it has a timestamp, not a current value. Pass a key only when the entry genuinely has a revisable identity.\n"+
				"• **Use this when:** you want a future session to be able to reconstruct why, not just what.\n"+
				"• **Don't use this for:** a settled conclusion worth recalling on its own — that is `memory_write`. An external reference — `knowledge_write`.\n"+
				"• **Read it back with:** `event_list` (the linear log read). Note that `tesseract_recall` does NOT search this domain unless you pass `domains: [\"event\"]` — the log is opt-in so its volume cannot drown the curated corpus.\n"+
				"• **Deeper:** `tesseract_skills event`.",
		),
		mcp.WithString("namespace", mcp.Required(), mcp.Description(
			"Event namespace: user/{id}/event/{type}, user/{id}/project/{pid}/event/{type} or user/{id}/session/{sid}/event/{type}. "+
				"{type} is a closed vocabulary naming the STREAM — allowed: "+memory.EventTypeList()+". "+
				"Session scope is the natural home for an agent's reasoning; user scope for a journal. "+
				"Do not put dates in the path — created_at is indexed and `event_list` filters on it.")),
		mcp.WithString("key", mcp.Description(
			"Optional logical key. Usually omit: a keyless write appends a new entry, which is what a log does. "+
				"When present it is held to the memory domain's lowercase dot-notation vocabulary, and a later write at the same key supersedes the earlier entry.")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("One-line statement of what happened (feeds embeddings)")),
		mcp.WithString("body", mcp.Description(
			"The reasoning itself, in prose (feeds embeddings). This is the field the domain exists for — a summary alone is a log line, which is what a trace already gives you.")),
		mcp.WithString("author_agent_id", mcp.Required(), mcp.Description("Agent ID of the writer")),
		mcp.WithString("author_version", mcp.Description("Agent version string")),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier")),
		mcp.WithString("consumer_state", mcp.Description(consumerStateArgDescription)),
		mcp.WithString("payload_data", mcp.Description(payloadDataArgDescription)),
		mcp.WithString("payload_data_schema_hash", mcp.Description(payloadDataSchemaHashArgDescription)),
		mcp.WithString("tags", mcp.Description("Optional JSON array of string tags")),
		mcp.WithNumber("ttl_seconds", mcp.Description(
			"Optional TTL in seconds (0 = no expiry, the default and the norm — long retention is the point of this domain)")),
		mcp.WithNumber("confidence", mcp.Description("Confidence score in [0, 1.0] (default 0.9)")),
		mcp.WithString("supersedes", mcp.Description("Optional revision_id this entry supersedes; requires `key`")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleEventWrite)

	a.addTool(s, mcp.NewTool("event_list",
		mcp.WithDescription(
			"**Read the event log in order** — the linear read path, not a ranked search.\n"+
				"• **Kind of content:** event-domain revisions in chronological order, newest first by default.\n"+
				"• **Result shape:** `{entries: [...], manifest: {returned, limit, direction, has_more, next_cursor, payload_mode}}`.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **No total, deliberately.** Counting a log means scanning it, which is the cost this read exists to avoid, and on an append-only log the answer would be stale before you read it. `has_more` is the question a total is a proxy for.\n"+
				"• **Paging is by position, not offset.** `next_cursor` names the entry the page stopped at, so entries appended while you page do not shift what you have already seen. A cursor is bound to its namespaces, direction and time window: change any of them and it is an error, not a plausible wrong page.\n"+
				"• **`direction`:** `newest_first` (default — what just happened) or `oldest_first` (replay, in the order it was reasoned).\n"+
				"• **`since`/`until`:** the time window. This is why event namespaces carry no date segments.\n"+
				"• **Use this when:** you want to know what happened, in order — catching up on a session, replaying reasoning, reading a journal.\n"+
				"• **Don't use this for:** finding an entry by topic. That is `tesseract_recall` with `domains: [\"event\"]` and a query.\n"+
				"• **Deprecated entries are excluded.** Retracting a log entry is what `tesseract_deprecate` means here; `tesseract_history` still shows it.\n"+
				"• **Deeper:** `tesseract_skills event`.",
		),
		mcp.WithString("namespaces", mcp.Required(), mcp.Description(
			"JSON array of event namespaces. Exact (`user/chrispian/event/journal`) or prefix — a bare "+
				"`user/chrispian/event` reads every stream under that scope, as does an explicit `user/chrispian/event/*`.")),
		mcp.WithString("direction", mcp.Description("newest_first (default) | oldest_first")),
		mcp.WithString("since", mcp.Description("RFC3339 lower bound on created_at, inclusive")),
		mcp.WithString("until", mcp.Description("RFC3339 upper bound on created_at, inclusive")),
		mcp.WithNumber("limit", mcp.Description("Entries per page (default 50, max 500)")),
		mcp.WithString("cursor", mcp.Description(
			"Opaque resume token from the previous response's `manifest.next_cursor`. Omit for the first page; "+
				"`next_cursor: null` means there is nothing left.")),
		mcp.WithString("payload_mode", mcp.Description(payloadModeArgDescription)),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleEventList)
}

func (a *Adapter) handleEventWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	ttlSeconds := int64(req.GetFloat("ttl_seconds", 0))

	in := event.WriteInput{
		Namespace:      req.GetString("namespace", ""),
		Key:            req.GetString("key", ""),
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

	rev, err := a.EventStore.Write(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

func (a *Adapter) handleEventList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	namespaces, _, err := parseStringArrayArg(req, "namespaces")
	if err != nil {
		return toolError(codeValidationError, "namespaces "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}
	if len(namespaces) == 0 {
		return toolError(codeValidationError, "namespaces is required and must name at least one event namespace"), nil
	}

	in := memory.EventLogInput{
		Namespaces: namespaces,
		Direction:  memory.LogDirection(req.GetString("direction", "")),
		Limit:      int(req.GetFloat("limit", 0)),
		Cursor:     req.GetString("cursor", ""),
	}
	if raw := req.GetString("since", ""); raw != "" {
		t, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return toolError(codeValidationError, "since must be RFC3339: "+parseErr.Error()), nil //nolint:nilerr // MCP tool pattern: a caller error is a tool result, not a transport error
		}
		in.Since = &t
	}
	if raw := req.GetString("until", ""); raw != "" {
		t, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return toolError(codeValidationError, "until must be RFC3339: "+parseErr.Error()), nil //nolint:nilerr // MCP tool pattern: a caller error is a tool result, not a transport error
		}
		in.Until = &t
	}

	mode, errRes := a.resolvePayloadMode(req)
	if errRes != nil {
		return errRes, nil
	}
	// The page limit is clamped by payload mode for the same reason recall's
	// is: `full` carries payload.body, and an event's body is the reasoning
	// itself — the longest prose in the corpus by design. ClampRecallLimit is
	// named for where it was first needed, not for a knob only recall has; the
	// cost curve it encodes is about payload weight, which is identical here.
	if effective, _ := memory.ClampRecallLimit(in.Limit, mode); in.Limit > 0 {
		in.Limit = effective
	}

	page, err := a.EventStore.ReadLog(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrInvalidCursor) || errors.Is(err, memory.ErrInvalidInput) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(memory.ProjectEventLogPage(page, mode)), nil
}

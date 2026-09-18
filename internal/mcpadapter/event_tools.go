package mcpadapter

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func (a *Adapter) registerEventTools(s *toolRegistrar) {
	a.addTool(s, gomcpTool("event_write",
		"**Append one entry to the event log** — an agent's reasoning about what it is doing, or a personal log/journal entry.\n"+
			"• **Kind of content:** narrative prose. What you were trying, why you chose this over that, what you noticed. The value is the reasoning a trace throws away.\n"+
			"• **NOT telemetry.** No spans, no metrics, no structured timing. If what you are recording is a duration or a count, this is the wrong tool and Tesseract is the wrong system.\n"+
			"• **Scope:** `memory:write`.\n"+
			"• **Append-only by default:** `key` is optional and usually omitted. A log entry records that something HAPPENED; it has a timestamp, not a current value. Pass a key only when the entry genuinely has a revisable identity.\n"+
			"• **Use this when:** you want a future session to be able to reconstruct why, not just what.\n"+
			"• **Don't use this for:** a settled conclusion worth recalling on its own — that is `memory_write`. An external reference — `knowledge_write`.\n"+
			"• **Read it back with:** `event_list` (the linear log read). Note that `tesseract_recall` does NOT search this domain unless you pass `domains: [\"event\"]` — the log is opt-in so its volume cannot drown the curated corpus.\n"+
			"• **Deeper:** `tesseract_skills event`.",
		inputSchema(
			strProp("namespace",
				"Event namespace: {scope}/{id}/event/{type} (e.g. session/{sid}/event/reasoning, project/{slug}/event/reasoning, system/event/reasoning, user/{id}/event/journal). "+
					"{type} is a closed vocabulary naming the STREAM — allowed: "+memory.EventTypeList()+". "+
					"Session scope is the natural home for an agent's reasoning; project scope for codebase reasoning and session close; user scope for a journal. "+
					"Do not put dates in the path — created_at is indexed and `event_list` filters on it.", true),
			strProp("key",
				"Optional logical key. Usually omit: a keyless write appends a new entry, which is what a log does. "+
					"When present it is held to the memory domain's lowercase dot-notation vocabulary, and a later write at the same key supersedes the earlier entry.", false),
			strProp("workstream_id", "Optional opaque workstream association. Omit to preserve; send an empty string to clear.", false),
			strProp("summary", "One-line statement of what happened (feeds embeddings)", true),
			strProp("body",
				"The reasoning itself, in prose (feeds embeddings). This is the field the domain exists for — a summary alone is a log line, which is what a trace already gives you.", false),
			strProp("author_agent_id", "Agent ID of the writer", true),
			strProp("author_version", "Agent version string", false),
			strProp("session_id", "Session identifier", true),
			strProp("consumer_state", consumerStateArgDescription, false),
			strProp("data", payloadDataArgDescription, false),
			strProp("data_schema_hash", payloadDataSchemaHashArgDescription, false),
			strProp("tags", "Optional JSON array of string tags", false),
			numProp("ttl_seconds",
				"Optional TTL in seconds (0 = no expiry, the default and the norm — long retention is the point of this domain)", false),
			numProp("confidence", "Confidence score in [0, 1.0] (default 0.9)", false),
			strProp("supersedes", "Optional revision_id this entry supersedes; requires `key`", false),
		),
		toolAnnotations{},
		a.handleEventWrite,
	))

	a.addTool(s, gomcpTool("event_list",
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
		inputSchema(
			strProp("namespaces",
				"JSON array of event namespaces. Exact (`project/tesseract/event/reasoning`) or prefix — a bare "+
					"`project/tesseract/event` reads every stream under that scope, as does an explicit `project/tesseract/event/*`.", true),
			strProp("direction", "newest_first (default) | oldest_first", false),
			strProp("since", "RFC3339 lower bound on created_at, inclusive", false),
			strProp("until", "RFC3339 upper bound on created_at, inclusive", false),
			numProp("limit", "Entries per page (default 50, max 500)", false),
			strProp("cursor",
				"Opaque resume token from the previous response's `manifest.next_cursor`. Omit for the first page; "+
					"`next_cursor: null` means there is nothing left.", false),
			strProp("payload_mode", payloadModeArgDescription, false),
			strProp("workstream_id", "Exact opaque workstream association filter.", false),
		),
		toolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		a.handleEventList,
	))
}

func (a *Adapter) handleEventWrite(ctx context.Context, req map[string]any) (any, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}

	tags, _, err := parseStringArrayArg(req, "tags")
	if err != nil {
		return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP tool pattern
	}

	ttlSeconds := int64(argFloat(req, "ttl_seconds", 0))
	workstreamID, workstreamErr := workstreamWriteArg(req)
	if workstreamErr != nil {
		return workstreamErr, nil
	}

	in := event.WriteInput{
		Namespace:      argString(req, "namespace", ""),
		Key:            argString(req, "key", ""),
		WorkstreamID:   workstreamID,
		Summary:        argString(req, "summary", ""),
		Body:           argString(req, "body", ""),
		Data:           payloadDataArg(req),
		DataSchemaHash: argString(req, "data_schema_hash", ""),
		Author: memory.Author{
			AgentID:      argString(req, "author_agent_id", ""),
			AgentVersion: argString(req, "author_version", ""),
		},
		SessionID:     argString(req, "session_id", ""),
		Tags:          tags,
		TTL:           time.Duration(ttlSeconds) * time.Second,
		Confidence:    argFloat(req, "confidence", 0),
		Supersedes:    argString(req, "supersedes", ""),
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

func (a *Adapter) handleEventList(ctx context.Context, req map[string]any) (any, error) {
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
	workstreamID, workstreamErr := workstreamReadArg(req)
	if workstreamErr != nil {
		return workstreamErr, nil
	}

	in := memory.EventLogInput{
		Namespaces:   namespaces,
		WorkstreamID: workstreamID,
		Direction:    memory.LogDirection(argString(req, "direction", "")),
		Limit:        int(argFloat(req, "limit", 0)),
		Cursor:       argString(req, "cursor", ""),
	}
	if raw := argString(req, "since", ""); raw != "" {
		t, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return toolError(codeValidationError, "since must be RFC3339: "+parseErr.Error()), nil //nolint:nilerr // MCP tool pattern: a caller error is a tool result, not a transport error
		}
		in.Since = &t
	}
	if raw := argString(req, "until", ""); raw != "" {
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

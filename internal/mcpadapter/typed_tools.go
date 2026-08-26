package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/contexttypes"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerTypedTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_typed_write",
		mcp.WithDescription("Write a typed context record with type, status, and optional TTL/pointers/provenance. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Target namespace")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON payload")),
		mcp.WithString("record_type", mcp.Description("Context type (e.g. task/spec, decision/adr)")),
		mcp.WithString("status", mcp.Description("Status: draft|reviewed|canonical|deprecated (default: draft)")),
		mcp.WithString("ttl", mcp.Description("Optional TTL as RFC3339 timestamp")),
		mcp.WithString("pointers", mcp.Description("Comma-separated list of pointer references")),
		mcp.WithString("actor", mcp.Description("Actor identity (default: mcp-agent)")),
	), a.handleTypedWrite)

	a.addTool(s, mcp.NewTool("context_status_set",
		mcp.WithDescription("Move a context record to a different lifecycle status. Requires 'write' scope. "+
			"`status` names the target and selects which transition rules apply — see its description. "+
			"See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Record namespace")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
		mcp.WithString("status", mcp.Description(statusSetArgDescription)),
		mcp.WithString("actor", mcp.Description("Actor identity (default: user)")),
	), a.handleStatusSet)

	a.addTool(s, mcp.NewTool("context_typed_view",
		mcp.WithDescription("Retrieve records matching a named view (e.g. task_exec, strategy) with type-based ranking. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("view_id", mcp.Required(), mcp.Description("View ID: task_exec, strategy, or custom")),
		mcp.WithString("namespaces", mcp.Description("Comma-separated namespace globs (default: all)")),
		mcp.WithNumber("max_items", mcp.Description("Max items to return")),
		mcp.WithBoolean("include_payload", mcp.Description("Include payload in results (default: true)")),
	), a.handleTypedView)

	a.addTool(s, mcp.NewTool("context_pack",
		mcp.WithDescription("Assemble a budget-bounded bundle of context records. "+
			"`shape` selects how the bundle is chosen and what envelope comes back — the two shapes take different arguments; see its description. "+
			"See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("shape", mcp.Description(packShapeArgDescription)),
		mcp.WithString("view_id", mcp.Description("shape=list only, required there: view ID — task_exec, strategy, or custom")),
		mcp.WithString("namespaces", mcp.Description("Comma-separated namespace globs. shape=list: narrows the view (default: all). shape=packet: the records to include.")),
		mcp.WithNumber("max_items", mcp.Description("Max items (default: 50 on both shapes)")),
		mcp.WithNumber("max_tokens", mcp.Description("shape=list only: max tokens estimate (default: 8000)")),
		mcp.WithNumber("max_tokens_estimate", mcp.Description("shape=packet only: token budget limit (default: 8000)")),
		mcp.WithBoolean("include_pins", mcp.Description("shape=packet only: prepend user/pins/* records (default true)")),
		mcp.WithNumber("payload_max_bytes", mcp.Description("shape=packet only. "+payloadMaxBytesArgDescription)),
	), a.handleContextPackShape)
}

// ── Merged-tool argument vocabulary ──────────────────────────────────────────

const (
	packShapeArgDescription = "How the bundle is chosen and what envelope comes back. The two shapes take DIFFERENT arguments; " +
		"passing one shape's argument to the other is a validation_error rather than a silently ignored knob. " +
		"`list` (default): rank the records of a named view (`view_id`, required) by type bias and status weight, bounded by `max_items` and `max_tokens`. " +
		"Answers `{view, generated_at, token_estimate, items, count}`, payloads always included. Peer of POST /v1/context/pack. " +
		"`packet`: take the heads under `namespaces`, optionally prepending user/pins/*, bounded by `max_items` and `max_tokens_estimate`, " +
		"filtered by the capability token's namespace globs when a token is configured. " +
		"Answers `{items, manifest}`, where the manifest reports what was included and why assembly stopped. MCP-only: " +
		"POST /v1/context/packet exists but answers a different budget and manifest shape."

	statusSetArgDescription = "Target lifecycle status. " +
		"Omit it to advance one step along draft → reviewed → canonical. " +
		"`reviewed` / `canonical`: the transition is checked against the record type's promotion rules and a status_promote audit event is written. " +
		"`deprecated`: the record is retired; deprecating an already-deprecated record is a validation_error. " +
		"Anything else is rejected by the type registry. " +
		"The retired context_status_promote spelled this `to_status`; that name is refused rather than ignored, because ignoring it would " +
		"silently advance one step instead of going where you asked. " +
		"NOTE: the two paths are not symmetric today — the deprecate path writes no audit event from this surface, while its HTTP peer " +
		"POST /v1/context/status/deprecate does. That asymmetry predates this tool and is preserved here rather than quietly changed."
)

// ── Merged-tool dispatchers ──────────────────────────────────────────────────

// handleContextPackShape serves the merged context_pack. `shape` selects the
// arm; the two arms are the pre-merge handlers unchanged.
func (a *Adapter) handleContextPackShape(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	shape := req.GetString("shape", "list")

	// Reject the other shape's knobs rather than accept and ignore them.
	reject := func(shapeName string, knobs ...string) *mcp.CallToolResult {
		for _, knob := range knobs {
			if raw, ok := req.GetArguments()[knob]; ok && raw != nil && raw != "" {
				return toolError("validation_error", knob+" is not accepted under shape="+shapeName)
			}
		}
		return nil
	}

	switch shape {
	case "list":
		if errResult := reject("list", "include_pins", "max_tokens_estimate", "payload_max_bytes", "payload_mode"); errResult != nil {
			return errResult, nil
		}
		return a.handleContextPack(ctx, req)
	case "packet":
		if errResult := reject("packet", "view_id", "max_tokens"); errResult != nil {
			return errResult, nil
		}
		return a.handlePacket(ctx, req)
	default:
		return toolError("validation_error",
			"shape must be one of list|packet, got "+strconv.Quote(shape)), nil
	}
}

// handleStatusSet serves the merged context_status_set. `status` names the
// target and selects which transition path runs.
//
// `deprecated` routes to the deprecation path — the one the retired
// context_status_deprecate tool ran — and every other target routes to the
// promotion path. That is the one input where the merge is not byte-identical
// to both predecessors: context_status_promote(to_status="deprecated") was a
// legal call that took the PROMOTION path (type-rule validation plus a
// status_promote audit event), and the same target now takes the deprecation
// path instead. See TestMerge_StatusSet_DeprecatedTargetTakesTheDeprecationPath.
//
// The retired context_status_promote spelled the target `to_status`. That name
// is refused outright rather than ignored: an ignored `to_status` leaves
// `status` empty, which means "advance one step" — so the caller would silently
// get `reviewed` where they asked for `canonical`, and a success envelope
// saying so. The check runs before the scope check because it is a fact about
// the arguments, not about the caller.
func (a *Adapter) handleStatusSet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if errResult := rejectRetiredArg(req, "to_status",
		"the target status is now named `status`; omit it to advance one step."); errResult != nil {
		return errResult, nil
	}
	if req.GetString("status", "") == "deprecated" {
		return a.handleStatusDeprecate(ctx, req)
	}
	return a.handleStatusPromote(ctx, req, req.GetString("status", ""))
}

// ── Session Snapshot ──────────────────────────────────────────────────────────

func (a *Adapter) registerSessionTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_session_snapshot",
		mcp.WithDescription("Write a structured session snapshot to Tesseract and auto-embed for semantic search. Combines `context_typed_write` + `context_embed` into one call with enforced session schema. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier")),
		mcp.WithString("project_id", mcp.Required(), mcp.Description("Project identifier (used in namespace)")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("Brief session summary (1-3 sentences)")),
		mcp.WithString("decisions", mcp.Description("JSON array of decisions made during the session")),
		mcp.WithString("tasks_touched", mcp.Description("JSON array of task IDs worked on")),
		mcp.WithString("context_learned", mcp.Description("Key context or insights gained")),
		mcp.WithString("open_questions", mcp.Description("JSON array of unresolved questions")),
		mcp.WithString("handoff_notes", mcp.Description("Notes for the next session/agent")),
		mcp.WithString("actor", mcp.Description("Actor identity (default: mcp-agent)")),
	), a.handleSessionSnapshot)
}

func (a *Adapter) handleSessionSnapshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Auth: require write scope
	errResult, claims := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	sessionID := req.GetString("session_id", "")
	projectID := req.GetString("project_id", "")
	summary := req.GetString("summary", "")
	actor := req.GetString("actor", "mcp-agent")

	if sessionID == "" || projectID == "" || summary == "" {
		return toolError("validation_error", "session_id, project_id, and summary are required"), nil
	}

	// Build namespace: system/sessions/<project_id>
	ns := "system/sessions/" + projectID
	key := sessionID

	// Check namespace permission
	if !globsPermit(claims.NamespaceGlobs, ns) {
		return toolError("namespace_not_permitted", fmt.Sprintf("token does not permit writes to namespace %q", ns)), nil
	}

	// Build structured payload
	payload := map[string]any{
		"session_id": sessionID,
		"project_id": projectID,
		"summary":    summary,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}

	// Parse optional JSON array fields
	if v := req.GetString("decisions", ""); v != "" {
		var arr []any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			payload["decisions"] = arr
		} else {
			payload["decisions"] = []string{v}
		}
	}
	if v := req.GetString("tasks_touched", ""); v != "" {
		var arr []any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			payload["tasks_touched"] = arr
		} else {
			payload["tasks_touched"] = []string{v}
		}
	}
	if v := req.GetString("context_learned", ""); v != "" {
		payload["context_learned"] = v
	}
	if v := req.GetString("open_questions", ""); v != "" {
		var arr []any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			payload["open_questions"] = arr
		} else {
			payload["open_questions"] = []string{v}
		}
	}
	if v := req.GetString("handoff_notes", ""); v != "" {
		payload["handoff_notes"] = v
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return toolError("internal_error", "failed to marshal payload: "+err.Error()), nil
	}

	// Write record
	rec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace:  ns,
		Key:        key,
		Actor:      actor,
		Payload:    payloadBytes,
		RecordType: "session/snapshot",
		Status:     "draft",
		Pointers:   []string{"session:" + sessionID},
	})
	if err != nil {
		return toolError("write_failed", err.Error()), nil
	}

	// Audit log
	_ = a.Store.EmitSessionSnapshot(ctx, actor, ns, key, rec.Revision, rec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"session_id":%q,"project_id":%q}`, sessionID, projectID)))

	result := map[string]any{
		"record_id": rec.RecordID,
		"revision":  rec.Revision,
		"namespace": rec.Namespace,
		"key":       rec.Key,
		"status":    "written",
	}

	// Auto-embed if provider is available
	if a.EmbeddingProvider != nil {
		text := extractTextForEmbedding(contextstore.Record{
			RecordID:  rec.RecordID,
			Namespace: rec.Namespace,
			Key:       rec.Key,
			Payload:   payloadBytes,
		})
		if text != "" {
			embResult, err := a.EmbeddingProvider.Embed(ctx, text, a.EmbeddingModel)
			if err == nil {
				_ = a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
					RecordID:   rec.RecordID,
					Model:      a.EmbeddingModel,
					Dimensions: len(embResult.Embedding),
					Vector:     embResult.Embedding,
				})
				result["embedded"] = true
				result["embedding_model"] = a.EmbeddingModel
				result["embedding_dimensions"] = len(embResult.Embedding)
			} else {
				result["embedded"] = false
				result["embedding_error"] = err.Error()
			}
		}
	}

	return toolJSON(result), nil
}

func (a *Adapter) getRegistry() *contexttypes.Registry {
	if a.TypeRegistry != nil {
		return a.TypeRegistry
	}
	return contexttypes.NewRegistry()
}

func (a *Adapter) handleTypedWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, claims := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	payloadStr := req.GetString("payload", "")
	recordType := req.GetString("record_type", "")
	status := req.GetString("status", "draft")
	ttl := req.GetString("ttl", "")
	pointersStr := req.GetString("pointers", "")
	actor := req.GetString("actor", "mcp-agent")

	if !globsPermit(claims.NamespaceGlobs, ns) {
		return toolError("namespace_not_permitted", "token namespace globs do not permit writing to: "+ns), nil
	}
	if ns == "" || key == "" || payloadStr == "" {
		return toolError("validation_error", "namespace, key, and payload are required"), nil
	}

	var payload json.RawMessage
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return toolError("validation_error", "payload must be valid JSON: "+err.Error()), nil
	}

	reg := a.getRegistry()
	if err := reg.ValidateType(recordType); err != nil {
		return toolError("validation_error", err.Error()), nil
	}
	if err := reg.ValidateStatus(recordType, status); err != nil {
		return toolError("validation_error", err.Error()), nil
	}

	// Validate required fields for the type.
	if recordType != "" {
		var payloadMap map[string]any
		if err := json.Unmarshal(payload, &payloadMap); err == nil {
			if err := reg.ValidateRequiredFields(recordType, payloadMap); err != nil {
				return toolError("validation_error", err.Error()), nil
			}
		}
	}

	// Apply default TTL.
	if ttl == "" && recordType != "" {
		ct, ok := reg.GetType(recordType)
		if ok {
			defaultTTL := ct.ParseDefaultTTL()
			if defaultTTL > 0 {
				ttl = time.Now().UTC().Add(defaultTTL).Format(time.RFC3339)
			}
		}
	}

	var pointers []string
	if pointersStr != "" {
		for _, p := range strings.Split(pointersStr, ",") {
			if t := strings.TrimSpace(p); t != "" {
				pointers = append(pointers, t)
			}
		}
	}

	rec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace:  ns,
		Key:        key,
		Actor:      actor,
		Payload:    payload,
		RecordType: recordType,
		Status:     status,
		TTL:        ttl,
		Pointers:   pointers,
	})
	if err != nil {
		return toolError("write_failed", err.Error()), nil
	}

	_ = a.Store.EmitTypedWrite(ctx, actor, ns, key, rec.Revision, rec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"source":"mcp","record_type":%q,"status":%q}`, recordType, status)))

	return toolJSON(map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"namespace":   rec.Namespace,
		"key":         rec.Key,
		"record_type": rec.RecordType,
		"status":      rec.Status,
		"ttl":         rec.TTL,
	}), nil
}

// handleStatusPromote is the promotion path of context_status_set. toStatus is
// resolved by the dispatcher rather than read here, because the merged tool
// spells it `status`; empty still means "next in the chain".
func (a *Adapter) handleStatusPromote(ctx context.Context, req mcp.CallToolRequest, toStatus string) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	actor := req.GetString("actor", "user")

	if ns == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}

	head, err := a.Store.Head(ctx, ns, key)
	if err != nil {
		return toolError("not_found", err.Error()), nil
	}

	reg := a.getRegistry()
	oldStatus := head.Status
	if oldStatus == "" {
		oldStatus = "draft"
	}

	newStatus := toStatus
	if newStatus == "" {
		newStatus = contexttypes.NextPromotionStatus(oldStatus)
		if newStatus == "" {
			return toolError("validation_error", fmt.Sprintf("cannot promote from status %q", oldStatus)), nil
		}
	}

	if err := reg.ValidateTransition(head.RecordType, oldStatus, newStatus, actor); err != nil {
		return toolError("validation_error", err.Error()), nil
	}

	rec, err := a.Store.UpdateRecordStatus(ctx, ns, key, actor, newStatus)
	if err != nil {
		return toolError("promote_failed", err.Error()), nil
	}

	_ = a.Store.EmitStatusPromote(ctx, actor, ns, key, rec.Revision, rec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"from":%q,"to":%q,"source":"mcp"}`, oldStatus, newStatus)))

	return toolJSON(map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"from_status": oldStatus,
		"to_status":   newStatus,
		"record_type": head.RecordType,
	}), nil
}

func (a *Adapter) handleStatusDeprecate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	actor := req.GetString("actor", "user")

	if ns == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}

	head, err := a.Store.Head(ctx, ns, key)
	if err != nil {
		return toolError("not_found", err.Error()), nil
	}

	oldStatus := head.Status
	if oldStatus == "" {
		oldStatus = "draft"
	}
	if oldStatus == "deprecated" {
		return toolError("validation_error", "item is already deprecated"), nil
	}

	rec, err := a.Store.UpdateRecordStatus(ctx, ns, key, actor, "deprecated")
	if err != nil {
		return toolError("deprecate_failed", err.Error()), nil
	}

	return toolJSON(map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"from_status": oldStatus,
		"to_status":   "deprecated",
		"record_type": head.RecordType,
	}), nil
}

func (a *Adapter) handleTypedView(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	viewID := req.GetString("view_id", "")
	if viewID == "" {
		return toolError("validation_error", "view_id is required"), nil
	}

	nsStr := req.GetString("namespaces", "")
	var namespaces []string
	if nsStr != "" {
		for _, ns := range strings.Split(nsStr, ",") {
			if t := strings.TrimSpace(ns); t != "" {
				namespaces = append(namespaces, t)
			}
		}
	}
	if len(namespaces) == 0 {
		namespaces = []string{"*"}
	}

	maxItems := req.GetInt("max_items", 0)
	includePayload := req.GetBool("include_payload", true)

	reg := a.getRegistry()
	viewDef, ok := reg.GetView(viewID)
	if !ok {
		return toolError("not_found", fmt.Sprintf("view %q not found", viewID)), nil
	}

	if maxItems <= 0 {
		maxItems = viewDef.MaxItems
	}
	if maxItems <= 0 {
		maxItems = 50
	}

	statuses := []string{"draft", "reviewed", "canonical"}
	items, err := a.Store.Select(ctx, contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
		Types:         viewDef.Types,
		Statuses:      statuses,
		Limit:         maxItems,
	})
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}

	// Apply token glob filtering.
	if claims, ok := a.loadClaims(ctx); ok {
		items = filterByGlobs(items, claims.NamespaceGlobs)
	}

	// Rank items.
	type rankedItem struct {
		rec   contextstore.Record
		score float64
	}
	ranked := make([]rankedItem, len(items))
	for i, rec := range items {
		typeScore := 1.0
		if ct, ok := reg.GetType(rec.RecordType); ok && ct.RetrievalRankBias > 0 {
			typeScore = ct.RetrievalRankBias
		}
		statusScore := 0.5
		if w, ok := viewDef.RankWeights[rec.Status]; ok {
			statusScore = w
		}
		ranked[i] = rankedItem{rec: rec, score: typeScore * statusScore}
	}

	// Sort by score desc.
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score ||
				(ranked[j].score == ranked[i].score && ranked[j].rec.RecordID < ranked[i].rec.RecordID) {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	resultItems := make([]map[string]any, len(ranked))
	for i, rr := range ranked {
		item := map[string]any{
			"record_id":       rr.rec.RecordID,
			"namespace":       rr.rec.Namespace,
			"key":             rr.rec.Key,
			"record_type":     rr.rec.RecordType,
			"status":          rr.rec.Status,
			"content_version": rr.rec.ContentVersion,
			"rank_score":      rr.score,
		}
		if includePayload {
			item["payload"] = json.RawMessage(rr.rec.Payload)
		}
		resultItems[i] = item
	}

	return toolJSON(map[string]any{
		"view":  viewID,
		"items": resultItems,
		"count": len(resultItems),
		"types": viewDef.Types,
	}), nil
}

func (a *Adapter) handleContextPack(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	viewID := req.GetString("view_id", "")
	if viewID == "" {
		return toolError("validation_error", "view_id is required"), nil
	}

	nsStr := req.GetString("namespaces", "")
	var namespaces []string
	if nsStr != "" {
		for _, ns := range strings.Split(nsStr, ",") {
			if t := strings.TrimSpace(ns); t != "" {
				namespaces = append(namespaces, t)
			}
		}
	}
	if len(namespaces) == 0 {
		namespaces = []string{"*"}
	}

	maxItems := req.GetInt("max_items", 50)
	maxTokens := req.GetInt("max_tokens", 8000)

	reg := a.getRegistry()
	viewDef, ok := reg.GetView(viewID)
	if !ok {
		return toolError("not_found", fmt.Sprintf("view %q not found", viewID)), nil
	}

	statuses := []string{"draft", "reviewed", "canonical"}
	items, err := a.Store.Select(ctx, contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
		Types:         viewDef.Types,
		Statuses:      statuses,
		Limit:         maxItems * 2,
	})
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}

	// Rank.
	type ri struct {
		rec   contextstore.Record
		score float64
	}
	ranked := make([]ri, len(items))
	for i, rec := range items {
		ts := 1.0
		if ct, ok := reg.GetType(rec.RecordType); ok && ct.RetrievalRankBias > 0 {
			ts = ct.RetrievalRankBias
		}
		ss := 0.5
		if w, ok := viewDef.RankWeights[rec.Status]; ok {
			ss = w
		}
		ranked[i] = ri{rec: rec, score: ts * ss}
	}
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	var packItems []map[string]any
	tokensSoFar := 0
	for _, rr := range ranked {
		if len(packItems) >= maxItems {
			break
		}
		tokens := contextstore.EstimateTokens(rr.rec.Payload)
		if maxTokens > 0 && tokensSoFar+tokens > maxTokens {
			break
		}
		packItems = append(packItems, map[string]any{
			"record_id":   rr.rec.RecordID,
			"namespace":   rr.rec.Namespace,
			"key":         rr.rec.Key,
			"record_type": rr.rec.RecordType,
			"status":      rr.rec.Status,
			"payload":     json.RawMessage(rr.rec.Payload),
		})
		tokensSoFar += tokens
	}
	if packItems == nil {
		packItems = []map[string]any{}
	}

	return toolJSON(map[string]any{
		"view":           viewID,
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"token_estimate": tokensSoFar,
		"items":          packItems,
		"count":          len(packItems),
	}), nil
}

func (a *Adapter) handleTypesList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reg := a.getRegistry()
	return toolJSON(map[string]any{
		"types": reg.ListTypes(),
	}), nil
}

func (a *Adapter) handleViewsList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reg := a.getRegistry()
	return toolJSON(map[string]any{
		"views": reg.ListViews(),
	}), nil
}

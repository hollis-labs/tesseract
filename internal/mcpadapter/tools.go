package mcpadapter

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_head",
		mcp.WithDescription("Read the current head (latest revision) of a record by namespace and key. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace path, e.g. user/memory/task-001")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
	), a.handleHead)

	a.addTool(s, mcp.NewTool("context_history",
		mcp.WithDescription("Read the revision history for a namespace/key, newest first. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace path")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
		mcp.WithNumber("limit", mcp.Description("Max revisions to return (default 10, max 25)")),
	), a.handleHistory)

	a.addTool(s, mcp.NewTool("context_view",
		mcp.WithDescription("Evaluate a view selector and return matching records. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespaces", mcp.Description("Comma-separated namespace glob patterns, e.g. \"user/memory/*,app/test/session/*\"")),
		mcp.WithString("revision_scope", mcp.Description("head or all (default: head)")),
		mcp.WithNumber("limit", mcp.Description("Max records to return (default 10, max 25). Returns summaries; use context_head for full record.")),
	), a.handleView)

	a.addTool(s, mcp.NewTool("context_packet",
		mcp.WithDescription("Retrieve a budget-bounded context packet with manifest. The primary agent continuity surface. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespaces", mcp.Description("Comma-separated namespace glob patterns to include")),
		mcp.WithBoolean("include_pins", mcp.Description("Whether to prepend user/pins/* records (default true)")),
		mcp.WithNumber("max_items", mcp.Description("Item budget limit (default 50)")),
		mcp.WithNumber("max_tokens_estimate", mcp.Description("Token budget limit (default 8000)")),
		mcp.WithString("payload_mode", mcp.Description("full or head_only (default: full)")),
	), a.handlePacket)

	a.addTool(s, mcp.NewTool("context_write",
		mcp.WithDescription("Write a record to a namespace. Requires 'write' scope in the configured capability token. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Target namespace, e.g. app/test/session/task-001")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON payload as a string, e.g. '{\"status\":\"in_progress\"}'")),
		mcp.WithString("actor", mcp.Description("Actor identity, e.g. app:my-agent (default: mcp-agent)")),
		mcp.WithString("record_type", mcp.Description("Record type tag (default: state)")),
	), a.handleWrite)

	a.addTool(s, mcp.NewTool("context_promote_request",
		mcp.WithDescription("Request promotion of a record from an app namespace to a user namespace. Requires 'promote.request' scope. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("source_namespace", mcp.Required(), mcp.Description("Source namespace (must be in app/*)")),
		mcp.WithString("source_key", mcp.Required(), mcp.Description("Source record key")),
		mcp.WithString("target_namespace", mcp.Required(), mcp.Description("Target namespace (typically user/memory/*)")),
		mcp.WithString("target_key", mcp.Required(), mcp.Description("Target record key")),
		mcp.WithString("reason", mcp.Description("Human-readable reason for the promotion")),
		mcp.WithString("actor", mcp.Description("Actor identity (default: mcp-agent)")),
	), a.handlePromoteRequest)

	a.addTool(s, mcp.NewTool("context_promote_list",
		mcp.WithDescription("List promotion requests. Read-only, no token required. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("status", mcp.Description("Filter by status: pending|approved|applied|all (default: pending)")),
		mcp.WithNumber("limit", mcp.Description("Max requests to return (default 10, max 25)")),
	), a.handlePromoteList)

	a.addTool(s, mcp.NewTool("context_promote_approve",
		mcp.WithDescription("Approve a pending promotion request. Requires 'promote.approve' scope. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("request_id", mcp.Required(), mcp.Description("Promotion request ID to approve")),
		mcp.WithString("notes", mcp.Description("Optional approval notes")),
		mcp.WithString("actor", mcp.Description("Actor identity (default: user)")),
	), a.handlePromoteApprove)

	a.addTool(s, mcp.NewTool("context_promote_apply",
		mcp.WithDescription("Apply an approved promotion request, writing the record to the target namespace. Requires 'promote.apply' scope. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("request_id", mcp.Required(), mcp.Description("Approved promotion request ID to apply")),
		mcp.WithString("actor", mcp.Description("Actor identity (default: user)")),
	), a.handlePromoteApply)

	// NOTE: MCP tool names retain "broker" for backward compatibility with consumers.
	// Internally these are the context query planner, not the universal ContextBroker.
	a.addTool(s, mcp.NewTool("context_broker_plan",
		mcp.WithDescription("Generate a context fetch plan for a given intent. Returns namespace patterns, budget, and rationale. No auth required. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("intent", mcp.Description("Intent: resume_task|boot_project|review_session|custom (default: custom)")),
		mcp.WithString("summary", mcp.Description("Task summary for keyword extraction (used with resume_task intent)")),
		mcp.WithNumber("budget_items", mcp.Description("Max items budget (default 50)")),
		mcp.WithNumber("budget_tokens", mcp.Description("Max tokens estimate budget (default 4000)")),
	), a.handleContextPlan)

	a.addTool(s, mcp.NewTool("context_broker_fetch",
		mcp.WithDescription("Execute a context plan and return a context packet in one call. Combines context_broker_plan and context_packet. No auth required. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("intent", mcp.Description("Intent: resume_task|boot_project|review_session|custom (default: custom)")),
		mcp.WithString("summary", mcp.Description("Task summary for keyword extraction")),
		mcp.WithNumber("budget_items", mcp.Description("Max items budget (default 50)")),
		mcp.WithNumber("budget_tokens", mcp.Description("Max tokens estimate budget (default 4000)")),
		mcp.WithString("payload_mode", mcp.Description("full or head_only (default: full)")),
	), a.handleContextFetch)

	a.addTool(s, mcp.NewTool("context_namespace_register",
		mcp.WithDescription("Register a namespace with ownership policy. Requires 'namespace.admin' scope. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace path to register")),
		mcp.WithString("owner_type", mcp.Required(), mcp.Description("Ownership type: user or app")),
		mcp.WithString("owner_id", mcp.Required(), mcp.Description("Owner identity (e.g. my-agent)")),
	), a.handleNamespaceRegister)

	a.addTool(s, mcp.NewTool("context_namespace_show",
		mcp.WithDescription("Show the ownership policy for a registered namespace. No auth required. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace path to inspect")),
	), a.handleNamespaceShow)

	a.addTool(s, mcp.NewTool("context_namespaces_list",
		mcp.WithDescription("List all registered namespaces with ownership policies. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("prefix", mcp.Description("Filter to namespaces whose name starts with this string prefix (e.g. \"user/chrispian/\", \"app/\"). Pure string-prefix match, not a glob.")),
		mcp.WithNumber("limit", mcp.Description("Max namespaces to return (default 10, max 25)")),
	), a.handleNamespacesList)

	a.addTool(s, mcp.NewTool("context_audit",
		mcp.WithDescription("Query the audit event log. No auth required. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Description("Filter by exact namespace")),
		mcp.WithString("event_type", mcp.Description("Filter by event type (e.g. write, promote)")),
		mcp.WithNumber("limit", mcp.Description("Max events to return (default 10, max 25)")),
		mcp.WithNumber("cursor", mcp.Description("Pagination cursor (ID from previous response's next_cursor)")),
	), a.handleAudit)
}

// --- Read tools ---

func (a *Adapter) handleHead(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if ns == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}
	rec, err := a.Store.Head(ctx, ns, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolError("not_found", fmt.Sprintf("no head record for %s/%s", ns, key)), nil
		}
		return toolError("internal_error", err.Error()), nil
	}
	return toolJSON(recordJSON(rec)), nil
}

func (a *Adapter) handleHistory(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if ns == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)
	records, err := a.Store.History(ctx, ns, key, limit)
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}
	summaries := make([]map[string]any, len(records))
	for i, rec := range records {
		summaries[i] = recordJSON(rec)
	}
	env := budget.Apply(summaries, budget.Config{Limit: limit}, "%d revisions available. Use context_head with namespace and key for full record content.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

func (a *Adapter) handleView(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	nsStr := req.GetString("namespaces", "")
	var namespaces []string
	if nsStr != "" {
		for _, ns := range strings.Split(nsStr, ",") {
			if t := strings.TrimSpace(ns); t != "" {
				namespaces = append(namespaces, t)
			}
		}
	}
	revScope := req.GetString("revision_scope", "head")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)
	sel := contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: revScope,
	}
	records, err := a.Store.Select(ctx, sel)
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}

	// Filter by token namespace globs if a token is present.
	if claims, ok := a.loadClaims(ctx); ok {
		records = filterByGlobs(records, claims.NamespaceGlobs)
	}

	// Build summary items (omit full payload from list view).
	summaries := make([]map[string]any, len(records))
	for i, rec := range records {
		s := recordJSON(rec)
		s["status"] = "active"
		s["updated_at"] = rec.CreatedAt
		summaries[i] = s
	}
	env := budget.Apply(summaries, budget.Config{Limit: limit}, "%d records available. Use context_head with namespace and key for full record content.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

// --- Packet tool ---

func (a *Adapter) handlePacket(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()

	nsStr := req.GetString("namespaces", "")
	var namespaces []string
	if nsStr != "" {
		for _, ns := range strings.Split(nsStr, ",") {
			if t := strings.TrimSpace(ns); t != "" {
				namespaces = append(namespaces, t)
			}
		}
	}

	includePins := req.GetBool("include_pins", true)
	maxItems := req.GetInt("max_items", 50)
	maxTokens := req.GetInt("max_tokens_estimate", 8000)
	payloadMode := req.GetString("payload_mode", "full")

	// Load token globs for optional namespace filtering.
	var tokenGlobs []string
	if claims, ok := a.loadClaims(ctx); ok {
		tokenGlobs = claims.NamespaceGlobs
	}

	reqID := newMCPRequestID()
	var items []map[string]any
	pinsIncluded := 0
	bytesSoFar := 0
	tokensSoFar := 0
	itemsTotal := 0
	truncated := false
	truncationReason := ""
	sources := map[string]int{}

	budgetExceeded := func() bool {
		if maxItems > 0 && len(items) >= maxItems {
			truncationReason = "budget.max_items"
			return true
		}
		if maxTokens > 0 && tokensSoFar >= maxTokens {
			truncationReason = "budget.max_tokens_estimate"
			return true
		}
		return false
	}

	addRecord := func(rec contextstore.Record) bool {
		if budgetExceeded() {
			return false
		}
		payload := rec.Payload
		if payloadMode == "head_only" && len(payload) > 512 {
			payload = payload[:512]
		}
		item := recordJSON(rec)
		item["payload"] = json.RawMessage(payload)
		items = append(items, item)
		bytesSoFar += len(payload)
		tokensSoFar += contextstore.EstimateTokens(payload)
		parts := strings.SplitN(rec.Namespace, "/", 3)
		nsKey := rec.Namespace
		if len(parts) >= 2 {
			nsKey = parts[0] + "/" + parts[1]
		}
		sources[nsKey]++
		return true
	}

	// Prepend pins (filtered by token globs when present).
	if includePins {
		pins, err := a.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{"user/pins/*"},
			RevisionScope: "head",
		})
		if err == nil {
			if len(tokenGlobs) > 0 {
				pins = filterByGlobs(pins, tokenGlobs)
			}
			for _, rec := range pins {
				if addRecord(rec) {
					pinsIncluded++
				}
			}
		}
	}

	// Main selector.
	candidates, err := a.Store.Select(ctx, contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
	})
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}

	// Filter by token globs when present.
	if len(tokenGlobs) > 0 {
		candidates = filterByGlobs(candidates, tokenGlobs)
	}

	itemsTotal = len(candidates)
	for _, rec := range candidates {
		if !addRecord(rec) {
			truncated = true
			break
		}
	}

	if items == nil {
		items = []map[string]any{}
	}

	manifest := map[string]any{
		"request_id":        reqID,
		"pins_included":     pinsIncluded,
		"items_total":       itemsTotal,
		"items_returned":    len(items) - pinsIncluded,
		"bytes_returned":    bytesSoFar,
		"tokens_estimate":   tokensSoFar,
		"truncated":         truncated,
		"truncation_reason": truncationReason,
		"sources":           sources,
	}

	return toolJSON(map[string]any{"items": items, "manifest": manifest}), nil
}

// --- Write tools ---

func (a *Adapter) handleWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, claims := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	payloadStr := req.GetString("payload", "")

	// Enforce token namespace globs on write target.
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

	actor := req.GetString("actor", "mcp-agent")

	rec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: ns,
		Key:       key,
		Actor:     actor,
		Payload:   payload,
	})
	if err != nil {
		return toolError("write_failed", err.Error()), nil
	}

	_ = a.Store.EmitWrite(ctx, actor, ns, key, rec.Revision, rec.RecordID, json.RawMessage(`{"source":"mcp"}`))

	return toolJSON(map[string]any{
		"record_id": rec.RecordID,
		"revision":  rec.Revision,
		"namespace": rec.Namespace,
		"key":       rec.Key,
	}), nil
}

func (a *Adapter) handlePromoteRequest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "promote.request")
	if errResult != nil {
		return errResult, nil
	}

	srcNS := req.GetString("source_namespace", "")
	srcKey := req.GetString("source_key", "")
	tgtNS := req.GetString("target_namespace", "")
	tgtKey := req.GetString("target_key", "")
	reason := req.GetString("reason", "")
	actor := req.GetString("actor", "mcp-agent")

	if srcNS == "" || srcKey == "" || tgtNS == "" || tgtKey == "" {
		return toolError("validation_error", "source_namespace, source_key, target_namespace, and target_key are required"), nil
	}

	srcHead, err := a.Store.Head(ctx, srcNS, srcKey)
	if err != nil {
		return toolError("validation_error", fmt.Sprintf("source record not found: %v", err)), nil
	}

	requestID := "req-" + newMCPRequestID()[4:]
	now := time.Now().UTC().Format(time.RFC3339)

	pr := contextstore.PromoteRequest{
		Type:             "promote.request",
		RequestID:        requestID,
		SourceNamespace:  srcHead.Namespace,
		SourceKey:        srcHead.Key,
		SourceRevisionID: srcHead.RecordID,
		SourceChecksum:   srcHead.Checksum,
		TargetNamespace:  tgtNS,
		TargetKey:        tgtKey,
		Reason:           reason,
		Status:           "pending",
		RequestedAt:      now,
		RequestedBy:      actor,
	}
	payload, _ := json.Marshal(pr)

	namespace := "app/mcp-agent/promotions"
	_, err = a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: namespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   payload,
	})
	if err != nil {
		return toolError("promote_failed", err.Error()), nil
	}

	_ = a.Store.EmitPromote(ctx, contextstore.EventPromoteRequest, actor, srcNS, srcKey, srcHead.Revision, srcHead.RecordID,
		json.RawMessage(fmt.Sprintf(`{"request_id":%q,"target_namespace":%q,"source":"mcp"}`, requestID, tgtNS)))

	return toolJSON(map[string]any{
		"request_id": requestID,
		"status":     "pending",
	}), nil
}

// --- Promote lifecycle tools ---

func (a *Adapter) handlePromoteList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	status := req.GetString("status", "pending")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)

	recs, err := a.Store.Select(ctx, contextstore.Selector{
		Namespaces:    []string{"app/*/promotions"},
		RevisionScope: "head",
	})
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}

	var items []map[string]any
	for _, rec := range recs {
		var pr contextstore.PromoteRequest
		if err := json.Unmarshal(rec.Payload, &pr); err != nil || pr.Type != "promote.request" {
			continue
		}
		if status != "all" && pr.Status != status {
			continue
		}
		items = append(items, map[string]any{
			"request_id":       pr.RequestID,
			"status":           pr.Status,
			"source_namespace": pr.SourceNamespace,
			"source_key":       pr.SourceKey,
			"target_namespace": pr.TargetNamespace,
			"target_key":       pr.TargetKey,
			"reason":           pr.Reason,
			"requested_at":     pr.RequestedAt,
			"requested_by":     pr.RequestedBy,
		})
	}
	if items == nil {
		items = []map[string]any{}
	}
	env := budget.Apply(items, budget.Config{Limit: limit}, "%d promotion requests available. Use context_promote_approve or context_promote_apply for specific requests.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

func (a *Adapter) handlePromoteApprove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "promote.approve")
	if errResult != nil {
		return errResult, nil
	}

	requestID := req.GetString("request_id", "")
	if requestID == "" {
		return toolError("validation_error", "request_id is required"), nil
	}
	notes := req.GetString("notes", "")
	actor := req.GetString("actor", "user")

	pr, reqNamespace, err := a.Store.GetPromoteRequest(ctx, requestID)
	if err != nil {
		return toolError("not_found", err.Error()), nil
	}
	if pr.Status != "pending" {
		return toolError("invalid_state", fmt.Sprintf("request status is %q, must be pending to approve", pr.Status)), nil
	}

	approvalID := "appr-" + newMCPRequestID()[4:]
	now := time.Now().UTC().Format(time.RFC3339)

	pa := contextstore.PromoteApproval{
		Type:             "promote.approve",
		ApprovalID:       approvalID,
		RequestID:        requestID,
		RequestNamespace: reqNamespace,
		ApprovedAt:       now,
		ApprovedBy:       actor,
		Notes:            notes,
	}
	approvalPayload, _ := json.Marshal(pa)
	if _, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: "user/promotions",
		Key:       approvalID,
		Actor:     actor,
		Payload:   approvalPayload,
	}); err != nil {
		return toolError("approve_failed", err.Error()), nil
	}

	pr.Status = "approved"
	pr.ApprovalID = approvalID
	pr.ApprovedBy = actor
	updPayload, _ := json.Marshal(pr)
	updRec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   updPayload,
	})
	if err != nil {
		return toolError("approve_failed", err.Error()), nil
	}

	_ = a.Store.EmitPromote(ctx, contextstore.EventPromoteApprove, actor, reqNamespace, requestID, updRec.Revision, updRec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"approval_id":%q,"source":"mcp"}`, approvalID)))

	return toolJSON(map[string]any{
		"approval_id": approvalID,
		"request_id":  requestID,
		"status":      "approved",
	}), nil
}

func (a *Adapter) handlePromoteApply(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "promote.apply")
	if errResult != nil {
		return errResult, nil
	}

	requestID := req.GetString("request_id", "")
	if requestID == "" {
		return toolError("validation_error", "request_id is required"), nil
	}
	actor := req.GetString("actor", "user")

	pr, reqNamespace, err := a.Store.GetPromoteRequest(ctx, requestID)
	if err != nil {
		return toolError("not_found", err.Error()), nil
	}
	if pr.Status != "approved" {
		return toolError("invalid_state", fmt.Sprintf("request status is %q, must be approved to apply", pr.Status)), nil
	}

	srcRec, err := a.Store.GetByRecordID(ctx, pr.SourceRevisionID)
	if err != nil {
		return toolError("not_found", fmt.Sprintf("source record not found: %v", err)), nil
	}

	newRec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: pr.TargetNamespace,
		Key:       pr.TargetKey,
		Actor:     actor,
		Payload:   srcRec.Payload,
	})
	if err != nil {
		return toolError("apply_failed", err.Error()), nil
	}

	pr.Status = "applied"
	appliedPayload, _ := json.Marshal(pr)
	_, _ = a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   appliedPayload,
	})

	_ = a.Store.EmitPromote(ctx, contextstore.EventPromote, actor, pr.TargetNamespace, pr.TargetKey, newRec.Revision, newRec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"request_id":%q,"source":"mcp"}`, requestID)))

	return toolJSON(map[string]any{
		"record_id":        newRec.RecordID,
		"request_id":       requestID,
		"status":           "applied",
		"target_namespace": pr.TargetNamespace,
		"target_key":       pr.TargetKey,
	}), nil
}

// --- Helpers ---

func recordJSON(rec contextstore.Record) map[string]any {
	return map[string]any{
		"record_id":  rec.RecordID,
		"namespace":  rec.Namespace,
		"key":        rec.Key,
		"revision":   rec.Revision,
		"actor":      rec.Actor,
		"created_at": rec.CreatedAt,
		"checksum":   rec.Checksum,
	}
}

// argsMap extracts the Arguments from a CallToolRequest as map[string]any.
// Returns nil if not present or wrong type — budget helpers handle nil safely.
func argsMap(req mcp.CallToolRequest) map[string]any {
	if m, ok := req.Params.Arguments.(map[string]any); ok {
		return m
	}
	return nil
}

func newMCPRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "mcp-" + hex.EncodeToString(b)
}

// --- Context planner tools ---

func (a *Adapter) handleContextPlan(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	intent := req.GetString("intent", "custom")
	summary := req.GetString("summary", "")
	maxItems := req.GetInt("budget_items", 50)
	maxTokens := req.GetInt("budget_tokens", 4000)

	namespaces, includePins, rationale := buildContextPlan(intent, summary, maxItems, maxTokens)

	return toolJSON(map[string]any{
		"plan": map[string]any{
			"namespaces":   namespaces,
			"include_pins": includePins,
			"budget": map[string]any{
				"max_items":           maxItems,
				"max_tokens_estimate": maxTokens,
			},
		},
		"rationale": rationale,
	}), nil
}

func (a *Adapter) handleContextFetch(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	intent := req.GetString("intent", "custom")
	summary := req.GetString("summary", "")
	maxItems := req.GetInt("budget_items", 50)
	maxTokens := req.GetInt("budget_tokens", 4000)
	payloadMode := req.GetString("payload_mode", "full")

	namespaces, includePins, rationale := buildContextPlan(intent, summary, maxItems, maxTokens)

	// Load optional token globs for filtering.
	var tokenGlobs []string
	if claims, ok := a.loadClaims(ctx); ok {
		tokenGlobs = claims.NamespaceGlobs
	}

	reqID := newMCPRequestID()
	var items []map[string]any
	pinsIncluded := 0
	bytesSoFar, tokensSoFar := 0, 0
	truncated := false
	truncationReason := ""
	sources := map[string]int{}

	budgetExceeded := func() bool {
		if maxItems > 0 && len(items) >= maxItems {
			truncationReason = "budget.max_items"
			return true
		}
		if maxTokens > 0 && tokensSoFar >= maxTokens {
			truncationReason = "budget.max_tokens_estimate"
			return true
		}
		return false
	}

	addRecord := func(rec contextstore.Record) bool {
		if budgetExceeded() {
			return false
		}
		payload := rec.Payload
		if payloadMode == "head_only" && len(payload) > 512 {
			payload = payload[:512]
		}
		item := recordJSON(rec)
		item["payload"] = json.RawMessage(payload)
		items = append(items, item)
		bytesSoFar += len(payload)
		tokensSoFar += contextstore.EstimateTokens(payload)
		parts := strings.SplitN(rec.Namespace, "/", 3)
		nsKey := rec.Namespace
		if len(parts) >= 2 {
			nsKey = parts[0] + "/" + parts[1]
		}
		sources[nsKey]++
		return true
	}

	// Pins first (when context plan includes them).
	if includePins {
		pins, err := a.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{"user/pins/*"},
			RevisionScope: "head",
		})
		if err == nil {
			if len(tokenGlobs) > 0 {
				pins = filterByGlobs(pins, tokenGlobs)
			}
			for _, rec := range pins {
				if addRecord(rec) {
					pinsIncluded++
				}
			}
		}
	}

	// Main namespaces from context plan.
	seen := map[string]bool{}
	for _, ns := range namespaces {
		if budgetExceeded() {
			truncated = true
			break
		}
		recs, err := a.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{ns},
			RevisionScope: "head",
		})
		if err != nil {
			continue
		}
		if len(tokenGlobs) > 0 {
			recs = filterByGlobs(recs, tokenGlobs)
		}
		for _, rec := range recs {
			if seen[rec.RecordID] {
				continue
			}
			seen[rec.RecordID] = true
			if !addRecord(rec) {
				truncated = true
				break
			}
		}
	}

	if items == nil {
		items = []map[string]any{}
	}

	manifest := map[string]any{
		"request_id":        reqID,
		"pins_included":     pinsIncluded,
		"items_returned":    len(items) - pinsIncluded,
		"bytes_returned":    bytesSoFar,
		"tokens_estimate":   tokensSoFar,
		"truncated":         truncated,
		"truncation_reason": truncationReason,
		"sources":           sources,
	}

	return toolJSON(map[string]any{
		"items":     items,
		"manifest":  manifest,
		"rationale": rationale,
	}), nil
}

// buildContextPlan derives namespace patterns and fetch strategy from an intent.
// This is Conduit's internal query planner — not the universal ContextBroker.
func buildContextPlan(intent, summary string, maxItems, _ int) (namespaces []string, includePins bool, rationale string) {
	switch intent {
	case "resume_task":
		keywords := plannerExtractKeywords(summary, 3)
		for _, kw := range keywords {
			namespaces = append(namespaces, "user/memory/"+kw+"*")
		}
		namespaces = append(namespaces, "user/pins/*")
		includePins = true
		if len(keywords) > 0 {
			rationale = fmt.Sprintf("resume_task: patterns from keywords [%s] + user/pins/*", strings.Join(keywords, ", "))
		} else {
			namespaces = append(namespaces, "user/memory/*")
			rationale = "resume_task: no keywords extracted; using user/memory/* + user/pins/*"
		}
	case "boot_project":
		namespaces = []string{"user/memory/*", "user/pins/*"}
		includePins = true
		if maxItems < 100 {
			maxItems = 100
		}
		rationale = "boot_project: user/memory/* + user/pins/* for full project boot"
	case "review_session":
		namespaces = []string{"user/cache/*", "user/pins/*"}
		includePins = true
		rationale = "review_session: user/cache/* + user/pins/*"
	default:
		namespaces = []string{"user/*"}
		rationale = "custom: using user/* (no explicit constraints provided)"
	}
	return namespaces, includePins, rationale
}

// plannerExtractKeywords pulls up to n meaningful words from text, skipping stopwords.
var plannerStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "in": true, "is": true, "it": true,
	"its": true, "of": true, "on": true, "or": true, "the": true,
	"this": true, "that": true, "to": true, "was": true, "with": true,
	"we": true, "i": true, "my": true, "me": true, "our": true,
	"will": true, "not": true, "but": true, "into": true, "just": true,
	"task": true, "work": true, "previous": true, "new": true,
	"using": true, "use": true, "via": true, "which": true, "all": true,
}

func plannerExtractKeywords(text string, n int) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9') && r != '-' && r != '_'
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) < 3 || plannerStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= n {
			break
		}
	}
	return out
}

// --- Namespace management tools ---

func (a *Adapter) handleNamespaceRegister(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "namespace.admin")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	ownerType := req.GetString("owner_type", "")
	ownerID := req.GetString("owner_id", "")

	if ns == "" || ownerType == "" || ownerID == "" {
		return toolError("validation_error", "namespace, owner_type, and owner_id are required"), nil
	}

	entry := contextstore.NamespacePolicyEntry{
		Namespace: ns,
		OwnerType: ownerType,
		OwnerID:   ownerID,
	}
	if err := a.Store.UpsertNamespacePolicy(ctx, entry); err != nil {
		return toolError("register_failed", err.Error()), nil
	}

	return toolJSON(map[string]any{
		"namespace":  ns,
		"owner_type": ownerType,
		"owner_id":   ownerID,
	}), nil
}

func (a *Adapter) handleNamespaceShow(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	if ns == "" {
		return toolError("validation_error", "namespace is required"), nil
	}

	entry, err := a.Store.GetNamespacePolicy(ctx, ns)
	if err != nil {
		return toolError("not_found", fmt.Sprintf("namespace policy not found: %v", err)), nil
	}

	return toolJSON(map[string]any{
		"namespace":  entry.Namespace,
		"owner_type": entry.OwnerType,
		"owner_id":   entry.OwnerID,
		"policy":     entry.Policy,
	}), nil
}

func (a *Adapter) handleNamespacesList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	prefix := req.GetString("prefix", "")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)

	entries, err := a.Store.ListNamespacePolicies(ctx)
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}

	// String-prefix filtering, matching the HTTP /v1/namespaces/list semantics.
	// Was using globsPermit, which treats `prefix` as a glob — divergent from
	// HTTP and surprising for callers who pass a literal path prefix like
	// `user/chrispian/project/`. CW-20260428-0005 follow-up.
	var items []map[string]any
	for _, entry := range entries {
		if prefix != "" && !strings.HasPrefix(entry.Namespace, prefix) {
			continue
		}
		items = append(items, map[string]any{
			"namespace":  entry.Namespace,
			"owner_type": entry.OwnerType,
			"owner_id":   entry.OwnerID,
			"policy":     entry.Policy,
		})
	}
	if items == nil {
		items = []map[string]any{}
	}
	env := budget.Apply(items, budget.Config{Limit: limit}, "%d namespaces available. Use context_namespace_show for details on a specific namespace.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

// --- Audit tool ---

func (a *Adapter) handleAudit(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	eventType := req.GetString("event_type", "")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)
	cursor := req.GetInt("cursor", 0)

	events, nextCursor, err := a.Store.QueryAuditEvents(ctx, contextstore.AuditQuery{
		Limit:     limit,
		Cursor:    int64(cursor),
		Namespace: ns,
		EventType: eventType,
	})
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}

	items := make([]map[string]any, len(events))
	for i, ev := range events {
		item := map[string]any{
			"id":         ev.ID,
			"event_type": ev.EventType,
			"actor":      ev.Actor,
			"namespace":  ev.Namespace,
			"key":        ev.Key,
			"revision":   ev.Revision,
			"created_at": ev.CreatedAt,
		}
		if ev.RecordID != "" {
			item["record_id"] = ev.RecordID
		}
		if len(ev.Metadata) > 0 {
			// Metadata is json.RawMessage; pass as-is so callers receive
			// the parsed object rather than a quoted string.
			item["metadata"] = json.RawMessage(ev.Metadata)
		}
		items[i] = item
	}

	env := budget.Apply(items, budget.Config{Limit: limit}, "%d audit events available. Use cursor parameter for pagination.")
	// Preserve cursor in the envelope for pagination continuity.
	envMap := map[string]any{}
	envJSON, _ := json.Marshal(env)
	_ = json.Unmarshal(envJSON, &envMap)
	if nextCursor != nil {
		envMap["next_cursor"] = *nextCursor
	}
	return mcp.NewToolResultText(budget.ToolJSON(envMap)), nil
}

// loadClaims returns the token claims if a token is configured and valid.
// Returns false (no error) when no token is set — callers use this for optional filtering.
func (a *Adapter) loadClaims(ctx context.Context) (contextstore.AuthToken, bool) {
	if a.Token == "" {
		return contextstore.AuthToken{}, false
	}
	claims, err := a.Store.ValidateAuthTokenWithClaims(ctx, a.Token)
	if err != nil {
		return contextstore.AuthToken{}, false
	}
	return claims, true
}

// globsPermit reports whether namespace is permitted by any of the globs.
// Matches the same logic as the HTTP API's requireNamespaceAccess:
//   - empty globs slice or glob=="*" → full access
//   - path.Match(glob, namespace) → exact glob
//   - glob ending in /* → hierarchical prefix match
func globsPermit(globs []string, namespace string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" || g == "*" || g == namespace {
			return true
		}
		if matched, _ := path.Match(g, namespace); matched {
			return true
		}
		if strings.HasSuffix(g, "/*") {
			prefix := strings.TrimSuffix(g, "*") // "app/test/"
			if strings.HasPrefix(namespace, prefix) {
				return true
			}
		}
	}
	return false
}

// filterByGlobs returns only the records whose namespace is permitted by globs.
func filterByGlobs(records []contextstore.Record, globs []string) []contextstore.Record {
	if len(globs) == 0 {
		return records
	}
	out := records[:0:0]
	for _, rec := range records {
		if globsPermit(globs, rec.Namespace) {
			out = append(out, rec)
		}
	}
	return out
}

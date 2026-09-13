package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"github.com/hollis-labs/tesseract/internal/workspacepromotion"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) itemService() *itemservice.Service {
	return &itemservice.Service{Revisions: a.revisionStore(), Workspace: a.WorkspaceStore}
}

func (a *Adapter) registerWorkspaceTools(s *toolRegistrar) {
	a.addTool(s, mcp.NewTool("workspace_write",
		mcp.WithDescription("Create or conditionally edit one mutable workspace item. Namespace selects create; item_id + version_token select edit. Creates return a small created/replayed receipt and edits return a fresh version token. Keyless creates require idempotency_key. Content is overwritten in place and workspace retains no revision history."),
		mcp.WithString("namespace", mcp.Description("Create selector: exact workspace namespace.")),
		mcp.WithString("item_id", mcp.Description("Edit selector: stable workspace item ID.")),
		mcp.WithString("version_token", mcp.Description("Edit selector: current concurrency token.")),
		mcp.WithString("idempotency_key", mcp.Description("Opaque retry key; required for keyless create and optional for keyed create.")),
		mcp.WithString("key", mcp.Description("Optional exact human key. On edit, use clear_fields to remove it.")),
		mcp.WithString("workstream_id", mcp.Description("Optional opaque association. On edit, use clear_fields to remove it.")),
		mcp.WithString("summary", mcp.Description("Required non-empty summary on create; optional replacement on edit.")),
		mcp.WithString("body", mcp.Description("Optional body. On edit, use clear_fields to remove it.")),
		mcp.WithString("data", mcp.Description(payloadDataArgDescription)),
		mcp.WithString("data_schema_hash", mcp.Description(payloadDataSchemaHashArgDescription)),
		mcp.WithString("tags", mcp.Description("JSON array of strings. Supplying it replaces the full tag list.")),
		mcp.WithString("consumer_state", mcp.Description(consumerStateArgDescription)),
		mcp.WithString("clear_fields", mcp.Description("Edit only: JSON array drawn from key, body, data, tags, consumer_state, workstream_id.")),
		mcp.WithString("author_agent_id", mcp.Description("Required author agent ID.")),
		mcp.WithString("author_version", mcp.Description("Optional author version.")),
		mcp.WithString("session_id", mcp.Description("Required authoring session ID.")),
		mcp.WithReadOnlyHintAnnotation(false), mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false), mcp.WithOpenWorldHintAnnotation(false),
	), a.handleWorkspaceWrite)

	a.addTool(s, mcp.NewTool("workspace_delete",
		mcp.WithDescription("Conditionally delete one workspace item's content while retaining its minimal identity tombstone. Repeating a delete returns the original deleted receipt. Errors distinguish version_conflict, deleted content reads, and not_found identities."),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Stable workspace item ID.")),
		mcp.WithString("version_token", mcp.Required(), mcp.Description("Current concurrency token.")),
		mcp.WithReadOnlyHintAnnotation(false), mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(true), mcp.WithOpenWorldHintAnnotation(false),
	), a.handleWorkspaceDelete)

	a.addTool(s, mcp.NewTool("workspace_promote",
		mcp.WithDescription("Promote reviewed live workspace content to a memory, knowledge, or event item through request, approve, and apply. The request freezes destination metadata and association; apply atomically verifies the source version and target preconditions, writes one revision, and records a replay receipt."),
		mcp.WithString("stage", mcp.Required(), mcp.Description("Required stage: request | approve | apply. Each stage checks only its matching promote.request, promote.approve, or promote.apply scope.")),
		mcp.WithString("source_item_id"), mcp.WithString("source_version_token"),
		mcp.WithString("request_id"), mcp.WithString("actor"), mcp.WithString("reason"), mcp.WithString("notes"),
		mcp.WithString("target_domain"), mcp.WithString("target_namespace"), mcp.WithString("target_key"),
		mcp.WithString("target_item_id"), mcp.WithString("expected_target_revision_id"),
		mcp.WithString("target_author_agent_id"), mcp.WithString("target_author_version"), mcp.WithString("target_session_id"),
		mcp.WithString("target_tags", mcp.Description("JSON array of destination tags.")),
		mcp.WithString("target_consumer_state", mcp.Description("JSON object stored as destination consumer_state.")),
		mcp.WithNumber("target_confidence"), mcp.WithNumber("target_ttl_seconds"),
		mcp.WithString("target_data_schema_hash"), mcp.WithString("target_workstream_id"),
		mcp.WithString("target_status"), mcp.WithString("target_trigger"), mcp.WithString("target_derived_from"),
		mcp.WithString("target_kind"), mcp.WithString("target_source"),
		mcp.WithString("target_pointer_scheme"), mcp.WithString("target_pointer_locator"), mcp.WithString("target_pointer_resolved_at"),
		mcp.WithReadOnlyHintAnnotation(false), mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false), mcp.WithOpenWorldHintAnnotation(false),
	), a.handleWorkspacePromote)
}

func (a *Adapter) handleWorkspacePromote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	stage := req.GetString("stage", "")
	if stage != "request" && stage != "approve" && stage != "apply" {
		return toolError(codeValidationError, "stage is required and must be request, approve, or apply"), nil
	}
	if a.WorkspacePromotionStore == nil {
		return toolError(codeDomainUnavailable, "workspace promotion store is not wired"), nil
	}
	res, claims := a.checkScope(ctx, "promote."+stage)
	if res != nil {
		return res, nil
	}
	args := req.GetArguments()
	for name, value := range args {
		if value == nil {
			return toolError(codeValidationError, name+" must not be null; omit it instead"), nil
		}
	}

	if stage == "request" {
		if _, ok := args["request_id"]; ok {
			return toolError(codeValidationError, "request_id is valid only for approve or apply"), nil
		}
		if _, ok := args["notes"]; ok {
			return toolError(codeValidationError, "notes is valid only for approve"), nil
		}
		sourceID, token, actor := req.GetString("source_item_id", ""), req.GetString("source_version_token", ""), req.GetString("actor", "")
		if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(actor) == "" {
			return toolError(codeValidationError, "source_item_id, source_version_token, and actor are required for request"), nil
		}
		source, err := a.itemService().LookupMetadata(ctx, sourceID)
		if err != nil {
			return workspacePromotionToolError(err), nil
		}
		if denied := a.authorizeWorkspacePromotion(ctx, claims, "promote.request", source.Namespace); denied != nil {
			return denied, nil
		}
		if source.Domain != workspace.Domain {
			return toolError(codeValidationError, "source_item_id must identify a workspace item"), nil
		}

		target, parseErr := workspacePromotionTarget(req)
		if parseErr != nil {
			return parseErr, nil
		}
		targetNamespace := target.Namespace
		if target.ItemID != "" {
			targetMeta, targetErr := a.itemService().LookupMetadata(ctx, target.ItemID)
			if targetErr != nil {
				return workspacePromotionToolError(targetErr), nil
			}
			if targetMeta.Domain == workspace.Domain || targetMeta.Domain == "context" {
				return toolError(codeValidationError, "target item must be revisioned"), nil
			}
			targetNamespace = targetMeta.Namespace
		}
		if denied := a.authorizeWorkspacePromotion(ctx, claims, "promote.request", targetNamespace); denied != nil {
			return denied, nil
		}
		receipt, err := a.WorkspacePromotionStore.Request(ctx, workspacepromotion.RequestInput{SourceItemID: sourceID, SourceVersionToken: token, Actor: actor, Reason: req.GetString("reason", ""), Target: target})
		if err != nil {
			return workspacePromotionToolError(err), nil
		}
		return toolJSON(receipt), nil
	}

	for name := range args {
		allowed := name == "stage" || name == "request_id" || name == "actor" || stage == "approve" && name == "notes"
		if !allowed {
			return toolError(codeValidationError, name+" is not valid for stage="+stage), nil
		}
	}
	requestID, actor := req.GetString("request_id", ""), req.GetString("actor", "")
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(actor) == "" {
		return toolError(codeValidationError, "request_id and actor are required for "+stage), nil
	}
	meta, err := a.WorkspacePromotionStore.LookupMetadata(ctx, requestID)
	if err != nil {
		return workspacePromotionToolError(err), nil
	}
	if denied := a.authorizeWorkspacePromotion(ctx, claims, "promote."+stage, meta.SourceNamespace, meta.TargetNamespace); denied != nil {
		return denied, nil
	}
	if stage == "approve" {
		approvalReceipt, approvalErr := a.WorkspacePromotionStore.Approve(ctx, workspacepromotion.ApproveInput{RequestID: requestID, Actor: actor, Notes: req.GetString("notes", "")})
		if approvalErr != nil {
			return workspacePromotionToolError(approvalErr), nil
		}
		return toolJSON(approvalReceipt), nil
	}
	receipt, err := a.WorkspacePromotionStore.Apply(ctx, workspacepromotion.ApplyInput{RequestID: requestID, Actor: actor})
	if err != nil {
		return workspacePromotionToolError(err), nil
	}
	return toolJSON(receipt), nil
}

func workspacePromotionTarget(req mcp.CallToolRequest) (workspacepromotion.Target, *mcp.CallToolResult) {
	args := req.GetArguments()
	if _, hasItem := args["target_item_id"]; hasItem {
		for _, redundant := range []string{"target_domain", "target_namespace", "target_key"} {
			if _, ok := args[redundant]; ok {
				return workspacepromotion.Target{}, toolError(codeValidationError, "target_item_id cannot be combined with "+redundant)
			}
		}
	}
	target := workspacepromotion.Target{
		Domain: domains.Domain(req.GetString("target_domain", "")), Namespace: req.GetString("target_namespace", ""), Key: req.GetString("target_key", ""),
		ItemID: req.GetString("target_item_id", ""), ExpectedRevisionID: req.GetString("expected_target_revision_id", ""),
		Author:    memory.Author{AgentID: req.GetString("target_author_agent_id", ""), AgentVersion: req.GetString("target_author_version", "")},
		SessionID: req.GetString("target_session_id", ""), Confidence: req.GetFloat("target_confidence", 0),
		DataSchemaHash: req.GetString("target_data_schema_hash", ""), Status: memory.Status(req.GetString("target_status", "")),
		Trigger: memory.Trigger(req.GetString("target_trigger", "")), DerivedFrom: memory.DerivedFrom(req.GetString("target_derived_from", "")),
		Kind: req.GetString("target_kind", ""), Source: req.GetString("target_source", ""),
	}
	tags, _, err := parseStringArrayArg(req, "target_tags")
	if err != nil {
		return target, toolError(codeValidationError, "target_tags "+err.Error())
	}
	target.Tags = tags
	if _, ok := args["target_consumer_state"]; ok {
		raw := args["target_consumer_state"]
		if raw == nil {
			target.ConsumerState = []byte("null")
		} else if value, ok := raw.(string); ok {
			target.ConsumerState = []byte(value)
		} else if encoded, err := json.Marshal(raw); err == nil {
			target.ConsumerState = encoded
		} else {
			target.ConsumerState = []byte("null")
		}
	}
	if _, ok := args["target_workstream_id"]; ok {
		value, errResult := workstreamWriteArgFor(req, "target_workstream_id")
		if errResult != nil {
			return target, errResult
		}
		target.WorkstreamID = value
	}
	if _, ok := args["target_ttl_seconds"]; ok {
		seconds, err := wholeNumberArg(req, "target_ttl_seconds", 0)
		if err != nil {
			return target, toolError(codeValidationError, err.Error())
		}
		target.TTLSeconds = int64(seconds)
	}
	scheme, schemeOK := args["target_pointer_scheme"]
	locator, locatorOK := args["target_pointer_locator"]
	resolved, resolvedOK := args["target_pointer_resolved_at"]
	if schemeOK || locatorOK || resolvedOK {
		_ = scheme
		_ = locator
		_ = resolved
		target.Pointer = &memory.Pointer{Scheme: req.GetString("target_pointer_scheme", ""), Locator: req.GetString("target_pointer_locator", "")}
		if resolvedOK {
			parsed, err := time.Parse(time.RFC3339Nano, req.GetString("target_pointer_resolved_at", ""))
			if err != nil {
				return target, toolError(codeValidationError, "target_pointer_resolved_at must be RFC3339")
			}
			target.Pointer.ResolvedAt = &parsed
		}
	}
	return target, nil
}

func workstreamWriteArgFor(req mcp.CallToolRequest, key string) (*string, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[key]
	if !ok {
		return nil, nil
	}
	value, ok := raw.(string)
	if !ok {
		return nil, toolError(codeValidationError, key+" must be a string; null is not omission")
	}
	if err := memory.ValidateWorkstreamID(value); err != nil {
		return nil, toolError(codeValidationError, err.Error())
	}
	return &value, nil
}

func (a *Adapter) authorizeWorkspacePromotion(ctx context.Context, claims contextstore.AuthToken, op string, namespaces ...string) *mcp.CallToolResult {
	for _, namespace := range namespaces {
		if namespace == "" {
			return toolError(codeValidationError, "source and target namespaces are required")
		}
		if !globsPermit(claims.NamespaceGlobs, namespace) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit one of the promotion namespaces")
		}
		entry, err := a.Store.GetNamespacePolicy(ctx, namespace)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return toolError(codePromoteFailed, err.Error())
		}
		if !contextpolicy.ParseTierPolicy(entry.Policy).HasOp(op) {
			return toolError(codeNamespaceNotPermitted, "promotion namespace policy denies "+op)
		}
	}
	return nil
}

func workspacePromotionToolError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, workspacepromotion.ErrInvalidInput), errors.Is(err, memory.ErrInvalidInput):
		return toolError(codeValidationError, err.Error())
	case errors.Is(err, workspacepromotion.ErrNotFound), errors.Is(err, workspacepromotion.ErrSourceNotFound), errors.Is(err, memory.ErrNotFound):
		return toolError(codeNotFound, err.Error())
	case errors.Is(err, workspacepromotion.ErrSourceDeleted):
		return toolError(codeDeleted, err.Error())
	case errors.Is(err, workspacepromotion.ErrSourceStale), errors.Is(err, workspacepromotion.ErrTargetStale):
		return toolError(codeVersionConflict, err.Error())
	case errors.Is(err, workspacepromotion.ErrTargetKeyOccupied):
		return toolError(codeKeyConflict, err.Error())
	case errors.Is(err, workspacepromotion.ErrNotApproved):
		return toolError(codeInvalidState, err.Error())
	default:
		return toolError(codePromoteFailed, err.Error())
	}
}

func (a *Adapter) handleWorkspaceWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	res, claims := a.checkScope(ctx, "memory:write")
	if res != nil {
		return res, nil
	}
	args := req.GetArguments()
	_, hasNamespace := args["namespace"]
	_, hasItemID := args["item_id"]
	_, hasVersion := args["version_token"]
	if hasNamespace {
		if hasItemID || hasVersion {
			return toolError(codeValidationError, "choose create by namespace or edit by item_id + version_token; do not mix selectors"), nil
		}
		if _, ok := args["clear_fields"]; ok {
			return toolError(codeValidationError, "clear_fields is valid only for edit"), nil
		}
		ns := req.GetString("namespace", "")
		if !globsPermit(claims.NamespaceGlobs, ns) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit writing: "+ns), nil
		}
		tags, _, err := parseStringArrayArg(req, "tags")
		if err != nil {
			return toolError(codeValidationError, "tags "+err.Error()), nil //nolint:nilerr // MCP application errors are tool results
		}
		workstreamID, workstreamErr := workstreamWriteArg(req)
		if workstreamErr != nil {
			return workstreamErr, nil
		}
		create := workspace.CreateRequest{CreateInput: workspace.CreateInput{
			Namespace: ns, Key: req.GetString("key", ""), WorkstreamID: workstreamID, Summary: req.GetString("summary", ""), Body: req.GetString("body", ""),
			Data: payloadDataArg(req), DataSchemaHash: req.GetString("data_schema_hash", ""), Tags: tags,
			ConsumerState: consumerStateArg(req), Author: memory.Author{AgentID: req.GetString("author_agent_id", ""), AgentVersion: req.GetString("author_version", "")}, SessionID: req.GetString("session_id", ""),
		}, IdempotencyKey: req.GetString("idempotency_key", "")}
		receipt, err := a.WorkspaceStore.CreateWithReceipt(ctx, create)
		if err != nil {
			return workspaceToolError(err), nil
		}
		return toolJSON(receipt), nil
	}
	if !hasItemID || !hasVersion {
		return toolError(codeValidationError, "choose create by namespace or edit by item_id + version_token"), nil
	}
	for _, createOnly := range []string{"namespace", "idempotency_key"} {
		if _, ok := args[createOnly]; ok {
			return toolError(codeValidationError, createOnly+" is valid only for create"), nil
		}
	}
	meta, err := a.itemService().LookupMetadata(ctx, req.GetString("item_id", ""))
	if err != nil {
		return workspaceToolError(err), nil
	}
	if meta.Domain != workspace.Domain {
		return toolError(codeValidationError, "item_id does not identify a workspace item"), nil
	}
	if !globsPermit(claims.NamespaceGlobs, meta.Namespace) {
		return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit writing: "+meta.Namespace), nil
	}
	edit, parseErr := workspaceEditInput(req)
	if parseErr != nil {
		return parseErr, nil
	}
	item, err := a.WorkspaceStore.Edit(ctx, edit)
	if err != nil {
		return workspaceToolError(err), nil
	}
	return toolJSON(workspace.MutationReceipt{Status: "updated", ItemID: item.ItemID, VersionToken: item.VersionToken}), nil
}

func workspaceEditInput(req mcp.CallToolRequest) (workspace.EditInput, *mcp.CallToolResult) {
	args := req.GetArguments()
	in := workspace.EditInput{ItemID: req.GetString("item_id", ""), VersionToken: req.GetString("version_token", ""), Author: memory.Author{AgentID: req.GetString("author_agent_id", ""), AgentVersion: req.GetString("author_version", "")}, SessionID: req.GetString("session_id", "")}
	for name, dst := range map[string]**string{"key": &in.Key, "summary": &in.Summary, "body": &in.Body, "data_schema_hash": &in.DataSchemaHash} {
		if _, ok := args[name]; ok {
			v := req.GetString(name, "")
			*dst = &v
		}
	}
	if _, ok := args["data"]; ok {
		raw := payloadDataArg(req)
		in.Data = &raw
	}
	if _, ok := args["consumer_state"]; ok {
		raw := consumerStateArg(req)
		in.ConsumerState = &raw
	}
	if _, ok := args["workstream_id"]; ok {
		value, errResult := workstreamWriteArg(req)
		if errResult != nil {
			return in, errResult
		}
		in.WorkstreamID = value
	}
	if _, ok := args["tags"]; ok {
		tags, _, err := parseStringArrayArg(req, "tags")
		if err != nil {
			return in, toolError(codeValidationError, "tags "+err.Error())
		}
		in.Tags = &tags
	}
	if _, ok := args["clear_fields"]; ok {
		fields, _, err := parseStringArrayArg(req, "clear_fields")
		if err != nil {
			return in, toolError(codeValidationError, "clear_fields "+err.Error())
		}
		for _, f := range fields {
			in.ClearFields = append(in.ClearFields, workspace.ClearField(f))
		}
	}
	return in, nil
}

func (a *Adapter) handleWorkspaceDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	res, claims := a.checkScope(ctx, "memory:write")
	if res != nil {
		return res, nil
	}
	itemID := strings.TrimSpace(req.GetString("item_id", ""))
	token := req.GetString("version_token", "")
	if itemID == "" || token == "" {
		return toolError(codeValidationError, "item_id and version_token are required"), nil
	}
	meta, err := a.itemService().LookupMetadata(ctx, itemID)
	if err != nil {
		return workspaceDeleteToolError(err), nil
	}
	if meta.Domain != workspace.Domain {
		return toolError(codeValidationError, "item_id does not identify a workspace item"), nil
	}
	if !globsPermit(claims.NamespaceGlobs, meta.Namespace) {
		return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit deleting: "+meta.Namespace), nil
	}
	receipt, err := a.WorkspaceStore.DeleteWithReceipt(ctx, workspace.DeleteInput{ItemID: itemID, VersionToken: token})
	if err != nil {
		return workspaceDeleteToolError(err), nil
	}
	return toolJSON(receipt), nil
}

func workspaceToolError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, workspace.ErrInvalidInput):
		return toolError(codeValidationError, err.Error())
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, memory.ErrNotFound):
		return toolError(codeNotFound, err.Error())
	case errors.Is(err, workspace.ErrDeleted):
		return toolError(codeDeleted, err.Error())
	case errors.Is(err, workspace.ErrKeyConflict):
		return toolError(codeKeyConflict, err.Error())
	case errors.Is(err, workspace.ErrVersionConflict):
		return toolError(codeVersionConflict, err.Error())
	case errors.Is(err, workspace.ErrIdempotencyConflict):
		return toolError(codeIdempotencyConflict, err.Error())
	case errors.Is(err, itemservice.ErrHistoryUnavailable):
		return toolError(codeHistoryUnavailable, err.Error())
	default:
		return toolError(codeWriteFailed, err.Error())
	}
}

func workspaceDeleteToolError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, workspace.ErrInvalidInput):
		return toolError(codeValidationError, err.Error())
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, memory.ErrNotFound):
		return toolError(codeNotFound, err.Error())
	case errors.Is(err, workspace.ErrDeleted):
		return toolError(codeDeleted, err.Error())
	case errors.Is(err, workspace.ErrVersionConflict):
		return toolError(codeVersionConflict, err.Error())
	default:
		return toolError(codeDeleteFailed, err.Error())
	}
}

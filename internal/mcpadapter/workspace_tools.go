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
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func (a *Adapter) itemService() *itemservice.Service {
	return &itemservice.Service{Revisions: a.revisionStore(), Workspace: a.WorkspaceStore}
}

func (a *Adapter) registerWorkspaceTools(s *toolRegistrar) {
	a.addTool(s, gomcpTool("workspace_write",
		"Create or conditionally edit one mutable workspace item. Namespace selects create; item_id + version_token select edit. Creates return a small created/replayed receipt and edits return a fresh version token. Keyless creates require idempotency_key. Content is overwritten in place and workspace retains no revision history.",
		inputSchema(
			strProp("namespace", "Create selector: exact workspace namespace.", false),
			strProp("item_id", "Edit selector: stable workspace item ID.", false),
			strProp("version_token", "Edit selector: current concurrency token.", false),
			strProp("idempotency_key", "Opaque retry key; required for keyless create and optional for keyed create.", false),
			strProp("key", "Optional exact human key. On edit, use clear_fields to remove it.", false),
			strProp("workstream_id", "Optional opaque association. On edit, use clear_fields to remove it.", false),
			strProp("summary", "Required non-empty summary on create; optional replacement on edit.", false),
			strProp("body", "Optional body. On edit, use clear_fields to remove it.", false),
			strProp("data", payloadDataArgDescription, false),
			strProp("data_schema_hash", payloadDataSchemaHashArgDescription, false),
			strProp("tags", "JSON array of strings. Supplying it replaces the full tag list.", false),
			strProp("consumer_state", consumerStateArgDescription, false),
			strProp("clear_fields", "Edit only: JSON array drawn from key, body, data, tags, consumer_state, workstream_id.", false),
			strProp("author_agent_id", "Required author agent ID.", false),
			strProp("author_version", "Optional author version.", false),
			strProp("session_id", "Required authoring session ID.", false),
		),
		toolAnnotations{},
		a.handleWorkspaceWrite,
	))

	a.addTool(s, gomcpTool("workspace_delete",
		"Conditionally delete one workspace item's content while retaining its minimal identity tombstone. Repeating a delete returns the original deleted receipt. Errors distinguish version_conflict, deleted content reads, and not_found identities.",
		inputSchema(
			strProp("item_id", "Stable workspace item ID.", true),
			strProp("version_token", "Current concurrency token.", true),
		),
		toolAnnotations{IdempotentHint: true, DestructiveHint: true},
		a.handleWorkspaceDelete,
	))

	a.addTool(s, gomcpTool("workspace_promote",
		"Promote reviewed live workspace content to a memory, knowledge, or event item through request, approve, and apply. The request freezes destination metadata and association; apply atomically verifies the source version and target preconditions, writes one revision, and records a replay receipt.",
		inputSchema(
			strProp("stage", "Required stage: request | approve | apply. Each stage checks only its matching promote.request, promote.approve, or promote.apply scope.", true),
			strProp("source_item_id", "", false), strProp("source_version_token", "", false),
			strProp("request_id", "", false), strProp("actor", "", false), strProp("reason", "", false), strProp("notes", "", false),
			strProp("target_domain", "", false), strProp("target_namespace", "", false), strProp("target_key", "", false),
			strProp("target_item_id", "", false), strProp("expected_target_revision_id", "", false),
			strProp("target_author_agent_id", "", false), strProp("target_author_version", "", false), strProp("target_session_id", "", false),
			strProp("target_tags", "JSON array of destination tags.", false),
			strProp("target_consumer_state", "JSON object stored as destination consumer_state.", false),
			numProp("target_confidence", "", false), numProp("target_ttl_seconds", "", false),
			strProp("target_data_schema_hash", "", false), strProp("target_workstream_id", "", false),
			strProp("target_status", "", false), strProp("target_trigger", "", false), strProp("target_derived_from", "", false),
			strProp("target_kind", "", false), strProp("target_source", "", false),
			strProp("target_pointer_scheme", "", false), strProp("target_pointer_locator", "", false), strProp("target_pointer_resolved_at", "", false),
		),
		toolAnnotations{IdempotentHint: true},
		a.handleWorkspacePromote,
	))
}

func (a *Adapter) handleWorkspacePromote(ctx context.Context, req map[string]any) (any, error) {
	stage := argString(req, "stage", "")
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
	args := req
	for name, value := range args {
		if value == nil {
			return toolError(codeValidationError, name+" must not be null; omit it instead"), nil
		}
	}
	if typeErr := validateWorkspacePromotionArgTypes(args); typeErr != nil {
		return typeErr, nil
	}

	if stage == "request" {
		if _, ok := args["request_id"]; ok {
			return toolError(codeValidationError, "request_id is valid only for approve or apply"), nil
		}
		if _, ok := args["notes"]; ok {
			return toolError(codeValidationError, "notes is valid only for approve"), nil
		}
		sourceID, token, actor := argString(req, "source_item_id", ""), argString(req, "source_version_token", ""), argString(req, "actor", "")
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
			targetNamespace = targetMeta.Namespace
			if denied := a.authorizeWorkspacePromotion(ctx, claims, "promote.request", targetNamespace); denied != nil {
				return denied, nil
			}
			if targetMeta.Domain == workspace.Domain || targetMeta.Domain == "context" {
				return toolError(codeValidationError, "target item must be revisioned"), nil
			}
		} else {
			if denied := a.authorizeWorkspacePromotion(ctx, claims, "promote.request", targetNamespace); denied != nil {
				return denied, nil
			}
		}
		receipt, err := a.WorkspacePromotionStore.Request(ctx, promotion.RequestInput{SourceItemID: sourceID, SourceVersionToken: token, Actor: actor, Reason: argString(req, "reason", ""), Target: target})
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
	requestID, actor := argString(req, "request_id", ""), argString(req, "actor", "")
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
		approvalReceipt, approvalErr := a.WorkspacePromotionStore.Approve(ctx, promotion.ApproveInput{RequestID: requestID, Actor: actor, Notes: argString(req, "notes", "")})
		if approvalErr != nil {
			return workspacePromotionToolError(approvalErr), nil
		}
		return toolJSON(approvalReceipt), nil
	}
	receipt, err := a.WorkspacePromotionStore.Apply(ctx, promotion.ApplyInput{RequestID: requestID, Actor: actor})
	if err != nil {
		return workspacePromotionToolError(err), nil
	}
	return toolJSON(receipt), nil
}

func validateWorkspacePromotionArgTypes(args map[string]any) any {
	stringFields := []string{
		"stage", "source_item_id", "source_version_token", "request_id", "actor", "reason", "notes",
		"target_domain", "target_namespace", "target_key", "target_item_id", "expected_target_revision_id",
		"target_author_agent_id", "target_author_version", "target_session_id", "target_data_schema_hash",
		"target_workstream_id", "target_status", "target_trigger", "target_derived_from", "target_kind", "target_source",
		"target_pointer_scheme", "target_pointer_locator", "target_pointer_resolved_at",
	}
	for _, name := range stringFields {
		if value, present := args[name]; present {
			if _, ok := value.(string); !ok {
				return toolError(codeValidationError, name+" must be a string")
			}
		}
	}
	for _, name := range []string{"target_confidence", "target_ttl_seconds"} {
		if value, present := args[name]; present {
			switch value.(type) {
			case float64, int:
			default:
				return toolError(codeValidationError, name+" must be a number")
			}
		}
	}
	return nil
}

func workspacePromotionTarget(req map[string]any) (promotion.Target, any) {
	args := req
	if _, hasItem := args["target_item_id"]; hasItem {
		for _, redundant := range []string{"target_domain", "target_namespace", "target_key"} {
			if _, ok := args[redundant]; ok {
				return promotion.Target{}, toolError(codeValidationError, "target_item_id cannot be combined with "+redundant)
			}
		}
	}
	target := promotion.Target{
		Domain: domains.Domain(argString(req, "target_domain", "")), Namespace: argString(req, "target_namespace", ""), Key: argString(req, "target_key", ""),
		ItemID: argString(req, "target_item_id", ""), ExpectedRevisionID: argString(req, "expected_target_revision_id", ""),
		Author:    memory.Author{AgentID: argString(req, "target_author_agent_id", ""), AgentVersion: argString(req, "target_author_version", "")},
		SessionID: argString(req, "target_session_id", ""), Confidence: argFloat(req, "target_confidence", 0),
		DataSchemaHash: argString(req, "target_data_schema_hash", ""), Status: memory.Status(argString(req, "target_status", "")),
		Trigger: memory.Trigger(argString(req, "target_trigger", "")), DerivedFrom: memory.DerivedFrom(argString(req, "target_derived_from", "")),
		Kind: argString(req, "target_kind", ""), Source: argString(req, "target_source", ""),
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
		target.Pointer = &memory.Pointer{Scheme: argString(req, "target_pointer_scheme", ""), Locator: argString(req, "target_pointer_locator", "")}
		if resolvedOK {
			parsed, err := time.Parse(time.RFC3339Nano, argString(req, "target_pointer_resolved_at", ""))
			if err != nil {
				return target, toolError(codeValidationError, "target_pointer_resolved_at must be RFC3339")
			}
			target.Pointer.ResolvedAt = &parsed
		}
	}
	return target, nil
}

func workstreamWriteArgFor(req map[string]any, key string) (*string, any) {
	raw, ok := req[key]
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

func (a *Adapter) authorizeWorkspacePromotion(ctx context.Context, claims contextstore.AuthToken, op string, namespaces ...string) any {
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

func workspacePromotionToolError(err error) any {
	switch {
	case errors.Is(err, promotion.ErrInvalidInput), errors.Is(err, memory.ErrInvalidInput):
		return toolError(codeValidationError, err.Error())
	case errors.Is(err, promotion.ErrNotFound), errors.Is(err, promotion.ErrSourceNotFound), errors.Is(err, memory.ErrNotFound):
		return toolError(codeNotFound, err.Error())
	case errors.Is(err, promotion.ErrSourceDeleted):
		return toolError(codeDeleted, err.Error())
	case errors.Is(err, promotion.ErrSourceStale), errors.Is(err, promotion.ErrTargetStale):
		return toolError(codeVersionConflict, err.Error())
	case errors.Is(err, promotion.ErrTargetKeyOccupied):
		return toolError(codeKeyConflict, err.Error())
	case errors.Is(err, promotion.ErrNotApproved):
		return toolError(codeInvalidState, err.Error())
	default:
		return toolError(codePromoteFailed, err.Error())
	}
}

func (a *Adapter) handleWorkspaceWrite(ctx context.Context, req map[string]any) (any, error) {
	res, claims := a.checkScope(ctx, "memory:write")
	if res != nil {
		return res, nil
	}
	args := req
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
		ns := argString(req, "namespace", "")
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
			Namespace: ns, Key: argString(req, "key", ""), WorkstreamID: workstreamID, Summary: argString(req, "summary", ""), Body: argString(req, "body", ""),
			Data: payloadDataArg(req), DataSchemaHash: argString(req, "data_schema_hash", ""), Tags: tags,
			ConsumerState: consumerStateArg(req), Author: memory.Author{AgentID: argString(req, "author_agent_id", ""), AgentVersion: argString(req, "author_version", "")}, SessionID: argString(req, "session_id", ""),
		}, IdempotencyKey: argString(req, "idempotency_key", "")}
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
	meta, err := a.itemService().LookupMetadata(ctx, argString(req, "item_id", ""))
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

func workspaceEditInput(req map[string]any) (workspace.EditInput, any) {
	args := req
	in := workspace.EditInput{ItemID: argString(req, "item_id", ""), VersionToken: argString(req, "version_token", ""), Author: memory.Author{AgentID: argString(req, "author_agent_id", ""), AgentVersion: argString(req, "author_version", "")}, SessionID: argString(req, "session_id", "")}
	for name, dst := range map[string]**string{"key": &in.Key, "summary": &in.Summary, "body": &in.Body, "data_schema_hash": &in.DataSchemaHash} {
		if _, ok := args[name]; ok {
			v := argString(req, name, "")
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

func (a *Adapter) handleWorkspaceDelete(ctx context.Context, req map[string]any) (any, error) {
	res, claims := a.checkScope(ctx, "memory:write")
	if res != nil {
		return res, nil
	}
	itemID := strings.TrimSpace(argString(req, "item_id", ""))
	token := argString(req, "version_token", "")
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

func workspaceToolError(err error) any {
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

func workspaceDeleteToolError(err error) any {
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

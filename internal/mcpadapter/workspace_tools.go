package mcpadapter

import (
	"context"
	"errors"
	"strings"

	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
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
		mcp.WithString("summary", mcp.Description("Required non-empty summary on create; optional replacement on edit.")),
		mcp.WithString("body", mcp.Description("Optional body. On edit, use clear_fields to remove it.")),
		mcp.WithString("data", mcp.Description(payloadDataArgDescription)),
		mcp.WithString("data_schema_hash", mcp.Description(payloadDataSchemaHashArgDescription)),
		mcp.WithString("tags", mcp.Description("JSON array of strings. Supplying it replaces the full tag list.")),
		mcp.WithString("consumer_state", mcp.Description(consumerStateArgDescription)),
		mcp.WithString("clear_fields", mcp.Description("Edit only: JSON array drawn from key, body, data, tags, consumer_state.")),
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
		create := workspace.CreateRequest{CreateInput: workspace.CreateInput{
			Namespace: ns, Key: req.GetString("key", ""), Summary: req.GetString("summary", ""), Body: req.GetString("body", ""),
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

package mcpadapter

import (
	"context"
	"strings"
	"testing"

	gomcpserver "github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestRegisteredWorkspaceWriteCapturesTetherProvenance(t *testing.T) {
	a := workspaceAdapter(t)
	srv := gomcpserver.NewServer("provenance-test", "0.0.0")
	a.RegisterAllTools(srv)
	args := map[string]any{
		"namespace": mcpWorkspaceNS, "idempotency_key": "provenance-create", "summary": "received",
		"author_agent_id": "mcp-test", "session_id": "author-session",
	}
	// WithMeta is go-mcp's own way for a caller driving a handler directly
	// (here, via Server.CallTool, which bypasses the protocol layer and so
	// installs no _meta of its own) to supply the protocol-level _meta object
	// a real client call would carry.
	ctx := gomcpserver.WithMeta(context.Background(), map[string]any{
		tetherProvenanceKey: map[string]any{"schema_version": float64(1), "session_id": "tether-session", "workstream_id": "ws-received"},
	})
	res, err := srv.CallTool(ctx, "workspace_write", args)
	if err != nil {
		t.Fatal(err)
	}
	created := wantNoError(t, parseResult(t, res))
	read := wantNoError(t, mustCallRegistered(t, a, "tesseract_get", map[string]any{"item_id": created["item_id"]}))
	if read["workstream_id"] != "ws-received" {
		t.Fatalf("context-derived association = %v", read)
	}
	provenance, _ := read["provenance"].(map[string]any)
	writeContext, _ := provenance["write_context"].(map[string]any)
	if writeContext["issuer"] != "tether" || writeContext["verification"] != "unverified" || writeContext["session_id"] != "tether-session" {
		t.Fatalf("normalized receipt = %v", read)
	}
}

const mcpWorkspaceNS = "project/tesseract/workspace/mcp-tests"

func workspaceAdapter(t *testing.T) *Adapter {
	t.Helper()
	cs := newTestStore(t)
	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "workspace", Scopes: []string{"memory:read", "memory:write", "promote.request", "promote.approve", "promote.apply"}, NamespaceGlobs: []string{"project/tesseract/workspace/*", "user/chrispian/memory/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspaceAdapterOnStore(cs, token)
}

func workspaceAdapterOnStore(cs *contextstore.Store, token string) *Adapter {
	a := New(cs, token)
	a.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a.WorkspaceStore = workspace.NewStore(cs.DB())
	a.WorkspacePromotionStore = promotion.NewStore(cs, a.MemoryStore)
	return a
}

func workspacePromotionToken(t *testing.T, cs *contextstore.Store, label string, scopes, globs []string) string {
	t.Helper()
	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{Label: label, Scopes: scopes, NamespaceGlobs: globs})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestWorkspacePromoteMCPFlatStages(t *testing.T) {
	a := workspaceAdapter(t)
	source, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{Namespace: mcpWorkspaceNS, Key: "promote-source", Summary: "MCP reviewed", Data: []byte(`{"large":90071992547409931234}`), Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	requested := wantNoError(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{
		"stage": "request", "source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester",
		"target_domain": "memory", "target_namespace": "user/chrispian/memory/notes", "target_key": "mcp.promoted",
		"target_author_agent_id": "reviewer", "target_session_id": "review", "target_trigger": "promotion", "target_derived_from": "project",
	}))
	requestID, _ := requested["request_id"].(string)
	if requestID == "" {
		t.Fatalf("request=%v", requested)
	}
	wantNoError(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "approve", "request_id": requestID, "actor": "approver"}))
	applied := wantNoError(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "apply", "request_id": requestID, "actor": "applier"}))
	if applied["target_revision_id"] == "" || applied["status"] != "applied" {
		t.Fatalf("apply=%v", applied)
	}
	if applied["source_item_id"] != source.ItemID || applied["source_version_token"] != source.VersionToken {
		t.Fatalf("apply receipt lost source identity=%v", applied)
	}
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "approve", "request_id": requestID, "actor": "approver", "target_key": "ignored"}), "validation_error")
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"source_item_id": source.ItemID}), "validation_error")
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "request ", "source_item_id": source.ItemID}), "validation_error")
}

func TestWorkspacePromoteMCPRejectsMalformedScalarsBeforeDefaults(t *testing.T) {
	a := workspaceAdapter(t)
	source, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: mcpWorkspaceNS, Key: "malformed-scalars", Summary: "source",
		Author: memory.Author{AgentID: "draft"}, SessionID: "draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{
		"stage": "request", "source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent",
		"target_domain": "memory", "target_namespace": "user/chrispian/memory/notes",
		"target_author_agent_id": "codex", "target_session_id": "review",
		"target_trigger": "promotion", "target_derived_from": "project",
	}
	clone := func() map[string]any {
		out := make(map[string]any, len(base)+1)
		for key, value := range base {
			out[key] = value
		}
		return out
	}
	stringFields := []string{
		"stage", "source_item_id", "source_version_token", "request_id", "actor", "reason", "notes",
		"target_domain", "target_namespace", "target_key", "target_item_id", "expected_target_revision_id",
		"target_author_agent_id", "target_author_version", "target_session_id", "target_data_schema_hash",
		"target_workstream_id", "target_status", "target_trigger", "target_derived_from", "target_kind", "target_source",
		"target_pointer_scheme", "target_pointer_locator", "target_pointer_resolved_at",
	}
	for _, field := range stringFields {
		t.Run(field, func(t *testing.T) {
			args := clone()
			args[field] = true
			wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", args), "validation_error")
		})
	}
	for _, field := range []string{"target_confidence", "target_ttl_seconds"} {
		t.Run(field, func(t *testing.T) {
			args := clone()
			args[field] = true
			wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", args), "validation_error")
		})
	}

	valid := clone()
	valid["target_key"] = "structured.transport"
	valid["target_tags"] = []any{"reviewed"}
	valid["target_consumer_state"] = map[string]any{"section": "now"}
	valid["target_confidence"] = 1
	valid["target_ttl_seconds"] = 60
	valid["target_workstream_id"] = ""
	valid["reason"] = ""
	requested := wantNoError(t, mustCallRegistered(t, a, "workspace_promote", valid))
	requestID := requested["request_id"].(string)
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "approve", "request_id": requestID, "actor": "approver", "notes": []any{"not prose"}}), "validation_error")
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "approve", "request_id": true, "actor": "approver"}), "validation_error")
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_promote", map[string]any{"stage": "apply", "request_id": requestID, "actor": 7}), "validation_error")
}

func TestWorkspacePromoteMCPAuthorizesExistingTargetBeforeDomainCheck(t *testing.T) {
	a := workspaceAdapter(t)
	ctx := context.Background()
	source, err := a.WorkspaceStore.Create(ctx, workspace.CreateInput{Namespace: mcpWorkspaceNS, Key: "target-auth-source", Summary: "source", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	forbiddenTarget, err := a.WorkspaceStore.Create(ctx, workspace.CreateInput{Namespace: "project/other/workspace/private", Key: "secret", Summary: "secret", Author: memory.Author{AgentID: "other"}, SessionID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	result := mustCallRegistered(t, a, "workspace_promote", map[string]any{
		"stage": "request", "source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent",
		"target_item_id": forbiddenTarget.ItemID, "expected_target_revision_id": "irrelevant",
		"target_author_agent_id": "codex", "target_session_id": "review",
	})
	wantErrorCode(t, result, "namespace_not_permitted")
	if message, _ := result["message"].(string); strings.Contains(message, "workspace") || strings.Contains(message, forbiddenTarget.Namespace) {
		t.Fatalf("authorization denial disclosed target facts: %v", result)
	}
	authorized := mustCallRegistered(t, a, "workspace_promote", map[string]any{
		"stage": "request", "source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "agent",
		"target_item_id": source.ItemID, "expected_target_revision_id": "irrelevant",
		"target_author_agent_id": "codex", "target_session_id": "review",
	})
	wantErrorCode(t, authorized, "validation_error")
}

func TestWorkspacePromoteMCPEachStageUsesItsOwnScope(t *testing.T) {
	for _, stage := range []string{"request", "approve", "apply"} {
		t.Run(stage, func(t *testing.T) {
			cs := newTestStore(t)
			full := workspaceAdapterOnStore(cs, workspacePromotionToken(t, cs, "full-"+stage, []string{"memory:read", "promote.request", "promote.approve", "promote.apply"}, []string{"*"}))
			source, err := full.WorkspaceStore.Create(context.Background(), workspace.CreateInput{Namespace: mcpWorkspaceNS, Key: "scope-" + stage, Summary: "scope", Author: memory.Author{AgentID: "draft"}, SessionID: "draft"})
			if err != nil {
				t.Fatal(err)
			}
			requestArgs := map[string]any{"stage": "request", "source_item_id": source.ItemID, "source_version_token": source.VersionToken, "actor": "requester",
				"target_domain": "memory", "target_namespace": "user/chrispian/memory/notes", "target_key": "scope." + stage,
				"target_author_agent_id": "reviewer", "target_session_id": "review", "target_trigger": "promotion", "target_derived_from": "project"}
			args := requestArgs
			if stage != "request" {
				requested := wantNoError(t, mustCallRegistered(t, full, "workspace_promote", requestArgs))
				requestID := requested["request_id"].(string)
				args = map[string]any{"stage": stage, "request_id": requestID, "actor": "actor"}
				if stage == "apply" {
					wantNoError(t, mustCallRegistered(t, full, "workspace_promote", map[string]any{"stage": "approve", "request_id": requestID, "actor": "approver"}))
				}
			}
			wrongScope := map[string]string{"request": "promote.approve", "approve": "promote.apply", "apply": "promote.request"}[stage]
			attacker := workspaceAdapterOnStore(cs, workspacePromotionToken(t, cs, "attacker-"+stage, []string{wrongScope}, []string{"*"}))
			wantErrorCode(t, mustCallRegistered(t, attacker, "workspace_promote", args), "insufficient_scope")
		})
	}
}

func TestWorkspaceMCPMutationAndTypedReadContract(t *testing.T) {
	a := workspaceAdapter(t)
	createArgs := map[string]any{
		"namespace": mcpWorkspaceNS, "idempotency_key": "mcp-retry-1", "summary": "MCP workspace alpha",
		"author_agent_id": "mcp-test", "session_id": "mcp-session",
	}
	created := wantNoError(t, mustCallRegistered(t, a, "workspace_write", createArgs))
	itemID, _ := created["item_id"].(string)
	version, _ := created["version_token"].(string)
	if created["status"] != "created" || itemID == "" || version == "" {
		t.Fatalf("create=%v", created)
	}
	replayed := wantNoError(t, mustCallRegistered(t, a, "workspace_write", createArgs))
	if replayed["status"] != "replayed" || replayed["item_id"] != itemID || replayed["availability"] != "live" {
		t.Fatalf("replay=%v", replayed)
	}
	if _, ok := replayed["version_token"]; ok {
		t.Fatalf("replay exposed stale token: %v", replayed)
	}

	read := wantNoError(t, mustCallRegistered(t, a, "tesseract_get", map[string]any{"item_id": itemID}))
	if read["domain"] != "workspace" || read["item_id"] != itemID || read["revision_id"] != nil {
		t.Fatalf("typed get=%v", read)
	}
	afterGet, err := a.WorkspaceStore.GetCurrent(context.Background(), itemID)
	if err != nil || afterGet.AccessCount != 1 || afterGet.Activation <= workspace.InitialActivation {
		t.Fatalf("MCP content get did not reinforce workspace: %#v err=%v", afterGet, err)
	}
	wantErrorCode(t, mustCallRegistered(t, a, "tesseract_history", map[string]any{"item_id": itemID}), "history_unavailable")

	recall := wantNoError(t, mustCallRegistered(t, a, "tesseract_recall", map[string]any{
		"namespaces": `[` + `"` + mcpWorkspaceNS + `"` + `]`, "domains": `["workspace"]`,
		"ranking": "relevance", "search_mode": "lexical", "query": "alpha", "payload_mode": "full",
	}))
	results, _ := recall["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["item"] == nil || results[0].(map[string]any)["revision"] != nil {
		t.Fatalf("typed recall=%v", recall)
	}
	afterRecall, err := a.WorkspaceStore.GetCurrent(context.Background(), itemID)
	if err != nil || afterRecall.AccessCount != afterGet.AccessCount {
		t.Fatalf("MCP recall reinforced workspace: before=%#v after=%#v err=%v", afterGet, afterRecall, err)
	}

	touched := wantNoError(t, mustCallRegistered(t, a, "tesseract_touch", map[string]any{"item_ids": `[` + `"` + itemID + `"` + `]`}))
	if int(touched["touched"].(float64)) != 1 {
		t.Fatalf("touch=%v", touched)
	}
	afterTouch, err := a.WorkspaceStore.GetCurrent(context.Background(), itemID)
	if err != nil || afterTouch.AccessCount != 2 || afterTouch.VersionToken != version {
		t.Fatalf("MCP touch did not reinforce exactly once or changed token: %#v err=%v", afterTouch, err)
	}
	wantErrorCode(t, mustCallRegistered(t, a, "tesseract_touch", map[string]any{"item_ids": `[]`}), "validation_error")
	forbidden, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/other/workspace/private", Summary: "private",
		Author: memory.Author{AgentID: "mcp-test"}, SessionID: "mcp-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeDenied, err := a.WorkspaceStore.GetCurrent(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	wantErrorCode(t, mustCallRegistered(t, a, "tesseract_touch", map[string]any{
		"item_ids": `[` + `"` + itemID + `","` + forbidden.ItemID + `"` + `]`,
	}), "namespace_not_permitted")
	afterDenied, err := a.WorkspaceStore.GetCurrent(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDenied.AccessCount != beforeDenied.AccessCount {
		t.Fatalf("authorized item moved before the whole touch batch was authorized: before=%d after=%d", beforeDenied.AccessCount, afterDenied.AccessCount)
	}
	deleted := wantNoError(t, mustCallRegistered(t, a, "workspace_delete", map[string]any{"item_id": itemID, "version_token": version}))
	if deleted["status"] != "deleted" {
		t.Fatalf("delete=%v", deleted)
	}
	wantErrorCode(t, mustCallRegistered(t, a, "tesseract_get", map[string]any{"item_id": itemID}), "deleted")
	wantErrorCode(t, mustCallRegistered(t, a, "workspace_write", map[string]any{
		"namespace": mcpWorkspaceNS, "idempotency_key": "mcp-retry-1", "summary": "changed",
		"author_agent_id": "mcp-test", "session_id": "mcp-session",
	}), "idempotency_conflict")
}

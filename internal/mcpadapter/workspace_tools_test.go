package mcpadapter

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"github.com/hollis-labs/tesseract/internal/workspacepromotion"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestRegisteredWorkspaceWriteCapturesTetherProvenance(t *testing.T) {
	a := workspaceAdapter(t)
	srv := server.NewMCPServer("provenance-test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)
	req := mcp.CallToolRequest{}
	req.Params.Name = "workspace_write"
	req.Params.Arguments = map[string]any{
		"namespace": mcpWorkspaceNS, "idempotency_key": "provenance-create", "summary": "received",
		"author_agent_id": "mcp-test", "session_id": "author-session",
	}
	req.Params.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		tetherProvenanceKey: map[string]any{"schema_version": float64(1), "session_id": "tether-session", "workstream_id": "ws-received"},
	}}
	res, err := srv.ListTools()["workspace_write"].Handler(context.Background(), req)
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
	a.WorkspacePromotionStore = workspacepromotion.NewStore(cs, a.MemoryStore)
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
	wantErrorCode(t, mustCallRegistered(t, a, "tesseract_history", map[string]any{"item_id": itemID}), "history_unavailable")

	recall := wantNoError(t, mustCallRegistered(t, a, "tesseract_recall", map[string]any{
		"namespaces": `[` + `"` + mcpWorkspaceNS + `"` + `]`, "domains": `["workspace"]`,
		"ranking": "relevance", "search_mode": "lexical", "query": "alpha", "payload_mode": "full",
	}))
	results, _ := recall["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["item"] == nil || results[0].(map[string]any)["revision"] != nil {
		t.Fatalf("typed recall=%v", recall)
	}

	touched := wantNoError(t, mustCallRegistered(t, a, "tesseract_touch", map[string]any{"item_ids": `[` + `"` + itemID + `"` + `]`}))
	if int(touched["touched"].(float64)) != 1 {
		t.Fatalf("touch=%v", touched)
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

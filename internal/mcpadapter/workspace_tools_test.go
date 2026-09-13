package mcpadapter

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

const mcpWorkspaceNS = "project/tesseract/workspace/mcp-tests"

func workspaceAdapter(t *testing.T) *Adapter {
	t.Helper()
	cs := newTestStore(t)
	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "workspace", Scopes: []string{"memory:read", "memory:write"}, NamespaceGlobs: []string{"project/tesseract/workspace/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := New(cs, token)
	a.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a.WorkspaceStore = workspace.NewStore(cs.DB())
	return a
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

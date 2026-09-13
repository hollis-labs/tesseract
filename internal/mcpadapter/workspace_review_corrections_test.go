package mcpadapter

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestWorkspaceRecallAuthorizesCompleteSelectorBeforeAnyProjection(t *testing.T) {
	a := workspaceAdapter(t)
	forbiddenNamespace := "project/other/workspace/private"
	_, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: forbiddenNamespace, Key: "private", Summary: "synthetic forbidden summary",
		Body: "synthetic forbidden content", Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []map[string]any{
		{"namespaces": `["` + forbiddenNamespace + `"]`, "domains": `["workspace"]`, "payload_mode": "full"},
		{"namespaces": `["project/*"]`, "domains": `["workspace"]`, "payload_mode": "keys"},
		{"namespaces": `["` + mcpWorkspaceNS + `","` + forbiddenNamespace + `"]`, "domains": `["memory","workspace"]`, "payload_mode": "summary"},
		{"namespaces": `["` + forbiddenNamespace + `"]`, "domains": `["workspace"]`, "estimate_only": true},
	}
	for i, args := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			wantErrorCode(t, mustCallRegistered(t, a, "tesseract_recall", args), "namespace_not_permitted")
		})
	}
}

func TestWorkspaceRecallMCPAcceptsAuthorizedBroadPrefix(t *testing.T) {
	a := workspaceAdapter(t)
	item, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: mcpWorkspaceNS + "/child", Key: "broad-prefix", Summary: "broad prefix target",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := wantNoError(t, mustCallRegistered(t, a, "tesseract_recall", map[string]any{
		"namespaces": `["project/tesseract/*"]`, "domains": `["workspace"]`,
		"ranking": "chronological", "payload_mode": "summary",
	}))
	results, _ := body["results"].([]any)
	for _, raw := range results {
		result := raw.(map[string]any)
		if got, ok := result["item"].(map[string]any); ok && got["item_id"] == item.ItemID {
			return
		}
	}
	t.Fatalf("authorized broad prefix omitted item: %v", body)
}

func TestWorkspaceCreateRejectsEditOnlyClearFieldsBeforeWriting(t *testing.T) {
	a := workspaceAdapter(t)
	for i, clearFields := range []any{`[]`, `["body"]`, `["unknown"]`, `not-json`} {
		key := fmt.Sprintf("unexpected-clear-%d", i)
		body := mustCallRegistered(t, a, "workspace_write", map[string]any{
			"namespace": mcpWorkspaceNS, "key": key, "summary": "create must reject edit fields",
			"author_agent_id": "review", "session_id": "review", "clear_fields": clearFields,
		})
		wantErrorCode(t, body, "validation_error")
		if _, err := a.WorkspaceStore.GetCurrentByKey(context.Background(), mcpWorkspaceNS, key); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("case %d wrote an item despite rejection: %v", i, err)
		}
	}
}

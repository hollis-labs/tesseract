package mcpadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
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

func TestWorkspaceRecallMCPRejectsLegacyNestedHeadOutsideGrant(t *testing.T) {
	cs := newTestStore(t)
	legacyNamespace := "user/chrispian/project/secret/workspace/private"
	item, err := workspace.NewStore(cs.DB()).Create(context.Background(), workspace.CreateInput{
		Namespace: legacyNamespace, Key: "legacy-private", Summary: "legacy private",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	newAdapter := func(glob string) *Adapter {
		t.Helper()
		token, _, tokenErr := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
			Label: "prefix-" + glob, Scopes: []string{"memory:read"}, NamespaceGlobs: []string{glob},
		})
		if tokenErr != nil {
			t.Fatal(tokenErr)
		}
		a := New(cs, token)
		a.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
		a.WorkspaceStore = workspace.NewStore(cs.DB())
		return a
	}

	narrow := newAdapter("user/chrispian/workspace/*")
	for _, args := range []map[string]any{
		{"namespaces": `["user/chrispian/*"]`, "domains": `["workspace"]`, "payload_mode": "full"},
		{"namespaces": `["user/chrispian/*"]`, "domains": `["workspace"]`, "estimate_only": true},
		{"namespaces": `["user/chrispian/*"]`, "domains": `["memory","workspace"]`, "payload_mode": "summary"},
	} {
		wantErrorCode(t, mustCallRegistered(t, narrow, "tesseract_recall", args), "namespace_not_permitted")
	}

	broad := newAdapter("user/chrispian/*")
	body := wantNoError(t, mustCallRegistered(t, broad, "tesseract_recall", map[string]any{
		"namespaces": `["user/chrispian/*"]`, "domains": `["workspace"]`, "payload_mode": "keys",
	}))
	if !strings.Contains(fmt.Sprint(body), item.ItemID) {
		t.Fatalf("broader user grant did not retain legacy nested recall: %v", body)
	}
}

func TestWorkspaceRecallMCPPrefixQueryIsCaseSensitiveAcrossProjections(t *testing.T) {
	cs := newTestStore(t)
	store := workspace.NewStore(cs.DB())
	allowed, err := store.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/private/allowed", Key: "allowed", Summary: "allowed",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	forbidden, err := store.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/Private/secret", Key: "forbidden", Summary: "forbidden",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "case-sensitive-prefix", Scopes: []string{"memory:read"},
		NamespaceGlobs: []string{"project/tesseract/workspace/private/*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := New(cs, token)
	a.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a.WorkspaceStore = store

	base := map[string]any{
		"namespaces": `["project/tesseract/workspace/private/*"]`,
		"domains":    `["workspace"]`,
	}
	for _, mode := range []string{"keys", "summary", "full"} {
		args := map[string]any{"namespaces": base["namespaces"], "domains": base["domains"], "payload_mode": mode}
		body := wantNoError(t, mustCallRegistered(t, a, "tesseract_recall", args))
		rendered := fmt.Sprint(body)
		if !strings.Contains(rendered, allowed.ItemID) || strings.Contains(rendered, forbidden.ItemID) {
			t.Fatalf("payload_mode=%s returned the wrong case-sensitive set: %v", mode, body)
		}
	}
	estimate := wantNoError(t, mustCallRegistered(t, a, "tesseract_recall", map[string]any{
		"namespaces": base["namespaces"], "domains": base["domains"], "estimate_only": true,
	}))
	manifest, _ := estimate["manifest"].(map[string]any)
	if got, _ := manifest["results_total"].(float64); int(got) != 1 {
		t.Fatalf("case-sensitive estimate results_total=%v, want 1: %v", manifest["results_total"], estimate)
	}
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

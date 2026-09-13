package mcpadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
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

func TestRecallSelectorGrantBoundaryMatrixMCP(t *testing.T) {
	for _, tc := range []struct {
		name      string
		globs     []string
		selector  string
		domains   []string
		permitted bool
	}{
		{"exact selector exact grant", []string{"project/tesseract/knowledge/archive"}, "project/tesseract/knowledge/archive", []string{"knowledge"}, true},
		{"explicit descendants exact grant", []string{"project/tesseract/workspace/private"}, "project/tesseract/workspace/private/*", []string{"workspace"}, false},
		{"explicit descendants descendant grant", []string{"project/tesseract/workspace/private/*"}, "project/tesseract/workspace/private/*", []string{"workspace"}, true},
		{"legacy memory exact grant", []string{"project/tesseract/memory"}, "project/tesseract/memory", []string{"memory"}, false},
		{"legacy memory descendant grant", []string{"project/tesseract/memory/*"}, "project/tesseract/memory", []string{"memory"}, true},
		{"legacy event exact grant", []string{"project/tesseract/event"}, "project/tesseract/event", []string{"event"}, false},
		{"legacy event descendant grant", []string{"project/tesseract/event/*"}, "project/tesseract/event", []string{"event"}, true},
		{"project domain narrowing", []string{"project/tesseract/workspace/*"}, "project/tesseract/*", []string{"workspace"}, true},
		{"other domain memory tail exact", []string{"project/tesseract/knowledge/archive/memory"}, "project/tesseract/knowledge/archive/memory", []string{"knowledge"}, true},
		{"mixed domains complete", []string{"project/tesseract/memory/*", "project/tesseract/workspace/*"}, "project/tesseract/*", []string{"memory", "workspace"}, true},
		{"mixed domains incomplete", []string{"project/tesseract/workspace/*"}, "project/tesseract/*", []string{"memory", "workspace"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := globsPermitRecallScope(tc.globs, tc.selector, tc.domains); got != tc.permitted {
				t.Fatalf("globsPermitRecallScope(%v, %q, %v)=%v, want %v", tc.globs, tc.selector, tc.domains, got, tc.permitted)
			}
		})
	}
}

func TestWorkspaceRecallMCPDescendantGrantExcludesParent(t *testing.T) {
	cs := newTestStore(t)
	store := workspace.NewStore(cs.DB())
	const parentNamespace = "project/tesseract/workspace/boundary"
	parent, err := store.Create(context.Background(), workspace.CreateInput{
		Namespace: parentNamespace, Key: "parent", Summary: "parent",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Create(context.Background(), workspace.CreateInput{
		Namespace: parentNamespace + "/child", Key: "child", Summary: "child",
		Author: memory.Author{AgentID: "review"}, SessionID: "review",
	})
	if err != nil {
		t.Fatal(err)
	}
	descendant := workspaceReviewAdapter(t, cs, store, "workspace-descendants", []string{parentNamespace + "/*"})
	wantErrorCode(t, mustCallRegistered(t, descendant, "tesseract_get", map[string]any{"item_id": parent.ItemID}), "namespace_not_permitted")
	wantNoError(t, mustCallRegistered(t, descendant, "tesseract_get", map[string]any{"item_id": child.ItemID}))
	body := wantNoError(t, mustCallRegistered(t, descendant, "tesseract_recall", map[string]any{
		"namespaces": `["` + parentNamespace + `/*"]`, "domains": `["workspace"]`, "payload_mode": "full",
	}))
	if rendered := fmt.Sprint(body); !strings.Contains(rendered, child.ItemID) || strings.Contains(rendered, parent.ItemID) {
		t.Fatalf("descendant recall returned the wrong boundary set: %v", body)
	}
	estimate := wantNoError(t, mustCallRegistered(t, descendant, "tesseract_recall", map[string]any{
		"namespaces": `["` + parentNamespace + `/*"]`, "domains": `["workspace"]`, "estimate_only": true,
	}))
	if got := int(estimate["manifest"].(map[string]any)["results_total"].(float64)); got != 1 {
		t.Fatalf("descendant estimate total=%d, want 1: %v", got, estimate)
	}

	exact := workspaceReviewAdapter(t, cs, store, "workspace-parent", []string{parentNamespace})
	wantErrorCode(t, mustCallRegistered(t, exact, "tesseract_recall", map[string]any{
		"namespaces": `["` + parentNamespace + `/*"]`, "domains": `["workspace"]`,
	}), "namespace_not_permitted")
	exactBody := wantNoError(t, mustCallRegistered(t, exact, "tesseract_recall", map[string]any{
		"namespaces": `["` + parentNamespace + `"]`, "domains": `["workspace"]`, "payload_mode": "keys",
	}))
	if rendered := fmt.Sprint(exactBody); !strings.Contains(rendered, parent.ItemID) || strings.Contains(rendered, child.ItemID) {
		t.Fatalf("exact recall returned the wrong boundary set: %v", exactBody)
	}
}

func TestMemoryRecallMCPLegacyPrefixRequiresDescendantGrant(t *testing.T) {
	cs := newTestStore(t)
	store := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	const root = "project/tesseract/memory"
	child, err := store.WriteRevision(context.Background(), memory.WriteInput{
		Domain: domains.Memory, Namespace: root + "/notes", MemoryKey: "boundary.memory",
		Summary: "memory child", Author: memory.Author{AgentID: "review"}, SessionID: "review",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	exact := workspaceReviewAdapter(t, cs, workspace.NewStore(cs.DB()), "memory-root", []string{root})
	wantErrorCode(t, mustCallRegistered(t, exact, "tesseract_get", map[string]any{"item_id": child.ItemID}), "namespace_not_permitted")
	wantErrorCode(t, mustCallRegistered(t, exact, "tesseract_recall", map[string]any{
		"namespaces": `["` + root + `"]`, "domains": `["memory"]`, "payload_mode": "full",
	}), "namespace_not_permitted")

	descendant := workspaceReviewAdapter(t, cs, workspace.NewStore(cs.DB()), "memory-children", []string{root + "/*"})
	wantNoError(t, mustCallRegistered(t, descendant, "tesseract_get", map[string]any{"item_id": child.ItemID}))
	body := wantNoError(t, mustCallRegistered(t, descendant, "tesseract_recall", map[string]any{
		"namespaces": `["` + root + `"]`, "domains": `["memory"]`, "payload_mode": "full",
	}))
	if !strings.Contains(fmt.Sprint(body), child.ItemID) {
		t.Fatalf("descendant grant did not preserve legacy bare recall: %v", body)
	}
}

func workspaceReviewAdapter(t *testing.T, cs *contextstore.Store, store *workspace.Store, label string, globs []string) *Adapter {
	t.Helper()
	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: label, Scopes: []string{"memory:read"}, NamespaceGlobs: globs,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := New(cs, token)
	a.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a.WorkspaceStore = store
	return a
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

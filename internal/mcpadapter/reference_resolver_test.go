package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func referenceAdapter(t *testing.T, globs []string) *Adapter {
	t.Helper()
	cs := newTestStore(t)
	token, _, err := cs.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "reference", Scopes: []string{"memory:read"}, NamespaceGlobs: globs,
	})
	if err != nil {
		t.Fatal(err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, token)
	a.MemoryStore, a.KnowledgeStore, a.EventStore, a.WorkspaceStore = ms, knowledge.New(ms), event.New(ms), workspace.NewStore(cs.DB())
	return a
}

func TestReferenceResolveMCPContract(t *testing.T) {
	a := referenceAdapter(t, []string{"*"})
	rev, err := a.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "resolver.mcp",
		Summary: "secret", Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/mcp-resolver", Key: "slash/key", Summary: "workspace secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range []map[string]any{
		{"item_id": rev.ItemID}, {"revision_id": rev.RevisionID},
		{"domain": "workspace", "namespace": item.Namespace, "key": item.Key},
		{"uri": "tesseract://item/" + item.ItemID},
	} {
		body := wantNoError(t, mustCallRegistered(t, a, "tesseract_ref_resolve", args))
		if body["status"] != "resolved" || body["ref"] == nil {
			t.Fatalf("args=%v body=%v", args, body)
		}
		for _, field := range []string{"payload", "summary", "body", "version_token", "namespace", "memory_key"} {
			if _, ok := body[field]; ok {
				t.Fatalf("response leaked %s: %v", field, body)
			}
		}
	}
	unsupported := wantNoError(t, mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{"uri": "legacy:opaque"}))
	if unsupported["status"] != "unsupported_reference" {
		t.Fatalf("unsupported=%v", unsupported)
	}
	notFound := wantNoError(t, mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{"revision_id": item.ItemID}))
	if notFound["status"] != "not_found" || notFound["ref"] != nil {
		t.Fatalf("wrong-kind=%v", notFound)
	}
	for _, args := range []map[string]any{
		{}, {"item_id": rev.ItemID, "uri": "tesseract://item/" + rev.ItemID},
		{"domain": "memory", "namespace": rev.Namespace}, {"memory_key": rev.MemoryKey},
		{"domain": "unknown", "namespace": rev.Namespace, "key": rev.MemoryKey},
	} {
		wantErrorCode(t, mustCallRegistered(t, a, "tesseract_ref_resolve", args), "validation_error")
	}
}

func TestReferenceResolveMCPRequiresReadScope(t *testing.T) {
	a := referenceAdapter(t, []string{"*"})
	readToken := a.Token
	item, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/resolver-scope", Key: "current", Summary: "secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeOnly, _, err := a.Store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: "resolver-write-only", Scopes: []string{"memory:write"}, NamespaceGlobs: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Token = writeOnly
	wantErrorCode(t, mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{"item_id": item.ItemID}), "insufficient_scope")
	a.Token = readToken
	wantNoError(t, mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{"item_id": item.ItemID}))
}

func TestReferenceResolveMCPAuthorizesLiveAndDeletedNamespace(t *testing.T) {
	a := referenceAdapter(t, []string{"project/other/*"})
	item, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: "project/tesseract/workspace/private", Key: "private", Summary: "secret",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	denied := mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{"item_id": item.ItemID})
	wantErrorCode(t, denied, "namespace_not_permitted")
	if strings.Contains(denied["message"].(string), item.Namespace) {
		t.Fatalf("authorization error leaked namespace: %v", denied)
	}
	if _, err := a.WorkspaceStore.Delete(context.Background(), workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken}); err != nil {
		t.Fatal(err)
	}
	deleted := mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{"item_id": item.ItemID})
	wantErrorCode(t, deleted, "namespace_not_permitted")
	if strings.Contains(deleted["message"].(string), "deleted") {
		t.Fatalf("authorization error leaked tombstone: %v", deleted)
	}
}

func TestReferenceResolveMCPUnavailableExplicitDomainBeforeNegativeAnswer(t *testing.T) {
	a := referenceAdapter(t, []string{"*"})
	rev, err := a.KnowledgeStore.Write(context.Background(), knowledge.WriteInput{
		Namespace: "project/tesseract/knowledge/resolver", Key: "present", Summary: "secret",
		Kind: "doc", Source: "test", Pointer: memory.Pointer{Scheme: "nil", Locator: "test"},
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
	})
	if err != nil {
		t.Fatal(err)
	}
	a.KnowledgeStore = nil
	for _, key := range []string{rev.MemoryKey, "absent"} {
		result := mustCallRegistered(t, a, "tesseract_ref_resolve", map[string]any{
			"domain": "knowledge", "namespace": rev.Namespace, "key": key,
		})
		wantErrorCode(t, result, "domain_unavailable")
	}
}

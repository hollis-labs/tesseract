package itemservice_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func writeReferenceRevision(t *testing.T, ms *memory.Store, domain domains.Domain, namespace, key string) memory.Revision {
	t.Helper()
	if domain == domains.Knowledge {
		rev, err := knowledge.New(ms).Write(context.Background(), knowledge.WriteInput{
			Namespace: namespace, Key: key, Kind: "doc", Source: "test",
			Pointer: memory.Pointer{Scheme: "nil", Locator: "nil"}, Summary: "secret content",
			Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
		})
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	rev, err := ms.WriteRevision(context.Background(), memory.WriteInput{
		Domain: domain, Namespace: namespace, MemoryKey: key, Summary: "secret content",
		Author: memory.Author{AgentID: "test"}, SessionID: "resolver",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestResolveReferenceSelectorsAndCanonicalURIs(t *testing.T) {
	cs, ms, ws, svc := newService(t)
	ctx := context.Background()
	mem := writeReferenceRevision(t, ms, domains.Memory, "project/tesseract/memory/notes", "resolver.current")
	key := " docs/reference resolver / padded "
	know := writeReferenceRevision(t, ms, domains.Knowledge, "user/test/knowledge/contracts", key)
	keyless := writeReferenceRevision(t, ms, domains.Event, "project/tesseract/event/reasoning", "")
	workspaceItem, err := ws.Create(ctx, wsInput("scratch/reference/path", "secret workspace content"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		selector   itemservice.ReferenceSelector
		wantID     string
		wantKind   itemservice.ReferenceKind
		wantDomain string
	}{
		{"revisioned item", itemservice.ReferenceSelector{ItemID: mem.ItemID}, mem.ItemID, itemservice.ReferenceKindItem, "memory"},
		{"keyless item", itemservice.ReferenceSelector{ItemID: keyless.ItemID}, keyless.ItemID, itemservice.ReferenceKindItem, "event"},
		{"workspace item", itemservice.ReferenceSelector{ItemID: workspaceItem.ItemID}, workspaceItem.ItemID, itemservice.ReferenceKindItem, "workspace"},
		{"knowledge item", itemservice.ReferenceSelector{ItemID: know.ItemID}, know.ItemID, itemservice.ReferenceKindItem, "knowledge"},
		{"revision", itemservice.ReferenceSelector{RevisionID: know.RevisionID}, know.RevisionID, itemservice.ReferenceKindRevision, "knowledge"},
		{"exact legacy key", itemservice.ReferenceSelector{Domain: "knowledge", Namespace: know.Namespace, Key: key}, know.ItemID, itemservice.ReferenceKindItem, "knowledge"},
		{"workspace legacy key", itemservice.ReferenceSelector{Domain: "workspace", Namespace: workspaceItem.Namespace, Key: workspaceItem.Key}, workspaceItem.ItemID, itemservice.ReferenceKindItem, "workspace"},
		{"item URI", itemservice.ReferenceSelector{URI: "tesseract://item/" + mem.ItemID}, mem.ItemID, itemservice.ReferenceKindItem, "memory"},
		{"revision URI", itemservice.ReferenceSelector{URI: "tesseract://revision/" + know.RevisionID}, know.RevisionID, itemservice.ReferenceKindRevision, "knowledge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, resolveErr := svc.ResolveReference(ctx, tc.selector)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			if got.Status != itemservice.ResolutionResolved || got.Ref == nil || got.Ref.RefID != tc.wantID || got.Ref.Kind != tc.wantKind || got.Domain != tc.wantDomain || got.ResolvedAt == nil {
				t.Fatalf("resolution = %+v", got)
			}
			wantSubject := "item"
			if tc.wantKind == itemservice.ReferenceKindRevision {
				wantSubject = "revision"
			}
			if got.Ref.URI != "tesseract://"+wantSubject+"/"+tc.wantID {
				t.Fatalf("uri = %q", got.Ref.URI)
			}
			if got.Namespace == "" {
				t.Fatal("authorization namespace was not retained internally")
			}
		})
	}

	var revisionCount int
	if countErr := cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions`).Scan(&revisionCount); countErr != nil {
		t.Fatal(countErr)
	}
	beforeState, err := ms.GetState(ctx, mem.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	beforeWorkspace, err := ws.GetCurrent(ctx, workspaceItem.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []itemservice.ReferenceSelector{{ItemID: mem.ItemID}, {RevisionID: mem.RevisionID}, {Domain: "workspace", Namespace: workspaceItem.Namespace, Key: workspaceItem.Key}} {
		if _, err := svc.ResolveReference(ctx, selector); err != nil {
			t.Fatal(err)
		}
	}
	afterState, _ := ms.GetState(ctx, mem.ItemID)
	afterWorkspace, _ := ws.GetCurrent(ctx, workspaceItem.ItemID)
	var afterRevisionCount int
	_ = cs.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions`).Scan(&afterRevisionCount)
	if afterState.AccessCount != beforeState.AccessCount || afterState.Activation != beforeState.Activation {
		t.Fatalf("revisioned state changed: before=%+v after=%+v", beforeState, afterState)
	}
	if afterWorkspace.AccessCount != beforeWorkspace.AccessCount || afterWorkspace.VersionToken != beforeWorkspace.VersionToken || !afterWorkspace.LastUsedAt.Equal(beforeWorkspace.LastUsedAt) || afterWorkspace.Summary != beforeWorkspace.Summary {
		t.Fatalf("workspace state changed: before=%+v after=%+v", beforeWorkspace, afterWorkspace)
	}
	if afterRevisionCount != revisionCount {
		t.Fatalf("revision count changed from %d to %d", revisionCount, afterRevisionCount)
	}
}

func TestResolveReferenceOutcomesValidationAndKeyReuse(t *testing.T) {
	_, ms, ws, svc := newService(t)
	ctx := context.Background()
	rev := writeReferenceRevision(t, ms, domains.Memory, "project/tesseract/memory/notes", "wrong.kind")
	item, err := ws.Create(ctx, wsInput("reuse/key", "old secret"))
	if err != nil {
		t.Fatal(err)
	}
	renamedKey := "renamed/key"
	item, err = ws.Edit(ctx, workspace.EditInput{ItemID: item.ItemID, VersionToken: item.VersionToken, Key: &renamedKey, Author: memory.Author{AgentID: "test"}, SessionID: "resolver"})
	if err != nil {
		t.Fatal(err)
	}
	oldKey, err := svc.ResolveReference(ctx, itemservice.ReferenceSelector{Domain: "workspace", Namespace: item.Namespace, Key: "reuse/key"})
	if err != nil || oldKey.Status != itemservice.ResolutionNotFound {
		t.Fatalf("old key after rename = %+v, %v", oldKey, err)
	}
	newKey, err := svc.ResolveReference(ctx, itemservice.ReferenceSelector{Domain: "workspace", Namespace: item.Namespace, Key: renamedKey})
	if err != nil || newKey.ItemID != item.ItemID {
		t.Fatalf("new key after rename = %+v, %v", newKey, err)
	}
	deleted, err := ws.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ResolveReference(ctx, itemservice.ReferenceSelector{ItemID: item.ItemID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != itemservice.ResolutionDeleted || got.Ref == nil || got.Ref.RefID != item.ItemID || got.DeletedAt == nil || !got.DeletedAt.Equal(*deleted.DeletedAt) {
		t.Fatalf("tombstone resolution = %+v", got)
	}
	raw := strings.ToLower(strings.Join([]string{got.ItemID, got.Domain, got.Namespace, got.Ref.URI}, " "))
	if strings.Contains(raw, "old secret") || strings.Contains(raw, "reuse/key") || strings.Contains(raw, renamedKey) {
		t.Fatalf("tombstone leaked content/key: %+v", got)
	}

	recreated, err := ws.Create(ctx, wsInput(renamedKey, "new secret"))
	if err != nil {
		t.Fatal(err)
	}
	byKey, err := svc.ResolveReference(ctx, itemservice.ReferenceSelector{Domain: "workspace", Namespace: recreated.Namespace, Key: recreated.Key})
	if err != nil {
		t.Fatal(err)
	}
	if byKey.ItemID != recreated.ItemID || byKey.ItemID == item.ItemID {
		t.Fatalf("key reuse resolution = %+v", byKey)
	}
	oldAgain, _ := svc.ResolveReference(ctx, itemservice.ReferenceSelector{ItemID: item.ItemID})
	if oldAgain.Status != itemservice.ResolutionDeleted {
		t.Fatalf("old identity after reuse = %+v", oldAgain)
	}

	for _, selector := range []itemservice.ReferenceSelector{
		{ItemID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		{RevisionID: rev.ItemID},
		{ItemID: rev.RevisionID},
		{Domain: "memory", Namespace: rev.Namespace, Key: "not.there"},
	} {
		result, resolveErr := svc.ResolveReference(ctx, selector)
		if resolveErr != nil || result.Status != itemservice.ResolutionNotFound || result.Ref != nil || result.ItemID != "" {
			t.Fatalf("not-found selector %+v => %+v, %v", selector, result, resolveErr)
		}
	}
	for _, uri := range []string{"https://example.test/item/x", "opaque locator", "tesseract://key/ns/k"} {
		result, resolveErr := svc.ResolveReference(ctx, itemservice.ReferenceSelector{URI: uri})
		if resolveErr != nil || result.Status != itemservice.ResolutionUnsupportedReference {
			t.Fatalf("uri %q => %+v, %v", uri, result, resolveErr)
		}
	}
	for _, selector := range []itemservice.ReferenceSelector{
		{},
		{ItemID: rev.ItemID, RevisionID: rev.RevisionID},
		{Domain: "memory", Namespace: rev.Namespace},
		{Domain: "unknown", Namespace: rev.Namespace, Key: rev.MemoryKey},
		{Domain: "memory", Namespace: "not/a/memory/namespace", Key: rev.MemoryKey},
		{URI: "tesseract://item/"},
		{URI: "tesseract://revision/id/extra"},
	} {
		if _, resolveErr := svc.ResolveReference(ctx, selector); !errors.Is(resolveErr, itemservice.ErrInvalidReference) {
			t.Fatalf("selector %+v error = %v", selector, resolveErr)
		}
	}
	if itemservice.ResolutionAmbiguous != "ambiguous" {
		t.Fatal("ambiguous status is not available to future supported legacy forms")
	}
}

func TestResolveReferenceRequiresCompleteBackendForNegativeItemLookup(t *testing.T) {
	_, ms, ws, _ := newService(t)
	for _, svc := range []*itemservice.Service{{Revisions: ms}, {Workspace: ws}, {}} {
		_, err := svc.ResolveReference(context.Background(), itemservice.ReferenceSelector{ItemID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		if !errors.Is(err, itemservice.ErrBackendUnavailable) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestResolveReferenceRaceWithWorkspaceDelete(t *testing.T) {
	_, _, ws, svc := newService(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		item, err := ws.Create(ctx, wsInput("race/"+string(rune('a'+i%26))+string(rune('a'+i/26)), "race content"))
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var resolved itemservice.ReferenceResolution
		var resolveErr, deleteErr error
		go func() {
			defer wg.Done()
			<-start
			resolved, resolveErr = svc.ResolveReference(ctx, itemservice.ReferenceSelector{ItemID: item.ItemID})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, deleteErr = ws.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken})
		}()
		close(start)
		wg.Wait()
		if resolveErr != nil || deleteErr != nil {
			t.Fatalf("resolve=%v delete=%v", resolveErr, deleteErr)
		}
		if resolved.Status != itemservice.ResolutionResolved && resolved.Status != itemservice.ResolutionDeleted {
			t.Fatalf("race observed impossible result: %+v", resolved)
		}
	}
}

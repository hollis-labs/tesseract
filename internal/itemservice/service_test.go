package itemservice_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

const itemWorkspaceNS = "project/tesseract/workspace/itemservice"

func newService(t *testing.T) (*contextstore.Store, *memory.Store, *workspace.Store, *itemservice.Service) {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ws := workspace.NewStore(cs.DB())
	return cs, ms, ws, &itemservice.Service{Revisions: ms, Workspace: ws}
}

func wsInput(key, summary string) workspace.CreateInput {
	return workspace.CreateInput{Namespace: itemWorkspaceNS, Key: key, Summary: summary, Author: memory.Author{AgentID: "test"}, SessionID: "itemservice"}
}

func TestReadItemKeepsRevisionAndWorkspaceAlternativesDistinct(t *testing.T) {
	_, ms, ws, svc := newService(t)
	ctx := context.Background()
	rev, err := ms.WriteRevision(ctx, memory.WriteInput{Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "typed.read", Summary: "revision", Author: memory.Author{AgentID: "test"}, SessionID: "itemservice", Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject})
	if err != nil {
		t.Fatal(err)
	}
	item, err := ws.Create(ctx, wsInput("typed/read", "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id           string
		wantRevision bool
	}{{rev.ItemID, true}, {item.ItemID, false}} {
		meta, lookupErr := svc.LookupMetadata(ctx, tc.id)
		if lookupErr != nil {
			t.Fatal(lookupErr)
		}
		got, readErr := svc.ReadCurrent(ctx, meta)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if (got.Revision != nil) != tc.wantRevision || (got.Item != nil) == tc.wantRevision {
			t.Fatalf("read %s = %+v", tc.id, got)
		}
	}
	if _, deleteErr := ws.Delete(ctx, workspace.DeleteInput{ItemID: item.ItemID, VersionToken: item.VersionToken}); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	meta, err := svc.LookupMetadata(ctx, item.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Deleted || meta.DeletedAt == nil {
		t.Fatalf("tombstone metadata = %+v", meta)
	}
	if _, err := svc.ReadCurrent(ctx, meta); !errors.Is(err, workspace.ErrDeleted) {
		t.Fatalf("deleted read error = %v", err)
	}
	if _, err := svc.History(ctx, meta); !errors.Is(err, itemservice.ErrHistoryUnavailable) {
		t.Fatalf("workspace history error = %v", err)
	}
}

func TestRecallWithoutWorkspacePreservesRevisionWireResult(t *testing.T) {
	_, ms, _, svc := newService(t)
	ctx := context.Background()
	if _, err := ms.WriteRevision(ctx, memory.WriteInput{Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "typed.same", Summary: "same", Author: memory.Author{AgentID: "test"}, SessionID: "itemservice", Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject}); err != nil {
		t.Fatal(err)
	}
	in := memory.RecallInput{Namespaces: []string{"project/tesseract/memory/notes"}}
	pr := memory.PageRequest{PayloadMode: memory.PayloadModeSummary}
	legacy, err := ms.RecallPaged(ctx, in, pr)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := svc.RecallPaged(ctx, in, pr)
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, _ := json.Marshal(legacy.Results)
	typedJSON, _ := json.Marshal(typed.Results)
	if string(legacyJSON) != string(typedJSON) || !reflect.DeepEqual(legacy.Manifest, typed.Manifest) {
		t.Fatalf("revision-only recall changed\nlegacy=%s\ntyped=%s", legacyJSON, typedJSON)
	}
}

func TestWorkspaceRecallProjectsTypedItemsAndFiltersBeforeLimit(t *testing.T) {
	_, _, ws, svc := newService(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		in := wsInput("ordinary/"+string(rune('a'+i)), "ordinary")
		if _, err := ws.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	target := wsInput("target/key", "needle alpha")
	target.Tags = []string{"selected"}
	target.Body = "full body"
	created, err := ws.Create(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	in := memory.RecallInput{Namespaces: []string{itemWorkspaceNS}, Ranking: memory.RankingRelevance, SearchMode: memory.SearchModeLexical, Query: "alpha", Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Domain(workspace.Domain)}, Tags: []string{"selected"}}}
	summary, err := svc.RecallPaged(ctx, in, memory.PageRequest{Limit: 1, PayloadMode: memory.PayloadModeSummary})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Kept) != 1 || summary.Kept[0].Item == nil || summary.Kept[0].Item.ItemID != created.ItemID {
		t.Fatalf("summary kept = %+v", summary.Kept)
	}
	if summary.Kept[0].Score != nil {
		t.Fatalf("workspace-only lexical score=%v; order should carry BM25 rank", *summary.Kept[0].Score)
	}
	raw, _ := json.Marshal(summary.Results)
	if string(raw) == "" || containsJSONField(raw, "version_token") || containsJSONField(raw, "revision_id") {
		t.Fatalf("summary leaked revision/token fields: %s", raw)
	}
	full, err := svc.RecallPaged(ctx, in, memory.PageRequest{Limit: 1, PayloadMode: memory.PayloadModeFull})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(full.Results)
	if !containsJSONField(raw, "version_token") || !containsJSONField(raw, "item") {
		t.Fatalf("full workspace result = %s", raw)
	}
}

func TestMixedLexicalRecallFusesSeparateStores(t *testing.T) {
	_, ms, ws, svc := newService(t)
	ctx := context.Background()
	if _, err := ms.WriteRevision(ctx, memory.WriteInput{Domain: domains.Memory, Namespace: "project/tesseract/memory/notes", MemoryKey: "fusion.memory", Summary: "fusion alpha", Author: memory.Author{AgentID: "test"}, SessionID: "itemservice", Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject}); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Create(ctx, wsInput("fusion/workspace", "fusion alpha")); err != nil {
		t.Fatal(err)
	}
	in := memory.RecallInput{Namespaces: []string{"project/tesseract/memory/notes", itemWorkspaceNS}, Ranking: memory.RankingRelevance, SearchMode: memory.SearchModeLexical, Query: "fusion", Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Memory, domains.Domain(workspace.Domain)}}}
	page, err := svc.RecallPaged(ctx, in, memory.PageRequest{PayloadMode: memory.PayloadModeFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Kept) != 2 {
		t.Fatalf("mixed hits=%d want 2: %+v", len(page.Kept), page.Kept)
	}
	if page.Kept[0].Score == nil || page.Kept[1].Score == nil {
		t.Fatalf("mixed scores not fused: %+v", page.Kept)
	}
	if (page.Kept[0].Item == nil) == (page.Kept[1].Item == nil) {
		t.Fatalf("mixed variants = %+v", page.Kept)
	}
}

func TestWorkspaceRecallRejectsRevisionOnlyModes(t *testing.T) {
	_, _, _, svc := newService(t)
	ctx := context.Background()
	base := memory.RecallInput{Namespaces: []string{itemWorkspaceNS}, Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Domain(workspace.Domain)}}}
	for _, mutate := range []func(*memory.RecallInput){func(in *memory.RecallInput) { in.Ranking = memory.RankingSimilarity; in.Query = "x" }, func(in *memory.RecallInput) { in.RevisionScope = memory.RevisionScopeTimeline }, func(in *memory.RecallInput) { in.Filters.FacetKinds = []string{"doc"} }} {
		in := base
		mutate(&in)
		if _, err := svc.RecallPaged(ctx, in, memory.PageRequest{}); !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("unsupported workspace recall error = %v", err)
		}
	}
}

func containsJSONField(raw []byte, field string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return findField(value, field)
}
func findField(v any, field string) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == field {
				return true
			}
			if findField(v, field) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if findField(v, field) {
				return true
			}
		}
	}
	return false
}

package itemservice_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestWorkspaceRecallPrefixesIncludeBroadDeepAndLiteralPaths(t *testing.T) {
	_, _, ws, svc := newService(t)
	ctx := context.Background()
	targetInput := wsInput("prefix-target", "prefix target")
	targetInput.Namespace = itemWorkspaceNS + `/literal_%\part`
	target, err := ws.Create(ctx, targetInput)
	if err != nil {
		t.Fatal(err)
	}
	neighborInput := wsInput("prefix-neighbor", "prefix neighbor")
	neighborInput.Namespace = itemWorkspaceNS + `/literal-XX\part-neighbor`
	neighbor, err := ws.Create(ctx, neighborInput)
	if err != nil {
		t.Fatal(err)
	}

	selectors := []string{
		targetInput.Namespace + "/*",
		itemWorkspaceNS + "/*",
		"project/tesseract/workspace/*",
		"project/tesseract/*",
		"project/*",
	}
	for _, selector := range selectors {
		t.Run(selector, func(t *testing.T) {
			page, recallErr := svc.RecallPaged(ctx, memory.RecallInput{
				Namespaces: []string{selector}, Ranking: memory.RankingChronological,
				Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Domain(workspace.Domain)}},
			}, memory.PageRequest{PayloadMode: memory.PayloadModeSummary})
			if recallErr != nil {
				t.Fatal(recallErr)
			}
			seenTarget := false
			for _, result := range page.Kept {
				if result.Item != nil && result.Item.ItemID == target.ItemID {
					seenTarget = true
				}
				if selector == targetInput.Namespace+"/*" && result.Item != nil && result.Item.ItemID == neighbor.ItemID {
					t.Fatalf("literal prefix included similarly named neighbor: %+v", result.Item)
				}
			}
			if !seenTarget {
				t.Fatalf("target absent for selector %q: %+v", selector, page.Manifest)
			}
		})
	}
}

func TestWorkspaceRecallCursorReachesBeyondStoreCandidateLimit(t *testing.T) {
	_, _, ws, svc := newService(t)
	ctx := context.Background()
	oldest, err := ws.Create(ctx, wsInput("oldest", "oldest target"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 204; i++ {
		if _, err := ws.Create(ctx, wsInput(fmt.Sprintf("later-%03d", i), "later")); err != nil {
			t.Fatal(err)
		}
	}

	in := memory.RecallInput{
		Namespaces: []string{itemWorkspaceNS}, Ranking: memory.RankingChronological,
		Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Domain(workspace.Domain)}},
	}
	seen := map[string]bool{}
	cursor := ""
	pageSizes := []int{37, 64, 19}
	for pageNo := 0; ; pageNo++ {
		page, recallErr := svc.RecallPaged(ctx, in, memory.PageRequest{
			Limit: pageSizes[pageNo%len(pageSizes)], PayloadMode: memory.PayloadModeSummary, Cursor: cursor,
		})
		if recallErr != nil {
			t.Fatal(recallErr)
		}
		if page.Manifest.ResultsTotal != 205 {
			t.Fatalf("results_total=%d, want 205", page.Manifest.ResultsTotal)
		}
		for _, result := range page.Kept {
			if result.Item == nil || seen[result.Item.ItemID] {
				t.Fatalf("invalid or repeated result: %+v", result)
			}
			seen[result.Item.ItemID] = true
		}
		if page.Manifest.NextCursor == nil {
			break
		}
		cursor = *page.Manifest.NextCursor
	}
	if len(seen) != 205 || !seen[oldest.ItemID] {
		t.Fatalf("walk reached %d items; oldest=%v", len(seen), seen[oldest.ItemID])
	}
}

func TestMixedRecallIncludesRevisionResultsBeyondFiveHundred(t *testing.T) {
	_, revisions, ws, svc := newService(t)
	ctx := context.Background()
	const revisionNamespace = "project/tesseract/memory/notes"
	for i := 0; i < 501; i++ {
		_, err := revisions.WriteRevision(ctx, memory.WriteInput{
			Domain: domains.Memory, Namespace: revisionNamespace,
			MemoryKey: fmt.Sprintf("mixed.%03d", i), Summary: "mixed revision",
			Author: memory.Author{AgentID: "test"}, SessionID: "itemservice",
			Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	workspaceItem, err := ws.Create(ctx, wsInput("mixed-workspace", "mixed workspace"))
	if err != nil {
		t.Fatal(err)
	}
	in := memory.RecallInput{
		Namespaces: []string{revisionNamespace, itemWorkspaceNS}, Ranking: memory.RankingChronological,
		Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Memory, domains.Domain(workspace.Domain)}},
	}
	seen := map[string]bool{}
	cursor := ""
	for pageNo := 0; ; pageNo++ {
		limit := 137
		if pageNo%2 == 1 {
			limit = 73
		}
		page, recallErr := svc.RecallPaged(ctx, in, memory.PageRequest{Limit: limit, PayloadMode: memory.PayloadModeSummary, Cursor: cursor})
		if recallErr != nil {
			t.Fatal(recallErr)
		}
		if page.Manifest.ResultsTotal != 502 {
			t.Fatalf("results_total=%d, want 502", page.Manifest.ResultsTotal)
		}
		for _, result := range page.Kept {
			id := ""
			if result.Item != nil {
				id = result.Item.ItemID
			} else if result.Revision != nil {
				id = result.Revision.RevisionID
			}
			if id == "" || seen[id] {
				t.Fatalf("invalid or repeated mixed result: %+v", result)
			}
			seen[id] = true
		}
		if page.Manifest.NextCursor == nil {
			break
		}
		cursor = *page.Manifest.NextCursor
	}
	if len(seen) != 502 || !seen[workspaceItem.ItemID] {
		t.Fatalf("walk reached %d results; workspace=%v", len(seen), seen[workspaceItem.ItemID])
	}
}

func TestWorkspaceRecallBudgetCursorContinuesAfterKeptRows(t *testing.T) {
	_, _, ws, svc := newService(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := ws.Create(ctx, wsInput(fmt.Sprintf("budget-%d", i), "budget")); err != nil {
			t.Fatal(err)
		}
	}
	in := memory.RecallInput{
		Namespaces: []string{itemWorkspaceNS}, Ranking: memory.RankingChronological,
		Filters: memory.RecallFilters{Domains: []domains.Domain{domains.Domain(workspace.Domain)}},
	}
	first, err := svc.RecallPaged(ctx, in, memory.PageRequest{
		Limit: 3, PayloadMode: memory.PayloadModeSummary, Budget: memory.Budget{Bytes: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Kept) != 1 || first.Manifest.NextCursor == nil || first.Manifest.ResultsTotal != 3 {
		t.Fatalf("budgeted first page=%+v", first.Manifest)
	}
	second, err := svc.RecallPaged(ctx, in, memory.PageRequest{
		Limit: 2, PayloadMode: memory.PayloadModeSummary, Cursor: *first.Manifest.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Kept) != 2 || second.Manifest.NextCursor != nil || second.Manifest.ResultsTotal != 3 {
		t.Fatalf("continued page=%+v", second.Manifest)
	}
	if first.Kept[0].Item.ItemID == second.Kept[0].Item.ItemID {
		t.Fatal("budget continuation repeated the consumed row")
	}
}

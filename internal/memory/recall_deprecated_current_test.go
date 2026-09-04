package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

const deprecatedCurrentQuery = "terminal status probe"

type deprecatedCurrentFixture struct {
	terminal    memory.Revision
	superseded  memory.Revision
	replacement memory.Revision
	active      memory.Revision
}

func seedDeprecatedCurrentFixture(t *testing.T, store *memory.Store) deprecatedCurrentFixture {
	t.Helper()
	ctx := context.Background()
	write := func(key string) memory.Revision {
		t.Helper()
		in := sampleInput(key)
		in.Payload.Summary = deprecatedCurrentQuery
		rev, err := store.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
		return rev
	}

	terminal := write("deprecated.terminal")
	if err := store.Deprecate(ctx, terminal.RevisionID); err != nil {
		t.Fatalf("deprecate terminal: %v", err)
	}

	superseded := write("deprecated.superseded")
	replacementIn := sampleInput("deprecated.superseded")
	replacementIn.Payload.Summary = deprecatedCurrentQuery
	replacementIn.Supersedes = superseded.RevisionID
	replacement, err := store.WriteRevision(ctx, replacementIn)
	if err != nil {
		t.Fatalf("write replacement: %v", err)
	}

	active := write("deprecated.active")

	for _, rev := range []memory.Revision{terminal, superseded, replacement, active} {
		if err := store.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
			t.Fatalf("embed %s: %v", rev.MemoryKey, err)
		}
	}

	return deprecatedCurrentFixture{
		terminal: terminal, superseded: superseded, replacement: replacement, active: active,
	}
}

func revisionIDSet(results []memory.RecallResult) map[string]bool {
	ids := make(map[string]bool, len(results))
	for _, result := range results {
		ids[result.Revision.RevisionID] = true
	}
	return ids
}

func TestRecallCurrent_ExplicitDeprecatedReturnsOnlyTerminalLeavesAcrossRetrievalPaths(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ranking    memory.Ranking
		searchMode memory.SearchMode
	}{
		{name: "activation metadata", ranking: memory.RankingActivation},
		{name: "chronological metadata", ranking: memory.RankingChronological},
		{name: "similarity dense", ranking: memory.RankingSimilarity},
		{name: "relevance lexical", ranking: memory.RankingRelevance, searchMode: memory.SearchModeLexical},
		{name: "relevance semantic", ranking: memory.RankingRelevance, searchMode: memory.SearchModeSemantic},
		{name: "relevance hybrid", ranking: memory.RankingRelevance, searchMode: memory.SearchModeHybrid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := newTestStoreWithEmbedder(t)
			defer cleanup()
			fixture := seedDeprecatedCurrentFixture(t, store)

			results, err := store.Recall(context.Background(), memory.RecallInput{
				Namespaces:    []string{"user/chrispian/memory/notes"},
				RevisionScope: memory.RevisionScopeCurrent,
				Ranking:       tc.ranking,
				SearchMode:    tc.searchMode,
				Query:         deprecatedCurrentQuery,
				Filters: memory.RecallFilters{
					Statuses: []memory.Status{memory.StatusDeprecated},
				},
			})
			if err != nil {
				t.Fatalf("Recall: %v", err)
			}
			if len(results) != 1 || results[0].Revision.RevisionID != fixture.terminal.RevisionID {
				t.Fatalf("current deprecated ids = %v, want only terminal %s",
					revisionIDSet(results), fixture.terminal.RevisionID)
			}
		})
	}
}

func TestRecallCurrent_DeprecatedWideningRequiresAnExplicitStatus(t *testing.T) {
	store, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	fixture := seedDeprecatedCurrentFixture(t, store)
	ctx := context.Background()

	for _, in := range []memory.RecallInput{
		{
			Namespaces: []string{"user/chrispian/memory/notes"},
			Ranking:    memory.RankingActivation,
		},
		{
			Namespaces: []string{"user/chrispian/memory/notes"},
			Ranking:    memory.RankingRelevance,
			SearchMode: memory.SearchModeLexical,
			Query:      deprecatedCurrentQuery,
		},
	} {
		results, err := store.Recall(ctx, in)
		if err != nil {
			t.Fatalf("default Recall: %v", err)
		}
		for _, result := range results {
			if result.Revision.Status == memory.StatusDeprecated {
				t.Fatalf("default current recall exposed deprecated revision %s", result.Revision.RevisionID)
			}
		}
	}

	mixed, err := store.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingActivation,
		Filters: memory.RecallFilters{
			Statuses: []memory.Status{memory.StatusDraft, memory.StatusDeprecated},
		},
	})
	if err != nil {
		t.Fatalf("mixed-status Recall: %v", err)
	}
	got := revisionIDSet(mixed)
	for _, want := range []string{fixture.terminal.RevisionID, fixture.replacement.RevisionID, fixture.active.RevisionID} {
		if !got[want] {
			t.Errorf("mixed current recall omitted %s", want)
		}
	}
	if got[fixture.superseded.RevisionID] {
		t.Errorf("mixed current recall exposed superseded predecessor %s", fixture.superseded.RevisionID)
	}
}

func TestRecallTimeline_DeprecatedStillReturnsTerminalAndSupersededHistory(t *testing.T) {
	store, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	fixture := seedDeprecatedCurrentFixture(t, store)

	for _, tc := range []struct {
		name       string
		ranking    memory.Ranking
		searchMode memory.SearchMode
		query      string
	}{
		{name: "metadata", ranking: memory.RankingChronological},
		{name: "lexical", ranking: memory.RankingRelevance, searchMode: memory.SearchModeLexical, query: deprecatedCurrentQuery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := store.Recall(context.Background(), memory.RecallInput{
				Namespaces:    []string{"user/chrispian/memory/notes"},
				RevisionScope: memory.RevisionScopeTimeline,
				Ranking:       tc.ranking,
				SearchMode:    tc.searchMode,
				Query:         tc.query,
				Filters: memory.RecallFilters{
					Statuses: []memory.Status{memory.StatusDeprecated},
				},
			})
			if err != nil {
				t.Fatalf("timeline Recall: %v", err)
			}
			got := revisionIDSet(results)
			if len(got) != 2 || !got[fixture.terminal.RevisionID] || !got[fixture.superseded.RevisionID] {
				t.Fatalf("timeline deprecated ids = %v, want terminal %s and superseded %s",
					got, fixture.terminal.RevisionID, fixture.superseded.RevisionID)
			}
		})
	}
}

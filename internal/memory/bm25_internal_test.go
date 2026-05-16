package memory

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func newBM25TestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := NewStore(cs.DB(), nil, "", 0, NoopQueue{})
	cleanup := func() { _ = cs.Close() }
	return ms, cleanup
}

func bm25SampleInput(key, summary, body string) WriteInput {
	return WriteInput{
		Namespace:  "user/chrispian/memory",
		MemoryKey:  key,
		Author:     Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger:    TriggerExplicit,
		SessionID:  "manual:bm25",
		Origin:     OriginUser,
		Confidence: 0.9,
		Status:     StatusDraft,
		Payload: Payload{
			Summary: summary,
			Body:    body,
		},
	}
}

func TestFetchBM25Candidates_RanksByKeyword(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Three memories — one clearly about FTS5, two unrelated.
	if _, err := ms.WriteRevision(ctx, bm25SampleInput("bm25.a",
		"FTS5 virtual tables power the BM25 arm", "external-content table with triggers")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, bm25SampleInput("bm25.b",
		"user prefers terse output", "no trailing summaries")); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, bm25SampleInput("bm25.c",
		"deterministic-first design principle", "explicit DTOs over implicit magic")); err != nil {
		t.Fatalf("write c: %v", err)
	}

	got, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Query:      "FTS5",
		Filters:    RecallFilters{Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical}},
	}, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least 1 match for 'FTS5', got 0")
	}
	if got[0].MemoryKey != "bm25.a" {
		t.Errorf("expected bm25.a as top BM25 hit for 'FTS5', got %q", got[0].MemoryKey)
	}
}

func TestFetchBM25Candidates_RespectsNamespaceFilter(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	in1 := bm25SampleInput("ns.a", "distinctive keyword xylophone", "")
	if _, err := ms.WriteRevision(ctx, in1); err != nil {
		t.Fatalf("write ns1: %v", err)
	}

	in2 := bm25SampleInput("ns.b", "distinctive keyword xylophone", "")
	in2.Namespace = "user/chrispian/project/other/memory"
	if _, err := ms.WriteRevision(ctx, in2); err != nil {
		t.Fatalf("write ns2: %v", err)
	}

	got, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Query:      "xylophone",
		Filters:    RecallFilters{Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical}},
	}, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 match in namespace filter, got %d", len(got))
	}
	if got[0].Namespace != "user/chrispian/memory" {
		t.Errorf("expected filtered namespace, got %s", got[0].Namespace)
	}
}

func TestFetchBM25Candidates_FilterStatusAtQueryTime(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Write rev1, then supersede it — write.go auto-deprecates rev1.
	rev1, err := ms.WriteRevision(ctx, bm25SampleInput("dep.key", "quantum flux capacitor", ""))
	if err != nil {
		t.Fatalf("write rev1: %v", err)
	}

	in2 := bm25SampleInput("dep.key", "quantum flux capacitor replacement", "")
	in2.Supersedes = rev1.RevisionID
	if _, err := ms.WriteRevision(ctx, in2); err != nil {
		t.Fatalf("write rev2: %v", err)
	}

	// Timeline scope with default status filter should return only rev2
	// — rev1 is now deprecated and filtered out at query time.
	got, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces:    []string{"user/chrispian/memory"},
		RevisionScope: RevisionScopeTimeline,
		Query:         "quantum",
		Filters:       RecallFilters{Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical}},
	}, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 non-deprecated match on timeline, got %d", len(got))
	}
	if got[0].Status == StatusDeprecated {
		t.Errorf("deprecated revision leaked past status filter: %+v", got[0])
	}

	// Widening the status filter to include deprecated should surface
	// both rev1 (deprecated) and rev2 — proves the FTS index still holds
	// rev1's content (status filter is query-time, not index-time).
	got2, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces:    []string{"user/chrispian/memory"},
		RevisionScope: RevisionScopeTimeline,
		Query:         "quantum",
		Filters: RecallFilters{
			Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical, StatusDeprecated},
		},
	}, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates (with deprecated): %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("expected 2 matches including deprecated on timeline, got %d", len(got2))
	}
}

func TestFetchBM25Candidates_EmptyQueryReturnsNil(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, bm25SampleInput("e.a", "anything", "anything")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Query:      "   ",
	}, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty query should return no candidates, got %d", len(got))
	}
}

func TestFetchBM25Candidates_LimitsToN(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		key := "lim." + string(rune('a'+i))
		if _, err := ms.WriteRevision(ctx, bm25SampleInput(key,
			"shared keyword hyperbole", "body")); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	got, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Query:      "hyperbole",
		Filters:    RecallFilters{Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical}},
	}, 3)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected exactly 3 results from n=3, got %d", len(got))
	}
}

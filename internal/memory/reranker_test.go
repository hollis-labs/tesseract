package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// reverseReranker returns its input in reverse order, truncated to topK.
// Used to prove the wiring: without a reranker, Recall returns sorted by
// relevance; with this reranker attached, the order flips.
var reverseReranker = memory.RerankerFunc(func(_ context.Context, _ string, candidates []memory.Revision, topK int) ([]memory.Revision, error) {
	out := make([]memory.Revision, len(candidates))
	for i, c := range candidates {
		out[len(candidates)-1-i] = c
	}
	if topK > 0 && topK < len(out) {
		out = out[:topK]
	}
	return out, nil
})

func TestRecall_Reranker_Reorders(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, relevanceInput("rr.a",
		"reranker keyword one two three", "")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, relevanceInput("rr.b",
		"reranker keyword two", "")); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, relevanceInput("rr.c",
		"reranker keyword three", "")); err != nil {
		t.Fatalf("write c: %v", err)
	}

	ms.RegisterReranker("reverse", reverseReranker)

	baseline, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingRelevance,
		Query:      "reranker",
	})
	if err != nil {
		t.Fatalf("baseline recall: %v", err)
	}
	if len(baseline) < 2 {
		t.Fatalf("baseline expected ≥2, got %d", len(baseline))
	}

	reranked, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingRelevance,
		Query:      "reranker",
		Reranker:   "reverse",
	})
	if err != nil {
		t.Fatalf("reranked recall: %v", err)
	}
	if len(reranked) != len(baseline) {
		t.Fatalf("reranked length mismatch: got %d want %d", len(reranked), len(baseline))
	}

	// Reranked order should be the reverse of baseline.
	for i, r := range reranked {
		want := baseline[len(baseline)-1-i].Revision.RevisionID
		if r.Revision.RevisionID != want {
			t.Errorf("position %d: got %s want %s (reverse of baseline)",
				i, r.Revision.RevisionID, want)
		}
	}
}

// TestRecall_Reranker_ReinforcesOnlyPostRerankSet asserts that when a
// reranker truncates the result set via RerankerTopK, only the memories
// actually returned to the caller get their access counters bumped.
// Reinforcement must reflect what the caller saw, not the superset that
// the reranker evaluated.
func TestRecall_Reranker_ReinforcesOnlyPostRerankSet(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	revs := make([]memory.Revision, 3)
	for i, key := range []string{"rrf.a", "rrf.b", "rrf.c"} {
		r, err := ms.WriteRevision(ctx, relevanceInput(key, "shared distinct keyword", ""))
		if err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
		revs[i] = r
	}

	// Top-1 reranker: keep only the first candidate it receives.
	top1 := memory.RerankerFunc(func(_ context.Context, _ string, c []memory.Revision, _ int) ([]memory.Revision, error) {
		if len(c) == 0 {
			return c, nil
		}
		return c[:1], nil
	})
	ms.RegisterReranker("top1", top1)

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces:   []string{"user/chrispian/memory"},
		Ranking:      memory.RankingRelevance,
		Query:        "keyword",
		Reranker:     "top1",
		RerankerTopK: 1,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result after top-1 rerank, got %d", len(got))
	}
	returnedID := got[0].Revision.MemoryID

	// The returned memory should have access_count == 1; the others 0.
	for _, r := range revs {
		var count int
		if err := ms.DB().QueryRowContext(ctx,
			`SELECT access_count FROM memory_state WHERE memory_id = ?`, r.MemoryID,
		).Scan(&count); err != nil {
			t.Fatalf("read access_count for %s: %v", r.MemoryID, err)
		}
		if r.MemoryID == returnedID {
			if count != 1 {
				t.Errorf("returned memory %s: access_count = %d, want 1", r.MemoryID, count)
			}
		} else {
			if count != 0 {
				t.Errorf("dropped memory %s: access_count = %d, want 0 (was not surfaced to caller)",
					r.MemoryID, count)
			}
		}
	}
}

// TestRecall_Reranker_PreservesInputSet asserts that when no explicit
// RerankerTopK is requested, any input candidate the reranker drops is
// appended to the output in its original order. A misbehaving or
// conservative reranker must not silently shrink Recall's result set.
func TestRecall_Reranker_PreservesInputSet(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	for _, key := range []string{"pres.a", "pres.b", "pres.c"} {
		if _, err := ms.WriteRevision(ctx, relevanceInput(key, "shared keyword preserve", "")); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	// Dropping reranker: returns only the first candidate.
	dropRest := memory.RerankerFunc(func(_ context.Context, _ string, c []memory.Revision, _ int) ([]memory.Revision, error) {
		if len(c) == 0 {
			return c, nil
		}
		return c[:1], nil
	})
	ms.RegisterReranker("drop-rest", dropRest)

	// Baseline order without reranker — establishes the "original order"
	// the dropped items should fall back to.
	baseline, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingRelevance,
		Query:      "preserve",
	})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if len(baseline) != 3 {
		t.Fatalf("baseline expected 3 results, got %d", len(baseline))
	}

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingRelevance,
		Query:      "preserve",
		Reranker:   "drop-rest",
		// RerankerTopK not set — caller did not ask for truncation.
	})
	if err != nil {
		t.Fatalf("reranked: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results (reranker drops treated as tail), got %d", len(got))
	}

	// First slot: reranker's pick (first baseline item).
	if got[0].Revision.RevisionID != baseline[0].Revision.RevisionID {
		t.Errorf("slot 0 should be reranker's pick; got %s want %s",
			got[0].Revision.RevisionID, baseline[0].Revision.RevisionID)
	}
	// Slots 1 and 2: dropped items in their original baseline order.
	if got[1].Revision.RevisionID != baseline[1].Revision.RevisionID {
		t.Errorf("slot 1 should be baseline[1] (original order); got %s want %s",
			got[1].Revision.RevisionID, baseline[1].Revision.RevisionID)
	}
	if got[2].Revision.RevisionID != baseline[2].Revision.RevisionID {
		t.Errorf("slot 2 should be baseline[2] (original order); got %s want %s",
			got[2].Revision.RevisionID, baseline[2].Revision.RevisionID)
	}
}

// TestRecall_Reranker_DedupesOutput asserts that duplicate revisions
// returned by a misbehaving reranker collapse to a single occurrence.
func TestRecall_Reranker_DedupesOutput(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	for _, key := range []string{"dup.a", "dup.b"} {
		if _, err := ms.WriteRevision(ctx, relevanceInput(key, "shared keyword dedupe", "")); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	doubler := memory.RerankerFunc(func(_ context.Context, _ string, c []memory.Revision, _ int) ([]memory.Revision, error) {
		// Return candidate[0] twice followed by candidate[1].
		if len(c) < 2 {
			return c, nil
		}
		return []memory.Revision{c[0], c[0], c[1]}, nil
	})
	ms.RegisterReranker("doubler", doubler)

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingRelevance,
		Query:      "dedupe",
		Reranker:   "doubler",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results after dedupe, got %d", len(got))
	}
	if got[0].Revision.RevisionID == got[1].Revision.RevisionID {
		t.Errorf("duplicate revision leaked into results: %s == %s",
			got[0].Revision.RevisionID, got[1].Revision.RevisionID)
	}
}

// TestRecall_Reranker_RespectsExplicitTopK asserts that an explicit
// RerankerTopK still caps the result set even after missing-candidate
// fallback. Caller intent beats completeness when they asked for a
// specific top-K.
func TestRecall_Reranker_RespectsExplicitTopK(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	for _, key := range []string{"cap.a", "cap.b", "cap.c"} {
		if _, err := ms.WriteRevision(ctx, relevanceInput(key, "shared keyword cap", "")); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}

	// Reranker returns only 1 of 3; caller asks for topK=2.
	picky := memory.RerankerFunc(func(_ context.Context, _ string, c []memory.Revision, _ int) ([]memory.Revision, error) {
		if len(c) == 0 {
			return c, nil
		}
		return c[:1], nil
	})
	ms.RegisterReranker("picky", picky)

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces:   []string{"user/chrispian/memory"},
		Ranking:      memory.RankingRelevance,
		Query:        "cap",
		Reranker:     "picky",
		RerankerTopK: 2,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("explicit RerankerTopK=2 should cap; got %d", len(got))
	}
}

// TestRecall_Reranker_ConcurrentRegistration exercises RegisterReranker
// racing with concurrent Recall calls. Intended to be run under
// `go test -race`: without the rerankers mutex, the race detector flags
// concurrent map read/write here.
func TestRecall_Reranker_ConcurrentRegistration(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, relevanceInput("race.a", "race keyword", "")); err != nil {
		t.Fatalf("write: %v", err)
	}

	identity := memory.RerankerFunc(func(_ context.Context, _ string, c []memory.Revision, _ int) ([]memory.Revision, error) {
		return c, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			ms.RegisterReranker("r-"+strconv.Itoa(i), identity)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = ms.Recall(ctx, memory.RecallInput{
				Namespaces: []string{"user/chrispian/memory"},
				Ranking:    memory.RankingRelevance,
				Query:      "race",
			})
		}()
	}
	wg.Wait()
}

func TestRecall_Reranker_NotRegistered(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, relevanceInput("rr.missing",
		"any keyword", "")); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory"},
		Ranking:    memory.RankingRelevance,
		Query:      "any",
		Reranker:   "does-not-exist",
	})
	if !errors.Is(err, memory.ErrRerankerUnavailable) {
		t.Fatalf("expected ErrRerankerUnavailable, got %v", err)
	}
}

// TestHTTPReranker_RoundTrip exercises the Cohere/Voyage-compatible
// HTTP adapter against a test server that returns a fixed reordering.
func TestHTTPReranker_RoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			Model     string   `json:"model"`
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
			TopN      int      `json:"top_n,omitempty"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Score documents such that later indices win — proves the
		// adapter sorts by relevance_score descending regardless of
		// server-provided ordering.
		results := make([]map[string]any, 0, len(body.Documents))
		for i := range body.Documents {
			results = append(results, map[string]any{
				"index":           i,
				"relevance_score": float64(i+1) / float64(len(body.Documents)),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	rr := memory.NewHTTPReranker(memory.HTTPRerankerConfig{
		Endpoint: server.URL,
		APIKey:   "test-key",
		Model:    "rerank-english-v3.0",
	})

	revs := []memory.Revision{
		{RevisionID: "id-1", Payload: memory.Payload{Summary: "first doc"}},
		{RevisionID: "id-2", Payload: memory.Payload{Summary: "second doc"}},
		{RevisionID: "id-3", Payload: memory.Payload{Summary: "third doc"}},
	}
	got, err := rr.Rerank(context.Background(), "my query", revs, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	wantOrder := []string{"id-3", "id-2", "id-1"}
	for i, r := range got {
		if r.RevisionID != wantOrder[i] {
			t.Errorf("position %d: got %s want %s", i, r.RevisionID, wantOrder[i])
		}
	}
}

func TestHTTPReranker_UpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	rr := memory.NewHTTPReranker(memory.HTTPRerankerConfig{
		Endpoint: server.URL,
		APIKey:   "any",
		Model:    "any",
	})

	_, err := rr.Rerank(context.Background(), "q", []memory.Revision{{RevisionID: "a"}}, 0)
	if err == nil {
		t.Fatal("expected error on upstream 429, got nil")
	}
}

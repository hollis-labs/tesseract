package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

package embedding

import (
	"context"
	"math"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
)

// Compile-time assertion: MockProvider must satisfy provider.Embedder.
var _ provider.Embedder = (*MockProvider)(nil)

func TestMockProvider_Deterministic(t *testing.T) {
	p := NewMockProvider(768)

	if p.EmbeddingDimensions("mock-embed") != 768 {
		t.Errorf("dimensions = %d, want 768", p.EmbeddingDimensions("mock-embed"))
	}

	r1, err := p.Embed(context.Background(), "hello world", "mock-embed")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(r1.Embedding) != 768 {
		t.Fatalf("len = %d, want 768", len(r1.Embedding))
	}

	// Same text → same vector.
	r2, err := p.Embed(context.Background(), "hello world", "mock-embed")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	for i := range r1.Embedding {
		if r1.Embedding[i] != r2.Embedding[i] {
			t.Fatalf("non-deterministic at index %d: %f != %f", i, r1.Embedding[i], r2.Embedding[i])
		}
	}

	// Different text → different vector.
	r3, err := p.Embed(context.Background(), "goodbye world", "mock-embed")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	same := true
	for i := range r1.Embedding {
		if r1.Embedding[i] != r3.Embedding[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different texts produced identical vectors")
	}
}

func TestMockProvider_UnitVector(t *testing.T) {
	p := NewMockProvider(128)
	result, err := p.Embed(context.Background(), "test", "mock-embed")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	var norm float64
	for _, f := range result.Embedding {
		norm += float64(f) * float64(f)
	}
	norm = math.Sqrt(norm)
	if math.Abs(norm-1.0) > 0.001 {
		t.Errorf("vector norm = %f, want ~1.0", norm)
	}
}

func TestMockProvider_EmbedBatch(t *testing.T) {
	p := NewMockProvider(64)
	texts := []string{"hello", "world"}

	results, err := p.EmbedBatch(context.Background(), texts, "mock-embed")
	if err != nil {
		t.Fatalf("embed batch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for i, r := range results {
		if len(r.Embedding) != 64 {
			t.Errorf("results[%d]: len = %d, want 64", i, len(r.Embedding))
		}
	}
	// Different texts should produce different vectors.
	same := true
	for i := range results[0].Embedding {
		if results[0].Embedding[i] != results[1].Embedding[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("batch: different texts produced identical vectors")
	}
}

func TestMockProvider_EmbeddingDimensions(t *testing.T) {
	p := NewMockProvider(512)
	// Returns configured dimensions regardless of model name.
	if got := p.EmbeddingDimensions("any-model"); got != 512 {
		t.Errorf("EmbeddingDimensions = %d, want 512", got)
	}
	if got := p.EmbeddingDimensions(""); got != 512 {
		t.Errorf("EmbeddingDimensions(\"\") = %d, want 512", got)
	}
}

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 0, 0}
	s := CosineSimilarity(a, a)
	if math.Abs(s-1.0) > 0.0001 {
		t.Errorf("identical vectors: similarity = %f, want 1.0", s)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	s := CosineSimilarity(a, b)
	if math.Abs(s) > 0.0001 {
		t.Errorf("orthogonal vectors: similarity = %f, want 0.0", s)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{-1, 0, 0}
	s := CosineSimilarity(a, b)
	if math.Abs(s+1.0) > 0.0001 {
		t.Errorf("opposite vectors: similarity = %f, want -1.0", s)
	}
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
	s := CosineSimilarity(nil, nil)
	if s != 0 {
		t.Errorf("empty vectors: similarity = %f, want 0.0", s)
	}
}

func TestCosineSimilarity_MismatchedLengths(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0, 0}
	s := CosineSimilarity(a, b)
	if s != 0 {
		t.Errorf("mismatched lengths: similarity = %f, want 0.0", s)
	}
}

func TestRankByCosineSimilarity(t *testing.T) {
	// Create a query and 5 candidates with known similarities.
	query := []float32{1, 0, 0}
	vectors := [][]float32{
		{0, 1, 0},     // orthogonal → 0
		{1, 0, 0},     // identical → 1
		{0.7, 0.7, 0}, // ~0.707
		{-1, 0, 0},    // opposite → -1
		{0.9, 0.4, 0}, // ~0.914
	}
	ids := []string{"a", "b", "c", "d", "e"}
	nss := []string{"ns/a", "ns/b", "ns/c", "ns/d", "ns/e"}
	keys := []string{"ka", "kb", "kc", "kd", "ke"}

	results := RankByCosineSimilarity(query, vectors, ids, nss, keys, 3, 0.5)

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// Should be sorted: b (1.0), e (~0.914), c (~0.707).
	if results[0].RecordID != "b" {
		t.Errorf("results[0].RecordID = %q, want b", results[0].RecordID)
	}
	if results[1].RecordID != "e" {
		t.Errorf("results[1].RecordID = %q, want e", results[1].RecordID)
	}
	if results[2].RecordID != "c" {
		t.Errorf("results[2].RecordID = %q, want c", results[2].RecordID)
	}

	// Verify metadata.
	if results[0].Namespace != "ns/b" || results[0].Key != "kb" {
		t.Errorf("results[0] metadata wrong: ns=%q key=%q", results[0].Namespace, results[0].Key)
	}
}

func TestRankByCosineSimilarity_ThresholdFiltering(t *testing.T) {
	query := []float32{1, 0, 0}
	vectors := [][]float32{
		{0.5, 0.5, 0.5}, // similarity ~0.577
		{0.9, 0.1, 0},   // similarity ~0.994
	}
	ids := []string{"a", "b"}
	nss := []string{"ns/a", "ns/b"}
	keys := []string{"ka", "kb"}

	// High threshold should filter out 'a'.
	results := RankByCosineSimilarity(query, vectors, ids, nss, keys, 10, 0.9)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (threshold filter)", len(results))
	}
	if results[0].RecordID != "b" {
		t.Errorf("expected record b, got %q", results[0].RecordID)
	}
}

func TestRankByCosineSimilarity_Empty(t *testing.T) {
	query := []float32{1, 0, 0}
	results := RankByCosineSimilarity(query, nil, nil, nil, nil, 10, 0.5)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty candidates, got %d", len(results))
	}
}

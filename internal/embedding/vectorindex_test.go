package embedding

import (
	"context"
	"testing"
)

// mockEmbeddingStore implements EmbeddingStore for testing.
type mockEmbeddingStore struct {
	embeddings map[string]EmbeddingCandidate // keyed by recordID
}

func newMockStore() *mockEmbeddingStore {
	return &mockEmbeddingStore{embeddings: make(map[string]EmbeddingCandidate)}
}

func (m *mockEmbeddingStore) UpsertEmbeddingVec(_ context.Context, recordID, _ string, _ int, vector []float32) error {
	c := m.embeddings[recordID]
	c.RecordID = recordID
	c.Vector = vector
	m.embeddings[recordID] = c
	return nil
}

func (m *mockEmbeddingStore) SearchEmbeddings(_ context.Context, _ string, _, _ []string) ([]EmbeddingCandidate, error) {
	out := make([]EmbeddingCandidate, 0, len(m.embeddings))
	for _, c := range m.embeddings {
		out = append(out, c)
	}
	return out, nil
}

func (m *mockEmbeddingStore) DeleteEmbeddingVec(_ context.Context, recordID, _ string) error {
	delete(m.embeddings, recordID)
	return nil
}

func TestSQLiteVectorIndex_UpsertAndSearch(t *testing.T) {
	store := newMockStore()
	// Pre-populate with namespace/key metadata.
	store.embeddings["rec-1"] = EmbeddingCandidate{RecordID: "rec-1", Namespace: "test", Key: "a", Vector: []float32{1, 0, 0}}
	store.embeddings["rec-2"] = EmbeddingCandidate{RecordID: "rec-2", Namespace: "test", Key: "b", Vector: []float32{0, 1, 0}}

	idx := NewSQLiteVectorIndex(store)
	ctx := context.Background()

	// Search for vector closest to [1, 0, 0] — should be rec-1.
	results, err := idx.Search(ctx, []float32{1, 0, 0}, SearchOptions{Limit: 5, Model: "test"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].RecordID != "rec-1" {
		t.Errorf("expected rec-1 first, got %s", results[0].RecordID)
	}
	if results[0].Score < 0.99 {
		t.Errorf("expected score ~1.0, got %f", results[0].Score)
	}
}

func TestSQLiteVectorIndex_Delete(t *testing.T) {
	store := newMockStore()
	store.embeddings["rec-1"] = EmbeddingCandidate{RecordID: "rec-1", Namespace: "test", Key: "a", Vector: []float32{1, 0, 0}}

	idx := NewSQLiteVectorIndex(store)
	ctx := context.Background()

	if err := idx.Delete(ctx, "rec-1", "test"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	results, err := idx.Search(ctx, []float32{1, 0, 0}, SearchOptions{Limit: 5, Model: "test"})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestVectorIndexInterface(t *testing.T) {
	// Verify SQLiteVectorIndex implements VectorIndex.
	var _ VectorIndex = (*SQLiteVectorIndex)(nil)
	// Verify PgVectorIndex implements VectorIndex.
	var _ VectorIndex = (*PgVectorIndex)(nil)
}

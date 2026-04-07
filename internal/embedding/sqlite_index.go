package embedding

import (
	"context"
)

// EmbeddingStore abstracts the contextstore methods needed by SQLiteVectorIndex.
// This avoids a circular import between embedding and contextstore packages.
type EmbeddingStore interface {
	// UpsertEmbeddingVec stores a vector for the given record.
	UpsertEmbeddingVec(ctx context.Context, recordID, model string, dimensions int, vector []float32) error
	// SearchEmbeddings returns embeddings and their metadata for ranking.
	SearchEmbeddings(ctx context.Context, model string, namespaces, types []string) ([]EmbeddingCandidate, error)
	// DeleteEmbeddingVec removes a vector for a given record and model.
	DeleteEmbeddingVec(ctx context.Context, recordID, model string) error
}

// EmbeddingCandidate is a store-agnostic embedding row returned by EmbeddingStore.
type EmbeddingCandidate struct {
	RecordID  string
	Namespace string
	Key       string
	Vector    []float32
}

// SQLiteVectorIndex implements VectorIndex using the existing SQLite brute-force approach.
type SQLiteVectorIndex struct {
	store EmbeddingStore
}

// NewSQLiteVectorIndex creates a vector index backed by the existing SQLite store.
func NewSQLiteVectorIndex(store EmbeddingStore) *SQLiteVectorIndex {
	return &SQLiteVectorIndex{store: store}
}

func (s *SQLiteVectorIndex) Upsert(ctx context.Context, entry VectorEntry) error {
	return s.store.UpsertEmbeddingVec(ctx, entry.RecordID, entry.Model, entry.Dimensions, entry.Vector)
}

func (s *SQLiteVectorIndex) Search(ctx context.Context, query []float32, opts SearchOptions) ([]SearchResult, error) {
	candidates, err := s.store.SearchEmbeddings(ctx, opts.Model, opts.Namespaces, opts.Types)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	vectors := make([][]float32, len(candidates))
	recordIDs := make([]string, len(candidates))
	namespaces := make([]string, len(candidates))
	keys := make([]string, len(candidates))
	for i, c := range candidates {
		vectors[i] = c.Vector
		recordIDs[i] = c.RecordID
		namespaces[i] = c.Namespace
		keys[i] = c.Key
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	return RankByCosineSimilarity(query, vectors, recordIDs, namespaces, keys, limit, opts.Threshold), nil
}

func (s *SQLiteVectorIndex) Delete(ctx context.Context, recordID, model string) error {
	return s.store.DeleteEmbeddingVec(ctx, recordID, model)
}

func (s *SQLiteVectorIndex) Close() error {
	return nil // SQLite lifecycle managed by the store
}

package embedding

import "context"

// VectorIndex provides vector storage and similarity search.
// Implementations may use different backends (SQLite brute-force, pgvector, etc.).
type VectorIndex interface {
	// Upsert stores or replaces a vector for the given record.
	Upsert(ctx context.Context, entry VectorEntry) error
	// Search returns the top-k records most similar to the query vector.
	Search(ctx context.Context, query []float32, opts SearchOptions) ([]SearchResult, error)
	// Delete removes a vector for the given record and model.
	Delete(ctx context.Context, recordID, model string) error
	// Close releases resources held by the index.
	Close() error
}

// VectorEntry represents a vector to store in the index.
type VectorEntry struct {
	RecordID   string
	Namespace  string
	Key        string
	RecordType string
	Model      string
	Dimensions int
	Vector     []float32
}

// SearchOptions controls similarity search behavior.
type SearchOptions struct {
	Limit      int      // max results (default 10)
	Threshold  float64  // minimum similarity (default 0.0)
	Model      string   // embedding model to search
	Namespaces []string // namespace prefix filters
	Types      []string // record type filters
}

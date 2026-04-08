package memory

import (
	"database/sql"

	"github.com/hollis-labs/go-providers/provider"
)

// Store is the memory subsystem's storage handle. It shares a *sql.DB with
// contextstore.Store but owns its own read/write paths against the memory_*
// tables.
type Store struct {
	db             *sql.DB
	embedder       provider.Embedder
	embeddingModel string
	dedupThreshold float64
	queue          JobQueue
}

// NewStore constructs a memory.Store bound to the given database. embedder may
// be nil when embedding is unavailable; embedding-dependent Store methods must
// handle that case internally. embeddingModel is the model name passed to the
// embedder for query embedding during similarity recall. dedupThreshold is the
// cosine-similarity threshold above which a write is considered a semantic
// duplicate (0 disables dedup). queue may be nil and will be replaced with
// NoopQueue{}.
func NewStore(db *sql.DB, embedder provider.Embedder, embeddingModel string, dedupThreshold float64, queue JobQueue) *Store {
	if queue == nil {
		queue = NoopQueue{}
	}
	return &Store{db: db, embedder: embedder, embeddingModel: embeddingModel, dedupThreshold: dedupThreshold, queue: queue}
}

// DB returns the underlying *sql.DB. Used by tests that need direct DB access
// (e.g., to backdate timestamps for decay testing).
func (s *Store) DB() *sql.DB { return s.db }

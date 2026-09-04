package memory

import (
	"database/sql"
	"log/slog"
	"sync"
	"sync/atomic"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
)

// Store is the memory subsystem's storage handle. It shares a *sql.DB with
// contextstore.Store but owns its own read/write paths against the memory_*
// tables.
type Store struct {
	db                 *sql.DB
	embedder           embedcontracts.Embedder
	embeddingModel     string
	dedupThreshold     float64
	queue              JobQueue
	auditSink          AuditSink          // nil = audit emits are no-ops
	namespaceRegistrar NamespaceRegistrar // nil = namespace auto-register is a no-op
	logger             *slog.Logger       // nil = slog.Default()

	// enqueueFailures counts embed jobs the queue refused since this Store
	// was constructed. It is the counter half of the enqueue-failure signal —
	// the log line says which revision, this says whether it is happening at
	// all — and it is read through DeferredEmbeddingStatus. Atomic because
	// WriteRevision is called concurrently from HTTP and MCP handlers.
	enqueueFailures atomic.Int64

	// rerankers is guarded by rerankersMu so RegisterReranker may be
	// called concurrently with Recall. The typical pattern is to register
	// at startup, but we don't want a data race if a caller wires new
	// rerankers dynamically (e.g., a hot-reload config path).
	rerankersMu sync.RWMutex
	rerankers   map[string]Reranker
}

// NewStore constructs a memory.Store bound to the given database. embedder may
// be nil when embedding is unavailable; embedding-dependent Store methods must
// handle that case internally. embeddingModel is the model name passed to the
// embedder for query embedding during similarity recall. dedupThreshold is the
// cosine-similarity threshold above which a write is considered a semantic
// duplicate (0 uses the default threshold). queue may be nil and will be
// replaced with NoopQueue{}.
func NewStore(db *sql.DB, embedder embedcontracts.Embedder, embeddingModel string, dedupThreshold float64, queue JobQueue) *Store {
	if queue == nil {
		queue = NoopQueue{}
	}
	return &Store{db: db, embedder: embedder, embeddingModel: embeddingModel, dedupThreshold: dedupThreshold, queue: queue}
}

// DB returns the underlying *sql.DB. Used by tests that need direct DB access
// (e.g., to backdate timestamps for decay testing).
func (s *Store) DB() *sql.DB { return s.db }

// SetAuditSink wires an AuditSink into the Store. Call this once at
// construction time if audit emit is desired; passing nil (or not calling
// at all) disables audit emit, which is the default. Tests generally do
// not call this.
func (s *Store) SetAuditSink(sink AuditSink) { s.auditSink = sink }

// SetNamespaceRegistrar wires a NamespaceRegistrar into the Store. Call this
// once at construction time so memory writes auto-register their namespace
// in the policy registry. nil (or not calling) disables auto-register, which
// is the default for tests. See CW-20260428-0005.
func (s *Store) SetNamespaceRegistrar(r NamespaceRegistrar) { s.namespaceRegistrar = r }

// SetLogger wires a structured logger for the store's best-effort paths —
// work that has already been committed and must not fail the caller, but must
// not disappear either (today: deferred embed enqueue). Passing nil restores
// slog.Default().
//
// It is a setter rather than a NewStore parameter for the same reason
// SetAuditSink is: the dependency is optional, has a working default, and
// every existing call site stays valid.
func (s *Store) SetLogger(l *slog.Logger) { s.logger = l }

// log returns the store's logger, defaulting to slog.Default(). The shape
// matches contextstore's audit emit path (WarnContext + key/value attrs) so
// the two best-effort subsystems read the same way in a log stream.
func (s *Store) log() *slog.Logger {
	if s.logger == nil {
		return slog.Default()
	}
	return s.logger
}

package conduit

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// Config holds the top-level configuration for a Conduit instance.
type Config struct {
	RootDir string
}

// Option is a functional option for Open.
type Option func(*options)

type options struct {
	embedder       provider.Embedder
	embeddingModel string
	dedupThreshold float64
	logger         func(string, ...any)
	queue          queue.Queue
}

// WithEmbedder sets the embedding provider used for vector indexing.
func WithEmbedder(e provider.Embedder) Option {
	return func(o *options) { o.embedder = e }
}

// WithEmbeddingModel sets the model name passed to the embedder.
func WithEmbeddingModel(model string) Option {
	return func(o *options) { o.embeddingModel = model }
}

// WithLogger sets a custom log function (defaults to log.Printf).
func WithLogger(fn func(string, ...any)) Option {
	return func(o *options) { o.logger = fn }
}

// WithDedupThreshold sets the similarity threshold for semantic dedup.
func WithDedupThreshold(t float64) Option {
	return func(o *options) { o.dedupThreshold = t }
}

// WithQueue sets the background job queue used for async embedding.
// When set, a worker is started automatically and a QueueAdapter is
// used instead of the default NoopQueue.
func WithQueue(q queue.Queue) Option {
	return func(o *options) { o.queue = q }
}

// Conduit is the top-level library handle. Create one via Open().
type Conduit struct {
	store          *contextstore.Store
	memoryStore    *memory.Store
	embedder       provider.Embedder
	embeddingModel string
	logger         func(string, ...any)
	cancel         context.CancelFunc
}

// Open creates a Conduit instance rooted at cfg.RootDir, initializing the
// SQLite store, memory subsystem, and decay goroutine.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Conduit, error) {
	if cfg.RootDir == "" {
		return nil, errors.New("conduit: RootDir is required")
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.logger == nil {
		o.logger = log.Printf
	}

	dataDir := filepath.Join(cfg.RootDir, "data")
	recordsDir := filepath.Join(dataDir, "records")
	indexDir := filepath.Join(dataDir, "index")
	for _, dir := range []string{recordsDir, indexDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}

	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    cfg.RootDir,
		RecordsDir: recordsDir,
		DBPath:     filepath.Join(indexDir, "context.db"),
	})
	if err != nil {
		return nil, err
	}

	var jobQueue memory.JobQueue = memory.NoopQueue{}
	if o.queue != nil {
		jobQueue = memory.NewQueueAdapter(o.queue, "conduit")
	}

	memStore := memory.NewStore(store.DB(), o.embedder, o.embeddingModel, o.dedupThreshold, jobQueue)

	workerCtx, cancel := context.WithCancel(ctx)
	decayJob := &memory.DecayJob{
		Store:    memStore,
		Interval: 1 * time.Hour,
		Logger:   o.logger,
	}
	go decayJob.Run(workerCtx)

	if o.queue != nil {
		w := queue.NewWorker(o.queue, queue.WorkerOpts{
			Queues:     []string{"conduit"},
			MaxTries:   3,
			RetryAfter: 30 * time.Second,
			OnError:    func(err error) { o.logger("queue worker error: %v", err) },
		})
		w.Register("embed", NewEmbedHandler(memStore, o.embeddingModel, o.logger))
		go w.Start(workerCtx)
	}

	return &Conduit{
		store:          store,
		memoryStore:    memStore,
		embedder:       o.embedder,
		embeddingModel: o.embeddingModel,
		logger:         o.logger,
		cancel:         cancel,
	}, nil
}

// Close stops the decay goroutine and closes the underlying store.
func (c *Conduit) Close() error {
	c.cancel()
	return c.store.Close()
}

// Store returns the underlying contextstore.Store.
func (c *Conduit) Store() *contextstore.Store { return c.store }

// MemoryStore returns the underlying memory.Store.
func (c *Conduit) MemoryStore() *memory.Store { return c.memoryStore }

// WriteMemory writes a new memory revision.
func (c *Conduit) WriteMemory(ctx context.Context, in memory.WriteInput) (memory.Revision, error) {
	return c.memoryStore.WriteRevision(ctx, in)
}

// RecallMemory retrieves memories by namespace, ranking, and filters.
func (c *Conduit) RecallMemory(ctx context.Context, in memory.RecallInput) ([]memory.RecallResult, error) {
	return c.memoryStore.Recall(ctx, in)
}

// GetCurrentRevision returns the current revision for a namespace/key pair.
func (c *Conduit) GetCurrentRevision(ctx context.Context, namespace, key string) (memory.Revision, error) {
	return c.memoryStore.GetCurrent(ctx, namespace, key)
}

// GetRevisionHistory returns all revisions for a namespace/key pair, newest first.
func (c *Conduit) GetRevisionHistory(ctx context.Context, namespace, key string) ([]memory.Revision, error) {
	return c.memoryStore.GetHistory(ctx, namespace, key)
}

// EmbedRevision generates and stores an embedding for a memory revision.
func (c *Conduit) EmbedRevision(ctx context.Context, revisionID string) error {
	if c.embedder == nil {
		return ErrEmbedderUnavailable
	}
	return c.memoryStore.EmbedRevision(ctx, revisionID, c.embeddingModel)
}

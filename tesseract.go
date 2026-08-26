package tesseract

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// Config holds the top-level configuration for a Tesseract instance.
type Config struct {
	// RootDir is the base directory of the tesseract data layout. When DBPath
	// and RecordsDir are empty, the main database and JSON record tree are
	// derived under RootDir/data/ — the layout embedded consumers (nanite,
	// tesseract lib v0.7.0) rely on. RootDir is always required.
	RootDir string

	// DBPath, when set, is the explicit path to the main context database,
	// overriding the RootDir/data/index/context.db derivation.
	//
	// RecordsDir, when set, is the explicit JSON record tree, overriding
	// RootDir/data/records.
	//
	// These let a go-apppaths-resolved caller (cmd/tesseract, cmd/smoke) point
	// the library at the XDG layout without changing default behavior for
	// RootDir-only consumers. Added for the go-apppaths migration
	// (CW-20260517-0066).
	DBPath     string
	RecordsDir string
}

// Option is a functional option for Open.
type Option func(*options)

type options struct {
	embedder       embedcontracts.Embedder
	embeddingModel string
	dedupThreshold float64
	logger         func(string, ...any)
	queue          queue.Queue
}

// WithEmbedder sets the embedding provider used for vector indexing.
func WithEmbedder(e embedcontracts.Embedder) Option {
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

// Tesseract is the top-level library handle. Create one via Open().
type Tesseract struct {
	store          *contextstore.Store
	memoryStore    *memory.Store
	embedder       embedcontracts.Embedder
	embeddingModel string
	logger         func(string, ...any)
	cancel         context.CancelFunc
	workers        sync.WaitGroup
}

// Open creates a Tesseract instance rooted at cfg.RootDir, initializing the
// SQLite store, memory subsystem, and decay goroutine.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Tesseract, error) {
	if cfg.RootDir == "" {
		return nil, errors.New("tesseract: RootDir is required")
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.logger == nil {
		o.logger = log.Printf
	}

	dataDir := filepath.Join(cfg.RootDir, "data")
	recordsDir := cfg.RecordsDir
	if recordsDir == "" {
		recordsDir = filepath.Join(dataDir, "records")
	}
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "index", "context.db")
	}
	for _, dir := range []string{recordsDir, filepath.Dir(dbPath)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}

	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    cfg.RootDir,
		RecordsDir: recordsDir,
		DBPath:     dbPath,
	})
	if err != nil {
		return nil, err
	}

	var jobQueue memory.JobQueue = memory.NoopQueue{}
	if o.queue != nil {
		jobQueue = memory.NewQueueAdapter(o.queue, "tesseract")
	}

	memStore := memory.NewStore(store.DB(), o.embedder, o.embeddingModel, o.dedupThreshold, jobQueue)
	memStore.SetAuditSink(store)
	memStore.SetNamespaceRegistrar(store)

	// Reconcile any namespaces that have data but no policy row. Idempotent —
	// only writes the first time a divergence is observed. CW-20260428-0005.
	if _, err := store.ReconcileNamespaceRegistry(ctx); err != nil {
		o.logger("namespace reconcile: %v", err)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	decayJob := &memory.DecayJob{
		Store:    memStore,
		Interval: 1 * time.Hour,
		Logger:   o.logger,
	}
	tesseract := &Tesseract{
		store:          store,
		memoryStore:    memStore,
		embedder:       o.embedder,
		embeddingModel: o.embeddingModel,
		logger:         o.logger,
		cancel:         cancel,
	}
	tesseract.workers.Add(1)
	go func() {
		defer tesseract.workers.Done()
		decayJob.Run(workerCtx)
	}()

	if o.queue != nil {
		w := queue.NewWorker(o.queue, queue.WorkerOpts{
			Queues:     []string{"tesseract"},
			MaxTries:   3,
			RetryAfter: 30 * time.Second,
			OnError:    func(err error) { o.logger("queue worker error: %v", err) },
		})
		w.Register("embed", NewEmbedHandler(memStore, o.embeddingModel, o.logger))
		tesseract.workers.Add(1)
		go func() {
			defer tesseract.workers.Done()
			w.Start(workerCtx)
		}()
	}

	return tesseract, nil
}

// Close stops the decay goroutine and closes the underlying store.
func (c *Tesseract) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.workers.Wait()
	return c.store.Close()
}

// Store returns the underlying contextstore.Store.
func (c *Tesseract) Store() *contextstore.Store { return c.store }

// MemoryStore returns the underlying memory.Store.
func (c *Tesseract) MemoryStore() *memory.Store { return c.memoryStore }

// WriteMemory writes a new memory revision.
func (c *Tesseract) WriteMemory(ctx context.Context, in memory.WriteInput) (memory.Revision, error) {
	return c.memoryStore.WriteRevision(ctx, in)
}

// RecallMemory retrieves memories by namespace, ranking, and filters.
func (c *Tesseract) RecallMemory(ctx context.Context, in memory.RecallInput) ([]memory.RecallResult, error) {
	return c.memoryStore.Recall(ctx, in)
}

// GetCurrentRevision returns the current revision for a namespace/key pair.
func (c *Tesseract) GetCurrentRevision(ctx context.Context, namespace, key string) (memory.Revision, error) {
	return c.memoryStore.GetCurrent(ctx, namespace, key)
}

// GetRevisionHistory returns all revisions for a namespace/key pair, newest first.
func (c *Tesseract) GetRevisionHistory(ctx context.Context, namespace, key string) ([]memory.Revision, error) {
	return c.memoryStore.GetHistory(ctx, namespace, key)
}

// EmbedRevision generates and stores an embedding for a memory revision.
func (c *Tesseract) EmbedRevision(ctx context.Context, revisionID string) error {
	if c.embedder == nil {
		return ErrEmbedderUnavailable
	}
	return c.memoryStore.EmbedRevision(ctx, revisionID, c.embeddingModel)
}

package cortex

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/memory"
	"github.com/hollis-labs/go-providers/provider"
)

// Config holds the top-level configuration for a Cortex instance.
type Config struct {
	RootDir string
}

// Option is a functional option for Open.
type Option func(*options)

type options struct {
	embedder       provider.Embedder
	embeddingModel string
	logger         func(string, ...any)
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

// Cortex is the top-level library handle. Create one via Open().
type Cortex struct {
	store          *contextstore.Store
	memoryStore    *memory.Store
	embedder       provider.Embedder
	embeddingModel string
	logger         func(string, ...any)
	cancel         context.CancelFunc
}

// Open creates a Cortex instance rooted at cfg.RootDir, initializing the
// SQLite store, memory subsystem, and decay goroutine.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Cortex, error) {
	if cfg.RootDir == "" {
		return nil, errors.New("cortex: RootDir is required")
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

	memStore := memory.NewStore(store.DB(), o.embedder, memory.NoopQueue{})

	decayCtx, cancel := context.WithCancel(ctx)
	decayJob := &memory.DecayJob{
		Store:    memStore,
		Interval: 1 * time.Hour,
		Logger:   o.logger,
	}
	go decayJob.Run(decayCtx)

	return &Cortex{
		store:          store,
		memoryStore:    memStore,
		embedder:       o.embedder,
		embeddingModel: o.embeddingModel,
		logger:         o.logger,
		cancel:         cancel,
	}, nil
}

// Close stops the decay goroutine and closes the underlying store.
func (c *Cortex) Close() error {
	c.cancel()
	return c.store.Close()
}

// Store returns the underlying contextstore.Store.
func (c *Cortex) Store() *contextstore.Store { return c.store }

// MemoryStore returns the underlying memory.Store.
func (c *Cortex) MemoryStore() *memory.Store { return c.memoryStore }

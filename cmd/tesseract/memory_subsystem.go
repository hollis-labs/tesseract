package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-queue/driver/sqlite"
	tesseract "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"
)

// memorySubsystem holds the components wired up by setupMemorySubsystem.
// Callers must invoke Close when shutting down to release the queue DB.
type memorySubsystem struct {
	Store          *memory.Store
	Queue          queue.Queue
	QueueDBPath    string
	Embedder       embedcontracts.Embedder
	EmbeddingModel string
	queueDB        *sql.DB
	lifecycleCtx   context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	closeOnce      sync.Once
	closeErr       error
}

// startWorker registers a background worker with the subsystem shutdown
// barrier. Close does not release queueDB until every registered worker has
// observed lifecycleCtx cancellation and returned.
func (m *memorySubsystem) startWorker(run func(context.Context)) {
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		run(m.lifecycleCtx)
	}()
}

// Close stops and joins the queue and decay workers before releasing their
// queue DB handle. Safe to call multiple times.
func (m *memorySubsystem) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		if m.cancel != nil {
			m.cancel()
		}
		m.workers.Wait()
		if m.queueDB != nil {
			m.closeErr = m.queueDB.Close()
		}
	})
	return m.closeErr
}

// setupMemorySubsystem wires the memory store, queue, embedder, embed
// handler, and decay job. Shared by the MCP stdio entry point and the HTTP
// serve entry point so both expose the same behavior.
//
// Returns nil subsystem when tesseract configuration cannot produce an
// embedder AND no queue is strictly required — but we always wire the
// queue so the embed path is consistent.
func setupMemorySubsystem(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config) (*memorySubsystem, error) {
	return setupMemorySubsystemWithEmbedder(ctx, store, stderr, layout, tesseractCfg, createEmbedder(tesseractCfg))
}

// setupMemorySubsystemWithEmbedder is the single assembly path for the memory
// runtime. Production passes createEmbedder's configured instance; tests can
// pass a deterministic implementation without changing process credentials.
func setupMemorySubsystemWithEmbedder(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config, embedder embedcontracts.Embedder) (*memorySubsystem, error) {
	// queue.db is STATE (the embed-job queue), so it lands under the
	// go-apppaths StateDir alongside records/ — not under DataDir with the
	// main DB. CW-20260517-0066.
	queueDBDir := layout.StateDir()
	if err := os.MkdirAll(queueDBDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir queue db dir: %w", err)
	}
	queueDBPath := filepath.Join(queueDBDir, "queue.db")
	queueDB, err := sql.Open("sqlite", sqlitedsn.DSN(queueDBPath))
	if err != nil {
		return nil, fmt.Errorf("open queue db: %w", err)
	}

	q, err := sqlite.New(queueDB, sqlite.Opts{})
	if err != nil {
		_ = queueDB.Close()
		return nil, fmt.Errorf("init queue driver: %w", err)
	}
	// The subsystem intentionally owns cancel beyond this function's return;
	// Close invokes it before joining the registered workers.
	lifecycleCtx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel ownership is transferred to memorySubsystem.Close.
	subsystem := &memorySubsystem{
		QueueDBPath:    queueDBPath,
		Queue:          q,
		Embedder:       embedder,
		EmbeddingModel: tesseractCfg.Embedding.Model,
		queueDB:        queueDB,
		lifecycleCtx:   lifecycleCtx,
		cancel:         cancel,
	}

	queueAdapter := memory.NewQueueAdapter(q, "tesseract")

	memStore := memory.NewStore(
		store.DB(),
		embedder,
		tesseractCfg.Embedding.Model,
		tesseractCfg.Dedup.SimilarityThreshold,
		queueAdapter,
	)
	memStore.SetAuditSink(store)
	memStore.SetNamespaceRegistrar(store)

	// Reconcile any namespaces that have data but no policy row. Idempotent —
	// only writes the first time a divergence is observed. CW-20260428-0005.
	if registered, err := store.ReconcileNamespaceRegistry(ctx); err != nil {
		log.Printf("namespace reconcile: %v", err)
	} else if registered > 0 {
		log.Printf("namespace reconcile: registered %d previously-unregistered namespaces", registered)
	}

	worker := queue.NewWorker(q, queue.WorkerOpts{
		Queues:     []string{"tesseract"},
		MaxTries:   3,
		RetryAfter: 30 * time.Second,
		OnError:    func(err error) { log.Printf("queue worker error: %v", err) },
	})
	worker.Register(memory.EmbedJobKind, tesseract.NewEmbedHandler(memStore, tesseractCfg.Embedding.Model, log.Printf))
	subsystem.startWorker(func(ctx context.Context) {
		if err := worker.Start(ctx); err != nil {
			log.Printf("queue worker stopped with error: %v", err)
		}
	})

	decayInterval := 1 * time.Hour
	if v := os.Getenv("TESSERACT_MEMORY_DECAY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			decayInterval = d
		} else if stderr != nil {
			_, _ = stderr.WriteString("warning: invalid TESSERACT_MEMORY_DECAY_INTERVAL, using default 1h\n")
		}
	}
	decayJob := &memory.DecayJob{
		Store:    memStore,
		Interval: decayInterval,
		Logger:   log.Printf,
	}
	subsystem.startWorker(decayJob.Run)

	// Say out loud which of the two deferred-embedding worlds this process is
	// in. Production always wires a real queue, so a disabled line here means
	// the wiring regressed; a non-zero failure count later means committed
	// revisions are going unembedded and need a queue backfill.
	// CW-20260826-0018.
	if status := memStore.DeferredEmbeddingStatus(); status.Enabled {
		log.Printf("deferred embedding enabled (queue=%s db=%s)", status.Queue, queueDBPath)
	} else {
		log.Printf("deferred embedding DISABLED (queue=%s): revisions will commit unembedded", status.Queue)
	}

	subsystem.Store = memStore
	return subsystem, nil
}

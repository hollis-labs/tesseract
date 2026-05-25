package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-queue/driver/sqlite"
	tesseract "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// memorySubsystem holds the components wired up by setupMemorySubsystem.
// Callers must invoke Close when shutting down to release the queue DB.
type memorySubsystem struct {
	Store       *memory.Store
	Queue       queue.Queue
	QueueDBPath string
	queueDB     *sql.DB
}

// Close releases the queue DB handle. Safe to call multiple times.
func (m *memorySubsystem) Close() error {
	if m == nil || m.queueDB == nil {
		return nil
	}
	db := m.queueDB
	m.queueDB = nil
	return db.Close()
}

// setupMemorySubsystem wires the memory store, queue, embedder, embed
// handler, and decay job. Shared by the MCP stdio entry point and the HTTP
// serve entry point so both expose the same behavior.
//
// Returns nil subsystem when tesseract configuration cannot produce an
// embedder AND no queue is strictly required — but we always wire the
// queue so the embed path is consistent.
func setupMemorySubsystem(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config) (*memorySubsystem, error) {
	// queue.db is STATE (the embed-job queue), so it lands under the
	// go-apppaths StateDir alongside records/ — not under DataDir with the
	// main DB. CW-20260517-0066.
	queueDBDir := layout.StateDir()
	if err := os.MkdirAll(queueDBDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir queue db dir: %w", err)
	}
	queueDBPath := filepath.Join(queueDBDir, "queue.db")
	queueDBDSN := fmt.Sprintf("file:%s?_busy_timeout=5000&_fk=1", queueDBPath)
	queueDB, err := sql.Open("sqlite", queueDBDSN)
	if err != nil {
		return nil, fmt.Errorf("open queue db: %w", err)
	}

	q, err := sqlite.New(queueDB, sqlite.Opts{})
	if err != nil {
		_ = queueDB.Close()
		return nil, fmt.Errorf("init queue driver: %w", err)
	}

	queueAdapter := memory.NewQueueAdapter(q, "tesseract")

	embedder := createEmbedder(tesseractCfg)
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
	worker.Register("embed", tesseract.NewEmbedHandler(memStore, tesseractCfg.Embedding.Model, log.Printf))
	go worker.Start(ctx)

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
	go decayJob.Run(ctx)

	return &memorySubsystem{Store: memStore, Queue: q, QueueDBPath: queueDBPath, queueDB: queueDB}, nil
}

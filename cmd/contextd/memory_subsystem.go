package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	conduit "github.com/hollis-labs/vanta-conduit"
	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-queue/driver/sqlite"
	"github.com/hollis-labs/vanta-conduit/internal/config"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// memorySubsystem holds the components wired up by setupMemorySubsystem.
// Callers must invoke Close when shutting down to release the queue DB.
type memorySubsystem struct {
	Store   *memory.Store
	Queue   queue.Queue
	queueDB *sql.DB
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
// Returns nil subsystem when conduit configuration cannot produce an
// embedder AND no queue is strictly required — but we always wire the
// queue so the embed path is consistent.
func setupMemorySubsystem(ctx context.Context, store *contextstore.Store, stderr *os.File, root string, conduitCfg config.Config) (*memorySubsystem, error) {
	queueDBDir := filepath.Join(root, "data")
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

	queueAdapter := memory.NewQueueAdapter(q, "conduit")

	embedder := createEmbedder(conduitCfg)
	memStore := memory.NewStore(
		store.DB(),
		embedder,
		conduitCfg.Embedding.Model,
		conduitCfg.Dedup.SimilarityThreshold,
		queueAdapter,
	)

	worker := queue.NewWorker(q, queue.WorkerOpts{
		Queues:     []string{"conduit"},
		MaxTries:   3,
		RetryAfter: 30 * time.Second,
		OnError:    func(err error) { log.Printf("queue worker error: %v", err) },
	})
	worker.Register("embed", conduit.NewEmbedHandler(memStore, conduitCfg.Embedding.Model, log.Printf))
	go worker.Start(ctx)

	decayInterval := 1 * time.Hour
	if v := os.Getenv("CONDUIT_MEMORY_DECAY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			decayInterval = d
		} else if stderr != nil {
			_, _ = stderr.WriteString("warning: invalid CONDUIT_MEMORY_DECAY_INTERVAL, using default 1h\n")
		}
	}
	decayJob := &memory.DecayJob{
		Store:    memStore,
		Interval: decayInterval,
		Logger:   log.Printf,
	}
	go decayJob.Run(ctx)

	return &memorySubsystem{Store: memStore, Queue: q, queueDB: queueDB}, nil
}

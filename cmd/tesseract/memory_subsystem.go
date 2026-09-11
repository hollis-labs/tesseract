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
	"github.com/hollis-labs/tesseract/internal/fsperm"
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
// There is deliberately NO "which role am I" parameter, and that is a fix for a
// mistake this file already invited once. The daemon PUBLISHES a claim; a child
// READS one and stands down. Those are not one operation with a flag — they are
// two different operations, and forcing them through one symmetric hole is
// precisely what let an early version of this wire `runMCP` as the daemon and
// `runServe` as the child. That inversion compiled, passed every role-level
// test, and would have had every MCP child advertising ownership while the real
// daemon stood down, so nothing embedded anything at all.
//
// With two constructors the inversion is not a bug to be caught by a test; it
// is a program nobody would write, because neither call site takes a value the
// other could be given.
func setupDaemonMemorySubsystem(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config) (*memorySubsystem, error) {
	return setupDaemonMemorySubsystemWithEmbedder(ctx, store, stderr, layout, tesseractCfg, createEmbedder(tesseractCfg))
}

// setupChildMemorySubsystem wires a `tesseract mcp` adapter: it reads the
// daemon's claim and runs its embed worker only while no live daemon holds one.
func setupChildMemorySubsystem(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config) (*memorySubsystem, error) {
	return setupChildMemorySubsystemWithEmbedder(ctx, store, stderr, layout, tesseractCfg, createEmbedder(tesseractCfg))
}

// setupDaemonMemorySubsystemWithEmbedder is the daemon seam used by tests.
func setupDaemonMemorySubsystemWithEmbedder(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config, embedder embedcontracts.Embedder) (*memorySubsystem, error) {
	return setupMemorySubsystemWithEmbedder(ctx, store, stderr, layout, tesseractCfg, embedder, ownsEmbedQueue)
}

// setupChildMemorySubsystemWithEmbedder is the child seam used by tests.
func setupChildMemorySubsystemWithEmbedder(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config, embedder embedcontracts.Embedder) (*memorySubsystem, error) {
	return setupMemorySubsystemWithEmbedder(ctx, store, stderr, layout, tesseractCfg, embedder, defersToEmbedQueueOwner)
}

// queueParticipation is what a process contributes to the shared embed queue.
//
// Not an enum. The daemon supplies a publisher and no gate; a child supplies a
// gate and no publisher. Neither field can be set to the other's behavior by
// changing a constant, and the two constructors above are the only things that
// build one.
type queueParticipation struct {
	// publish advertises ownership. Daemon only; nil for a child.
	publish func(ctx context.Context, db *sql.DB) error

	// gate is consulted before each reservation. Child only; nil for the
	// daemon, which is what makes the daemon structurally incapable of
	// deferring to its own claim.
	gate func(db *sql.DB) func(context.Context) bool
}

func ownsEmbedQueue() queueParticipation {
	return queueParticipation{
		publish: func(ctx context.Context, db *sql.DB) error { return ensureDaemonClaim(ctx, db) },
	}
}

func defersToEmbedQueueOwner() queueParticipation {
	return queueParticipation{
		gate: func(db *sql.DB) func(context.Context) bool {
			return childCanReserve(db, daemonClaimFreshness, log.Printf)
		},
	}
}

// setupMemorySubsystemWithEmbedder is the single assembly path for the memory
// runtime. Production passes createEmbedder's configured instance; tests can
// pass a deterministic implementation without changing process credentials.
func setupMemorySubsystemWithEmbedder(ctx context.Context, store *contextstore.Store, stderr *os.File, layout paths.Layout, tesseractCfg config.Config, embedder embedcontracts.Embedder, participate func() queueParticipation) (*memorySubsystem, error) {
	// queue.db is STATE (the embed-job queue), so it lands under the
	// go-apppaths StateDir alongside records/ — not under DataDir with the
	// main DB. CW-20260517-0066.
	//
	// StateDir is Tesseract's own XDG state root, but go-apppaths materializes
	// it 0755 and cannot be told otherwise, so it is tightened here after the
	// fact. Only the directory itself: records/ also lives under StateDir and
	// converting that tree is contextstore.Open's job, which has the marker
	// that keeps it from re-walking a large store on every start.
	queueDBDir := layout.StateDir()
	if err := fsperm.EnsureDir(queueDBDir); err != nil {
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
	// The driver has now created queue.db (and possibly its WAL sidecars); the
	// SQLite layer knows nothing of the owner-only policy, so the mode is
	// corrected once the files exist. This also converts a queue.db left at
	// 0644 by an older build.
	for _, path := range []string{queueDBPath, queueDBPath + "-wal", queueDBPath + "-shm"} {
		if err := fsperm.TightenPath(path); err != nil {
			_ = queueDB.Close()
			return nil, fmt.Errorf("secure queue db: %w", err)
		}
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

	// Who runs the embed worker, and on whose terms (CW-20260911-0039).
	//
	// Both roles build the SAME worker against the SAME queue. What differs is
	// what each brought with it: the daemon a publisher, a child a gate. A
	// daemon has no gate to consult, so it cannot find its own claim, conclude
	// a daemon owns the queue and stand down — which would stop the system
	// embedding anything at all, silently.
	part := participate()
	opts := queue.WorkerOpts{
		Queues:     []string{"tesseract"},
		MaxTries:   3,
		RetryAfter: 30 * time.Second,
		OnError:    func(err error) { log.Printf("queue worker error: %v", err) },
	}
	if part.gate != nil {
		// CanReserve (go-queue v0.2.0) is asked once per poll cycle, before a
		// reservation is attempted. It gates only NEW work: a job already held
		// runs to completion, which is why this replaced an earlier supervisor
		// that canceled the worker's context and could abandon a paid
		// embedding mid-flight.
		opts.CanReserve = part.gate(queueDB)
	}

	worker := queue.NewWorker(q, opts)
	worker.Register(memory.EmbedJobKind, tesseract.NewEmbedHandler(memStore, tesseractCfg.Embedding.Model, log.Printf))

	if part.publish != nil {
		if err = part.publish(ctx, queueDB); err != nil {
			_ = queueDB.Close()
			return nil, err
		}
		subsystem.startWorker(func(ctx context.Context) {
			runDaemonClaim(ctx, queueDB, log.Printf)
		})
	}
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

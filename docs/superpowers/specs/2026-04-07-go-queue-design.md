# go-queue: Shared Queue Go Module — Design Spec

Status: approved
Date: 2026-04-07
Module: `github.com/hollis-labs/go-queue`
Roadmap: Workstream I (Cortex release roadmap)

## Purpose

A standalone, Laravel-inspired job queue library for Go. Provides jobs with
serializable payloads, in-process workers, retry/backoff, failed-jobs
persistence, and a pluggable driver interface. At v1.0: in-process workers
with SQLite-backed persistence. First consumer: Cortex memory auto-embed.

## Why shared

Every app in the portfolio (Cortex, Nanite, Engine, Hadron) ends up needing
reliable background processing. Building it once as a versioned module
eliminates drift and makes every bug fix available to all consumers. The
Laravel queue mental model is well-understood and proven in production.

## Why Laravel-style

The queue pattern (dispatch, serialize, store, poll, execute, retry/fail) is
well-established. Laravel's implementation is mature and covers edge cases
that a greenfield design would rediscover. The user is fluent in Laravel's
mental model. This is an informed port, not a blind copy — adapted to Go
idioms and scoped to what's needed.

---

## Decisions

### D1. Module and repo

Standalone repo at `github.com/hollis-labs/go-queue`. Own `go.mod`, own
release cycle. Follows the `go-` prefix convention for shared Go modules
(`go-queue`, `go-provider`, `go-otel`, etc.).

### D2. Architecture — Queue interface + Worker

Approach B from brainstorming. Two core types:

1. **`Queue` interface** — driver contract for push/pop/delete/release
2. **`Worker`** — poll loop that dispatches to registered handler functions

No manager layer, no connector factory. If an app needs multiple
connections, it creates multiple Queue + Worker pairs. Simplest thing that
works; extensible without rework.

### D3. v1.0 drivers

Three drivers ship at v1.0:

- **SQLite** — persistent, production-grade, first-class citizen
- **Memory** — in-process, for testing and transient workloads
- **Noop** — drops all jobs silently, for feature-flagging or stubs

Additional drivers (PostgreSQL, Redis) are post-1.0 via the same interface.

### D4. Job definition pattern — handler functions

Jobs are type-string + JSON payload. Handlers are registered by type string:

```go
w.Register("embed", func(ctx context.Context, job *QueuedJob) error {
    // decode payload, do work
    return nil
})
```

No struct serialization, no reflection, no `encoding/gob`. Matches Cortex's
existing `Job{Kind, Payload}` pattern. Each app defines its own payload
schemas.

### D5. Worker model — in-process goroutines at v1.0

Workers are goroutines started via `worker.Start(ctx)`. Graceful shutdown
via context cancellation. Matches how Cortex runs its decay job.

Post-1.0: standalone CLI worker binary that polls the same database.

### D6. Retry and failure — maxTries + flat retryAfter

v1.0 retry semantics:
- `maxTries` — per-job override via `WithMaxTries`. If 0 (default at push
  time), inherits `WorkerOpts.MaxTries` (default 3). Explicit 0 on
  WorkerOpts means unlimited retries.
- `retryAfter` flat delay before retry (no exponential backoff at v1.0)
- Stuck job reclaim: reserved longer than `retryAfter` becomes available
- Failed jobs table: jobs that exhaust retries are moved with the error

Post-1.0: exponential backoff arrays, `retryUntil`, `maxExceptions`.

### D7. Lifecycle hooks — simple callbacks

Four callbacks on `WorkerOpts`:
- `OnProcessing` — before handler is called
- `OnProcessed` — after successful completion
- `OnFailed` — after job exhausts retries
- `OnError` — non-job errors (pop failures, etc.)

Post-1.0: multi-listener event emitter if needed. Apps with their own event
systems (Nanite's plugin events) can bridge via callbacks.

### D8. Cortex integration — adapter pattern

Cortex keeps its existing `JobQueue` interface. A thin adapter (~5 lines)
wraps `go-queue.Queue.Push` behind it. No changes to the memory write path
or existing tests. Integration is isolated to `main.go` wiring.

---

## Package Layout

```
github.com/hollis-labs/go-queue/
├── queue.go              # Core interfaces: Queue, QueuedJob, Handler, PushOption
├── worker.go             # Worker struct, poll loop, dispatch
├── errors.go             # Sentinel errors
├── driver/
│   ├── sqlite/
│   │   ├── sqlite.go     # SQLite driver implementing Queue
│   │   ├── schema.go     # Table DDL
│   │   └── sqlite_test.go
│   ├── memory/
│   │   ├── memory.go     # In-memory driver
│   │   └── memory_test.go
│   └── noop/
│       └── noop.go       # Noop driver
├── worker_test.go
└── go.mod
```

Root package (`queue`) holds interfaces and the worker. Drivers are
sub-packages under `driver/`. Consumers import the root + their chosen
driver.

---

## Core Interfaces

### Queue

```go
type Queue interface {
    Push(ctx context.Context, jobType string, payload []byte, opts ...PushOption) error
    Pop(ctx context.Context, queueName string) (*QueuedJob, error)
    Delete(ctx context.Context, id string) error
    Release(ctx context.Context, id string, delay time.Duration) error
    Size(ctx context.Context, queueName string) (int, error)
    Failed(ctx context.Context, job *QueuedJob, errMsg string) error
}
```

### QueuedJob

```go
type QueuedJob struct {
    ID          string
    Type        string
    Queue       string
    Payload     []byte
    Attempts    int
    MaxTries    int
    CreatedAt   time.Time
    AvailableAt time.Time
    ReservedAt  *time.Time
}
```

### Handler

```go
type Handler func(ctx context.Context, job *QueuedJob) error
```

### PushOption

Functional options for push-time configuration:

```go
type PushOption func(*pushConfig)

type pushConfig struct {
    queue    string        // default: "default"
    delay    time.Duration // default: 0 (immediate)
    maxTries int           // default: 0 (inherit from WorkerOpts.MaxTries)
}


func OnQueue(name string) PushOption { ... }
func WithDelay(d time.Duration) PushOption { ... }
func WithMaxTries(n int) PushOption { ... }
```

---

## Worker

```go
type WorkerOpts struct {
    Queues        []string        // priority order, default: ["default"]
    Concurrency   int             // polling goroutines, default: 1
    PollInterval  time.Duration   // sleep between empty polls, default: 3s
    MaxTries      int             // default max attempts, default: 3
    RetryAfter    time.Duration   // delay before retry, default: 60s
    MaxMemoryMB   int             // stop if exceeded, default: 0 (no limit)
    StopWhenEmpty bool            // exit when queue drained, default: false

    OnProcessing func(job *QueuedJob)
    OnProcessed  func(job *QueuedJob)
    OnFailed     func(job *QueuedJob, err error)
    OnError      func(err error)
}

type Worker struct { ... }

func NewWorker(q Queue, opts WorkerOpts) *Worker
func (w *Worker) Register(jobType string, h Handler)
func (w *Worker) Start(ctx context.Context) error
```

### Poll loop

1. Spawn `Concurrency` goroutines.
2. Each goroutine loops:
   - Try `Pop` on each queue in priority order (strict priority — higher
     queues fully drained before lower queues are checked).
   - Job found: look up handler by `job.Type`.
     - Handler exists + success: `Delete` the job.
     - Handler exists + error + attempts < maxTries: `Release` with
       `RetryAfter` delay.
     - Handler exists + error + attempts >= maxTries: `Failed`, fire
       `OnFailed`.
     - No handler registered: permanent failure, fire `OnError`.
   - No job found: sleep `PollInterval`.
3. Context cancellation: finish current job, then exit.

---

## SQLite Driver

### Jobs table

```sql
CREATE TABLE IF NOT EXISTS jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    queue        TEXT    NOT NULL DEFAULT 'default',
    type         TEXT    NOT NULL,
    payload      BLOB,
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_tries    INTEGER NOT NULL DEFAULT 0,
    reserved_at  INTEGER,
    available_at INTEGER NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_queue_available ON jobs (queue, available_at);
CREATE INDEX IF NOT EXISTS idx_jobs_queue_reserved  ON jobs (queue, reserved_at);
```

### Failed jobs table

```sql
CREATE TABLE IF NOT EXISTS failed_jobs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    queue     TEXT    NOT NULL,
    type      TEXT    NOT NULL,
    payload   BLOB,
    error     TEXT    NOT NULL,
    attempts  INTEGER NOT NULL,
    failed_at INTEGER NOT NULL
);
```

All timestamps are Unix integers. `reserved_at` NULL means available.
`attempts` starts at 0, incremented on each pop.

### Pop mechanism

Inside a transaction:

```sql
SELECT * FROM jobs
WHERE queue = ?
AND (
    (reserved_at IS NULL AND available_at <= ?)
    OR (reserved_at <= ?)
)
ORDER BY id ASC
LIMIT 1;
```

The second condition reclaims stuck jobs where `reserved_at <= now -
retryAfter`. On match:

```sql
UPDATE jobs SET reserved_at = ?, attempts = attempts + 1 WHERE id = ?;
```

SQLite's single-writer serialization makes this safe without `FOR UPDATE
SKIP LOCKED`.

### Release (delete + insert)

```sql
DELETE FROM jobs WHERE id = ?;
INSERT INTO jobs (queue, type, payload, attempts, max_tries, reserved_at, available_at, created_at)
VALUES (?, ?, ?, ?, ?, NULL, ?, ?);
```

`available_at = now + delay`. Attempts preserved from deleted row. New ID
preserves FIFO ordering — retried jobs go to the back of the line.

### Driver construction

```go
q, err := sqlite.New(db, sqlite.Opts{
    Table:       "jobs",          // default: "jobs"
    FailedTable: "failed_jobs",   // default: "failed_jobs"
    RetryAfter:  60 * time.Second, // stuck job reclaim, default: 60s
})
```

Takes a `*sql.DB` — consumer owns the connection. Tables auto-created on
`New` if they don't exist.

---

## Memory Driver

Channel-free, slice-backed implementation. Satisfies the same `Queue`
interface. No persistence. Serves as both:

- A production driver for apps that don't need durability
- The reference test implementation (behavioral contract defined by its tests)

---

## Noop Driver

Accepts every `Push`, returns nil from `Pop`. Never errors. Replaces
Cortex's current `NoopQueue` for cases where queue functionality is disabled.

---

## Cortex Integration

Cortex's existing `JobQueue` interface is preserved. A thin adapter wraps
`go-queue`:

```go
type queueAdapter struct {
    q     queue.Queue
    queue string
}

func (a *queueAdapter) Enqueue(ctx context.Context, job Job) error {
    return a.q.Push(ctx, job.Kind, job.Payload, queue.OnQueue(a.queue))
}
```

In `cmd/contextd/main.go`:

```go
q, err := sqlite.New(db, sqlite.Opts{})
w := queue.NewWorker(q, queue.WorkerOpts{MaxTries: 3})
w.Register("embed", makeEmbedHandler(store))
go w.Start(ctx)

adapter := &queueAdapter{q: q, queue: "default"}
store := memory.NewStore(db, embedder, adapter)
```

No changes to memory write path or existing tests.

---

## Testing Strategy

### Driver unit tests

Each driver tested against the `Queue` interface behavioral contract:
- Push/Pop/Delete/Release lifecycle
- Priority ordering
- Delayed jobs not visible until `available_at`
- Stuck job reclaim
- Failed jobs storage and retrieval

Memory driver tests define the reference contract. SQLite tests verify the
same behaviors with persistence.

### Worker unit tests

Tested against the memory driver:
- Handler dispatch by job type
- Retry on error (release with delay)
- Failure after max tries
- Unregistered job type becomes permanent failure
- Graceful shutdown via context cancellation
- Concurrency
- Priority queue ordering
- Lifecycle callbacks fire at correct points

### SQLite integration tests

- Pop locking under concurrent goroutines
- Release preserves attempts, gets new ID
- Table auto-creation
- Shared `*sql.DB` scenario

### Conventions

- stdlib `testing`, external test packages (`_test` suffix)
- Table-driven subtests with `t.Run()`
- `var _ Queue = (*sqlite.Driver)(nil)` compile-time assertions
- No mocks — memory driver is the test double

---

## Post-1.0 Roadmap

- Standalone CLI worker binary
- Exponential/custom backoff arrays
- `retryUntil` (time-based retry)
- `maxExceptions` (separate from maxTries)
- Job middleware pipeline
- Batching (job groups with aggregate callbacks)
- Rate limiting
- Unique jobs / dedup locks
- Channel-based internal routing (approach C architecture)
- Additional drivers: PostgreSQL, Redis
- Multi-listener event emitter

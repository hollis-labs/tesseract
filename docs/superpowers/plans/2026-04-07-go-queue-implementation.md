# go-queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `github.com/hollis-labs/go-queue` — a standalone, Laravel-inspired job queue library for Go with SQLite, memory, and noop drivers.

**Architecture:** Queue interface + Worker (approach B). Drivers implement a `Queue` interface with Push/Pop/Delete/Release/Size/Failed. A `Worker` polls a driver and dispatches to registered handler functions by job type string. No manager layer.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite` for the SQLite driver, stdlib `testing` for tests.

**Repo:** `~/Projects-apps/libs/go-queue` → `github.com/hollis-labs/go-queue`

**Design spec:** `cortex/docs/superpowers/specs/2026-04-07-go-queue-design.md`

---

## File Structure

```
go-queue/
├── go.mod                    # module github.com/hollis-labs/go-queue
├── go.sum
├── queue.go                  # Queue interface, QueuedJob, Handler, PushOption, pushConfig
├── worker.go                 # Worker struct, WorkerOpts, NewWorker, Register, Start
├── errors.go                 # Sentinel errors: ErrNoJob, ErrHandlerNotFound
├── driver/
│   ├── memory/
│   │   ├── memory.go         # In-memory Queue implementation (slice-backed)
│   │   └── memory_test.go    # Reference contract tests
│   ├── sqlite/
│   │   ├── sqlite.go         # SQLite Queue implementation
│   │   ├── schema.go         # DDL for jobs + failed_jobs tables
│   │   └── sqlite_test.go    # SQLite-specific tests + contract tests
│   └── noop/
│       ├── noop.go           # Noop Queue implementation
│       └── noop_test.go      # Verify noop behavior
└── worker_test.go            # Worker tests against memory driver
```

---

## Task 1: Module scaffold and core types

**Files:**
- Create: `go-queue/go.mod`
- Create: `go-queue/queue.go`
- Create: `go-queue/errors.go`

- [ ] **Step 1: Initialize the Go module**

```bash
cd ~/Projects-apps/libs/go-queue
go mod init github.com/hollis-labs/go-queue
```

- [ ] **Step 2: Write queue.go with all core types**

Create `queue.go`:

```go
package queue

import (
	"context"
	"time"
)

// Queue is the driver interface. Each backend (SQLite, memory, noop)
// implements this contract. Consumers depend on this interface, not
// concrete drivers.
type Queue interface {
	// Push enqueues a new job. The jobType identifies which handler
	// processes it; payload is an opaque JSON blob.
	Push(ctx context.Context, jobType string, payload []byte, opts ...PushOption) error

	// Pop retrieves and reserves the next available job from the named
	// queue. Returns nil, nil when the queue is empty.
	Pop(ctx context.Context, queueName string) (*QueuedJob, error)

	// Delete removes a completed job from the queue.
	Delete(ctx context.Context, id string) error

	// Release puts a job back on the queue with a delay. The job gets
	// a new position (FIFO ordering preserved) but keeps its attempt
	// count.
	Release(ctx context.Context, id string, delay time.Duration) error

	// Size returns the total number of jobs on the named queue.
	Size(ctx context.Context, queueName string) (int, error)

	// Failed moves a job to the failed jobs store with the given error
	// message. The original job is removed from the active queue.
	Failed(ctx context.Context, job *QueuedJob, errMsg string) error
}

// QueuedJob is a job that has been popped from the queue. It carries
// the metadata the worker needs for dispatch, retry, and failure.
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

// Handler processes a job payload. Registered with the worker by job
// type string.
type Handler func(ctx context.Context, job *QueuedJob) error

// PushOption configures optional parameters for Push.
type PushOption func(*pushConfig)

type pushConfig struct {
	queue    string
	delay    time.Duration
	maxTries int
}

func defaultPushConfig() pushConfig {
	return pushConfig{
		queue:    "default",
		delay:    0,
		maxTries: 0, // 0 = inherit from WorkerOpts.MaxTries
	}
}

func resolvePushConfig(opts []PushOption) pushConfig {
	cfg := defaultPushConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// OnQueue sets the queue name for the job. Default: "default".
func OnQueue(name string) PushOption {
	return func(c *pushConfig) { c.queue = name }
}

// WithDelay sets a delay before the job becomes available. Default: 0.
func WithDelay(d time.Duration) PushOption {
	return func(c *pushConfig) { c.delay = d }
}

// WithMaxTries sets the maximum number of attempts for this job.
// 0 means inherit from WorkerOpts.MaxTries.
func WithMaxTries(n int) PushOption {
	return func(c *pushConfig) { c.maxTries = n }
}
```

- [ ] **Step 3: Write errors.go**

Create `errors.go`:

```go
package queue

import "errors"

// ErrNoJob is returned by Pop when no job is available. Drivers return
// nil, nil instead of this error — this sentinel exists for internal
// worker logic.
var ErrNoJob = errors.New("no job available")

// ErrHandlerNotFound is used when a job is popped but no handler is
// registered for its type.
var ErrHandlerNotFound = errors.New("handler not found for job type")
```

- [ ] **Step 4: Verify it compiles**

```bash
cd ~/Projects-apps/libs/go-queue
go build ./...
```

Expected: clean exit, no errors.

- [ ] **Step 5: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add go.mod queue.go errors.go
git commit -m "feat: add core types — Queue interface, QueuedJob, Handler, PushOption"
```

---

## Task 2: Memory driver — Push, Pop, Delete

**Files:**
- Create: `go-queue/driver/memory/memory.go`
- Create: `go-queue/driver/memory/memory_test.go`

- [ ] **Step 1: Write the failing tests for basic lifecycle**

Create `driver/memory/memory_test.go`:

```go
package memory_test

import (
	"context"
	"testing"
	"time"

	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-queue/driver/memory"
)

// Compile-time assertion.
var _ queue.Queue = (*memory.Driver)(nil)

func TestPushPopDelete(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	// Push a job.
	err := q.Push(ctx, "test-job", []byte(`{"key":"val"}`))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Size should be 1.
	size, err := q.Size(ctx, "default")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != 1 {
		t.Fatalf("Size = %d, want 1", size)
	}

	// Pop the job.
	job, err := q.Pop(ctx, "default")
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if job == nil {
		t.Fatal("Pop returned nil, want job")
	}
	if job.Type != "test-job" {
		t.Errorf("Type = %q, want %q", job.Type, "test-job")
	}
	if string(job.Payload) != `{"key":"val"}` {
		t.Errorf("Payload = %q, want %q", job.Payload, `{"key":"val"}`)
	}
	if job.Queue != "default" {
		t.Errorf("Queue = %q, want %q", job.Queue, "default")
	}
	if job.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", job.Attempts)
	}

	// Delete.
	if err := q.Delete(ctx, job.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Size should be 0.
	size, err = q.Size(ctx, "default")
	if err != nil {
		t.Fatalf("Size after delete: %v", err)
	}
	if size != 0 {
		t.Fatalf("Size after delete = %d, want 0", size)
	}
}

func TestPopEmptyQueue(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	job, err := q.Pop(ctx, "default")
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if job != nil {
		t.Fatalf("Pop = %+v, want nil", job)
	}
}

func TestPopFIFOOrdering(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	_ = q.Push(ctx, "first", nil)
	_ = q.Push(ctx, "second", nil)
	_ = q.Push(ctx, "third", nil)

	for _, want := range []string{"first", "second", "third"} {
		job, err := q.Pop(ctx, "default")
		if err != nil {
			t.Fatalf("Pop: %v", err)
		}
		if job.Type != want {
			t.Errorf("Pop = %q, want %q", job.Type, want)
		}
		_ = q.Delete(ctx, job.ID)
	}
}

func TestNamedQueues(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	_ = q.Push(ctx, "high-job", nil, queue.OnQueue("high"))
	_ = q.Push(ctx, "low-job", nil, queue.OnQueue("low"))

	// Pop from "high" should get high-job.
	job, _ := q.Pop(ctx, "high")
	if job == nil || job.Type != "high-job" {
		t.Fatalf("Pop(high) = %+v, want high-job", job)
	}

	// Pop from "low" should get low-job.
	job, _ = q.Pop(ctx, "low")
	if job == nil || job.Type != "low-job" {
		t.Fatalf("Pop(low) = %+v, want low-job", job)
	}

	// Pop from "high" should be empty now.
	job, _ = q.Pop(ctx, "high")
	if job != nil {
		t.Fatalf("Pop(high) = %+v, want nil", job)
	}
}

func TestDelayedJob(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	// Push with 1-hour delay.
	_ = q.Push(ctx, "delayed", nil, queue.WithDelay(1*time.Hour))

	// Should not be visible yet.
	job, _ := q.Pop(ctx, "default")
	if job != nil {
		t.Fatalf("Pop = %+v, want nil (job is delayed)", job)
	}

	// Size still counts it.
	size, _ := q.Size(ctx, "default")
	if size != 1 {
		t.Fatalf("Size = %d, want 1", size)
	}
}

func TestMaxTriesStoredOnJob(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	_ = q.Push(ctx, "retry-job", nil, queue.WithMaxTries(5))

	job, _ := q.Pop(ctx, "default")
	if job.MaxTries != 5 {
		t.Errorf("MaxTries = %d, want 5", job.MaxTries)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/memory/ -v
```

Expected: compilation error — `memory` package does not exist.

- [ ] **Step 3: Write the memory driver**

Create `driver/memory/memory.go`:

```go
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	queue "github.com/hollis-labs/go-queue"
)

// Driver is an in-memory Queue implementation. Jobs are stored in a
// slice, ordered by ID. No persistence. Thread-safe via mutex.
type Driver struct {
	mu     sync.Mutex
	jobs   []*entry
	failed []*failedEntry
	nextID int64
}

type entry struct {
	id          int64
	jobType     string
	queueName   string
	payload     []byte
	attempts    int
	maxTries    int
	reservedAt  *time.Time
	availableAt time.Time
	createdAt   time.Time
}

type failedEntry struct {
	id        int64
	jobType   string
	queueName string
	payload   []byte
	errMsg    string
	attempts  int
	failedAt  time.Time
}

// New creates a new in-memory queue driver.
func New() *Driver {
	return &Driver{}
}

func (d *Driver) Push(_ context.Context, jobType string, payload []byte, opts ...queue.PushOption) error {
	cfg := queue.ResolvePushConfig(opts)
	d.mu.Lock()
	defer d.mu.Unlock()

	d.nextID++
	now := time.Now().UTC()
	availableAt := now.Add(cfg.Delay)

	d.jobs = append(d.jobs, &entry{
		id:          d.nextID,
		jobType:     jobType,
		queueName:   cfg.Queue,
		payload:     payload,
		attempts:    0,
		maxTries:    cfg.MaxTries,
		availableAt: availableAt,
		createdAt:   now,
	})
	return nil
}

func (d *Driver) Pop(_ context.Context, queueName string) (*queue.QueuedJob, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	for _, e := range d.jobs {
		if e.queueName != queueName {
			continue
		}
		if e.reservedAt != nil {
			continue // already reserved
		}
		if e.availableAt.After(now) {
			continue // not yet available
		}
		// Reserve it.
		e.attempts++
		rNow := now
		e.reservedAt = &rNow

		return d.toQueuedJob(e), nil
	}
	return nil, nil
}

func (d *Driver) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, e := range d.jobs {
		if fmt.Sprintf("%d", e.id) == id {
			d.jobs = append(d.jobs[:i], d.jobs[i+1:]...)
			return nil
		}
	}
	return nil // idempotent
}

func (d *Driver) Release(_ context.Context, id string, delay time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, e := range d.jobs {
		if fmt.Sprintf("%d", e.id) != id {
			continue
		}
		// Delete + re-insert with new ID (preserves FIFO on retry).
		attempts := e.attempts
		maxTries := e.maxTries
		newEntry := &entry{
			id:          0, // assigned below
			jobType:     e.jobType,
			queueName:   e.queueName,
			payload:     e.payload,
			attempts:    attempts,
			maxTries:    maxTries,
			reservedAt:  nil,
			availableAt: time.Now().UTC().Add(delay),
			createdAt:   e.createdAt,
		}
		d.jobs = append(d.jobs[:i], d.jobs[i+1:]...)
		d.nextID++
		newEntry.id = d.nextID
		d.jobs = append(d.jobs, newEntry)
		return nil
	}
	return nil // idempotent
}

func (d *Driver) Size(_ context.Context, queueName string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	count := 0
	for _, e := range d.jobs {
		if e.queueName == queueName {
			count++
		}
	}
	return count, nil
}

func (d *Driver) Failed(_ context.Context, job *queue.QueuedJob, errMsg string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Remove from active jobs.
	for i, e := range d.jobs {
		if fmt.Sprintf("%d", e.id) == job.ID {
			d.jobs = append(d.jobs[:i], d.jobs[i+1:]...)
			break
		}
	}

	d.failed = append(d.failed, &failedEntry{
		id:        0, // not needed for memory driver
		jobType:   job.Type,
		queueName: job.Queue,
		payload:   job.Payload,
		errMsg:    errMsg,
		attempts:  job.Attempts,
		failedAt:  time.Now().UTC(),
	})
	return nil
}

func (d *Driver) toQueuedJob(e *entry) *queue.QueuedJob {
	return &queue.QueuedJob{
		ID:          fmt.Sprintf("%d", e.id),
		Type:        e.jobType,
		Queue:       e.queueName,
		Payload:     e.payload,
		Attempts:    e.attempts,
		MaxTries:    e.maxTries,
		CreatedAt:   e.createdAt,
		AvailableAt: e.availableAt,
		ReservedAt:  e.reservedAt,
	}
}

// FailedJobs returns all failed jobs. Useful for test assertions.
func (d *Driver) FailedJobs() []queue.QueuedJob {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]queue.QueuedJob, len(d.failed))
	for i, f := range d.failed {
		out[i] = queue.QueuedJob{
			Type:     f.jobType,
			Queue:    f.queueName,
			Payload:  f.payload,
			Attempts: f.attempts,
		}
	}
	return out
}
```

Note: The memory driver calls `queue.ResolvePushConfig(opts)`. We need to export this helper. Update `queue.go` — change `resolvePushConfig` to `ResolvePushConfig` and `pushConfig` fields to exported:

Update `queue.go` — replace the unexported types:

```go
// PushConfig holds resolved push options. Exported so drivers can
// access the resolved values.
type PushConfig struct {
	Queue    string
	Delay    time.Duration
	MaxTries int
}

func defaultPushConfig() PushConfig {
	return PushConfig{
		Queue:    "default",
		Delay:    0,
		MaxTries: 0,
	}
}

// ResolvePushConfig applies options and returns the resolved config.
func ResolvePushConfig(opts []PushOption) PushConfig {
	cfg := defaultPushConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// PushOption configures optional parameters for Push.
type PushOption func(*PushConfig)

// OnQueue sets the queue name for the job. Default: "default".
func OnQueue(name string) PushOption {
	return func(c *PushConfig) { c.Queue = name }
}

// WithDelay sets a delay before the job becomes available. Default: 0.
func WithDelay(d time.Duration) PushOption {
	return func(c *PushConfig) { c.Delay = d }
}

// WithMaxTries sets the maximum number of attempts for this job.
// 0 means inherit from WorkerOpts.MaxTries.
func WithMaxTries(n int) PushOption {
	return func(c *PushConfig) { c.MaxTries = n }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/memory/ -v
```

Expected: all 6 tests pass.

- [ ] **Step 5: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add .
git commit -m "feat: add memory driver with Push/Pop/Delete/Release/Size/Failed"
```

---

## Task 3: Memory driver — Release and stuck job reclaim

**Files:**
- Modify: `go-queue/driver/memory/memory_test.go`

- [ ] **Step 1: Write failing tests for Release and stuck reclaim**

Append to `driver/memory/memory_test.go`:

```go
func TestRelease(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	_ = q.Push(ctx, "retry-me", []byte(`{"attempt":true}`))
	job, _ := q.Pop(ctx, "default")
	if job == nil {
		t.Fatal("Pop returned nil")
	}
	originalID := job.ID

	// Release with no delay.
	err := q.Release(ctx, job.ID, 0)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Pop again — should get the same job with new ID and preserved attempts.
	job2, _ := q.Pop(ctx, "default")
	if job2 == nil {
		t.Fatal("Pop after release returned nil")
	}
	if job2.ID == originalID {
		t.Error("Released job should have a new ID")
	}
	if job2.Type != "retry-me" {
		t.Errorf("Type = %q, want %q", job2.Type, "retry-me")
	}
	// Attempts: 1 from first pop + 1 from second pop = 2.
	if job2.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", job2.Attempts)
	}
}

func TestReleaseFIFOOrdering(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	_ = q.Push(ctx, "first", nil)
	_ = q.Push(ctx, "second", nil)

	// Pop first, release it.
	job, _ := q.Pop(ctx, "default")
	_ = q.Release(ctx, job.ID, 0)

	// Pop should get "second" next (released "first" goes to back).
	job2, _ := q.Pop(ctx, "default")
	if job2 == nil || job2.Type != "second" {
		t.Fatalf("Pop = %+v, want second", job2)
	}

	// Then "first" (re-inserted at end).
	_ = q.Delete(ctx, job2.ID)
	job3, _ := q.Pop(ctx, "default")
	if job3 == nil || job3.Type != "first" {
		t.Fatalf("Pop = %+v, want first", job3)
	}
}

func TestFailedJobStorage(t *testing.T) {
	ctx := context.Background()
	q := memory.New()

	_ = q.Push(ctx, "fail-me", []byte(`{"data":1}`))
	job, _ := q.Pop(ctx, "default")

	err := q.Failed(ctx, job, "something broke")
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}

	// Job should be removed from active queue.
	size, _ := q.Size(ctx, "default")
	if size != 0 {
		t.Errorf("Size = %d, want 0 after Failed", size)
	}

	// Should be in failed jobs.
	failed := q.FailedJobs()
	if len(failed) != 1 {
		t.Fatalf("FailedJobs len = %d, want 1", len(failed))
	}
	if failed[0].Type != "fail-me" {
		t.Errorf("Failed Type = %q, want %q", failed[0].Type, "fail-me")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

These tests exercise code already written in Task 2 (Release and Failed are implemented). Run:

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/memory/ -v
```

Expected: all 9 tests pass.

- [ ] **Step 3: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add driver/memory/memory_test.go
git commit -m "test: add Release, FIFO ordering, and Failed storage tests for memory driver"
```

---

## Task 4: Noop driver

**Files:**
- Create: `go-queue/driver/noop/noop.go`
- Create: `go-queue/driver/noop/noop_test.go`

- [ ] **Step 1: Write the failing test**

Create `driver/noop/noop_test.go`:

```go
package noop_test

import (
	"context"
	"testing"

	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-queue/driver/noop"
)

var _ queue.Queue = (*noop.Driver)(nil)

func TestNoopPushAndPop(t *testing.T) {
	ctx := context.Background()
	q := noop.New()

	// Push should succeed silently.
	if err := q.Push(ctx, "job", []byte("data")); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Pop should always return nil (jobs are dropped).
	job, err := q.Pop(ctx, "default")
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if job != nil {
		t.Fatalf("Pop = %+v, want nil", job)
	}

	// Size is always 0.
	size, err := q.Size(ctx, "default")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != 0 {
		t.Fatalf("Size = %d, want 0", size)
	}
}

func TestNoopDeleteAndRelease(t *testing.T) {
	ctx := context.Background()
	q := noop.New()

	// Delete and Release should be no-ops.
	if err := q.Delete(ctx, "nonexistent"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := q.Release(ctx, "nonexistent", 0); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestNoopFailed(t *testing.T) {
	ctx := context.Background()
	q := noop.New()

	job := &queue.QueuedJob{ID: "1", Type: "test"}
	if err := q.Failed(ctx, job, "err"); err != nil {
		t.Fatalf("Failed: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/noop/ -v
```

Expected: compilation error — `noop` package does not exist.

- [ ] **Step 3: Write the noop driver**

Create `driver/noop/noop.go`:

```go
package noop

import (
	"context"
	"time"

	queue "github.com/hollis-labs/go-queue"
)

// Driver is a no-op Queue implementation. Push accepts and drops every
// job. Pop always returns nil. Used when queue functionality is disabled.
type Driver struct{}

// New creates a new noop driver.
func New() *Driver { return &Driver{} }

func (*Driver) Push(_ context.Context, _ string, _ []byte, _ ...queue.PushOption) error {
	return nil
}

func (*Driver) Pop(_ context.Context, _ string) (*queue.QueuedJob, error) {
	return nil, nil
}

func (*Driver) Delete(_ context.Context, _ string) error        { return nil }
func (*Driver) Release(_ context.Context, _ string, _ time.Duration) error { return nil }
func (*Driver) Size(_ context.Context, _ string) (int, error)   { return 0, nil }
func (*Driver) Failed(_ context.Context, _ *queue.QueuedJob, _ string) error { return nil }
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/noop/ -v
```

Expected: all 3 tests pass.

- [ ] **Step 5: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add driver/noop/
git commit -m "feat: add noop driver — accepts and drops all jobs"
```

---

## Task 5: Worker — core poll loop and dispatch

**Files:**
- Create: `go-queue/worker.go`
- Create: `go-queue/worker_test.go`

- [ ] **Step 1: Write failing tests for basic dispatch**

Create `worker_test.go`:

```go
package queue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	queue "github.com/hollis-labs/go-queue"
	"github.com/hollis-labs/go-queue/driver/memory"
)

func TestWorkerDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var received atomic.Value

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		StopWhenEmpty: true,
	})
	w.Register("greet", func(_ context.Context, job *queue.QueuedJob) error {
		received.Store(string(job.Payload))
		return nil
	})

	_ = q.Push(ctx, "greet", []byte(`"hello"`))

	err := w.Start(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Start: %v", err)
	}

	val := received.Load()
	if val == nil || val.(string) != `"hello"` {
		t.Fatalf("received = %v, want %q", val, `"hello"`)
	}
}

func TestWorkerRetryOnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var attempts atomic.Int32

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		MaxTries:      3,
		RetryAfter:    0, // immediate retry for testing
		StopWhenEmpty: true,
	})
	w.Register("flaky", func(_ context.Context, job *queue.QueuedJob) error {
		attempts.Add(1)
		return errors.New("fail")
	})

	_ = q.Push(ctx, "flaky", nil)
	_ = w.Start(ctx)

	// Should have been attempted 3 times (maxTries=3).
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}

	// Should be in failed jobs.
	failed := q.FailedJobs()
	if len(failed) != 1 {
		t.Fatalf("failed jobs = %d, want 1", len(failed))
	}
}

func TestWorkerUnregisteredHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var errorCalled atomic.Bool

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		StopWhenEmpty: true,
		OnError: func(err error) {
			errorCalled.Store(true)
		},
	})
	// No handler registered for "unknown".

	_ = q.Push(ctx, "unknown", nil)
	_ = w.Start(ctx)

	if !errorCalled.Load() {
		t.Fatal("OnError was not called for unregistered handler")
	}

	failed := q.FailedJobs()
	if len(failed) != 1 {
		t.Fatalf("failed jobs = %d, want 1", len(failed))
	}
}

func TestWorkerGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	q := memory.New()
	var started sync.WaitGroup
	started.Add(1)

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval: 10 * time.Millisecond,
	})
	w.Register("slow", func(_ context.Context, job *queue.QueuedJob) error {
		started.Done()
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	_ = q.Push(ctx, "slow", nil)

	done := make(chan error, 1)
	go func() { done <- w.Start(ctx) }()

	// Wait for handler to start, then cancel.
	started.Wait()
	cancel()

	err := <-done
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
}

func TestWorkerCallbacks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var processing, processed atomic.Bool

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		StopWhenEmpty: true,
		OnProcessing: func(job *queue.QueuedJob) {
			processing.Store(true)
		},
		OnProcessed: func(job *queue.QueuedJob) {
			processed.Store(true)
		},
	})
	w.Register("cb-test", func(_ context.Context, job *queue.QueuedJob) error {
		return nil
	})

	_ = q.Push(ctx, "cb-test", nil)
	_ = w.Start(ctx)

	if !processing.Load() {
		t.Error("OnProcessing was not called")
	}
	if !processed.Load() {
		t.Error("OnProcessed was not called")
	}
}

func TestWorkerOnFailedCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var failedType atomic.Value

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		MaxTries:      1,
		RetryAfter:    0,
		StopWhenEmpty: true,
		OnFailed: func(job *queue.QueuedJob, err error) {
			failedType.Store(job.Type)
		},
	})
	w.Register("doomed", func(_ context.Context, job *queue.QueuedJob) error {
		return errors.New("doom")
	})

	_ = q.Push(ctx, "doomed", nil)
	_ = w.Start(ctx)

	val := failedType.Load()
	if val == nil || val.(string) != "doomed" {
		t.Fatalf("OnFailed type = %v, want %q", val, "doomed")
	}
}

func TestWorkerPriorityQueues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var order []string
	var mu sync.Mutex

	w := queue.NewWorker(q, queue.WorkerOpts{
		Queues:        []string{"high", "low"},
		PollInterval:  10 * time.Millisecond,
		StopWhenEmpty: true,
	})
	w.Register("track", func(_ context.Context, job *queue.QueuedJob) error {
		mu.Lock()
		order = append(order, string(job.Payload))
		mu.Unlock()
		return nil
	})

	// Push to low first, then high.
	_ = q.Push(ctx, "track", []byte("low-1"), queue.OnQueue("low"))
	_ = q.Push(ctx, "track", []byte("high-1"), queue.OnQueue("high"))
	_ = q.Push(ctx, "track", []byte("low-2"), queue.OnQueue("low"))

	_ = w.Start(ctx)

	mu.Lock()
	defer mu.Unlock()
	// High should be processed before low.
	if len(order) != 3 {
		t.Fatalf("processed %d jobs, want 3", len(order))
	}
	if order[0] != "high-1" {
		t.Errorf("order[0] = %q, want %q", order[0], "high-1")
	}
}

func TestWorkerPerJobMaxTries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := memory.New()
	var attempts atomic.Int32

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		MaxTries:      10, // high default
		RetryAfter:    0,
		StopWhenEmpty: true,
	})
	w.Register("limited", func(_ context.Context, job *queue.QueuedJob) error {
		attempts.Add(1)
		return errors.New("fail")
	})

	// Per-job override to 2 tries.
	_ = q.Push(ctx, "limited", nil, queue.WithMaxTries(2))
	_ = w.Start(ctx)

	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./... -v
```

Expected: compilation error — `NewWorker` does not exist.

- [ ] **Step 3: Write the Worker**

Create `worker.go`:

```go
package queue

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// WorkerOpts configures worker behavior.
type WorkerOpts struct {
	// Queues to poll in priority order. Default: ["default"].
	Queues []string

	// Concurrency is the number of polling goroutines. Default: 1.
	Concurrency int

	// PollInterval is the sleep duration between empty polls. Default: 3s.
	PollInterval time.Duration

	// MaxTries is the default max attempts per job. Jobs with
	// WithMaxTries override this. 0 = unlimited. Default: 3.
	MaxTries int

	// RetryAfter is the delay before a failed job is retried. Default: 60s.
	RetryAfter time.Duration

	// MaxMemoryMB stops the worker if memory exceeds this. 0 = no limit.
	MaxMemoryMB int

	// StopWhenEmpty exits after the queue is drained. Default: false.
	StopWhenEmpty bool

	// Lifecycle callbacks.
	OnProcessing func(job *QueuedJob)
	OnProcessed  func(job *QueuedJob)
	OnFailed     func(job *QueuedJob, err error)
	OnError      func(err error)
}

// Worker polls a Queue and dispatches jobs to registered handlers.
type Worker struct {
	queue    Queue
	opts     WorkerOpts
	handlers map[string]Handler
	mu       sync.RWMutex
}

// NewWorker creates a Worker bound to the given Queue.
func NewWorker(q Queue, opts WorkerOpts) *Worker {
	if len(opts.Queues) == 0 {
		opts.Queues = []string{"default"}
	}
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 3 * time.Second
	}
	if opts.MaxTries == 0 {
		opts.MaxTries = 3
	}
	if opts.RetryAfter == 0 {
		opts.RetryAfter = 60 * time.Second
	}
	return &Worker{
		queue:    q,
		opts:     opts,
		handlers: make(map[string]Handler),
	}
}

// Register adds a handler for the given job type.
func (w *Worker) Register(jobType string, h Handler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[jobType] = h
}

// Start begins polling. Blocks until ctx is cancelled or StopWhenEmpty
// triggers. Returns nil on clean shutdown.
func (w *Worker) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < w.opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.pollLoop(ctx)
		}()
	}
	wg.Wait()
	return nil
}

func (w *Worker) pollLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		job := w.popNextJob(ctx)
		if job == nil {
			if w.opts.StopWhenEmpty {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.opts.PollInterval):
				continue
			}
		}

		w.processJob(ctx, job)
	}
}

func (w *Worker) popNextJob(ctx context.Context) *QueuedJob {
	for _, qName := range w.opts.Queues {
		job, err := w.queue.Pop(ctx, qName)
		if err != nil {
			if w.opts.OnError != nil {
				w.opts.OnError(err)
			}
			continue
		}
		if job != nil {
			return job
		}
	}
	return nil
}

func (w *Worker) processJob(ctx context.Context, job *QueuedJob) {
	w.mu.RLock()
	handler, ok := w.handlers[job.Type]
	w.mu.RUnlock()

	if !ok {
		// No handler registered — permanent failure.
		_ = w.queue.Failed(ctx, job, fmt.Sprintf("%v: %s", ErrHandlerNotFound, job.Type))
		if w.opts.OnError != nil {
			w.opts.OnError(fmt.Errorf("%w: %s", ErrHandlerNotFound, job.Type))
		}
		return
	}

	if w.opts.OnProcessing != nil {
		w.opts.OnProcessing(job)
	}

	err := handler(ctx, job)

	if err == nil {
		_ = w.queue.Delete(ctx, job.ID)
		if w.opts.OnProcessed != nil {
			w.opts.OnProcessed(job)
		}
		return
	}

	// Determine effective maxTries: per-job overrides worker default.
	maxTries := w.opts.MaxTries
	if job.MaxTries > 0 {
		maxTries = job.MaxTries
	}

	if maxTries > 0 && job.Attempts >= maxTries {
		_ = w.queue.Failed(ctx, job, err.Error())
		if w.opts.OnFailed != nil {
			w.opts.OnFailed(job, err)
		}
		return
	}

	// Retry: release with delay.
	_ = w.queue.Release(ctx, job.ID, w.opts.RetryAfter)
}
```

- [ ] **Step 4: Run all tests**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./... -v
```

Expected: all tests pass (memory driver tests + worker tests).

- [ ] **Step 5: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add worker.go worker_test.go
git commit -m "feat: add Worker with poll loop, dispatch, retry, and failure handling"
```

---

## Task 6: SQLite driver — schema and Push/Pop

**Files:**
- Create: `go-queue/driver/sqlite/schema.go`
- Create: `go-queue/driver/sqlite/sqlite.go`
- Create: `go-queue/driver/sqlite/sqlite_test.go`

- [ ] **Step 1: Add modernc.org/sqlite dependency**

```bash
cd ~/Projects-apps/libs/go-queue
go get modernc.org/sqlite
```

- [ ] **Step 2: Write the failing tests**

Create `driver/sqlite/sqlite_test.go`:

```go
package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	queue "github.com/hollis-labs/go-queue"
	qsqlite "github.com/hollis-labs/go-queue/driver/sqlite"
	_ "modernc.org/sqlite"
)

var _ queue.Queue = (*qsqlite.Driver)(nil)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// Enable WAL mode for better concurrency.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("WAL: %v", err)
	}
	return db
}

func TestSQLitePushPopDelete(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, err := qsqlite.New(db, qsqlite.Opts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Push.
	err = q.Push(ctx, "test-job", []byte(`{"key":"val"}`))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Size.
	size, err := q.Size(ctx, "default")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != 1 {
		t.Fatalf("Size = %d, want 1", size)
	}

	// Pop.
	job, err := q.Pop(ctx, "default")
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if job == nil {
		t.Fatal("Pop returned nil")
	}
	if job.Type != "test-job" {
		t.Errorf("Type = %q, want %q", job.Type, "test-job")
	}
	if string(job.Payload) != `{"key":"val"}` {
		t.Errorf("Payload = %q, want %q", job.Payload, `{"key":"val"}`)
	}
	if job.Queue != "default" {
		t.Errorf("Queue = %q, want %q", job.Queue, "default")
	}
	if job.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", job.Attempts)
	}

	// Delete.
	if err := q.Delete(ctx, job.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	size, _ = q.Size(ctx, "default")
	if size != 0 {
		t.Fatalf("Size after delete = %d, want 0", size)
	}
}

func TestSQLitePopEmpty(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	job, err := q.Pop(ctx, "default")
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if job != nil {
		t.Fatalf("Pop = %+v, want nil", job)
	}
}

func TestSQLiteFIFO(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "first", nil)
	_ = q.Push(ctx, "second", nil)
	_ = q.Push(ctx, "third", nil)

	for _, want := range []string{"first", "second", "third"} {
		job, _ := q.Pop(ctx, "default")
		if job == nil || job.Type != want {
			t.Fatalf("Pop = %+v, want %q", job, want)
		}
		_ = q.Delete(ctx, job.ID)
	}
}

func TestSQLiteNamedQueues(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "high-job", nil, queue.OnQueue("high"))
	_ = q.Push(ctx, "low-job", nil, queue.OnQueue("low"))

	job, _ := q.Pop(ctx, "high")
	if job == nil || job.Type != "high-job" {
		t.Fatalf("Pop(high) = %+v, want high-job", job)
	}

	job, _ = q.Pop(ctx, "low")
	if job == nil || job.Type != "low-job" {
		t.Fatalf("Pop(low) = %+v, want low-job", job)
	}

	job, _ = q.Pop(ctx, "high")
	if job != nil {
		t.Fatalf("Pop(high) = %+v, want nil", job)
	}
}

func TestSQLiteDelayedJob(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "delayed", nil, queue.WithDelay(1*time.Hour))

	job, _ := q.Pop(ctx, "default")
	if job != nil {
		t.Fatalf("Pop = %+v, want nil (job is delayed)", job)
	}
}

func TestSQLiteMaxTries(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "retry-job", nil, queue.WithMaxTries(5))

	job, _ := q.Pop(ctx, "default")
	if job.MaxTries != 5 {
		t.Errorf("MaxTries = %d, want 5", job.MaxTries)
	}
}

func TestSQLiteRelease(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "retry-me", []byte(`{"attempt":true}`))
	job, _ := q.Pop(ctx, "default")
	originalID := job.ID

	err := q.Release(ctx, job.ID, 0)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	job2, _ := q.Pop(ctx, "default")
	if job2 == nil {
		t.Fatal("Pop after release returned nil")
	}
	if job2.ID == originalID {
		t.Error("Released job should have a new ID")
	}
	if job2.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", job2.Attempts)
	}
}

func TestSQLiteReleaseFIFO(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "first", nil)
	_ = q.Push(ctx, "second", nil)

	job, _ := q.Pop(ctx, "default")
	_ = q.Release(ctx, job.ID, 0)

	job2, _ := q.Pop(ctx, "default")
	if job2 == nil || job2.Type != "second" {
		t.Fatalf("Pop = %+v, want second", job2)
	}
	_ = q.Delete(ctx, job2.ID)

	job3, _ := q.Pop(ctx, "default")
	if job3 == nil || job3.Type != "first" {
		t.Fatalf("Pop = %+v, want first", job3)
	}
}

func TestSQLiteFailed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	_ = q.Push(ctx, "fail-me", []byte(`{"data":1}`))
	job, _ := q.Pop(ctx, "default")

	err := q.Failed(ctx, job, "something broke")
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}

	size, _ := q.Size(ctx, "default")
	if size != 0 {
		t.Errorf("Size = %d, want 0 after Failed", size)
	}

	// Verify failed_jobs table has the entry.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM failed_jobs").Scan(&count)
	if count != 1 {
		t.Errorf("failed_jobs count = %d, want 1", count)
	}
}

func TestSQLiteTableAutoCreate(t *testing.T) {
	db := newTestDB(t)
	_, err := qsqlite.New(db, qsqlite.Opts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Verify tables exist by querying them.
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&n); err != nil {
		t.Fatalf("jobs table not created: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM failed_jobs").Scan(&n); err != nil {
		t.Fatalf("failed_jobs table not created: %v", err)
	}
}

func TestSQLiteStuckJobReclaim(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{
		RetryAfter: 1 * time.Second,
	})

	_ = q.Push(ctx, "stuck", nil)
	job, _ := q.Pop(ctx, "default")
	if job == nil {
		t.Fatal("Pop returned nil")
	}

	// Manually backdate reserved_at to simulate a stuck job.
	stuckTime := time.Now().Add(-10 * time.Second).Unix()
	_, err := db.Exec("UPDATE jobs SET reserved_at = ? WHERE id = ?", stuckTime, job.ID)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Pop should reclaim the stuck job.
	job2, err := q.Pop(ctx, "default")
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if job2 == nil {
		t.Fatal("Pop did not reclaim stuck job")
	}
	if job2.ID != job.ID {
		t.Errorf("reclaimed job ID = %s, want %s", job2.ID, job.ID)
	}
	if job2.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", job2.Attempts)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/sqlite/ -v
```

Expected: compilation error — `sqlite` package does not exist.

- [ ] **Step 4: Write schema.go**

Create `driver/sqlite/schema.go`:

```go
package sqlite

import "database/sql"

const defaultJobsTable = "jobs"
const defaultFailedTable = "failed_jobs"

func createTables(db *sql.DB, table, failedTable string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS ` + table + ` (
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
CREATE INDEX IF NOT EXISTS idx_` + table + `_queue_available ON ` + table + ` (queue, available_at);
CREATE INDEX IF NOT EXISTS idx_` + table + `_queue_reserved  ON ` + table + ` (queue, reserved_at);

CREATE TABLE IF NOT EXISTS ` + failedTable + ` (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    queue     TEXT    NOT NULL,
    type      TEXT    NOT NULL,
    payload   BLOB,
    error     TEXT    NOT NULL,
    attempts  INTEGER NOT NULL,
    failed_at INTEGER NOT NULL
);`
	_, err := db.Exec(ddl)
	return err
}
```

- [ ] **Step 5: Write sqlite.go**

Create `driver/sqlite/sqlite.go`:

```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	queue "github.com/hollis-labs/go-queue"
)

// Opts configures the SQLite driver.
type Opts struct {
	// Table is the jobs table name. Default: "jobs".
	Table string

	// FailedTable is the failed jobs table name. Default: "failed_jobs".
	FailedTable string

	// RetryAfter controls when reserved-but-incomplete jobs are
	// reclaimed. Default: 60s.
	RetryAfter time.Duration
}

// Driver is a SQLite-backed Queue implementation.
type Driver struct {
	db          *sql.DB
	table       string
	failedTable string
	retryAfter  time.Duration
}

// New creates a SQLite queue driver. Tables are auto-created if they
// don't exist. The caller owns the *sql.DB connection.
func New(db *sql.DB, opts Opts) (*Driver, error) {
	if opts.Table == "" {
		opts.Table = defaultJobsTable
	}
	if opts.FailedTable == "" {
		opts.FailedTable = defaultFailedTable
	}
	if opts.RetryAfter == 0 {
		opts.RetryAfter = 60 * time.Second
	}
	if err := createTables(db, opts.Table, opts.FailedTable); err != nil {
		return nil, fmt.Errorf("create tables: %w", err)
	}
	return &Driver{
		db:          db,
		table:       opts.Table,
		failedTable: opts.FailedTable,
		retryAfter:  opts.RetryAfter,
	}, nil
}

func (d *Driver) Push(ctx context.Context, jobType string, payload []byte, opts ...queue.PushOption) error {
	cfg := queue.ResolvePushConfig(opts)
	now := time.Now().UTC()
	availableAt := now.Add(cfg.Delay)

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO `+d.table+` (queue, type, payload, max_tries, available_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		cfg.Queue, jobType, payload, cfg.MaxTries,
		availableAt.Unix(), now.Unix(),
	)
	return err
}

func (d *Driver) Pop(ctx context.Context, queueName string) (*queue.QueuedJob, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Unix()
	stuckBefore := time.Now().UTC().Add(-d.retryAfter).Unix()

	var id int64
	var qName, jobType string
	var payload []byte
	var attempts, maxTries int
	var reservedAt sql.NullInt64
	var availableAt, createdAt int64

	err = tx.QueryRowContext(ctx,
		`SELECT id, queue, type, payload, attempts, max_tries, reserved_at, available_at, created_at
		 FROM `+d.table+`
		 WHERE queue = ?
		 AND (
		     (reserved_at IS NULL AND available_at <= ?)
		     OR (reserved_at <= ?)
		 )
		 ORDER BY id ASC
		 LIMIT 1`,
		queueName, now, stuckBefore,
	).Scan(&id, &qName, &jobType, &payload, &attempts, &maxTries, &reservedAt, &availableAt, &createdAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select job: %w", err)
	}

	// Reserve: set reserved_at and increment attempts.
	_, err = tx.ExecContext(ctx,
		`UPDATE `+d.table+` SET reserved_at = ?, attempts = attempts + 1 WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return nil, fmt.Errorf("reserve job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	rNow := time.Unix(now, 0).UTC()
	return &queue.QueuedJob{
		ID:          fmt.Sprintf("%d", id),
		Type:        jobType,
		Queue:       qName,
		Payload:     payload,
		Attempts:    attempts + 1, // reflects the increment
		MaxTries:    maxTries,
		CreatedAt:   time.Unix(createdAt, 0).UTC(),
		AvailableAt: time.Unix(availableAt, 0).UTC(),
		ReservedAt:  &rNow,
	}, nil
}

func (d *Driver) Delete(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM `+d.table+` WHERE id = ?`, id)
	return err
}

func (d *Driver) Release(ctx context.Context, id string, delay time.Duration) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read the existing row.
	var qName, jobType string
	var payload []byte
	var attempts, maxTries int
	var createdAt int64

	err = tx.QueryRowContext(ctx,
		`SELECT queue, type, payload, attempts, max_tries, created_at FROM `+d.table+` WHERE id = ?`,
		id,
	).Scan(&qName, &jobType, &payload, &attempts, &maxTries, &createdAt)
	if err == sql.ErrNoRows {
		return nil // idempotent
	}
	if err != nil {
		return fmt.Errorf("read job: %w", err)
	}

	// Delete old row.
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+d.table+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	// Insert new row with new ID, preserved attempts, new available_at.
	availableAt := time.Now().UTC().Add(delay).Unix()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO `+d.table+` (queue, type, payload, attempts, max_tries, reserved_at, available_at, created_at)
		 VALUES (?, ?, ?, ?, ?, NULL, ?, ?)`,
		qName, jobType, payload, attempts, maxTries, availableAt, createdAt,
	)
	if err != nil {
		return fmt.Errorf("re-insert: %w", err)
	}

	return tx.Commit()
}

func (d *Driver) Size(ctx context.Context, queueName string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+d.table+` WHERE queue = ?`,
		queueName,
	).Scan(&count)
	return count, err
}

func (d *Driver) Failed(ctx context.Context, job *queue.QueuedJob, errMsg string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Remove from active queue.
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+d.table+` WHERE id = ?`, job.ID); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	// Insert into failed jobs.
	now := time.Now().UTC().Unix()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO `+d.failedTable+` (queue, type, payload, error, attempts, failed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		job.Queue, job.Type, job.Payload, errMsg, job.Attempts, now,
	)
	if err != nil {
		return fmt.Errorf("insert failed: %w", err)
	}

	return tx.Commit()
}
```

- [ ] **Step 6: Run tests**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/sqlite/ -v
```

Expected: all 12 SQLite tests pass.

- [ ] **Step 7: Run all tests**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./... -v
```

Expected: all tests across all packages pass.

- [ ] **Step 8: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add .
git commit -m "feat: add SQLite driver with jobs and failed_jobs tables"
```

---

## Task 7: Worker + SQLite integration test

**Files:**
- Modify: `go-queue/worker_test.go`

- [ ] **Step 1: Write the integration test**

Append to `worker_test.go`:

```go
func TestWorkerWithSQLite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.Exec("PRAGMA journal_mode=WAL")

	q, err := qsqlite.New(db, qsqlite.Opts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var results []string
	var mu sync.Mutex

	w := queue.NewWorker(q, queue.WorkerOpts{
		PollInterval:  10 * time.Millisecond,
		MaxTries:      2,
		RetryAfter:    0,
		StopWhenEmpty: true,
	})
	w.Register("process", func(_ context.Context, job *queue.QueuedJob) error {
		mu.Lock()
		results = append(results, string(job.Payload))
		mu.Unlock()
		return nil
	})

	_ = q.Push(ctx, "process", []byte("a"))
	_ = q.Push(ctx, "process", []byte("b"))
	_ = q.Push(ctx, "process", []byte("c"))

	_ = w.Start(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(results) != 3 {
		t.Fatalf("processed %d jobs, want 3", len(results))
	}
	// FIFO ordering.
	for i, want := range []string{"a", "b", "c"} {
		if results[i] != want {
			t.Errorf("results[%d] = %q, want %q", i, results[i], want)
		}
	}
}
```

Add imports at top of `worker_test.go`:

```go
import (
	"database/sql"
	// ... existing imports ...
	qsqlite "github.com/hollis-labs/go-queue/driver/sqlite"
	_ "modernc.org/sqlite"
)
```

- [ ] **Step 2: Run the test**

```bash
cd ~/Projects-apps/libs/go-queue
go test -run TestWorkerWithSQLite -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add worker_test.go
git commit -m "test: add Worker + SQLite integration test"
```

---

## Task 8: Concurrency test

**Files:**
- Modify: `go-queue/driver/sqlite/sqlite_test.go`

- [ ] **Step 1: Write concurrent pop test**

Append to `driver/sqlite/sqlite_test.go`:

```go
func TestSQLiteConcurrentPop(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	q, _ := qsqlite.New(db, qsqlite.Opts{})

	// Push 100 jobs.
	for i := 0; i < 100; i++ {
		_ = q.Push(ctx, "concurrent", []byte(fmt.Sprintf(`{"i":%d}`, i)))
	}

	// Pop from 10 goroutines concurrently.
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]bool)

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := q.Pop(ctx, "default")
				if err != nil {
					t.Errorf("Pop: %v", err)
					return
				}
				if job == nil {
					return // queue empty
				}
				mu.Lock()
				if seen[job.ID] {
					t.Errorf("duplicate job ID: %s", job.ID)
				}
				seen[job.ID] = true
				mu.Unlock()
				_ = q.Delete(ctx, job.ID)
			}
		}()
	}
	wg.Wait()

	if len(seen) != 100 {
		t.Errorf("processed %d jobs, want 100", len(seen))
	}
}
```

Add to imports:

```go
import (
	"fmt"
	"sync"
	// ... existing imports ...
)
```

- [ ] **Step 2: Run the test**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./driver/sqlite/ -run TestSQLiteConcurrentPop -v -count=1
```

Expected: PASS — 100 unique jobs processed, no duplicates.

- [ ] **Step 3: Commit**

```bash
cd ~/Projects-apps/libs/go-queue
git add driver/sqlite/sqlite_test.go
git commit -m "test: add concurrent pop test for SQLite driver"
```

---

## Task 9: go vet and final validation

**Files:** none (validation only)

- [ ] **Step 1: Run go vet**

```bash
cd ~/Projects-apps/libs/go-queue
go vet ./...
```

Expected: no warnings.

- [ ] **Step 2: Run full test suite**

```bash
cd ~/Projects-apps/libs/go-queue
go test ./... -v -count=1
```

Expected: all tests pass.

- [ ] **Step 3: Verify module is clean**

```bash
cd ~/Projects-apps/libs/go-queue
go mod tidy
git diff go.mod go.sum
```

Expected: no unexpected changes.

- [ ] **Step 4: Commit if go mod tidy changed anything**

```bash
cd ~/Projects-apps/libs/go-queue
git add go.mod go.sum
git diff --cached --quiet || git commit -m "chore: tidy go.mod"
```

---

## Task 10: Initial commit and push

**Files:** none (git operations)

- [ ] **Step 1: Review git log**

```bash
cd ~/Projects-apps/libs/go-queue
git log --oneline
```

Expected: 7-8 clean commits from tasks 1-9.

- [ ] **Step 2: Add remote and push**

```bash
cd ~/Projects-apps/libs/go-queue
git remote add origin git@github.com:hollis-labs/go-queue.git
git push -u origin main
```

- [ ] **Step 3: Tag initial release**

```bash
cd ~/Projects-apps/libs/go-queue
git tag v0.1.0
git push origin v0.1.0
```

---

## Spec Coverage Checklist

| Spec Section | Task |
|---|---|
| D1. Module and repo | Task 1 (go mod init), Task 10 (push) |
| D2. Queue interface + Worker | Task 1 (interfaces), Task 5 (worker) |
| D3. Drivers (SQLite, memory, noop) | Tasks 2-3 (memory), Task 4 (noop), Task 6 (SQLite) |
| D4. Handler functions | Task 5 (Register + dispatch) |
| D5. In-process goroutines | Task 5 (Start with goroutines) |
| D6. Retry + failed jobs | Task 5 (retry logic), Tasks 2-3 + 6 (Failed storage) |
| D7. Lifecycle callbacks | Task 5 (OnProcessing/OnProcessed/OnFailed/OnError) |
| D8. Cortex integration | Not in scope — adapter is in Cortex repo, not go-queue |
| Package layout | All tasks follow the spec layout |
| Testing strategy | Tasks 2-3, 5-8 (memory contract, worker, SQLite, integration, concurrency) |

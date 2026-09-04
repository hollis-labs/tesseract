package memory_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// recordingQueue records every job it is handed and the context it was handed
// it on. The context matters as much as the job: the deferred embed enqueue
// happens after the transaction commits, so it must not be running on a
// context the caller can cancel.
type recordingQueue struct {
	mu   sync.Mutex
	jobs []memory.Job
	ctxs []context.Context
	err  error
}

func (q *recordingQueue) Enqueue(ctx context.Context, job memory.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, job)
	q.ctxs = append(q.ctxs, ctx)
	return q.err
}

func (q *recordingQueue) snapshot() ([]memory.Job, []context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]memory.Job(nil), q.jobs...), append([]context.Context(nil), q.ctxs...)
}

// errQueueDown is the failure a real queue reports when its backing DB is
// unreachable — the case the write path must survive without losing the
// revision and without losing the fact that the job never landed.
var errQueueDown = errors.New("queue unavailable")

func newStoreWithQueue(t *testing.T, q memory.JobQueue) *memory.Store {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return memory.NewStore(cs.DB(), nil, "", 0, q)
}

// TestWriteRevisionEnqueuesEmbedJobAfterCommit pins the behavior nothing
// asserted before: a committed revision produces exactly one embed job naming
// it. Without this, a regression to NoopQueue (or to no enqueue at all) is
// invisible to the suite.
func TestWriteRevisionEnqueuesEmbedJobAfterCommit(t *testing.T) {
	q := &recordingQueue{}
	ms := newStoreWithQueue(t, q)

	rev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	jobs, _ := q.snapshot()
	if len(jobs) != 1 {
		t.Fatalf("enqueued %d jobs, want exactly 1 embed job", len(jobs))
	}
	if jobs[0].Kind != memory.EmbedJobKind {
		t.Errorf("job kind = %q, want %q", jobs[0].Kind, memory.EmbedJobKind)
	}
	if !strings.Contains(string(jobs[0].Payload), rev.RevisionID) {
		t.Errorf("job payload %q does not name revision %q", jobs[0].Payload, rev.RevisionID)
	}
}

// TestWriteRevisionEnqueueFailureIsObservableAndWriteSurvives is the crux of
// CW-20260826-0018: the enqueue error used to be dropped on the floor with
// `_ =`, so a revision could commit durably and never be embedded with nothing
// anywhere recording it. The write must still succeed; the failure must be
// both counted and logged, with identity and no payload content.
func TestWriteRevisionEnqueueFailureIsObservableAndWriteSurvives(t *testing.T) {
	q := &recordingQueue{err: errQueueDown}
	ms := newStoreWithQueue(t, q)

	var logs strings.Builder
	ms.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	in := sampleInput("prefs.output_style")
	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("WriteRevision must not fail because the queue did: %v", err)
	}

	// Persistence is not rolled back by an enqueue failure.
	cur, err := ms.GetCurrent(context.Background(), in.Namespace, in.MemoryKey)
	if err != nil {
		t.Fatalf("revision did not survive the enqueue failure: %v", err)
	}
	if cur.RevisionID != rev.RevisionID {
		t.Errorf("current revision = %q, want %q", cur.RevisionID, rev.RevisionID)
	}

	// The failure is counted.
	if got := ms.DeferredEmbeddingStatus().EnqueueFailures; got != 1 {
		t.Errorf("EnqueueFailures = %d, want 1", got)
	}

	// The failure is logged, with enough identity to re-enqueue it.
	out := logs.String()
	if !strings.Contains(out, "embed enqueue failed") {
		t.Fatalf("enqueue failure was not logged; got %q", out)
	}
	for _, want := range []string{rev.RevisionID, in.Namespace, memory.EmbedJobKind, errQueueDown.Error()} {
		if !strings.Contains(out, want) {
			t.Errorf("log line is missing %q: %s", want, out)
		}
	}
	// And with none of the memory's contents.
	for _, forbidden := range []string{in.Payload.Summary, in.Payload.Body} {
		if strings.Contains(out, forbidden) {
			t.Errorf("log line leaked payload content %q: %s", forbidden, out)
		}
	}
}

type ctxKey string

// TestWriteRevisionEnqueueSurvivesCallerCancellation proves the enqueue runs
// on a context detached from the caller's. A cancelled HTTP request or MCP
// call between commit and enqueue used to drop the job permanently; the
// revision was durable and the embedding was gone.
func TestWriteRevisionEnqueueSurvivesCallerCancellation(t *testing.T) {
	q := &recordingQueue{}
	ms := newStoreWithQueue(t, q)

	const key ctxKey = "trace-id"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "trace-abc"))
	defer cancel()

	if _, err := ms.WriteRevision(ctx, sampleInput("prefs.output_style")); err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	_, ctxs := q.snapshot()
	if len(ctxs) != 1 {
		t.Fatalf("enqueued %d jobs, want exactly 1", len(ctxs))
	}
	enqueueCtx := ctxs[0]

	// Values (trace context, request-scoped metadata) must survive.
	if got := enqueueCtx.Value(key); got != "trace-abc" {
		t.Errorf("enqueue context lost caller values: %v", got)
	}

	// Cancellation must not. Cancelling the caller's context after the write
	// leaves the enqueue context alive; if the enqueue were still derived
	// from the caller, this is where the job would have been lost.
	cancel()
	if err := enqueueCtx.Err(); err != nil {
		t.Errorf("enqueue context died with the caller's context: %v", err)
	}
}

// TestDeferredEmbeddingStatusDistinguishesDisabledFromLive covers the second
// half of the defect: a NoopQueue store and a live-queue store used to be
// indistinguishable at runtime, so "embedding is intentionally off" and
// "embedding is silently dropping" read the same to an operator.
func TestDeferredEmbeddingStatusDistinguishesDisabledFromLive(t *testing.T) {
	t.Run("explicit NoopQueue reports disabled", func(t *testing.T) {
		st := newStoreWithQueue(t, memory.NoopQueue{}).DeferredEmbeddingStatus()
		if st.Enabled {
			t.Error("NoopQueue store reports deferred embedding enabled")
		}
		if !strings.Contains(st.Queue, "NoopQueue") {
			t.Errorf("Queue = %q, want it to name NoopQueue", st.Queue)
		}
	})

	t.Run("nil queue reports disabled", func(t *testing.T) {
		// NewStore nil-coalesces to NoopQueue; the status must expose that
		// rather than let the fallback pass for a working queue.
		st := newStoreWithQueue(t, nil).DeferredEmbeddingStatus()
		if st.Enabled {
			t.Error("nil-queue store reports deferred embedding enabled")
		}
	})

	t.Run("real queue reports enabled and counts failures", func(t *testing.T) {
		q := &recordingQueue{}
		ms := newStoreWithQueue(t, q)
		st := ms.DeferredEmbeddingStatus()
		if !st.Enabled {
			t.Error("live-queue store reports deferred embedding disabled")
		}
		if st.EnqueueFailures != 0 {
			t.Errorf("EnqueueFailures = %d on a fresh store, want 0", st.EnqueueFailures)
		}
	})
}

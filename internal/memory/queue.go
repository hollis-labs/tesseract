package memory

import (
	"context"
	"fmt"
)

// EmbedJobKind is the job kind the memory write path enqueues after a commit
// and the embed handler registers for. It is a constant rather than a literal
// on each side because a producer/consumer mismatch is silent: the job would
// be accepted, never handled, and the revision would stay unembedded with
// nothing reporting it.
const EmbedJobKind = "embed"

// Job is a unit of deferred work. Kind identifies the job handler; Payload
// is an opaque JSON-encoded blob the handler will decode.
type Job struct {
	Kind    string
	Payload []byte
}

// JobQueue is the abstract queue interface. Production wires QueueAdapter
// over go-queue (persistent, retried); NoopQueue is the explicit "deferred
// work is disabled" implementation.
type JobQueue interface {
	Enqueue(ctx context.Context, job Job) error
}

// DiscardingQueue is implemented by a JobQueue that accepts jobs and never
// runs them. A queue that says so lets Store.DeferredEmbeddingStatus report
// "embedding is intentionally off" instead of leaving callers to infer it
// from an absence of embeddings. A JobQueue that does not implement it is
// assumed to run what it accepts.
type DiscardingQueue interface {
	JobQueue

	// Discards reports whether enqueued jobs are dropped rather than run.
	Discards() bool
}

// Compile-time assertions that NoopQueue satisfies both interfaces.
var (
	_ JobQueue        = NoopQueue{}
	_ DiscardingQueue = NoopQueue{}
)

// NoopQueue accepts every job, performs no work, and never fails. It is the
// explicit "deferred embedding is off" wiring: NewStore falls back to it when
// no queue is passed, and the library facade (tesseract.Open) uses it unless
// WithQueue is given. A revision written against it commits normally and its
// embedding columns stay NULL until a backfill runs.
//
// Because a queue that drops everything on purpose is indistinguishable by
// observation from one that is dropping jobs by accident, NoopQueue reports
// Discards() == true so DeferredEmbeddingStatus can tell an operator which of
// the two they are looking at. See CW-20260826-0018.
type NoopQueue struct{}

// Enqueue accepts and discards the job. Never errors.
func (NoopQueue) Enqueue(_ context.Context, _ Job) error { return nil }

// Discards reports that jobs handed to a NoopQueue are dropped.
func (NoopQueue) Discards() bool { return true }

// DeferredEmbeddingStatus reports whether a Store's writes will actually be
// embedded in the background, and how the enqueue path has been faring.
//
// It exists because "embedding is off" and "embedding is silently broken"
// used to look identical from outside the process: NewStore nil-coalesces a
// missing queue to NoopQueue and the library facade defaults to NoopQueue, so
// a store dropping every embed job was indistinguishable at runtime from one
// wired to a live queue. CW-20260826-0018.
type DeferredEmbeddingStatus struct {
	// Enabled is false when the configured queue reports that it discards
	// the jobs it accepts. False means embeddings will never be produced by
	// the write path; it does not mean anything is broken.
	Enabled bool

	// Queue names the concrete JobQueue implementation backing the store,
	// for logs and operator diagnostics.
	Queue string

	// EnqueueFailures counts embed jobs the queue refused since the Store
	// was constructed. Non-zero means committed revisions exist that no
	// worker will ever see; POST /v1/admin/queue/backfill re-enqueues them.
	EnqueueFailures int64
}

// DeferredEmbeddingStatus returns the store's current deferred-embedding
// posture. Callers (health endpoints, startup logs, tests that must fail if
// production quietly falls back to NoopQueue) use it to distinguish a
// deliberate no-op queue from a live queue that is failing.
func (s *Store) DeferredEmbeddingStatus() DeferredEmbeddingStatus {
	st := DeferredEmbeddingStatus{
		Enabled:         true,
		Queue:           fmt.Sprintf("%T", s.queue),
		EnqueueFailures: s.enqueueFailures.Load(),
	}
	if d, ok := s.queue.(DiscardingQueue); ok && d.Discards() {
		st.Enabled = false
	}
	return st
}

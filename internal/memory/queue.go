package memory

import "context"

// Job is a unit of deferred work. Kind identifies the job handler; Payload
// is an opaque JSON-encoded blob the handler will decode.
type Job struct {
	Kind    string
	Payload []byte
}

// JobQueue is the abstract queue interface. D-core ships with NoopQueue,
// which silently accepts and drops every job. Track I will provide a real
// implementation that persists and retries.
type JobQueue interface {
	Enqueue(ctx context.Context, job Job) error
}

// Compile-time assertion that NoopQueue satisfies JobQueue.
var _ JobQueue = NoopQueue{}

// NoopQueue is the D-core stub. Accepts every job, performs no work, never
// fails. Used so the memory write path can enqueue an embedding job before
// Track I is ready — the call succeeds, the job is discarded, and the
// revision row's embedding columns remain NULL.
type NoopQueue struct{}

// Enqueue accepts and discards the job. Never errors.
func (NoopQueue) Enqueue(_ context.Context, _ Job) error { return nil }

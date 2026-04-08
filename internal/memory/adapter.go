package memory

import (
	"context"

	queue "github.com/hollis-labs/go-queue"
)

// QueueAdapter bridges the internal JobQueue interface to a go-queue
// Queue implementation.
type QueueAdapter struct {
	q         queue.Queue
	queueName string
}

// Compile-time assertion.
var _ JobQueue = (*QueueAdapter)(nil)

// NewQueueAdapter returns a QueueAdapter that pushes jobs onto the
// given queue using the specified queue name.
func NewQueueAdapter(q queue.Queue, queueName string) *QueueAdapter {
	return &QueueAdapter{q: q, queueName: queueName}
}

// Enqueue satisfies JobQueue by delegating to the underlying go-queue Push.
func (a *QueueAdapter) Enqueue(ctx context.Context, job Job) error {
	return a.q.Push(ctx, job.Kind, job.Payload, queue.OnQueue(a.queueName))
}

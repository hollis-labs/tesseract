package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestNoopQueueSwallowsJobs(t *testing.T) {
	var q memory.JobQueue = memory.NoopQueue{}
	err := q.Enqueue(context.Background(), memory.Job{Kind: "embed", Payload: []byte("{}")})
	if err != nil {
		t.Fatalf("NoopQueue.Enqueue should never error, got %v", err)
	}
	// Should also accept an empty job.
	if err := q.Enqueue(context.Background(), memory.Job{}); err != nil {
		t.Fatalf("NoopQueue.Enqueue on empty job should not error, got %v", err)
	}
}

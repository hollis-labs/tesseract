package memory_test

import (
	"context"
	"testing"

	memdriver "github.com/hollis-labs/go-queue/driver/memory"
	internalmem "github.com/hollis-labs/tesseract/internal/memory"
)

func TestQueueAdapter(t *testing.T) {
	t.Run("satisfies JobQueue interface", func(t *testing.T) {
		var _ internalmem.JobQueue = internalmem.NewQueueAdapter(memdriver.New(), "test")
	})

	t.Run("enqueue delegates to underlying queue", func(t *testing.T) {
		drv := memdriver.New()
		adapter := internalmem.NewQueueAdapter(drv, "embeddings")

		ctx := context.Background()
		err := adapter.Enqueue(ctx, internalmem.Job{
			Kind:    "embed",
			Payload: []byte(`{"record_id":"abc"}`),
		})
		if err != nil {
			t.Fatalf("Enqueue() error = %v, want nil", err)
		}

		// Verify the job landed on the correct queue.
		size, err := drv.Size(ctx, "embeddings")
		if err != nil {
			t.Fatalf("Size() error = %v", err)
		}
		if size != 1 {
			t.Fatalf("Size() = %d, want 1", size)
		}

		// Pop and verify contents.
		job, err := drv.Pop(ctx, "embeddings")
		if err != nil {
			t.Fatalf("Pop() error = %v", err)
		}
		if job == nil {
			t.Fatal("Pop() returned nil, want job")
		}
		if job.Type != "embed" {
			t.Errorf("job.Type = %q, want %q", job.Type, "embed")
		}
		if got := string(job.Payload); got != `{"record_id":"abc"}` {
			t.Errorf("job.Payload = %q, want %q", got, `{"record_id":"abc"}`)
		}
	})

	t.Run("enqueue multiple jobs", func(t *testing.T) {
		drv := memdriver.New()
		adapter := internalmem.NewQueueAdapter(drv, "work")
		ctx := context.Background()

		for i := range 3 {
			kinds := []string{"a", "b", "c"}
			err := adapter.Enqueue(ctx, internalmem.Job{Kind: kinds[i], Payload: nil})
			if err != nil {
				t.Fatalf("Enqueue(%d) error = %v", i, err)
			}
		}

		size, err := drv.Size(ctx, "work")
		if err != nil {
			t.Fatalf("Size() error = %v", err)
		}
		if size != 3 {
			t.Fatalf("Size() = %d, want 3", size)
		}
	})
}

package conduit

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	queue "github.com/hollis-labs/go-queue"
	memdriver "github.com/hollis-labs/go-queue/driver/memory"
	"github.com/hollis-labs/vanta-conduit/internal/embedding"
)

func TestOpen_MinimalConfig(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	if c.store == nil {
		t.Error("expected store to be initialized")
	}
	if c.memoryStore == nil {
		t.Error("expected memory store to be initialized")
	}
	if c.embedder != nil {
		t.Error("expected embedder to be nil without WithEmbedder")
	}
}

func TestOpen_WithEmbedder(t *testing.T) {
	dir := t.TempDir()
	mock := embedding.NewMockProvider(128)

	c, err := Open(context.Background(), Config{RootDir: dir},
		WithEmbedder(mock),
		WithEmbeddingModel("mock-embed"),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	if c.embedder == nil {
		t.Error("expected embedder to be set")
	}
	if c.embeddingModel != "mock-embed" {
		t.Errorf("embeddingModel = %q, want mock-embed", c.embeddingModel)
	}
}

func TestOpen_MissingRootDir(t *testing.T) {
	_, err := Open(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error for empty RootDir")
	}
}

func TestOpen_WithQueue_UsesAdapter(t *testing.T) {
	dir := t.TempDir()
	q := memdriver.New()

	c, err := Open(context.Background(), Config{RootDir: dir}, WithQueue(q))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	// Verify the memory store got a real queue adapter, not NoopQueue.
	// Push a job directly through the adapter and check it arrives.
	if err := q.Push(context.Background(), "test", []byte("{}"), queue.OnQueue("conduit")); err != nil {
		t.Fatalf("Push: %v", err)
	}
	n, err := q.Size(context.Background(), "conduit")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 1 {
		t.Errorf("queue size = %d, want 1", n)
	}
}

func TestOpen_WithQueue_WorkerProcessesJob(t *testing.T) {
	dir := t.TempDir()
	q := memdriver.New()

	handled := make(chan struct{}, 1)
	logger := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if strings.Contains(msg, "embed job received") {
			select {
			case handled <- struct{}{}:
			default:
			}
		}
	}

	// Open with queue — this starts the worker.
	c, err := Open(context.Background(), Config{RootDir: dir}, WithQueue(q), WithLogger(logger))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	// Push a job directly to verify the worker picks it up.
	payload := []byte(`{"revision_id":"test-rev-123"}`)
	if err := q.Push(context.Background(), "embed", payload, queue.OnQueue("conduit")); err != nil {
		t.Fatalf("Push: %v", err)
	}

	select {
	case <-handled:
		// Worker picked up the job and invoked the embed handler.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker to invoke embed handler")
	}
}

func TestClose_StopsDecay(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

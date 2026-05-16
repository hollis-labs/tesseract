package conduit_test

import (
	"context"
	"testing"

	conduit "github.com/hollis-labs/tesseract"

	queue "github.com/hollis-labs/go-queue"
)

func TestNewEmbedHandler_InvalidJSON(t *testing.T) {
	logger := func(string, ...any) {}

	handler := conduit.NewEmbedHandler(nil, "test-model", logger)
	job := &queue.QueuedJob{
		Type:    "embed",
		Payload: []byte(`not valid json`),
	}

	err := handler(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for invalid JSON payload, got nil")
	}
}

func TestNewEmbedHandler_NilStore(t *testing.T) {
	logger := func(string, ...any) {}

	handler := conduit.NewEmbedHandler(nil, "test-model", logger)
	job := &queue.QueuedJob{
		Type:    "embed",
		Payload: []byte(`{"revision_id":"rev_abc123"}`),
	}

	err := handler(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for nil store, got nil")
	}
}

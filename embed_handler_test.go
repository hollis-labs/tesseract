package conduit_test

import (
	"context"
	"fmt"
	"testing"

	conduit "github.com/hollis-labs/vanta-conduit"

	queue "github.com/hollis-labs/go-queue"
)

func TestNewEmbedHandler_ValidPayload(t *testing.T) {
	var logged string
	logger := func(format string, args ...any) {
		logged = fmt.Sprintf(format, args...)
	}

	handler := conduit.NewEmbedHandler(logger)
	job := &queue.QueuedJob{
		Type:    "embed",
		Payload: []byte(`{"revision_id":"rev_abc123"}`),
	}

	err := handler(context.Background(), job)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if logged != "embed job received: revision_id=rev_abc123" {
		t.Fatalf("unexpected log output: %q", logged)
	}
}

func TestNewEmbedHandler_InvalidJSON(t *testing.T) {
	logger := func(string, ...any) {}

	handler := conduit.NewEmbedHandler(logger)
	job := &queue.QueuedJob{
		Type:    "embed",
		Payload: []byte(`not valid json`),
	}

	err := handler(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for invalid JSON payload, got nil")
	}
}

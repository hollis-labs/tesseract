package cortex

import (
	"context"
	"testing"

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

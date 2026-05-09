package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

// mockEmbedder implements embedcontracts.Embedder with a fixed vector.
type mockEmbedder struct {
	vector []float32
}

func (m *mockEmbedder) Embed(_ context.Context, _ string, _ string) (*embedcontracts.EmbeddingResult, error) {
	return &embedcontracts.EmbeddingResult{
		Embedding:  m.vector,
		TokenCount: 3,
	}, nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	results := make([]embedcontracts.EmbeddingResult, len(texts))
	for i := range texts {
		results[i] = embedcontracts.EmbeddingResult{Embedding: m.vector, TokenCount: 3}
	}
	return results, nil
}

func (m *mockEmbedder) EmbeddingDimensions(_ string) int {
	return len(m.vector)
}

func newTestStoreWithEmbedder(t *testing.T) (*memory.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	embedder := &mockEmbedder{vector: []float32{0.1, 0.2, 0.3}}
	ms := memory.NewStore(cs.DB(), embedder, "test-model", 0.85, memory.NoopQueue{})
	cleanup := func() { _ = cs.Close() }
	return ms, cleanup
}

func newTestStoreNoEmbedder(t *testing.T) (*memory.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	cleanup := func() { _ = cs.Close() }
	return ms, cleanup
}

func TestEmbedRevision_Success(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	// Write a revision to embed.
	rev, err := ms.WriteRevision(context.Background(), sampleInput("embed.test"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	// Embed it.
	err = ms.EmbedRevision(context.Background(), rev.RevisionID, "test-model")
	if err != nil {
		t.Fatalf("EmbedRevision: %v", err)
	}

	// Re-read and verify.
	got, err := ms.GetRevisionByID(context.Background(), rev.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if got.EmbeddingModel != "test-model" {
		t.Fatalf("expected embedding_model=test-model, got %q", got.EmbeddingModel)
	}
	want := []float32{0.1, 0.2, 0.3}
	if len(got.EmbeddingVector) != len(want) {
		t.Fatalf("expected %d-dim vector, got %d", len(want), len(got.EmbeddingVector))
	}
	for i, v := range want {
		if got.EmbeddingVector[i] != v {
			t.Fatalf("vector[%d]: expected %f, got %f", i, v, got.EmbeddingVector[i])
		}
	}
}

func TestEmbedRevision_NoEmbedder(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()

	err := ms.EmbedRevision(context.Background(), "nonexistent", "test-model")
	if !errors.Is(err, memory.ErrEmbedderUnavailable) {
		t.Fatalf("expected ErrEmbedderUnavailable, got %v", err)
	}
}

func TestEmbedRevision_EmptyPayload(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	// WriteRevision requires summary, so write normally then clear via DB.
	rev, err := ms.WriteRevision(context.Background(), sampleInput("embed.empty"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	// Clear payload fields directly so embed sees empty text.
	_, err = ms.DB().ExecContext(context.Background(),
		`UPDATE memory_revisions SET payload_summary = '', payload_body = '' WHERE revision_id = ?`,
		rev.RevisionID)
	if err != nil {
		t.Fatalf("clear payload: %v", err)
	}

	err = ms.EmbedRevision(context.Background(), rev.RevisionID, "test-model")
	if err == nil {
		t.Fatal("expected error for empty payload, got nil")
	}
	if !strings.Contains(err.Error(), "no embeddable text") {
		t.Fatalf("expected 'no embeddable text' error, got: %v", err)
	}
}

func TestEmbedRevision_NotFound(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	err := ms.EmbedRevision(context.Background(), "nonexistent-revision-id", "test-model")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

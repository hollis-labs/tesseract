package tesseract

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/embedding"
)

func openTestTesseract(t *testing.T, opts ...Option) *Tesseract {
	t.Helper()
	dir := t.TempDir()
	c, err := Open(context.Background(), Config{RootDir: dir}, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func writeTestRecord(t *testing.T, c *Tesseract, ns, key, payload string) contextstore.Record {
	t.Helper()
	rec, err := c.store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: ns,
		Key:       key,
		Actor:     "test",
		Payload:   []byte(payload),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return rec
}

func TestEmbed_NoProvider(t *testing.T) {
	c := openTestTesseract(t)
	rec := writeTestRecord(t, c, "user/test", "doc", `{"content":"hello"}`)
	err := c.Embed(context.Background(), rec.RecordID)
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Errorf("expected ErrEmbedderUnavailable, got %v", err)
	}
}

func TestEmbed_Success(t *testing.T) {
	mock := embedding.NewMockProvider(128)
	c := openTestTesseract(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
	rec := writeTestRecord(t, c, "user/test", "doc", `{"content":"hello world"}`)
	err := c.Embed(context.Background(), rec.RecordID)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	embeddings, _, err := c.store.ListEmbeddings(context.Background(), contextstore.EmbeddingFilter{Model: "mock-embed"})
	if err != nil {
		t.Fatalf("ListEmbeddings: %v", err)
	}
	if len(embeddings) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(embeddings))
	}
	if embeddings[0].RecordID != rec.RecordID {
		t.Errorf("embedding record_id = %q, want %q", embeddings[0].RecordID, rec.RecordID)
	}
}

func TestEmbed_Idempotent(t *testing.T) {
	mock := embedding.NewMockProvider(128)
	c := openTestTesseract(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
	rec := writeTestRecord(t, c, "user/test", "doc", `{"content":"idempotent"}`)
	for i := 0; i < 3; i++ {
		if err := c.Embed(context.Background(), rec.RecordID); err != nil {
			t.Fatalf("Embed attempt %d: %v", i, err)
		}
	}
	embeddings, _, err := c.store.ListEmbeddings(context.Background(), contextstore.EmbeddingFilter{Model: "mock-embed"})
	if err != nil {
		t.Fatalf("ListEmbeddings: %v", err)
	}
	if len(embeddings) != 1 {
		t.Errorf("expected 1 embedding after 3 upserts, got %d", len(embeddings))
	}
}

func TestEmbed_RecordNotFound(t *testing.T) {
	mock := embedding.NewMockProvider(128)
	c := openTestTesseract(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
	err := c.Embed(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent record")
	}
}

func TestSearch_NoProvider(t *testing.T) {
	c := openTestTesseract(t)
	_, err := c.Search(context.Background(), "test query", SearchOptions{})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Errorf("expected ErrEmbedderUnavailable, got %v", err)
	}
}

func TestSearch_EmptyResults(t *testing.T) {
	mock := embedding.NewMockProvider(128)
	c := openTestTesseract(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
	results, err := c.Search(context.Background(), "test query", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_FindsEmbeddedRecords(t *testing.T) {
	mock := embedding.NewMockProvider(128)
	c := openTestTesseract(t, WithEmbedder(mock), WithEmbeddingModel("mock-embed"))
	rec1 := writeTestRecord(t, c, "app/notes", "alpha", `{"content":"machine learning neural networks"}`)
	rec2 := writeTestRecord(t, c, "app/notes", "beta", `{"content":"database schema migration"}`)
	if err := c.Embed(context.Background(), rec1.RecordID); err != nil {
		t.Fatalf("Embed alpha: %v", err)
	}
	if err := c.Embed(context.Background(), rec2.RecordID); err != nil {
		t.Fatalf("Embed beta: %v", err)
	}
	results, err := c.Search(context.Background(), "neural networks", SearchOptions{Limit: 10, Threshold: -1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got 0")
	}
	for _, r := range results {
		if r.RecordID == "" {
			t.Error("result has empty RecordID")
		}
	}
}

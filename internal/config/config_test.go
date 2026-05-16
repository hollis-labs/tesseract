package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
)

func TestLoad_FullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
embedding:
  provider: openai
  model: text-embedding-3-large

dedup:
  similarity_threshold: 0.90
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Embedding.Provider != "openai" {
		t.Errorf("provider: got %q, want openai", cfg.Embedding.Provider)
	}
	if cfg.Embedding.Model != "text-embedding-3-large" {
		t.Errorf("model: got %q, want text-embedding-3-large", cfg.Embedding.Model)
	}
	if cfg.Dedup.SimilarityThreshold != 0.90 {
		t.Errorf("threshold: got %f, want 0.90", cfg.Dedup.SimilarityThreshold)
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Embedding.Model != "text-embedding-3-large" {
		t.Errorf("default model: got %q, want text-embedding-3-large", cfg.Embedding.Model)
	}
	if cfg.Dedup.SimilarityThreshold != 0.85 {
		t.Errorf("default threshold: got %f, want 0.85", cfg.Dedup.SimilarityThreshold)
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
dedup:
  similarity_threshold: 0.70
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Embedding.Model != "text-embedding-3-large" {
		t.Errorf("model: got %q, want default", cfg.Embedding.Model)
	}
	if cfg.Dedup.SimilarityThreshold != 0.70 {
		t.Errorf("threshold: got %f, want 0.70", cfg.Dedup.SimilarityThreshold)
	}
}

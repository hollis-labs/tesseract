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

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	want := config.Config{
		Embedding: config.EmbeddingConfig{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
		Dedup: config.DedupConfig{
			SimilarityThreshold: 0.92,
		},
		Synthesis: config.SynthesisConfig{
			Provider:     "anthropic",
			Model:        "claude-sonnet-4-5",
			MaxTokens:    2048,
			Temperature:  0.3,
			SystemPrompt: "Answer with citations.",
		},
	}

	if err := config.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Embedding.Provider != want.Embedding.Provider || got.Embedding.Model != want.Embedding.Model {
		t.Fatalf("embedding mismatch: got %+v want %+v", got.Embedding, want.Embedding)
	}
	if got.Dedup.SimilarityThreshold != want.Dedup.SimilarityThreshold {
		t.Fatalf("dedup mismatch: got %+v want %+v", got.Dedup, want.Dedup)
	}
	if got.Synthesis.Provider != want.Synthesis.Provider ||
		got.Synthesis.Model != want.Synthesis.Model ||
		got.Synthesis.MaxTokens != want.Synthesis.MaxTokens ||
		got.Synthesis.Temperature != want.Synthesis.Temperature ||
		got.Synthesis.SystemPrompt != want.Synthesis.SystemPrompt {
		t.Fatalf("synthesis mismatch: got %+v want %+v", got.Synthesis, want.Synthesis)
	}
}

// ── read.payload_mode (CW-20260825-0003) ─────────────────────────────────

func TestReadPayloadMode_DefaultAndNormalize(t *testing.T) {
	if got := config.Defaults().Read.PayloadMode; got != "summary" {
		t.Errorf("Defaults().Read.PayloadMode = %q, want summary", got)
	}

	for _, tc := range []struct {
		in   string
		want string
	}{
		{"keys", "keys"},
		{"summary", "summary"},
		{"full", "full"},
		{"", "summary"},      // unset falls back
		{"brief", "summary"}, // a typo falls back rather than taking the service down
		{"FULL", "summary"},  // the vocabulary is case-sensitive
	} {
		cfg := config.Defaults()
		cfg.Read.PayloadMode = tc.in
		if got := config.Normalize(cfg).Read.PayloadMode; got != tc.want {
			t.Errorf("Normalize(read.payload_mode=%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A configured payload_mode must survive a Save/Load round trip — the knob
// is useless if the file it lives in cannot hold it.
func TestReadPayloadMode_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Defaults()
	cfg.Read.PayloadMode = "full"

	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Read.PayloadMode != "full" {
		t.Errorf("Read.PayloadMode = %q after round trip, want full", got.Read.PayloadMode)
	}
}

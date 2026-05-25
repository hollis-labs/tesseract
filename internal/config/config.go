package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level tesseract configuration loaded from config.yaml.
type Config struct {
	Embedding EmbeddingConfig `yaml:"embedding"`
	Dedup     DedupConfig     `yaml:"dedup"`
	Synthesis SynthesisConfig `yaml:"synthesis"`
}

// SynthesisConfig configures the LLM-backed answer synthesis path
// (POST /v1/synthesis/ask). When Provider is empty the synthesis route
// returns 503 service_unavailable. Provider credential is read by the
// go-providers adapter from its conventional env var (ANTHROPIC_API_KEY,
// OPENAI_API_KEY, etc).
type SynthesisConfig struct {
	Provider     string  `yaml:"provider"`
	Model        string  `yaml:"model"`
	SystemPrompt string  `yaml:"system_prompt,omitempty"`
	MaxTokens    int     `yaml:"max_tokens,omitempty"`
	Temperature  float64 `yaml:"temperature,omitempty"`
}

// EmbeddingConfig configures the embedding provider and model.
type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// DedupConfig configures semantic dedup behavior.
type DedupConfig struct {
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
}

// Defaults returns a Config with sensible defaults.
func Defaults() Config {
	return Config{
		Embedding: EmbeddingConfig{
			Provider: "openai",
			Model:    "text-embedding-3-large",
		},
		Dedup: DedupConfig{
			SimilarityThreshold: 0.85,
		},
	}
}

// Load reads a config file from path. If the file does not exist, returns
// defaults. Partial configs are merged over defaults.
func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return Normalize(cfg), nil
}

// Normalize reapplies defaults for zero values that should not remain zero in
// runtime/admin representations.
func Normalize(cfg Config) Config {
	defaults := Defaults()
	if cfg.Embedding.Provider == "" {
		cfg.Embedding.Provider = defaults.Embedding.Provider
	}
	if cfg.Embedding.Model == "" {
		cfg.Embedding.Model = defaults.Embedding.Model
	}
	if cfg.Dedup.SimilarityThreshold == 0 {
		cfg.Dedup.SimilarityThreshold = defaults.Dedup.SimilarityThreshold
	}
	if cfg.Synthesis.Provider != "" {
		if cfg.Synthesis.MaxTokens == 0 {
			cfg.Synthesis.MaxTokens = 1024
		}
		if cfg.Synthesis.SystemPrompt == "" {
			cfg.Synthesis.SystemPrompt = DefaultSynthesisSystemPrompt
		}
	}
	return cfg
}

// Save writes cfg to path as YAML, creating the parent directory when needed.
func Save(path string, cfg Config) error {
	cfg = Normalize(cfg)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// DefaultSynthesisSystemPrompt is used when Synthesis.SystemPrompt is empty.
// It instructs the model to answer using ONLY the supplied source revisions
// and to cite by [n] markers that the frontend can resolve back to the
// numbered Sources list.
const DefaultSynthesisSystemPrompt = `You are an answer-synthesis assistant for a personal memory + knowledge store.

You will be given a question and a numbered list of source revisions. Each source has a namespace, key, summary, and (optionally) body.

Rules:
- Answer ONLY using the supplied sources. Do not invent facts.
- Cite each claim with [n] where n is the source number.
- If the sources don't answer the question, say so directly — do not pad.
- Keep the answer focused and scannable: short paragraphs or a tight list.`

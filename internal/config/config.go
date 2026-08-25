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
	Read      ReadConfig      `yaml:"read"`
}

// ReadConfig configures how much of each record the recall/lookup read paths
// return by default. Deployments differ: an agent-facing install wants the
// condensed projection to protect its context budget, while a UI-facing or
// archival install may want everything. Hence config, not a hardcoded answer.
//
// PayloadMode is one of keys|summary|full and is overridable per call.
// Kept as a plain string (like Embedding.Provider) so this package stays
// dependency-free; memory.PayloadMode is the typed vocabulary, and
// memory.DefaultPayloadMode is the canonical default this must match.
//
// BudgetBytes / BudgetTokens are the deployment-level response ceilings for
// recall and lookup, overridable per call by the arguments of the same name.
// Both default to 0, meaning no ceiling.
//
// Zero is the deliberate default rather than a chosen number. A non-zero
// default would start truncating every existing recall on the next deploy,
// silently changing what every already-deployed agent receives — which is the
// class of change this repo binds with tests elsewhere rather than ships as a
// side effect. The mechanism ships here; turning it on is a deployment
// decision with a visible config line behind it. A caller that wants a
// bounded read without touching config passes the per-call argument.
type ReadConfig struct {
	PayloadMode  string `yaml:"payload_mode"`
	BudgetBytes  int    `yaml:"budget_bytes"`
	BudgetTokens int    `yaml:"budget_tokens"`
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
		Read: ReadConfig{
			PayloadMode: "summary",
		},
	}
}

// validPayloadModes is the closed vocabulary accepted for Read.PayloadMode.
// Mirrors memory.PayloadMode; duplicated rather than imported so that this
// package keeps no dependency beyond the stdlib and yaml.
var validPayloadModes = map[string]bool{"keys": true, "summary": true, "full": true}

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
	// An unset or unrecognized payload_mode falls back to the default rather
	// than failing the load: a typo in config.yaml must not take the service
	// down, and serving "full" by surprise would silently defeat the budget
	// this knob exists to protect. A bad per-call argument, by contrast, is a
	// validation_error — the caller is present and can be told.
	if !validPayloadModes[cfg.Read.PayloadMode] {
		cfg.Read.PayloadMode = defaults.Read.PayloadMode
	}
	// A negative budget is meaningless and a zero one can only produce an
	// empty page. Both normalize to "no ceiling" for the same reason a bad
	// payload_mode falls back rather than failing the load: a typo in
	// config.yaml must not take the service down. A bad per-call budget, by
	// contrast, is a validation_error — the caller is present and can be told.
	if cfg.Read.BudgetBytes < 0 {
		cfg.Read.BudgetBytes = 0
	}
	if cfg.Read.BudgetTokens < 0 {
		cfg.Read.BudgetTokens = 0
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

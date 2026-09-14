package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/hollis-labs/tesseract/internal/fsperm"
)

var ErrWorkspaceRetentionConfig = errors.New("invalid workspace retention configuration")

// Config holds the top-level tesseract configuration loaded from config.yaml.
type Config struct {
	Embedding EmbeddingConfig `yaml:"embedding"`
	Dedup     DedupConfig     `yaml:"dedup"`
	Synthesis SynthesisConfig `yaml:"synthesis"`
	Read      ReadConfig      `yaml:"read"`
	Workspace WorkspaceConfig `yaml:"workspace"`
}

type WorkspaceConfig struct {
	Retention WorkspaceRetentionConfig `yaml:"retention"`
}

func (c *WorkspaceConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: workspace must be an object", ErrWorkspaceRetentionConfig)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != "retention" {
			return fmt.Errorf("%w: unknown workspace setting %q", ErrWorkspaceRetentionConfig, node.Content[i].Value)
		}
	}
	type plain WorkspaceConfig
	if err := node.Decode((*plain)(c)); err != nil {
		return fmt.Errorf("%w: %w", ErrWorkspaceRetentionConfig, err)
	}
	return nil
}

// WorkspaceRetentionConfig controls operator and daemon purge behavior.
// PurgeEnabled is an explicit opt-in; AutomaticInterval separately selects a
// daemon loop and is valid only with purge enabled. Both default off.
type WorkspaceRetentionConfig struct {
	PurgeEnabled      bool   `yaml:"purge_enabled"`
	MinimumIdle       string `yaml:"minimum_idle"`
	AutomaticInterval string `yaml:"automatic_interval,omitempty"`
	BatchSize         int    `yaml:"batch_size"`
}

type ParsedWorkspaceRetention struct {
	PurgeEnabled      bool
	MinimumIdle       time.Duration
	AutomaticInterval time.Duration
	BatchSize         int
}

const minimumWorkspaceRetentionIdle = 30 * 24 * time.Hour

func (c *WorkspaceRetentionConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: workspace.retention must be an object", ErrWorkspaceRetentionConfig)
	}
	allowed := map[string]bool{"purge_enabled": true, "minimum_idle": true, "automatic_interval": true, "batch_size": true}
	for i := 0; i < len(node.Content); i += 2 {
		if !allowed[node.Content[i].Value] {
			return fmt.Errorf("%w: unknown workspace.retention setting %q", ErrWorkspaceRetentionConfig, node.Content[i].Value)
		}
	}
	type plain WorkspaceRetentionConfig
	if err := node.Decode((*plain)(c)); err != nil {
		return fmt.Errorf("%w: %w", ErrWorkspaceRetentionConfig, err)
	}
	return nil
}

func ParseWorkspaceRetention(c WorkspaceRetentionConfig) (ParsedWorkspaceRetention, error) {
	minimumIdle, err := time.ParseDuration(strings.TrimSpace(c.MinimumIdle))
	if err != nil {
		return ParsedWorkspaceRetention{}, fmt.Errorf("%w: workspace.retention.minimum_idle: %w", ErrWorkspaceRetentionConfig, err)
	}
	if minimumIdle < minimumWorkspaceRetentionIdle {
		return ParsedWorkspaceRetention{}, fmt.Errorf("%w: workspace.retention.minimum_idle must be at least %s", ErrWorkspaceRetentionConfig, minimumWorkspaceRetentionIdle)
	}
	var automaticInterval time.Duration
	if strings.TrimSpace(c.AutomaticInterval) != "" {
		automaticInterval, err = time.ParseDuration(c.AutomaticInterval)
		if err != nil || automaticInterval <= 0 {
			return ParsedWorkspaceRetention{}, fmt.Errorf("%w: workspace.retention.automatic_interval must be a positive duration", ErrWorkspaceRetentionConfig)
		}
	}
	if automaticInterval > 0 && !c.PurgeEnabled {
		return ParsedWorkspaceRetention{}, fmt.Errorf("%w: workspace.retention.automatic_interval requires purge_enabled: true", ErrWorkspaceRetentionConfig)
	}
	if c.BatchSize < 1 || c.BatchSize > 1000 {
		return ParsedWorkspaceRetention{}, fmt.Errorf("%w: workspace.retention.batch_size must be between 1 and 1000", ErrWorkspaceRetentionConfig)
	}
	return ParsedWorkspaceRetention{c.PurgeEnabled, minimumIdle, automaticInterval, c.BatchSize}, nil
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
// recall and lookup ONLY, overridable per call by the arguments of the same
// name. Both default to 0, meaning no ceiling.
//
// They deliberately do not reach tesseract_history. It
// answers with a bare array under the memory and knowledge domains unless the
// caller passes a paging knob, and a bare
// array has nowhere to report truncation — so a configured ceiling there could
// only either flip the response shape for every caller (breaking the shipped
// web UI, which parses both routes as arrays) or silently drop revisions. A
// per-call budget on a history read is still honored, and brings the envelope
// that reports it.
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
		Workspace: WorkspaceConfig{Retention: WorkspaceRetentionConfig{
			MinimumIdle: "720h", BatchSize: 100,
		}},
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
	cfg = Normalize(cfg)
	if _, err := ParseWorkspaceRetention(cfg.Workspace.Retention); err != nil {
		return cfg, err
	}
	return cfg, nil
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
	if cfg.Workspace.Retention.MinimumIdle == "" {
		cfg.Workspace.Retention.MinimumIdle = defaults.Workspace.Retention.MinimumIdle
	}
	if cfg.Workspace.Retention.BatchSize == 0 {
		cfg.Workspace.Retention.BatchSize = defaults.Workspace.Retention.BatchSize
	}
	return cfg
}

// Save writes cfg to path as YAML, creating the parent directory when needed.
//
// config.yaml carries provider credentials, so both it and the directory it
// lives in are owner-only. fsperm re-applies the mode after the write because
// O_CREATE's mode is ignored for a file that already exists: a config saved by
// a build older than CW-20260904-0078 would otherwise keep its 0644.
func Save(path string, cfg Config) error {
	cfg = Normalize(cfg)
	if _, err := ParseWorkspaceRetention(cfg.Workspace.Retention); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := fsperm.EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return fsperm.WriteFile(path, data)
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

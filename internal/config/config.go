package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level conduit configuration loaded from config.yaml.
type Config struct {
	Embedding EmbeddingConfig `yaml:"embedding"`
	Dedup     DedupConfig     `yaml:"dedup"`
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

	// Re-apply defaults for zero values that shouldn't be zero.
	if cfg.Embedding.Model == "" {
		cfg.Embedding.Model = Defaults().Embedding.Model
	}
	if cfg.Dedup.SimilarityThreshold == 0 {
		cfg.Dedup.SimilarityThreshold = Defaults().Dedup.SimilarityThreshold
	}

	return cfg, nil
}

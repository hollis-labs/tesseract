package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
)

// Compile-time assertion: MockProvider must satisfy embedcontracts.Embedder.
var _ embedcontracts.Embedder = (*MockProvider)(nil)

// MockProvider generates deterministic embeddings for testing.
// The same text always produces the same vector, enabling reproducible tests.
type MockProvider struct {
	dimensions int
}

// NewMockProvider creates a mock that generates deterministic vectors.
func NewMockProvider(dimensions int) *MockProvider {
	return &MockProvider{dimensions: dimensions}
}

// deterministicVector generates a normalized unit vector from text using a sha256 hash.
func deterministicVector(text string, dimensions int) []float32 {
	hash := sha256.Sum256([]byte(text))
	vec := make([]float32, dimensions)
	for i := range vec {
		// Use hash bytes cyclically to seed deterministic floats.
		idx := (i * 4) % len(hash)
		bits := binary.LittleEndian.Uint32(hash[idx : idx+4])
		// Map to [-1, 1] range.
		vec[i] = float32(bits)/float32(math.MaxUint32)*2 - 1
	}
	// Normalize to unit vector for cosine similarity.
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

// Embed generates a deterministic embedding vector for the given text.
// The model parameter is accepted for interface compatibility but ignored.
func (p *MockProvider) Embed(_ context.Context, text string, _ string) (*embedcontracts.EmbeddingResult, error) {
	return &embedcontracts.EmbeddingResult{
		Embedding:  deterministicVector(text, p.dimensions),
		TokenCount: len(text),
	}, nil
}

// EmbedBatch generates deterministic embedding vectors for multiple texts.
// The model parameter is accepted for interface compatibility but ignored.
func (p *MockProvider) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	results := make([]embedcontracts.EmbeddingResult, len(texts))
	for i, text := range texts {
		r, err := p.Embed(ctx, text, model)
		if err != nil {
			return nil, err
		}
		results[i] = *r
	}
	return results, nil
}

// EmbeddingDimensions returns the configured output dimensions.
// The model parameter is accepted for interface compatibility but ignored.
func (p *MockProvider) EmbeddingDimensions(_ string) int {
	return p.dimensions
}

package conduit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/embedding"
)

// ErrEmbedderUnavailable is returned when an embedding operation is attempted
// but no embedder has been configured on the Cortex instance.
var ErrEmbedderUnavailable = errors.New("embedder unavailable")

// SearchOptions controls the behavior of a Search call.
type SearchOptions struct {
	Limit      int
	Threshold  float64
	Namespaces []string
	Types      []string
}

// Embed generates and stores a vector embedding for the record identified by
// recordID. It fetches the record from the store, extracts embeddable text,
// calls the configured embedder, and upserts the resulting vector.
func (c *Cortex) Embed(ctx context.Context, recordID string) error {
	if c.embedder == nil {
		return ErrEmbedderUnavailable
	}

	rec, err := c.store.GetByRecordID(ctx, recordID)
	if err != nil {
		return fmt.Errorf("cortex: failed to load record %s: %w", recordID, err)
	}

	text := extractTextForEmbedding(rec)
	if text == "" {
		return fmt.Errorf("cortex: record %s has no embeddable text content", recordID)
	}

	result, err := c.embedder.Embed(ctx, text, c.embeddingModel)
	if err != nil {
		return fmt.Errorf("cortex: embedding failed: %w", err)
	}

	return c.store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
		RecordID:   recordID,
		Model:      c.embeddingModel,
		Dimensions: len(result.Embedding),
		Vector:     result.Embedding,
	})
}

// Search embeds the query string and returns the top matching records ranked by
// cosine similarity against all stored embeddings for the configured model.
func (c *Cortex) Search(ctx context.Context, query string, opts SearchOptions) ([]embedding.SearchResult, error) {
	if c.embedder == nil {
		return nil, ErrEmbedderUnavailable
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	queryResult, err := c.embedder.Embed(ctx, query, c.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("cortex: query embedding failed: %w", err)
	}

	filter := contextstore.EmbeddingFilter{
		Model: c.embeddingModel,
	}
	if len(opts.Namespaces) > 0 {
		filter.Namespaces = opts.Namespaces
	}
	if len(opts.Types) > 0 {
		filter.Types = opts.Types
	}

	embeddings, records, err := c.store.ListEmbeddings(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("cortex: failed to load embeddings: %w", err)
	}

	if len(embeddings) == 0 {
		return nil, nil
	}

	vectors := make([][]float32, len(embeddings))
	recordIDs := make([]string, len(embeddings))
	namespaces := make([]string, len(embeddings))
	keys := make([]string, len(embeddings))
	for i, e := range embeddings {
		vectors[i] = e.Vector
		recordIDs[i] = e.RecordID
		namespaces[i] = records[i].Namespace
		keys[i] = records[i].Key
	}

	return embedding.RankByCosineSimilarity(queryResult.Embedding, vectors, recordIDs, namespaces, keys, limit, opts.Threshold), nil
}

// extractTextForEmbedding pulls embeddable text from a record's JSON payload.
// It tries common prose fields first; if none are found it falls back to the
// raw payload bytes.
func extractTextForEmbedding(rec contextstore.Record) string {
	if rec.Payload == nil {
		return ""
	}

	var obj map[string]any
	if err := json.Unmarshal(rec.Payload, &obj); err == nil {
		var parts []string
		for _, field := range []string{"title", "summary", "description", "content", "body", "text", "message"} {
			if v, ok := obj[field]; ok {
				if s, ok := v.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}

	return string(rec.Payload)
}

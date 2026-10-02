package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/embedding"
)

func (a *Adapter) registerEmbeddingTools(s *toolRegistrar) {
	a.addTool(s, gomcpTool("context_embed",
		"Generate and store an embedding for a record. Requires a configured embedding provider. Idempotent: re-embedding overwrites the previous vector. See `tesseract_skills start-here` for the primitive model.",
		inputSchema(
			strProp("record_id", "Record ID to embed", true),
			strProp("namespace", "Record namespace", true),
			strProp("key", "Record key", true),
			strProp("model", "Embedding model (default: provider's configured model)", false),
		),
		toolAnnotations{OpenWorldHint: true},
		a.handleEmbed,
	))

	a.addTool(s, gomcpTool("context_search",
		"Semantic search across records using embeddings. Returns ranked results by cosine similarity. Requires a configured embedding provider. See `tesseract_skills start-here` for the primitive model.",
		inputSchema(
			strProp("query", "Search query text", true),
			numProp("limit", "Max results to return (default 10, max 25)", false),
			strProp("namespace", "Namespace prefix filter", false),
			strProp("type", "Record type filter", false),
			strProp("tags", "Comma-separated tag filter (any match)", false),
			numProp("threshold", "Minimum similarity score (default 0.7)", false),
		),
		toolAnnotations{ReadOnlyHint: true, OpenWorldHint: true},
		a.handleSearch,
	))
}

func (a *Adapter) handleEmbed(ctx context.Context, req map[string]any) (any, error) {
	if a.EmbeddingProvider == nil {
		return toolError(codeEmbeddingUnavailable, "no embedding provider configured"), nil
	}

	recordID := argString(req, "record_id", "")
	ns := argString(req, "namespace", "")
	key := argString(req, "key", "")
	if recordID == "" || ns == "" || key == "" {
		return toolError(codeValidationError, "record_id, namespace, and key are required"), nil
	}

	// Fetch the record to get its payload text.
	rec, err := a.Store.GetByRecordID(ctx, recordID)
	if err != nil {
		return toolError(codeNotFound, fmt.Sprintf("record %s not found: %v", recordID, err)), nil
	}

	// Extract text from the payload for embedding.
	text := extractTextForEmbedding(rec)
	if text == "" {
		return toolError(codeValidationError, "record has no embeddable text content"), nil
	}

	// Generate embedding.
	model := argString(req, "model", a.EmbeddingModel)
	result, err := a.EmbeddingProvider.Embed(ctx, text, model)
	if err != nil {
		return toolError(codeEmbeddingError, fmt.Sprintf("embedding generation failed: %v", err)), nil
	}

	// Store the embedding.
	if err := a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
		RecordID:   recordID,
		Model:      model,
		Dimensions: len(result.Embedding),
		Vector:     result.Embedding,
	}); err != nil {
		return toolError(codeInternalError, fmt.Sprintf("failed to store embedding: %v", err)), nil
	}

	return toolJSON(map[string]any{
		"record_id":  recordID,
		"namespace":  ns,
		"key":        key,
		"model":      model,
		"dimensions": len(result.Embedding),
		"status":     "stored",
	}), nil
}

func (a *Adapter) handleSearch(ctx context.Context, req map[string]any) (any, error) {
	if a.EmbeddingProvider == nil {
		return toolError(codeEmbeddingUnavailable, "no embedding provider configured"), nil
	}

	query := argString(req, "query", "")
	if query == "" {
		return toolError(codeValidationError, "query is required"), nil
	}

	limit := int(argFloat(req, "limit", 10))
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}

	threshold := argFloat(req, "threshold", 0.7)

	// Build filter.
	filter := contextstore.EmbeddingFilter{
		Model: a.EmbeddingModel,
	}
	if ns := argString(req, "namespace", ""); ns != "" {
		filter.Namespaces = []string{ns}
	}
	if t := argString(req, "type", ""); t != "" {
		filter.Types = []string{t}
	}
	if tags := argString(req, "tags", ""); tags != "" {
		for _, tag := range strings.Split(tags, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				filter.Tags = append(filter.Tags, tag)
			}
		}
	}

	// Load candidate embeddings.
	embeddings, records, err := a.Store.ListEmbeddings(ctx, filter)
	if err != nil {
		return toolError(codeInternalError, fmt.Sprintf("failed to load embeddings: %v", err)), nil
	}

	if len(embeddings) == 0 {
		return toolJSON(map[string]any{"results": []any{}, "count": 0}), nil
	}

	// Embed the query.
	queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
	if err != nil {
		return toolError(codeEmbeddingError, fmt.Sprintf("query embedding failed: %v", err)), nil
	}
	queryVec := queryResult.Embedding

	// Build parallel arrays for the ranking function.
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

	results := embedding.RankByCosineSimilarity(queryVec, vectors, recordIDs, namespaces, keys, limit, threshold)

	return toolJSON(map[string]any{
		"results": results,
		"count":   len(results),
	}), nil
}

// extractTextForEmbedding converts a record payload to a string suitable for embedding.
func extractTextForEmbedding(rec contextstore.Record) string {
	if rec.Payload == nil {
		return ""
	}

	// Try to parse as JSON and extract meaningful text fields.
	var obj map[string]any
	if err := json.Unmarshal(rec.Payload, &obj); err == nil {
		var parts []string
		// Common text fields to embed.
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

	// Fall back to the raw payload as text.
	return string(rec.Payload)
}

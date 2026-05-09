package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/embedding"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerEmbeddingTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_embed",
		mcp.WithDescription("Generate and store an embedding for a record. Requires a configured embedding provider. Idempotent: re-embedding overwrites the previous vector. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("record_id", mcp.Required(), mcp.Description("Record ID to embed")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Record namespace")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
		mcp.WithString("model", mcp.Description("Embedding model (default: provider's configured model)")),
	), a.handleEmbed)

	a.addTool(s, mcp.NewTool("context_search",
		mcp.WithDescription("Semantic search across records using embeddings. Returns ranked results by cosine similarity. Requires a configured embedding provider. See `vanta_skills start-here` for the primitive model."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query text")),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default 10, max 25)")),
		mcp.WithString("namespace", mcp.Description("Namespace prefix filter")),
		mcp.WithString("type", mcp.Description("Record type filter")),
		mcp.WithString("tags", mcp.Description("Comma-separated tag filter (any match)")),
		mcp.WithNumber("threshold", mcp.Description("Minimum similarity score (default 0.7)")),
	), a.handleSearch)
}

func (a *Adapter) handleEmbed(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()

	if a.EmbeddingProvider == nil {
		return toolError("embedding_unavailable", "no embedding provider configured"), nil
	}

	recordID := req.GetString("record_id", "")
	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if recordID == "" || ns == "" || key == "" {
		return toolError("validation_error", "record_id, namespace, and key are required"), nil
	}

	// Fetch the record to get its payload text.
	rec, err := a.Store.GetByRecordID(ctx, recordID)
	if err != nil {
		return toolError("not_found", fmt.Sprintf("record %s not found: %v", recordID, err)), nil
	}

	// Extract text from the payload for embedding.
	text := extractTextForEmbedding(rec)
	if text == "" {
		return toolError("validation_error", "record has no embeddable text content"), nil
	}

	// Generate embedding.
	model := req.GetString("model", a.EmbeddingModel)
	result, err := a.EmbeddingProvider.Embed(ctx, text, model)
	if err != nil {
		return toolError("embedding_error", fmt.Sprintf("embedding generation failed: %v", err)), nil
	}

	// Store the embedding.
	if err := a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
		RecordID:   recordID,
		Model:      model,
		Dimensions: len(result.Embedding),
		Vector:     result.Embedding,
	}); err != nil {
		return toolError("internal_error", fmt.Sprintf("failed to store embedding: %v", err)), nil
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

func (a *Adapter) handleSearch(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()

	if a.EmbeddingProvider == nil {
		return toolError("embedding_unavailable", "no embedding provider configured"), nil
	}

	query := req.GetString("query", "")
	if query == "" {
		return toolError("validation_error", "query is required"), nil
	}

	limit := int(req.GetFloat("limit", 10))
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}

	threshold := req.GetFloat("threshold", 0.7)

	// Build filter.
	filter := contextstore.EmbeddingFilter{
		Model: a.EmbeddingModel,
	}
	if ns := req.GetString("namespace", ""); ns != "" {
		filter.Namespaces = []string{ns}
	}
	if t := req.GetString("type", ""); t != "" {
		filter.Types = []string{t}
	}
	if tags := req.GetString("tags", ""); tags != "" {
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
		return toolError("internal_error", fmt.Sprintf("failed to load embeddings: %v", err)), nil
	}

	if len(embeddings) == 0 {
		return toolJSON(map[string]any{"results": []any{}, "count": 0}), nil
	}

	// Embed the query.
	queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
	if err != nil {
		return toolError("embedding_error", fmt.Sprintf("query embedding failed: %v", err)), nil
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

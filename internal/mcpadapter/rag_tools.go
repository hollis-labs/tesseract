package mcpadapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/internal/embedding"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerRAGTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("context_rag_query",
		mcp.WithDescription("RAG retrieval: semantic search that returns ranked text content ready for LLM context injection. Embeds the query, searches similar records, and returns payloads with relevance scores."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural language query")),
		mcp.WithNumber("limit", mcp.Description("Max results (default: 5, max: 20)")),
		mcp.WithNumber("threshold", mcp.Description("Minimum similarity score 0.0-1.0 (default: 0.6)")),
		mcp.WithString("namespace", mcp.Description("Namespace prefix filter")),
		mcp.WithString("types", mcp.Description("Comma-separated record type filter")),
		mcp.WithNumber("max_tokens", mcp.Description("Approximate max tokens in combined results (default: 4000)")),
		mcp.WithBoolean("include_metadata", mcp.Description("Include record metadata (namespace, key, type) in results (default: true)")),
	), a.handleRAGQuery)
}

func (a *Adapter) handleRAGQuery(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()

	if a.EmbeddingProvider == nil {
		return toolError("embedding_unavailable", "no embedding provider configured — RAG queries require an embedding provider"), nil
	}

	query := req.GetString("query", "")
	if query == "" {
		return toolError("validation_error", "query is required"), nil
	}

	limit := req.GetInt("limit", 5)
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	threshold := req.GetFloat("threshold", 0.6)
	maxTokens := req.GetInt("max_tokens", 4000)
	includeMetadata := req.GetBool("include_metadata", true)

	// Build search options.
	opts := embedding.SearchOptions{
		Limit:     limit * 2, // fetch extra for token budget trimming
		Threshold: threshold,
		Model:     a.EmbeddingModel,
	}

	if ns := req.GetString("namespace", ""); ns != "" {
		opts.Namespaces = []string{ns}
	}
	if typesStr := req.GetString("types", ""); typesStr != "" {
		for _, t := range strings.Split(typesStr, ",") {
			if t = strings.TrimSpace(t); t != "" {
				opts.Types = append(opts.Types, t)
			}
		}
	}

	// Use VectorIndex if available, otherwise fall back to brute-force.
	var results []embedding.SearchResult
	if a.VectorIndex != nil {
		queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
		if err != nil {
			return toolError("embedding_error", fmt.Sprintf("query embedding failed: %v", err)), nil
		}
		results, err = a.VectorIndex.Search(ctx, queryResult.Embedding, opts)
		if err != nil {
			return toolError("search_error", fmt.Sprintf("vector search failed: %v", err)), nil
		}
	} else {
		// Brute-force via Store.
		filter := contextstore.EmbeddingFilter{
			Model: a.EmbeddingModel,
		}
		if len(opts.Namespaces) > 0 {
			filter.Namespaces = opts.Namespaces
		}
		if len(opts.Types) > 0 {
			filter.Types = opts.Types
		}

		embeddings, records, err := a.Store.ListEmbeddings(ctx, filter)
		if err != nil {
			return toolError("search_error", fmt.Sprintf("failed to load embeddings: %v", err)), nil
		}

		if len(embeddings) > 0 {
			queryResult, err := a.EmbeddingProvider.Embed(ctx, query, a.EmbeddingModel)
			if err != nil {
				return toolError("embedding_error", fmt.Sprintf("query embedding failed: %v", err)), nil
			}
			queryVec := queryResult.Embedding

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

			results = embedding.RankByCosineSimilarity(queryVec, vectors, recordIDs, namespaces, keys, limit*2, threshold)
		}
	}

	// Fetch payloads and build response with token budget.
	type ragResult struct {
		RecordID   string  `json:"record_id"`
		Namespace  string  `json:"namespace,omitempty"`
		Key        string  `json:"key,omitempty"`
		RecordType string  `json:"record_type,omitempty"`
		Score      float64 `json:"score"`
		Text       string  `json:"text"`
	}

	var ragResults []ragResult
	tokensSoFar := 0

	for _, sr := range results {
		if len(ragResults) >= limit {
			break
		}

		rec, err := a.Store.GetByRecordID(ctx, sr.RecordID)
		if err != nil {
			continue // skip records we can't fetch
		}

		// Extract text from payload.
		text := extractTextForEmbedding(rec)
		if text == "" {
			text = string(rec.Payload)
		}

		tokens := contextstore.EstimateTokens([]byte(text))
		if maxTokens > 0 && tokensSoFar+tokens > maxTokens && len(ragResults) > 0 {
			break
		}

		rr := ragResult{
			RecordID: sr.RecordID,
			Score:    sr.Score,
			Text:     text,
		}
		if includeMetadata {
			rr.Namespace = rec.Namespace
			rr.Key = rec.Key
			rr.RecordType = rec.RecordType
		}

		ragResults = append(ragResults, rr)
		tokensSoFar += tokens
	}

	// Build context block — a single string concatenation of all results for easy injection.
	var contextBlock strings.Builder
	for i, rr := range ragResults {
		if i > 0 {
			contextBlock.WriteString("\n---\n")
		}
		if includeMetadata {
			fmt.Fprintf(&contextBlock, "[%s/%s] (score: %.2f)\n", rr.Namespace, rr.Key, rr.Score)
		}
		contextBlock.WriteString(rr.Text)
	}

	return toolJSON(map[string]any{
		"query":          query,
		"results":        ragResults,
		"count":          len(ragResults),
		"token_estimate": tokensSoFar,
		"context_block":  contextBlock.String(),
	}), nil
}

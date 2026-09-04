package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/embedding"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ingestModeArgDescription documents the arm selector on context_ingest.
const ingestModeArgDescription = "What is being ingested, which decides how it is split into records and what comes back. " +
	"The two modes take DIFFERENT arguments; passing one mode's argument to the other is a validation_error rather than a silently ignored knob. " +
	"`bulk` (default): you already have the records. `items` (required) is a JSON array; each is validated, written, and optionally embedded. " +
	"Answers `{total, written, embedded, errors, results}` with one result per input item. Peer of POST /v1/context/bulk-ingest. " +
	"`chunked`: you have one document too large to embed. `namespace`, `key_prefix` and `text` (all required) are split by `strategy` into " +
	"<key_prefix>/chunk-000, chunk-001, … each auto-embedded. Answers `{namespace, key_prefix, strategy, total_chunks, embedded, results}`. " +
	"MCP-only: chunked ingest has no HTTP peer."

func (a *Adapter) registerBulkTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_ingest",
		mcp.WithDescription("Read this first: call `tesseract_skills start-here` for the per-record payload shape — each entry of `items` is the same object `context_typed_write` takes, and one malformed entry in a batch of fifty is reported per-item rather than failing the call. "+
			"Writes records into the context store in batch. "+
			"`mode` selects whether the input is a list of records or one document to be chunked — see its description. "+
			"Requires 'write' scope."),
		mcp.WithString("mode", mcp.Description(ingestModeArgDescription)),
		// Per-mode requiredness is enforced in the handlers, not in the schema:
		// the two modes require disjoint argument sets, so a schema-level
		// Required() would demand `items` on a chunked call.
		mcp.WithString("items", mcp.Description("mode=bulk, required there: JSON array of items. Each item: {namespace, key, payload (JSON string or object), record_type?, status?, ttl?, pointers? (comma-sep), actor?}")),
		mcp.WithBoolean("embed", mcp.Description("mode=bulk only: auto-embed each record after writing (default: false). Requires a configured embedding provider. mode=chunked always embeds when a provider is configured.")),
		mcp.WithBoolean("stop_on_error", mcp.Description("mode=bulk only: stop processing on first error (default: false). When false, errors are collected per-item.")),
		mcp.WithString("namespace", mcp.Description("mode=chunked, required there: target namespace")),
		mcp.WithString("key_prefix", mcp.Description("mode=chunked, required there: key prefix — chunks are named <prefix>/chunk-000, chunk-001, etc.")),
		mcp.WithString("text", mcp.Description("mode=chunked, required there: full document text to chunk")),
		mcp.WithString("record_type", mcp.Description("mode=chunked only: context type for chunks (default: brief/summary). Under mode=bulk this is a per-item field, not a tool argument.")),
		mcp.WithString("strategy", mcp.Description("mode=chunked only: chunking strategy — fixed, sentence, paragraph (default: sentence)")),
		mcp.WithNumber("max_chars", mcp.Description("mode=chunked only: max characters per chunk (default: 1000)")),
		mcp.WithNumber("overlap_pct", mcp.Description("mode=chunked only: overlap percentage for the fixed strategy (default: 10, range 0-50)")),
		mcp.WithString("actor", mcp.Description("mode=chunked only: actor identity (default: mcp-agent). Under mode=bulk this is a per-item field, not a tool argument.")),
	), a.handleContextIngest)
}

// handleContextIngest serves the merged context_ingest. `mode` selects the arm;
// the two arms are the pre-merge handlers unchanged.
func (a *Adapter) handleContextIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	mode := req.GetString("mode", "bulk")

	reject := func(modeName string, knobs ...string) *mcp.CallToolResult {
		for _, knob := range knobs {
			if raw, ok := req.GetArguments()[knob]; ok && raw != nil && raw != "" {
				return toolError(codeValidationError, knob+" is not accepted under mode="+modeName)
			}
		}
		return nil
	}

	switch mode {
	case "bulk":
		if errResult := reject("bulk", "namespace", "key_prefix", "text", "record_type", "strategy", "max_chars", "overlap_pct", "actor"); errResult != nil {
			return errResult, nil
		}
		return a.handleBulkIngest(ctx, req)
	case "chunked":
		if errResult := reject("chunked", "items", "embed", "stop_on_error"); errResult != nil {
			return errResult, nil
		}
		return a.handleChunkedIngest(ctx, req)
	default:
		return toolError(codeValidationError,
			"mode must be one of bulk|chunked, got "+strconv.Quote(mode)), nil
	}
}

// bulkItem is the per-item input for bulk ingestion.
type bulkItem struct {
	Namespace  string `json:"namespace"`
	Key        string `json:"key"`
	Payload    any    `json:"payload"`
	RecordType string `json:"record_type"`
	Status     string `json:"status"`
	TTL        string `json:"ttl"`
	Pointers   string `json:"pointers"`
	Actor      string `json:"actor"`
}

// bulkResult is the per-item output.
type bulkResult struct {
	Index    int    `json:"index"`
	RecordID string `json:"record_id,omitempty"`
	Status   string `json:"status"` // "written", "embedded", "error"
	Error    string `json:"error,omitempty"`
	Embedded bool   `json:"embedded,omitempty"`
}

func (a *Adapter) handleBulkIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, claims := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	itemsStr := req.GetString("items", "")
	if itemsStr == "" {
		return toolError(codeValidationError, "items is required"), nil
	}

	var items []bulkItem
	if err := json.Unmarshal([]byte(itemsStr), &items); err != nil {
		return toolError(codeValidationError, "items must be a valid JSON array: "+err.Error()), nil //nolint:nilerr // MCP tool pattern: return result with error details, not Go error
	}
	if len(items) == 0 {
		return toolError(codeValidationError, "items array is empty"), nil
	}
	if len(items) > 100 {
		return toolError(codeValidationError, "max 100 items per batch"), nil
	}

	embed := req.GetBool("embed", false)
	stopOnError := req.GetBool("stop_on_error", false)

	reg := a.getRegistry()
	results := make([]bulkResult, 0, len(items))
	written := 0
	embedded := 0
	errCount := 0

	for i, item := range items {
		res := bulkResult{Index: i}

		// Validate required fields.
		if item.Namespace == "" || item.Key == "" {
			res.Status = "error"
			res.Error = "namespace and key are required"
			results = append(results, res)
			errCount++
			if stopOnError {
				break
			}
			continue
		}

		// Check namespace permission.
		if !globsPermit(claims.NamespaceGlobs, item.Namespace) {
			res.Status = "error"
			res.Error = fmt.Sprintf("token does not permit writes to namespace %q", item.Namespace)
			results = append(results, res)
			errCount++
			if stopOnError {
				break
			}
			continue
		}

		// Marshal payload — accept both string and object forms.
		var payloadBytes json.RawMessage
		switch v := item.Payload.(type) {
		case string:
			if err := json.Unmarshal([]byte(v), &payloadBytes); err != nil {
				res.Status = "error"
				res.Error = "payload must be valid JSON: " + err.Error()
				results = append(results, res)
				errCount++
				if stopOnError {
					break
				}
				continue
			}
		default:
			var err error
			payloadBytes, err = json.Marshal(v)
			if err != nil {
				res.Status = "error"
				res.Error = "failed to marshal payload: " + err.Error()
				results = append(results, res)
				errCount++
				if stopOnError {
					break
				}
				continue
			}
		}

		// Type validation.
		if err := reg.ValidateType(item.RecordType); err != nil {
			res.Status = "error"
			res.Error = err.Error()
			results = append(results, res)
			errCount++
			if stopOnError {
				break
			}
			continue
		}

		status := item.Status
		if status == "" {
			status = "draft"
		}
		if err := reg.ValidateStatus(item.RecordType, status); err != nil {
			res.Status = "error"
			res.Error = err.Error()
			results = append(results, res)
			errCount++
			if stopOnError {
				break
			}
			continue
		}

		// Required fields validation.
		if item.RecordType != "" {
			var payloadMap map[string]any
			if err := json.Unmarshal(payloadBytes, &payloadMap); err == nil {
				if err := reg.ValidateRequiredFields(item.RecordType, payloadMap); err != nil {
					res.Status = "error"
					res.Error = err.Error()
					results = append(results, res)
					errCount++
					if stopOnError {
						break
					}
					continue
				}
			}
		}

		// Apply default TTL.
		ttl := item.TTL
		if ttl == "" && item.RecordType != "" {
			ct, ok := reg.GetType(item.RecordType)
			if ok {
				defaultTTL := ct.ParseDefaultTTL()
				if defaultTTL > 0 {
					ttl = time.Now().UTC().Add(defaultTTL).Format(time.RFC3339)
				}
			}
		}

		// Parse pointers.
		var pointers []string
		if item.Pointers != "" {
			for _, p := range strings.Split(item.Pointers, ",") {
				if t := strings.TrimSpace(p); t != "" {
					pointers = append(pointers, t)
				}
			}
		}

		actor := item.Actor
		if actor == "" {
			actor = "mcp-agent"
		}

		// Write.
		rec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
			Namespace:  item.Namespace,
			Key:        item.Key,
			Actor:      actor,
			Payload:    payloadBytes,
			RecordType: item.RecordType,
			Status:     status,
			TTL:        ttl,
			Pointers:   pointers,
		})
		if err != nil {
			res.Status = "error"
			res.Error = "write failed: " + err.Error()
			results = append(results, res)
			errCount++
			if stopOnError {
				break
			}
			continue
		}

		res.RecordID = rec.RecordID
		res.Status = "written"
		written++

		// Audit.
		_ = a.Store.EmitBulkIngest(ctx, actor, item.Namespace, item.Key, rec.Revision, rec.RecordID,
			json.RawMessage(fmt.Sprintf(`{"source":"mcp","record_type":%q,"status":%q,"batch_index":%d}`, item.RecordType, status, i)))

		// Embed if requested.
		if embed && a.EmbeddingProvider != nil {
			text := extractTextForEmbedding(contextstore.Record{
				RecordID:  rec.RecordID,
				Namespace: rec.Namespace,
				Key:       rec.Key,
				Payload:   payloadBytes,
			})
			if text != "" {
				embResult, embedErr := a.EmbeddingProvider.Embed(ctx, text, a.EmbeddingModel)
				if embedErr == nil {
					if storeErr := a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
						RecordID:   rec.RecordID,
						Model:      a.EmbeddingModel,
						Dimensions: len(embResult.Embedding),
						Vector:     embResult.Embedding,
					}); storeErr != nil {
						res.Error = "embedding store failed: " + storeErr.Error()
					} else {
						res.Status = "embedded"
						res.Embedded = true
						embedded++
					}
				} else {
					res.Error = "embedding failed: " + embedErr.Error()
				}
			}
		}

		results = append(results, res)
	}

	return toolJSON(map[string]any{
		"total":    len(items),
		"written":  written,
		"embedded": embedded,
		"errors":   errCount,
		"results":  results,
	}), nil
}

func (a *Adapter) handleChunkedIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, claims := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	keyPrefix := req.GetString("key_prefix", "")
	text := req.GetString("text", "")
	recordType := req.GetString("record_type", "brief/summary")
	strategyStr := req.GetString("strategy", "sentence")
	maxChars := req.GetInt("max_chars", 1000)
	overlapPct := req.GetInt("overlap_pct", 10)
	actor := req.GetString("actor", "mcp-agent")

	if ns == "" || keyPrefix == "" || text == "" {
		return toolError(codeValidationError, "namespace, key_prefix, and text are required"), nil
	}
	if !globsPermit(claims.NamespaceGlobs, ns) {
		return toolError(codeNamespaceNotPermitted, "token does not permit writes to namespace: "+ns), nil
	}

	// Map strategy string.
	var strategy embedding.ChunkStrategy
	switch strategyStr {
	case "fixed":
		strategy = embedding.ChunkFixed
	case "paragraph":
		strategy = embedding.ChunkParagraph
	default:
		strategy = embedding.ChunkSentence
	}

	chunks := embedding.ChunkText(text, embedding.ChunkerConfig{
		Strategy:   strategy,
		MaxChars:   maxChars,
		OverlapPct: overlapPct,
	})

	if len(chunks) == 0 {
		return toolError(codeValidationError, "text produced no chunks"), nil
	}

	reg := a.getRegistry()
	if err := reg.ValidateType(recordType); err != nil {
		return toolError(codeValidationError, err.Error()), nil
	}

	type chunkResult struct {
		Index    int    `json:"index"`
		Key      string `json:"key"`
		RecordID string `json:"record_id"`
		Embedded bool   `json:"embedded"`
		Chars    int    `json:"chars"`
		Error    string `json:"error,omitempty"`
	}

	results := make([]chunkResult, 0, len(chunks))
	embeddedCount := 0

	for _, chunk := range chunks {
		key := fmt.Sprintf("%s/chunk-%03d", keyPrefix, chunk.Index)

		payload, err := json.Marshal(map[string]any{
			"text":         chunk.Text,
			"chunk_index":  chunk.Index,
			"total_chunks": chunk.TotalCount,
			"start_char":   chunk.StartChar,
			"end_char":     chunk.EndChar,
			"source_key":   keyPrefix,
		})
		if err != nil {
			return toolError(codeInternalError, fmt.Sprintf("serialize chunk %d: %v", chunk.Index, err)), nil
		}

		rec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
			Namespace:  ns,
			Key:        key,
			Actor:      actor,
			Payload:    payload,
			RecordType: recordType,
			Status:     "draft",
			Pointers:   []string{"source:" + keyPrefix},
		})
		if err != nil {
			return toolError(codeWriteFailed, fmt.Sprintf("chunk %d: %v", chunk.Index, err)), nil
		}

		res := chunkResult{
			Index:    chunk.Index,
			Key:      key,
			RecordID: rec.RecordID,
			Chars:    len(chunk.Text),
		}

		// Auto-embed.
		if a.EmbeddingProvider != nil {
			embResult, embedErr := a.EmbeddingProvider.Embed(ctx, chunk.Text, a.EmbeddingModel)
			if embedErr == nil {
				if storeErr := a.Store.UpsertEmbedding(ctx, contextstore.EmbeddingRow{
					RecordID:   rec.RecordID,
					Model:      a.EmbeddingModel,
					Dimensions: len(embResult.Embedding),
					Vector:     embResult.Embedding,
				}); storeErr != nil {
					res.Error = "embedding store failed: " + storeErr.Error()
				} else {
					res.Embedded = true
					embeddedCount++
				}
			} else {
				res.Error = "embedding failed: " + embedErr.Error()
			}
		}

		_ = a.Store.EmitChunkedIngest(ctx, actor, ns, key, rec.Revision, rec.RecordID,
			json.RawMessage(fmt.Sprintf(`{"chunk_index":%d,"total_chunks":%d,"source":%q}`, chunk.Index, chunk.TotalCount, keyPrefix)))

		results = append(results, res)
	}

	return toolJSON(map[string]any{
		"namespace":    ns,
		"key_prefix":   keyPrefix,
		"strategy":     strategyStr,
		"total_chunks": len(chunks),
		"embedded":     embeddedCount,
		"results":      results,
	}), nil
}

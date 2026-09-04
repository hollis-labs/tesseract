package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestProductionMCPAdapterSharesConfiguredSubsystemEmbedder(t *testing.T) {
	layout := hermeticLayout(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const credential = "production-wiring-secret-must-not-be-logged"
	t.Setenv("OPENAI_API_KEY", credential)
	cfg := config.Defaults()
	cfg.Embedding.Model = "configured-contract-model"
	var logs bytes.Buffer

	mem, err := setupMemorySubsystem(context.Background(), store, nil, layout, cfg)
	if err != nil {
		t.Fatalf("setup memory subsystem: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	adapter := newMCPAdapter(store, "", mem, cfg, &logs)
	if adapter.EmbeddingProvider == nil {
		t.Fatal("production MCP adapter has no configured embedding provider")
	}
	if adapter.EmbeddingProvider != mem.Embedder {
		t.Fatal("production MCP adapter constructed a second embedder instead of sharing the subsystem instance")
	}
	if adapter.EmbeddingModel != cfg.Embedding.Model || mem.EmbeddingModel != cfg.Embedding.Model {
		t.Fatalf("embedding model drift: adapter=%q subsystem=%q config=%q",
			adapter.EmbeddingModel, mem.EmbeddingModel, cfg.Embedding.Model)
	}
	if strings.Contains(logs.String(), credential) {
		t.Fatal("production MCP construction logged the embedding credential")
	}

	queueDB := mem.queueDB
	if err := mem.Close(); err != nil {
		t.Fatalf("shutdown memory subsystem: %v", err)
	}
	if err := queueDB.Ping(); err == nil {
		t.Fatal("queue DB remains open after production MCP subsystem shutdown")
	}
}

type mcpContractEmbedder struct {
	mu     sync.Mutex
	models []string
	err    error
}

func (e *mcpContractEmbedder) Embed(ctx context.Context, text, model string) (*embedcontracts.EmbeddingResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.models = append(e.models, model)
	if e.err != nil {
		return nil, e.err
	}
	text = strings.ToLower(text)
	vector := []float32{0, 1}
	if strings.Contains(text, "mars") || strings.Contains(text, "red planet") {
		vector = []float32{1, 0}
	}
	return &embedcontracts.EmbeddingResult{Embedding: vector, TokenCount: len(text)}, nil
}

func (e *mcpContractEmbedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	results := make([]embedcontracts.EmbeddingResult, len(texts))
	for i, text := range texts {
		result, err := e.Embed(ctx, text, model)
		if err != nil {
			return nil, err
		}
		results[i] = *result
	}
	return results, nil
}

func (*mcpContractEmbedder) EmbeddingDimensions(string) int { return 2 }

func (e *mcpContractEmbedder) setError(err error) {
	e.mu.Lock()
	e.err = err
	e.mu.Unlock()
}

func (e *mcpContractEmbedder) calledModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.models...)
}

func TestProductionMCPEmbeddingToolsEndToEnd(t *testing.T) {
	layout := hermeticLayout(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.Defaults()
	cfg.Embedding.Model = "semantic-contract-model"
	provider := &mcpContractEmbedder{}
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	mem, err := setupMemorySubsystemWithEmbedder(lifecycleCtx, store, nil, layout, cfg, provider)
	if err != nil {
		cancel()
		t.Fatalf("setup memory subsystem: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = mem.Close()
	})

	adapter := newMCPAdapter(store, "", mem, cfg, &bytes.Buffer{})
	mcpServer := server.NewMCPServer("production-construction-contract", "test")
	adapter.RegisterAllTools(mcpServer)

	mars := appendMCPContractRecord(t, store, "knowledge/space", "mars", `{"title":"Mars","content":"Mars is known as the red planet."}`)
	ocean := appendMCPContractRecord(t, store, "knowledge/earth", "ocean", `{"title":"Ocean","content":"Earth has a deep blue ocean."}`)
	for _, record := range []contextstore.Record{mars, ocean} {
		body := callRegisteredMCPTool(context.Background(), t, mcpServer, "context_embed", map[string]any{
			"record_id": record.RecordID,
			"namespace": record.Namespace,
			"key":       record.Key,
		})
		if body["status"] != "stored" || body["model"] != cfg.Embedding.Model {
			t.Fatalf("context_embed did not use configured runtime: %v", body)
		}
	}

	search := callRegisteredMCPTool(context.Background(), t, mcpServer, "context_search", map[string]any{
		"query":     "Which world is the red planet?",
		"threshold": 0.9,
	})
	assertOnlyMCPRecord(t, search, mars.RecordID, "context_search")

	rag := callRegisteredMCPTool(context.Background(), t, mcpServer, "context_rag_query", map[string]any{
		"query":     "Which world is the red planet?",
		"threshold": 0.9,
	})
	assertOnlyMCPRecord(t, rag, mars.RecordID, "context_rag_query")
	if contextBlock, _ := rag["context_block"].(string); !strings.Contains(contextBlock, "Mars is known as the red planet") {
		t.Fatalf("RAG context block omitted retrieved payload: %v", rag)
	}

	calledModels := provider.calledModels()
	if len(calledModels) != 4 {
		t.Fatalf("embedding provider calls = %d, want two context_embed calls plus search and RAG", len(calledModels))
	}
	for _, model := range calledModels {
		if model != cfg.Embedding.Model {
			t.Fatalf("embedding call used model %q, want configured %q", model, cfg.Embedding.Model)
		}
	}

	provider.setError(errors.New("provider offline"))
	failure := callRegisteredMCPTool(context.Background(), t, mcpServer, "context_embed", map[string]any{
		"record_id": mars.RecordID,
		"namespace": mars.Namespace,
		"key":       mars.Key,
	})
	if failure["code"] != "embedding_error" {
		t.Fatalf("provider failure was not observable as embedding_error: %v", failure)
	}

	queueDB := mem.queueDB
	cancel()
	if err := mem.Close(); err != nil {
		t.Fatalf("shutdown memory subsystem: %v", err)
	}
	if err := queueDB.Ping(); err == nil {
		t.Fatal("queue DB remains open after MCP embedding runtime shutdown")
	}
}

func TestProductionMCPEmbeddingToolsReportDisabledRuntime(t *testing.T) {
	layout := hermeticLayout(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.Defaults()
	mem, err := setupMemorySubsystemWithEmbedder(context.Background(), store, nil, layout, cfg, nil)
	if err != nil {
		t.Fatalf("setup memory subsystem: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	var logs bytes.Buffer
	adapter := newMCPAdapter(store, "", mem, cfg, &logs)
	mcpServer := server.NewMCPServer("disabled-production-contract", "test")
	adapter.RegisterAllTools(mcpServer)
	if !strings.Contains(logs.String(), "embedding tools disabled") || !strings.Contains(logs.String(), "return embedding_unavailable") {
		t.Fatalf("disabled embedding runtime was not observable at startup: %q", logs.String())
	}

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"context_embed", map[string]any{"record_id": "missing", "namespace": "n", "key": "k"}},
		{"context_search", map[string]any{"query": "q"}},
		{"context_rag_query", map[string]any{"query": "q"}},
	} {
		body := callRegisteredMCPTool(context.Background(), t, mcpServer, tc.tool, tc.args)
		if body["code"] != "embedding_unavailable" {
			t.Errorf("%s disabled result = %v, want embedding_unavailable", tc.tool, body)
		}
	}
}

func appendMCPContractRecord(t *testing.T, store *contextstore.Store, namespace, key, payload string) contextstore.Record {
	t.Helper()
	record, err := store.AppendRecord(context.Background(), contextstore.AppendInput{
		Namespace: namespace,
		Key:       key,
		Actor:     "mcp-production-contract",
		Payload:   json.RawMessage(payload),
	})
	if err != nil {
		t.Fatalf("append %s/%s: %v", namespace, key, err)
	}
	return record
}

func callRegisteredMCPTool(ctx context.Context, t *testing.T, mcpServer *server.MCPServer, name string, args map[string]any) map[string]any {
	t.Helper()
	tool := mcpServer.GetTool(name)
	if tool == nil {
		t.Fatalf("production adapter did not register %s", name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := tool.Handler(ctx, req)
	if err != nil {
		t.Fatalf("%s transport error: %v", name, err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("%s result has no single content item: %#v", name, result)
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("%s content type = %T, want mcp.TextContent", name, result.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(content.Text), &body); err != nil {
		t.Fatalf("decode %s result %q: %v", name, content.Text, err)
	}
	return body
}

func assertOnlyMCPRecord(t *testing.T, body map[string]any, recordID, tool string) {
	t.Helper()
	results, ok := body["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("%s results = %v, want exactly one", tool, body)
	}
	result, ok := results[0].(map[string]any)
	if !ok || result["record_id"] != recordID {
		t.Fatalf("%s top result = %v, want record %s", tool, results[0], recordID)
	}
}

package mcpadapter

import (
	"context"
	"sync"
	"testing"
	"time"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
)

type cancellationWaitingEmbedder struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *cancellationWaitingEmbedder) Embed(ctx context.Context, _ string, _ string) (*embedcontracts.EmbeddingResult, error) {
	e.once.Do(func() { close(e.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.release:
		return &embedcontracts.EmbeddingResult{Embedding: []float32{1, 0}}, nil
	}
}

func (e *cancellationWaitingEmbedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	results := make([]embedcontracts.EmbeddingResult, len(texts))
	for i := range texts {
		result, err := e.Embed(ctx, texts[i], model)
		if err != nil {
			return nil, err
		}
		results[i] = *result
	}
	return results, nil
}

func (*cancellationWaitingEmbedder) EmbeddingDimensions(string) int { return 2 }

func TestEmbeddingMCPToolsPropagateRequestCancellationToProvider(t *testing.T) {
	store := newTestStore(t)
	record := writeRecord(t, store, "knowledge/space", "mars", `{"content":"Mars is the red planet."}`)
	const model = "cancellation-contract-model"
	if err := store.UpsertEmbedding(context.Background(), contextstore.EmbeddingRow{
		RecordID:   record.RecordID,
		Model:      model,
		Dimensions: 2,
		Vector:     []float32{1, 0},
	}); err != nil {
		t.Fatalf("seed embedding: %v", err)
	}

	for _, tc := range []struct {
		name string
		call func(*Adapter, context.Context) (*mcp.CallToolResult, error)
	}{
		{
			name: "context_embed",
			call: func(adapter *Adapter, ctx context.Context) (*mcp.CallToolResult, error) {
				req := mcp.CallToolRequest{}
				req.Params.Arguments = map[string]any{
					"record_id": record.RecordID,
					"namespace": record.Namespace,
					"key":       record.Key,
				}
				return adapter.handleEmbed(ctx, req)
			},
		},
		{
			name: "context_search",
			call: func(adapter *Adapter, ctx context.Context) (*mcp.CallToolResult, error) {
				req := mcp.CallToolRequest{}
				req.Params.Arguments = map[string]any{"query": "red planet", "threshold": -1.0}
				return adapter.handleSearch(ctx, req)
			},
		},
		{
			name: "context_rag_query",
			call: func(adapter *Adapter, ctx context.Context) (*mcp.CallToolResult, error) {
				req := mcp.CallToolRequest{}
				req.Params.Arguments = map[string]any{"query": "red planet", "threshold": -1.0}
				return adapter.handleRAGQuery(ctx, req)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &cancellationWaitingEmbedder{
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(provider.release) }) })
			adapter := New(store, "")
			adapter.EmbeddingProvider = provider
			adapter.EmbeddingModel = model

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			type outcome struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := tc.call(adapter, ctx)
				done <- outcome{result: result, err: err}
			}()

			select {
			case <-provider.started:
			case <-time.After(time.Second):
				t.Fatal("embedding provider was not called")
			}
			cancel()

			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("transport error: %v", got.err)
				}
				body := parseResult(t, got.result)
				if body["code"] != "embedding_error" {
					t.Fatalf("canceled provider result = %v, want embedding_error", body)
				}
			case <-time.After(time.Second):
				releaseOnce.Do(func() { close(provider.release) })
				t.Fatal("tool did not stop after its request context was canceled")
			}
		})
	}
}

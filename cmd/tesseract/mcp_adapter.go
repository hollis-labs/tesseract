package main

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// newMCPAdapter is the production MCP assembly boundary. In particular, it
// shares the memory subsystem's configured embedder instead of constructing a
// second provider with an independently drifting model or credential state.
// A nil embedder is preserved: the embedding tools remain discoverable and
// answer with embedding_unavailable when called.
func newMCPAdapter(store *contextstore.Store, token string, mem *memorySubsystem, cfg config.Config, logWriter io.Writer) *mcpadapter.Adapter {
	adapter := mcpadapter.New(store, token)
	// One source of version truth: the handshake reports what --version reports.
	adapter.Version = buildVersion()
	if mem != nil {
		adapter.MemoryStore = mem.Store
		adapter.KnowledgeStore = knowledge.New(mem.Store)
		adapter.EmbeddingProvider = mem.Embedder
		adapter.EmbeddingModel = mem.EmbeddingModel
		if mem.Embedder == nil && logWriter != nil {
			_, _ = fmt.Fprintf(logWriter,
				"warning: MCP embedding tools disabled: no embedding provider available (configured provider=%q, model=%q); context_embed, context_search, and context_rag_query return embedding_unavailable\n",
				cfg.Embedding.Provider, mem.EmbeddingModel)
		}
	}
	adapter.DefaultPayloadMode = memory.PayloadMode(cfg.Read.PayloadMode)
	adapter.DefaultBudget = memory.Budget{
		Bytes:  cfg.Read.BudgetBytes,
		Tokens: cfg.Read.BudgetTokens,
	}
	if logWriter != nil {
		// MCP protocol traffic owns stdout. Sanitizer diagnostics are routed to
		// the caller-provided stderr without including provider credentials.
		adapter.Logger = slog.New(slog.NewTextHandler(logWriter, nil))
	}
	return adapter
}

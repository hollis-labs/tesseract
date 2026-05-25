package mcpadapter

import (
	"context"
	"encoding/json"
	"log/slog"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	mcpsanitize "github.com/hollis-labs/go-mcp-sanitize"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/contexttypes"
	"github.com/hollis-labs/tesseract/internal/embedding"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Adapter exposes context memory service operations as MCP tools over stdio.
//
// Read tools (context_head, context_history, context_view) work without a token.
// Write tools (context_write, context_promote_request) require a capability token
// configured at startup via the Token field.
// context_packet is read-only but respects namespace_globs from the token when present.
type Adapter struct {
	Store             *contextstore.Store
	Token             string // capability token for mutating ops; may be empty
	TypeRegistry      *contexttypes.Registry
	EmbeddingProvider embedcontracts.Embedder // optional; nil disables context_embed/context_search
	EmbeddingModel    string                  // model name passed to EmbeddingProvider (default: "")
	VectorIndex       embedding.VectorIndex   // optional; nil uses brute-force search via Store
	MemoryStore       *memory.Store           // optional; nil disables memory_* tools
	KnowledgeStore    *knowledge.Store        // optional; nil disables knowledge_* tools
	Logger            *slog.Logger            // optional; nil falls back to slog.Default()
}

// New creates an Adapter for the given store and optional capability token.
func New(store *contextstore.Store, token string) *Adapter {
	return &Adapter{Store: store, Token: token}
}

// Run registers all tools and starts the MCP stdio server. Blocks until ctx is
// cancelled or the client disconnects.
func (a *Adapter) Run(ctx context.Context) error {
	s := server.NewMCPServer(
		"tesseract",
		"0.7.0",
		server.WithToolCapabilities(true),
	)
	a.RegisterAllTools(s)
	ctxFunc := func(_ context.Context) context.Context { return ctx }
	return server.ServeStdio(s, server.WithStdioContextFunc(ctxFunc))
}

// RegisterAllTools registers every MCP tool supported by this adapter on s.
// The parity test uses this to introspect the live tool surface without
// starting a stdio server.
func (a *Adapter) RegisterAllTools(s *server.MCPServer) {
	a.registerTools(s)
	a.registerTypedTools(s)
	a.registerEmbeddingTools(s)
	a.registerSessionTools(s)
	a.registerBulkTools(s)
	a.registerRAGTools(s)
	if a.MemoryStore != nil {
		a.registerMemoryTools(s)
		a.registerLookupTools(s)
	}
	if a.KnowledgeStore != nil {
		a.registerKnowledgeTools(s)
	}
	a.registerParityTools(s)
	a.registerSkillsTool(s)
}

// addTool wraps every MCP tool handler with the go-mcp-sanitize middleware,
// which auto-cleans malformed agent tool-call XML in free-text params before
// the handler runs. Clean calls are silent; cleaned calls emit one warn-level
// slog line (see github.com/hollis-labs/go-mcp-sanitize).
//
// All registerXxx helpers must call a.addTool(s, tool, handler) instead of
// s.AddTool(tool, handler) directly so the protection stays uniform.
func (a *Adapter) addTool(s *server.MCPServer, t mcp.Tool, h server.ToolHandlerFunc) {
	logger := a.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s.AddTool(t, mcpsanitize.Middleware(logger)(h))
}

// checkScope validates the configured token and checks for the required scope.
// Returns a non-nil error JSON result if auth fails; nil means the caller may proceed.
func (a *Adapter) checkScope(ctx context.Context, scope string) (*mcp.CallToolResult, contextstore.AuthToken) {
	if a.Token == "" {
		return toolError("auth_required", "no capability token configured for mutating operations"), contextstore.AuthToken{}
	}
	claims, err := a.Store.ValidateAuthTokenWithClaims(ctx, a.Token)
	if err != nil {
		return toolError("auth_required", "token invalid or expired: "+err.Error()), contextstore.AuthToken{}
	}
	for _, s := range claims.Scopes {
		if s == scope {
			return nil, claims
		}
	}
	return toolError("insufficient_scope", "token does not have scope: "+scope), claims
}

// toolError returns a CallToolResult containing a JSON error body agents can parse.
func toolError(code, message string) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return mcp.NewToolResultText(string(body))
}

// toolJSON marshals v to JSON and wraps it as a tool result.
func toolJSON(v any) *mcp.CallToolResult {
	body, _ := json.Marshal(v)
	return mcp.NewToolResultText(string(body))
}

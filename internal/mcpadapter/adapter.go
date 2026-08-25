package mcpadapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

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

	// DefaultPayloadMode is the projection applied to memory_recall and
	// tesseract_lookup results when the call does not pass payload_mode.
	// Wired from config (read.payload_mode). Empty or unrecognized falls
	// back to memory.DefaultPayloadMode.
	DefaultPayloadMode memory.PayloadMode

	// DefaultBudget is the response ceiling applied to memory_recall and
	// tesseract_lookup when the call passes no budget_bytes / budget_tokens.
	// Wired from config (read.budget_bytes, read.budget_tokens), which
	// defaults both to 0 — no ceiling. contextapi.Server resolves the same
	// two config fields for the HTTP peers.
	DefaultBudget memory.Budget
}

// resolvePageRequest builds the shared paging/budget half of a read call from
// MCP arguments.
//
// defaultBudget is the ceiling to apply when the call passes no budget of its
// own. Recall and lookup pass the config-wired a.DefaultBudget; the history
// tools pass the zero Budget — see resolveHistoryPageRequest.
//
// Every knob here has a declared HTTP peer that must accept the same name,
// validate identically, and resolve the same default. The two surfaces
// therefore share memory.PageRequest and everything downstream of it; only
// the argument decoding differs, and TestBudgetCursorParity_MCPvsHTTP
// exercises both against the same store.
func (a *Adapter) resolvePageRequest(req mcp.CallToolRequest, mode memory.PayloadMode, defaultBudget memory.Budget) (memory.PageRequest, *mcp.CallToolResult) {
	pr := memory.PageRequest{
		Cursor:      req.GetString("cursor", ""),
		PayloadMode: mode,
		Budget:      defaultBudget,
	}

	// limit goes through wholeNumberArg rather than a bare GetFloat: a
	// fractional limit is a decode error on both HTTP peers (json into an
	// int for recall/lookup, strconv.Atoi for the history routes), so
	// silently truncating 2.5 to 2 here would make the same call succeed on
	// one door and fail on the other. Non-positive stays "unspecified",
	// matching RecallInput.Limit and both HTTP peers.
	limit, err := wholeNumberArg(req, "limit", 0)
	if err != nil {
		return memory.PageRequest{}, toolError("validation_error", err.Error())
	}
	if limit > 0 {
		pr.Limit = limit
	}

	// A budget argument is read through a presence check rather than a
	// default, so an explicit 0 is distinguishable from an absent field. It
	// has to be: 0 is inside the type's range but outside its meaning, and a
	// zero budget can only ever return one oversized row plus a truncation
	// flag. Telling the caller is better than silently serving something
	// they did not ask for or silently ignoring what they did.
	for _, knob := range []struct {
		name string
		dst  *int
	}{
		{"budget_bytes", &pr.Budget.Bytes},
		{"budget_tokens", &pr.Budget.Tokens},
	} {
		raw, ok := req.GetArguments()[knob.name]
		if !ok || raw == nil {
			continue
		}
		v, err := wholeNumberArg(req, knob.name, 0)
		if err != nil {
			return memory.PageRequest{}, toolError("validation_error", err.Error())
		}
		if v <= 0 {
			return memory.PageRequest{}, toolError("validation_error",
				knob.name+" must be greater than 0; omit it for no ceiling")
		}
		*knob.dst = v
	}

	return pr, nil
}

// resolveHistoryPageRequest builds the paging half of a memory_history or
// knowledge_history call.
//
// It differs from the recall path in exactly one way, and the difference is
// load-bearing: the server-configured budget is NOT applied. History answers
// with a bare array unless the caller engages a knob, and a bare array has
// nowhere to report truncation — so a deployment-level ceiling there could
// only either flip the response shape for every caller (breaking the shipped
// web UI, whose bundle is not rebuilt here) or silently drop revisions with no
// manifest to say so. Neither is acceptable, so read.budget_bytes and
// read.budget_tokens are a recall/lookup ceiling only.
//
// A caller that passes budget_bytes on a history call still gets it honored,
// and gets the envelope that reports what it did.
//
// PayloadModeFull is passed because history serializes bare Revisions; it
// selects nothing here beyond making the byte accounting measure the shape
// actually written.
func (a *Adapter) resolveHistoryPageRequest(req mcp.CallToolRequest) (memory.PageRequest, *mcp.CallToolResult) {
	return a.resolvePageRequest(req, memory.PayloadModeFull, memory.Budget{})
}

// resolvePayloadMode picks the projection for one recall/lookup call.
//
// Precedence is per-call argument, then the config-wired adapter default,
// then memory.DefaultPayloadMode (D4). An explicit argument outside the
// closed vocabulary is a validation_error rather than a silent fallback:
// the caller is present and can be told, and quietly serving a different
// projection than the one asked for is exactly the failure this knob's
// contract has to avoid.
func (a *Adapter) resolvePayloadMode(req mcp.CallToolRequest) (memory.PayloadMode, *mcp.CallToolResult) {
	if raw := req.GetString("payload_mode", ""); raw != "" {
		mode := memory.PayloadMode(raw)
		if !mode.Valid() {
			return "", toolError("validation_error", "payload_mode must be one of keys|summary|full, got "+raw)
		}
		return mode, nil
	}
	if a.DefaultPayloadMode.Valid() {
		return a.DefaultPayloadMode, nil
	}
	return memory.DefaultPayloadMode, nil
}

// payloadModeArgDescription is the shared parameter blurb for the recall and
// lookup tools. One string so the two surfaces cannot drift apart.
const payloadModeArgDescription = "How much of each result to return: " +
	"`keys` (identity only: revision_id, memory_id, domain, namespace, memory_key, created_at — the browse/enumerate shape), " +
	"`summary` (keys + status, tags, confidence, payload.summary), or " +
	"`full` (everything, including payload.body and state). " +
	"Default comes from server config (read.payload_mode). " +
	"Under keys and summary each result carries `payload_mode`, so an absent body means withheld, not empty."

// searchModeArgDescription documents the retrieval-arm knob. Shared by
// memory_recall and tesseract_lookup so the two cannot describe it differently,
// and rendered from memory.SearchModeVocabulary so it cannot advertise a value
// the store rejects.
var searchModeArgDescription = "Which retrieval signal answers the query, under ranking=relevance: " +
	strings.Join(memory.SearchModeVocabulary(), " | ") + " (default: hybrid). " +
	"`hybrid` fuses keyword and semantic matching — the right default when you are describing a topic in your own words. " +
	"`lexical` runs keyword (BM25) matching alone, ordered by match strength. " +
	"Reach for it when you know the exact string you are looking for and fusion would only blur it: " +
	"a ticket ID (CW-20260519-0032), a function or symbol name, a namespace, a literal error message. " +
	"Semantic similarity is the wrong tool for an identifier — it returns things that MEAN something like your query, " +
	"and an identifier means nothing, it only matches. " +
	"Under `lexical` a punctuated token is matched as an adjacent phrase, so CW-20260519-0032 finds that ticket rather than " +
	"documents mentioning CW, 20260519 and 0032 in unrelated places; `score` is absent because the order is the signal. " +
	"`semantic` runs embedding (cosine) matching alone, ordered by similarity. " +
	"Reach for it when you know the words in the corpus will NOT be the words in your query. " +
	"It is a service_unavailable error when no embedder is configured — it never falls back to keyword matching, " +
	"because getting keyword results labeled semantic is worse than being told. " +
	"`lexical` and `semantic` require ranking=relevance (the default when a query is set); passing them with another ranking is a validation_error."

// Shared parameter blurbs for the budget/cursor knobs. One string per knob so
// the recall, lookup and history tools cannot drift apart, and so the HTTP
// peers' field docs have a single thing to agree with.
const (
	cursorArgDescription = "Opaque resume token from a previous response's `manifest.next_cursor`. " +
		"Omit to start at the beginning. A cursor is bound to the ordering it was issued for: " +
		"resuming it after changing `ranking`, `search_mode`, `namespaces`, `revision_scope`, `query`, any filter, or the reranker " +
		"is a validation_error, not a silently wrong page. Changing `payload_mode` or `limit` is fine — neither reorders anything."

	budgetBytesArgDescription = "Max serialized bytes for the results array. " +
		"Omit for no ceiling (default comes from server config read.budget_bytes). " +
		"When it binds, the response carries `manifest.truncated: true`, `truncation_reason: \"budget_bytes\"`, and a `next_cursor`. " +
		"At least one result is always returned even if it alone exceeds the budget, so paging can still make progress."

	budgetTokensArgDescription = "Max estimated tokens for the results array (~4 chars per token). " +
		"Omit for no ceiling (default comes from server config read.budget_tokens). " +
		"When both budgets are set the tighter one binds and `truncation_reason` names it."

	recallLimitArgDescription = "Max results per page (default 30). " +
		"Max 500 under `payload_mode` keys or summary, 100 under full — full carries payload bodies and costs roughly ten times as much per result. " +
		"Asking for more is clamped, never silently: the response reports `truncation_reason: \"payload_mode_limit_cap\"` and issues a `next_cursor`, " +
		"so the rows past the cap are reached by paging rather than by raising this."

	historyLimitArgDescription = "Max revisions to return (default: all, max 500). " +
		"Passing it switches the response from a bare array to `{results, manifest}`. " +
		"Chains are shallow today, so this is a ceiling against unbounded growth rather than a routine knob."

	manifestResultShapeDescription = "• **Envelope:** `{results, manifest}`. `manifest` carries `results_total`, `results_returned`, " +
		"`bytes_returned`, `tokens_estimate`, `truncated`, `truncation_reason`, and `next_cursor`. " +
		"Every field is always present: `truncated: false` means you got everything, and `next_cursor: null` means there is nothing left. " +
		"Never infer completeness from the array length.\n"
)

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

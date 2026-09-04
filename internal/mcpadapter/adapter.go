package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
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
// Read tools (tesseract_get, tesseract_history, context_view) work without a
// token for the context domain; the memory and knowledge domains check
// memory:read.
// Write tools (context_write, context_promote) require a capability token
// configured at startup via the Token field. context_promote checks a
// different scope per stage.
// context_pack is read-only; under shape=packet it respects namespace_globs
// from the token when present.
type Adapter struct {
	Store             *contextstore.Store
	Token             string // capability token for mutating ops; may be empty
	TypeRegistry      *contexttypes.Registry
	EmbeddingProvider embedcontracts.Embedder // optional; nil disables context_embed/context_search
	EmbeddingModel    string                  // model name passed to EmbeddingProvider (default: "")
	VectorIndex       embedding.VectorIndex   // optional; nil uses brute-force search via Store
	MemoryStore       *memory.Store           // optional; nil disables memory_write / memory_promote
	KnowledgeStore    *knowledge.Store        // optional; nil disables knowledge_write
	Logger            *slog.Logger            // optional; nil falls back to slog.Default()

	// DefaultPayloadMode is the projection applied to tesseract_recall
	// results when the call does not pass payload_mode.
	// Wired from config (read.payload_mode). Empty or unrecognized falls
	// back to memory.DefaultPayloadMode.
	DefaultPayloadMode memory.PayloadMode

	// DefaultBudget is the response ceiling applied to tesseract_recall when
	// the call passes no budget_bytes / budget_tokens.
	// Wired from config (read.budget_bytes, read.budget_tokens), which
	// defaults both to 0 — no ceiling. contextapi.Server resolves the same
	// two config fields for the HTTP peers.
	DefaultBudget memory.Budget
}

// resolvePageRequest builds the shared paging/budget half of a read call from
// MCP arguments.
//
// defaultBudget is the ceiling to apply when the call passes no budget of its
// own. tesseract_recall passes the config-wired a.DefaultBudget; the history
// path passes the zero Budget — see resolveHistoryPageRequest.
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
		return memory.PageRequest{}, toolError(codeValidationError, err.Error())
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
			return memory.PageRequest{}, toolError(codeValidationError, err.Error())
		}
		if v <= 0 {
			return memory.PageRequest{}, toolError(codeValidationError,
				knob.name+" must be greater than 0; omit it for no ceiling")
		}
		*knob.dst = v
	}

	// estimate_only rides on PageRequest because it is a knob on what gets
	// serialized, alongside payload_mode and the budgets, not on what gets
	// retrieved. It is read as a plain bool: false is both the zero value and
	// the meaning of an absent argument, so unlike the budgets there is no
	// third state to preserve and no presence check to do.
	pr.EstimateOnly = req.GetBool("estimate_only", false)

	return pr, nil
}

// resolveSimilarityMin reads the optional cosine floor from an MCP call.
//
// Read through a presence check rather than req.GetFloat's default, for the
// reason RecallFilters.SimilarityMin documents: 0.0 is a legal floor that means
// something different from absent, and a default-based read cannot tell them
// apart. GetFloat would also return 0 for a caller who passed the string
// "0.5" — silently installing a floor of zero where the caller asked for
// half — so the argument's type is checked rather than coerced.
//
// The VALUE is not validated here. Range and applicability are checked by
// RecallPage, which both MCP tools and both HTTP peers funnel through, so all
// four doors reject the same floors with the same message by construction
// rather than by four copies agreeing. This is the pattern search_mode
// established.
func resolveSimilarityMin(req mcp.CallToolRequest) (*float64, *mcp.CallToolResult) {
	raw, ok := req.GetArguments()["similarity_min"]
	if !ok || raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case float64:
		return &v, nil
	case float32:
		f := float64(v)
		return &f, nil
	case int:
		f := float64(v)
		return &f, nil
	case int64:
		f := float64(v)
		return &f, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, toolError(codeValidationError,
				"similarity_min must be a number, got "+v.String())
		}
		return &f, nil
	}
	return nil, toolError(codeValidationError,
		fmt.Sprintf("similarity_min must be a number, got %T", raw))
}

// estimateEnvelope renders an estimate_only response: the envelope the tool
// would have returned, minus the rows.
//
// The rule it implements is that every field of an estimate has a referent in
// the SAME tool's non-estimate response. That is what makes the identity
// checkable field by field rather than by eye: tesseract_recall answers with
// {results, facets, manifest}, so its estimate is {facets, manifest}. An
// estimate that advertised a field the real call cannot return would be the
// opposite of an identity, so facets stays `any` and is skipped when nil —
// a caller that has no histogram to report passes none rather than inventing
// an empty one.
//
// `results` is OMITTED, not emitted as an empty array. An empty array is
// indistinguishable from "your query matched nothing" — the same absent-versus-
// empty conflation that payload_mode carries `payload_mode` on every projected
// row to avoid. estimate_only: true is the positive marker that says withheld.
//
// facets is `any` and skipped when nil so the two tools' differently-shaped
// histograms both pass through unchanged; the caller builds it exactly as it
// would for a real response, so the estimate cannot compute it differently.
func estimateEnvelope(page memory.PagedRecall, facets any) map[string]any {
	out := map[string]any{
		"manifest":      page.Manifest,
		"estimate_only": true,
	}
	if facets != nil {
		out["facets"] = facets
	}
	return out
}

// resolveHistoryPageRequest builds the paging half of a tesseract_history call
// under the memory and knowledge domains.
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
			return "", toolError(codeValidationError, "payload_mode must be one of keys|summary|full, got "+raw)
		}
		return mode, nil
	}
	if a.DefaultPayloadMode.Valid() {
		return a.DefaultPayloadMode, nil
	}
	return memory.DefaultPayloadMode, nil
}

// payloadModeArgDescription is the parameter blurb for the recall tool. One
// string so the tool and its HTTP peers cannot drift apart.
const payloadModeArgDescription = "How much of each result to return: " +
	"`keys` (identity only: revision_id, memory_id, domain, namespace, memory_key, created_at — the browse/enumerate shape), " +
	"`summary` (keys + status, tags, confidence, payload.summary), or " +
	"`full` (everything, including payload.body and state). " +
	"Default comes from server config (read.payload_mode). " +
	"Under keys and summary each result carries `payload_mode`, so an absent body means withheld, not empty."

// searchModeArgDescription documents the retrieval-arm knob. Rendered from
// memory.SearchModeVocabulary so it cannot advertise a value the store
// rejects.
//
// Every capability sentence below is one the code actually has. Two earlier
// drafts of this string did not clear that bar — it advertised a
// service_unavailable code this path never emits, and multi-word phrase search
// the mode does not do — and a description that overstates is worse than a
// missing one, because an agent branches on it.
var searchModeArgDescription = "Which retrieval signal answers the query, under ranking=relevance: " +
	strings.Join(memory.SearchModeVocabulary(), " | ") + " (default: hybrid). " +
	"`hybrid` fuses keyword and semantic matching — the right default when you are describing a topic in your own words. " +
	"`lexical` runs keyword (BM25) matching alone, ordered by match strength, with every term required. " +
	"Reach for it when you know the exact string you are looking for and fusion would only blur it: " +
	"a ticket ID (CW-20260519-0032), a function or symbol name, a dotted or slashed path, a memory_key. " +
	"Semantic similarity is the wrong tool for an identifier — it returns things that MEAN something like your query, " +
	"and an identifier means nothing, it only matches. " +
	"`memory_key` is indexed and weighted ABOVE the prose columns, so searching an exact key returns the record that " +
	"OWNS that key ahead of the records that merely cite it. " +
	"`namespace` is deliberately NOT in the index — it is already an exact filter via the required `namespaces` " +
	"argument, so name it there rather than in the query. " +
	"What `lexical` binds as an adjacent phrase is a PUNCTUATION-joined run: CW-20260519-0032 finds that ticket rather than " +
	"documents mentioning CW, 20260519 and 0032 in unrelated places. " +
	"What it does NOT do is multi-word phrase search — space-separated words are each required to appear, not to appear together, " +
	"so `sqlite NOT NULL` finds documents carrying all three words anywhere rather than the phrase. " +
	"AND, OR and NOT are matched as literal words here, not as operators (they ARE operators under hybrid). " +
	"A query containing a non-ASCII letter is a validation_error rather than an empty page: lexical tokens are [A-Za-z0-9_] only. " +
	"`score` is absent under `lexical` because the order is the signal. " +
	"`semantic` runs embedding (cosine) matching alone, ordered by similarity. " +
	"Reach for it when you know the words in the corpus will NOT be the words in your query. " +
	"It is a similarity_unavailable error when no embedder is configured — it never falls back to keyword matching, " +
	"because getting keyword results labeled semantic is worse than being told. " +
	"`lexical` and `semantic` require ranking=relevance (the default when a query is set); passing them with another ranking is a validation_error."

// Shared parameter blurbs for the budget/cursor knobs. One string per knob so
// the recall and history tools cannot drift apart, and so the HTTP peers'
// field docs have a single thing to agree with.
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

	// similarityMinArgDescription documents the cosine floor. One string, so
	// the tool and its HTTP peers' field docs have one thing to agree with.
	//
	// Every sentence here is a claim the code makes good on and a test
	// exercises: the [-1, 1] range check, the 0.0-is-not-absent distinction,
	// the ranking/search_mode restriction, and validation_error as the code
	// emitted on each. The contrast with confidence_min is stated because the
	// two are easy to reach for interchangeably and measure different things.
	similarityMinArgDescription = "Floor on cosine similarity between your query and each result. " +
		"Results scoring below it are dropped before `limit` applies, so this narrows the qualifying set rather than thinning a page of it. " +
		"Range [-1, 1]; anything outside is a validation_error. " +
		// Rendered from the store's own statement of the rule rather than
		// restated here, so this description and the fingerprint guard's
		// failure message cannot describe two different floors.
		"Omit for no floor — 0.0 is NOT the same as omitting it: " + memory.SimilarityMinBoundaryRule + ". " +
		"Only honored where cosine similarity is the score: `ranking=similarity`, or `ranking=relevance` with `search_mode=semantic`. " +
		"Passing it under any other combination — including the default `search_mode=hybrid`, whose score is an RRF fusion rather than a similarity — is a validation_error rather than a silently ignored knob. " +
		"NOT the same as `confidence_min`, which filters on the confidence the memory's author recorded when writing it; a result can match your query closely and still have been written tentatively."

	// estimateOnlyArgDescription documents the pre-flight knob. Its central
	// claim — that the numbers equal what the same call without it returns —
	// is the one an agent will act on, so it is stated as the exact identity it
	// is rather than as an approximation.
	//
	// The shape rule is stated here, in the description, and not only in
	// estimateEnvelope's Go doc: a caller comparing an estimate against a real
	// read can see the shape but cannot see a comment, so the rule that ties
	// the two together has to travel with the tool.
	estimateOnlyArgDescription = "Return the envelope describing the results without the results themselves — the pre-flight for deciding whether to spend context on a read. " +
		"The response is the envelope THIS tool returns, minus `results`: `tesseract_recall` answers `{facets, manifest, estimate_only: true}`. " +
		"An estimate reports exactly what its own read would report and never a field that read cannot return, which is what makes every number in it checkable against the real call. " +
		"There is no `results` key at all; an absent array means withheld, never empty. " +
		"The numbers are exact, not approximate: `results_total`, `results_returned`, `bytes_returned` and `tokens_estimate` — and every facet count, where the tool has facets — are the same values the identical call WITHOUT this argument reports, under the same `payload_mode`. " +
		"Because `bytes_returned` depends on `payload_mode`, estimate under the mode you intend to read at. " +
		"It is worth most where a read would be cut short: under `budget_bytes` or `budget_tokens` the estimate carries the same `truncated`, `truncation_reason` and `next_cursor` the real read would. " +
		"`manifest.next_cursor` from an estimate is a valid cursor for the real read — this changes what is serialized, never which rows match or in what order."

	// touchLoopDescription states the reinforcement contract on the two tools
	// that return ranked results. Shared so they cannot describe the loop
	// differently, and phrased as the default workflow rather than as an option,
	// because an agent that reads it as optional will not do it — and a
	// reinforcement signal nobody sends is the state this loop exists to leave.
	//
	// The under- vs over-reporting sentence is caller guidance the ranking
	// depends on and is worded to be read as a rule, not a preference.
	touchLoopDescription = "• **Results are unreinforced until you touch them.** Recall does not bump `activation` — being returned by a search is the ranker's guess about what you need, and letting a guess reinforce itself is how popular-because-returned beats actually-useful within a few cycles. " +
		"So the loop has three steps, not two: **recall → use → `tesseract_touch`**. When you have finished reasoning, pass the `revision_id`s that actually shaped the turn to `tesseract_touch`. That is what tells the ranking your guess was right, and it is the only input activation has. " +
		"Touch only what genuinely shaped the turn. **Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal.**\n"

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
	// Domain-specific writes stay gated on their own store: memory_write needs
	// a memory store, knowledge_write needs a knowledge store, and their
	// required-field sets differ materially (D10).
	if a.MemoryStore != nil {
		a.registerMemoryTools(s)
	}
	if a.KnowledgeStore != nil {
		a.registerKnowledgeTools(s)
	}
	// The cross-domain reads are gated on what they actually need, not on which
	// field happens to be set. tesseract_recall needs some revision store;
	// registerCrossDomainReadTools makes the same call for the revision-level
	// ops and registers the keyed reads unconditionally, answering
	// domain_unavailable per domain.
	if a.revisionStore() != nil {
		a.registerRecallTool(s)
	}
	a.registerCrossDomainReadTools(s)
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
		return toolError(codeAuthRequired, "no capability token configured for mutating operations"), contextstore.AuthToken{}
	}
	claims, err := a.Store.ValidateAuthTokenWithClaims(ctx, a.Token)
	if err != nil {
		return toolError(codeAuthRequired, "token invalid or expired: "+err.Error()), contextstore.AuthToken{}
	}
	for _, s := range claims.Scopes {
		if s == scope {
			return nil, claims
		}
	}
	return toolError(codeInsufficientScope, "token does not have scope: "+scope), claims
}

// toolError returns a CallToolResult containing a JSON error body agents can parse.
//
// code is an errorCode rather than a string so that a call site cannot name a
// code that does not exist. The wire shape is unchanged — the constant's value
// is the same literal that used to be written here.
func toolError(code errorCode, message string) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]string{"code": string(code), "message": message})
	return mcp.NewToolResultText(string(body))
}

// toolJSON marshals v to JSON and wraps it as a tool result.
func toolJSON(v any) *mcp.CallToolResult {
	body, _ := json.Marshal(v)
	return mcp.NewToolResultText(string(body))
}

package mcpadapter

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("context_view",
		mcp.WithDescription("Evaluate a view over the context store and return matching records. "+
			"`include_meta` selects between two evaluation arms — see its description; they differ in more than whether metadata is attached. "+
			"See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("selector", mcp.Description(viewSelectorArgDescription)),
		mcp.WithString("namespaces", mcp.Description("Comma-separated namespace glob patterns, e.g. \"user/memory/*,app/test/session/*\". "+
			"Shorthand for `selector` in its glob form; passing both is a validation_error.")),
		mcp.WithString("revision_scope", mcp.Description("head or all (default: head). Ignored when `selector` is a JSON object — put revision_scope inside it; passing both is a validation_error.")),
		mcp.WithBoolean("include_payload", mcp.Description("Include record payloads in the response (default false). "+
			"Only the `include_meta: true` arm can carry payloads; passing true without include_meta is a validation_error rather than a silently dropped knob.")),
		mcp.WithBoolean("include_meta", mcp.Description(viewIncludeMetaArgDescription)),
		mcp.WithNumber("limit", mcp.Description("Max records to return. Under the default arm: default 10, max 25, returns summaries — use `tesseract_get` with domain=\"context\" for the full record. "+
			"Under `include_meta: true`: overrides selector.limit (0 = use selector's own limit or the store default).")),
	), a.handleContextView)

	a.addTool(s, mcp.NewTool("context_write",
		mcp.WithDescription("Write a record to a namespace. Requires 'write' scope in the configured capability token. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Target namespace, e.g. app/test/session/task-001")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Record key")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON payload as a string, e.g. '{\"status\":\"in_progress\"}'")),
		mcp.WithString("actor", mcp.Description("Actor identity, e.g. app:my-agent (default: mcp-agent)")),
		mcp.WithString("record_type", mcp.Description("Record type tag (default: state)")),
	), a.handleWrite)

	a.addTool(s, mcp.NewTool("context_promote",
		mcp.WithDescription("Move a record from an app namespace to a user namespace, in three stages. "+
			"`stage` selects which stage runs AND which capability scope is required — see its description. "+
			"See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("stage", mcp.Required(), mcp.Description(promoteStageArgDescription)),
		// Per-stage requiredness is enforced in the handler, not in the schema:
		// each stage needs a different subset, and a schema-level Required()
		// would demand `request` fields on an `apply` call.
		mcp.WithString("source_namespace", mcp.Description("stage=request: source namespace (must be in app/*)")),
		mcp.WithString("source_key", mcp.Description("stage=request: source record key")),
		mcp.WithString("target_namespace", mcp.Description("stage=request: target namespace (typically user/memory/*)")),
		mcp.WithString("target_key", mcp.Description("stage=request: target record key")),
		mcp.WithString("reason", mcp.Description("stage=request: human-readable reason for the promotion")),
		mcp.WithString("request_id", mcp.Description("stage=approve, stage=apply: the promotion request ID to act on")),
		mcp.WithString("notes", mcp.Description("stage=approve: optional approval notes")),
		mcp.WithString("actor", mcp.Description("Actor identity (default: mcp-agent under stage=request, user under approve/apply)")),
	), a.handlePromote)

	a.addTool(s, mcp.NewTool("context_promote_list",
		mcp.WithDescription("List promotion requests. Read-only, no token required. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("status", mcp.Description("Filter by status: pending|approved|applied|all (default: pending)")),
		mcp.WithNumber("limit", mcp.Description("Max requests to return (default 10, max 25)")),
	), a.handlePromoteList)

	// NOTE: The MCP tool name retains "broker" for continuity with consumers.
	// Internally this is the context query planner, not the universal
	// ContextBroker.
	a.addTool(s, mcp.NewTool("context_broker",
		mcp.WithDescription("Plan a context fetch for a given intent, and optionally execute it. No auth required. "+
			"`execute` selects between returning the plan and returning the records the plan selects. "+
			"See `tesseract_skills start-here` for the primitive model."),
		mcp.WithBoolean("execute", mcp.Description("false (default): return the plan only — namespace patterns, budget, and rationale, with no store read. "+
			"true: run the plan and return the records it selects, plus a manifest and the same rationale. "+
			"There is no HTTP peer for execute=true; POST /v1/broker/plan is the peer of the default arm.")),
		mcp.WithString("intent", mcp.Description("Intent: resume_task|boot_project|review_session|custom (default: custom)")),
		mcp.WithString("summary", mcp.Description("Task summary for keyword extraction (used with resume_task intent)")),
		mcp.WithNumber("budget_items", mcp.Description("Max items budget (default 50)")),
		mcp.WithNumber("budget_tokens", mcp.Description("Max tokens estimate budget (default 4000)")),
		mcp.WithNumber("payload_max_bytes", mcp.Description(payloadMaxBytesArgDescription)),
	), a.handleContextBroker)

	a.addTool(s, mcp.NewTool("context_namespace_register",
		mcp.WithDescription("Register a namespace with ownership policy. Requires 'namespace.admin' scope. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace path to register")),
		mcp.WithString("owner_type", mcp.Required(), mcp.Description("Ownership type: user or app")),
		mcp.WithString("owner_id", mcp.Required(), mcp.Description("Owner identity (e.g. my-agent)")),
	), a.handleNamespaceRegister)

	a.addTool(s, mcp.NewTool("context_registry_list",
		mcp.WithDescription("List what the context domain has registered: types, views, or namespaces. "+
			"`kind` selects which registry is read; each answers with its own shape. No auth required. "+
			"See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("kind", mcp.Required(), mcp.Description(registryKindArgDescription)),
		mcp.WithString("name", mcp.Description("kind=namespaces only: return the single named namespace's ownership policy instead of the list. "+
			"Answers `{namespace, owner_type, owner_id, policy}` — a different shape from the list, because it is a different question. "+
			"Not accepted under kind=types or kind=views; passing it there is a validation_error rather than a silently ignored knob.")),
		mcp.WithString("prefix", mcp.Description("kind=namespaces only: filter to namespaces whose name starts with this string prefix (e.g. \"user/chrispian/\", \"app/\"). Pure string-prefix match, not a glob.")),
		mcp.WithNumber("limit", mcp.Description("kind=namespaces only: max namespaces to return (default 10, max 25)")),
	), a.handleRegistryList)

	a.addTool(s, mcp.NewTool("context_audit",
		mcp.WithDescription("Query the audit event log. No auth required. See `tesseract_skills start-here` for the primitive model."),
		mcp.WithString("namespace", mcp.Description("Filter by exact namespace")),
		mcp.WithString("event_type", mcp.Description("Filter by event type (e.g. write, promote)")),
		mcp.WithNumber("limit", mcp.Description("Max events to return (default 10, max 25)")),
		mcp.WithNumber("cursor", mcp.Description("Pagination cursor (ID from previous response's next_cursor)")),
	), a.handleAudit)
}

// ── Merged-tool argument vocabulary ──────────────────────────────────────────
//
// Each knob below SELECTS a behavior rather than annotating one. That
// distinction is the whole point of merging two tools into one, and it is
// checkable: every value named here routes to a different code path, and a
// value outside the stated set is a validation_error rather than a silent
// fallback to the default arm.

const (
	viewSelectorArgDescription = "What to select, in either of two forms. " +
		"GLOB FORM: a comma-separated namespace glob list, e.g. \"user/memory/*,app/test/session/*\" — the same thing `namespaces` takes. " +
		"SELECTOR FORM: a JSON object matching contextstore.Selector (namespaces, keys, revision_scope, order, limit, tags_any, types, statuses), " +
		"recognized by a leading `{`. The selector form is the only way to filter on keys, tags, types or statuses. " +
		"Omit it and pass `namespaces` instead if globs are all you need; passing both is a validation_error."

	viewIncludeMetaArgDescription = "Which evaluation arm answers, NOT merely whether metadata is attached. " +
		"false (default): the store is queried for heads matching the selector, results are filtered by the capability token's namespace globs when a token is configured, " +
		"and the answer is the shared budget envelope of record SUMMARIES — no payloads, ever. " +
		"true: the selector is evaluated with the store's view semantics (deterministic sort, selector limit, truncation flag) and the answer is `{items, evaluation_meta}` " +
		"carrying full records, with payloads when `include_payload` is set. " +
		"The two arms differ in one way that is not about shape: the `true` arm does NOT filter by the token's namespace globs, because it is the exact peer of " +
		"POST /v1/views/evaluate, which does not either. Neither arm can reach a record the other cannot — the same pair of behaviors was reachable before this merge " +
		"as two separate tools — but do not read `include_meta` as a display knob."

	promoteStageArgDescription = "Which stage of the promotion workflow runs. Each stage requires a DIFFERENT capability scope, and the scope is checked for the stage you named: " +
		"`request` (scope promote.request) records a request to copy a record from an app namespace to a user namespace; " +
		"`approve` (scope promote.approve) marks a pending request approved; " +
		"`apply` (scope promote.apply) writes the approved record to its target namespace. " +
		"There is no default: an absent, empty or unrecognized stage is a validation_error and nothing is read, written or authorized. " +
		"Holding one stage's scope grants that stage only — naming a stage you lack the scope for is an insufficient_scope error, exactly as calling a separate tool for it was."

	payloadMaxBytesArgDescription = "Cap on the bytes of each record's payload that come back. Omit or 0 for no cap. " +
		"When the cap binds, the item carries NO `payload` key at all; instead it carries `payload_head` (a JSON string holding the first N bytes), " +
		"`payload_truncated: true`, and `payload_bytes` (the full payload's size). An absent `payload` therefore means capped, never empty. " +
		"This replaces the former `payload_mode: head_only`, which cut the payload mid-JSON and left the `payload` field unparseable. " +
		"The former `payload_mode=head_only` is now `payload_max_bytes=512`."

	registryKindArgDescription = "Which registry to read. Each answers a different shape, because each lists a different thing: " +
		"`types` → `{types: [...]}`, the registered context types and their metadata (peer of GET /v1/context/types); " +
		"`views` → `{views: [...]}`, the registered views and their type configurations (peer of GET /v1/context/views); " +
		"`namespaces` → the shared budget envelope of registered namespaces and their ownership policies (peer of GET /v1/namespaces/list), " +
		"or, with `name` set, that one namespace's policy (peer of GET /v1/namespaces/get). " +
		"There is no default: an absent or unrecognized kind is a validation_error."
)

// ── Merged-tool dispatchers ──────────────────────────────────────────────────

// handleContextView serves the merged context_view. `include_meta` selects the
// arm; see viewIncludeMetaArgDescription for what each one does.
//
// The two arms are the pre-merge handlers unchanged — handleViewSummaries was
// context_view and handleViewsEvaluate was views_evaluate — so preservation is
// a property of the routing rather than of two reimplementations agreeing.
func (a *Adapter) handleContextView(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawSelector := strings.TrimSpace(req.GetString("selector", ""))
	nsArg := strings.TrimSpace(req.GetString("namespaces", ""))
	revScopeArg := strings.TrimSpace(req.GetString("revision_scope", ""))
	includeMeta := req.GetBool("include_meta", false)
	includePayload := req.GetBool("include_payload", false)

	if rawSelector != "" && nsArg != "" {
		return toolError("validation_error", "pass either selector or namespaces, not both"), nil
	}
	if includePayload && !includeMeta {
		return toolError("validation_error",
			"include_payload is only honored under include_meta: true; the default arm returns summaries and never payloads"), nil
	}

	var sel contextstore.Selector
	selectorIsJSON := strings.HasPrefix(rawSelector, "{")
	switch {
	case selectorIsJSON:
		if err := json.Unmarshal([]byte(rawSelector), &sel); err != nil {
			return toolError("validation_error", "selector must be a JSON object or a comma-separated glob list: "+err.Error()), nil //nolint:nilerr // MCP tool pattern
		}
		if revScopeArg != "" {
			return toolError("validation_error",
				"revision_scope belongs inside a JSON selector; pass it as selector.revision_scope, not as a separate argument"), nil
		}
	case rawSelector != "":
		sel.Namespaces = splitCommaList(rawSelector)
	default:
		sel.Namespaces = splitCommaList(nsArg)
	}

	if includeMeta {
		if len(sel.Namespaces) == 0 && !selectorIsJSON {
			return toolError("validation_error", "selector is required"), nil
		}
		return a.handleViewsEvaluate(ctx, sel, req)
	}
	if selectorIsJSON {
		// The default arm reads exactly two selector fields; anything else in a
		// JSON selector would be accepted and silently not applied.
		if len(sel.Keys) > 0 || len(sel.Order) > 0 || sel.Limit != 0 ||
			len(sel.TagsAny) > 0 || len(sel.Types) > 0 || len(sel.Statuses) > 0 {
			return toolError("validation_error",
				"the default arm honors only selector.namespaces and selector.revision_scope; "+
					"pass include_meta: true to evaluate the full selector"), nil
		}
		revScopeArg = sel.RevisionScope
	}
	if revScopeArg == "" {
		revScopeArg = "head"
	}
	sel.RevisionScope = revScopeArg
	return a.handleViewSummaries(ctx, sel, req)
}

// handlePromote serves the merged context_promote. `stage` selects the arm AND
// the capability scope that arm checks.
//
// Routing happens before any scope check, store read, or write, and an
// unrecognized stage falls through to a validation_error — so a stage that
// fails to parse fails CLOSED. Each arm keeps its own a.checkScope call, so the
// scope a caller must hold is unchanged from when the three tools were separate.
func (a *Adapter) handlePromote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	switch req.GetString("stage", "") {
	case "request":
		return a.handlePromoteRequest(ctx, req)
	case "approve":
		return a.handlePromoteApprove(ctx, req)
	case "apply":
		return a.handlePromoteApply(ctx, req)
	default:
		return toolError("validation_error",
			"stage must be one of request|approve|apply, got "+strconv.Quote(req.GetString("stage", ""))), nil
	}
}

// handleContextBroker serves the merged context_broker. `execute` selects
// between planning and running the plan.
func (a *Adapter) handleContextBroker(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if req.GetBool("execute", false) {
		return a.handleContextFetch(ctx, req)
	}
	// The planning arm reads no payloads, so a byte cap there would be a knob
	// reporting it is honoring something it never consults.
	if raw, ok := req.GetArguments()["payload_max_bytes"]; ok && raw != nil {
		return toolError("validation_error",
			"payload_max_bytes applies only under execute: true; the planning arm returns no records"), nil
	}
	if errResult := rejectRetiredPayloadMode(req); errResult != nil {
		return errResult, nil
	}
	return a.handleContextPlan(ctx, req)
}

// handleRegistryList serves the merged context_registry_list. `kind` selects
// which registry is read.
func (a *Adapter) handleRegistryList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	kind := req.GetString("kind", "")
	name := strings.TrimSpace(req.GetString("name", ""))

	if kind == "types" || kind == "views" {
		// These two registries take no arguments. Accepting a knob and not
		// applying it is the failure this merge exists to avoid.
		for _, knob := range []string{"name", "prefix", "limit"} {
			if raw, ok := req.GetArguments()[knob]; ok && raw != nil && raw != "" {
				return toolError("validation_error", knob+" is not accepted under kind="+kind), nil
			}
		}
	}

	switch kind {
	case "types":
		return a.handleTypesList(ctx, req)
	case "views":
		return a.handleViewsList(ctx, req)
	case "namespaces":
		if name != "" {
			return a.namespaceShowResult(ctx, name), nil
		}
		return a.handleNamespacesList(ctx, req)
	default:
		return toolError("validation_error",
			"kind must be one of types|views|namespaces, got "+strconv.Quote(kind)), nil
	}
}

// ── Shared argument helpers ──────────────────────────────────────────────────

// splitCommaList parses a comma-separated argument into trimmed, non-empty
// entries. Returns nil for an empty input, which selectors read as "unset".
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// resolvePayloadMaxBytes reads the payload byte cap for the packet-shaped
// tools, and rejects the retired payload_mode vocabulary on the way past.
//
// A negative cap is an error rather than a clamp: it is outside the knob's
// meaning, and quietly reading it as "no cap" would return MORE context than
// the caller asked for — the direction of failure this whole surface is trying
// to close off.
func resolvePayloadMaxBytes(req mcp.CallToolRequest) (int, *mcp.CallToolResult) {
	if errResult := rejectRetiredPayloadMode(req); errResult != nil {
		return 0, errResult
	}
	n, err := wholeNumberArg(req, "payload_max_bytes", 0)
	if err != nil {
		return 0, toolError("validation_error", err.Error())
	}
	if n < 0 {
		return 0, toolError("validation_error", "payload_max_bytes must be >= 0; omit it or pass 0 for no cap")
	}
	return n, nil
}

// rejectRetiredPayloadMode fails closed on the packet-shaped tools' former
// payload_mode vocabulary.
//
// `head_only` used to cut a payload mid-JSON, and every OTHER value — `keys`,
// `summary`, a typo — fell through to full payloads with no error at all. Both
// halves of that are fixed here by refusing the argument: `payload_mode` now
// means exactly one thing across the surface (the keys|summary|full projection
// on the recall/lookup tools), and these tools cap bytes with
// payload_max_bytes instead.
//
// `full` is accepted as a no-op because it was the default and means the same
// thing here as it does everywhere else: everything.
func rejectRetiredPayloadMode(req mcp.CallToolRequest) *mcp.CallToolResult {
	raw := strings.TrimSpace(req.GetString("payload_mode", ""))
	if raw == "" || raw == "full" {
		return nil
	}
	return toolError("validation_error",
		"payload_mode is not a projection knob on this tool: it accepts only \"full\". "+
			"The former payload_mode=head_only is now payload_max_bytes=512. Got: "+raw)
}

// capPayload applies a payload_max_bytes cap to one item.
//
// When the cap binds the `payload` key is REMOVED and replaced by
// `payload_head` (a JSON string), `payload_truncated` and `payload_bytes`.
// It cannot simply shorten `payload`: the prefix of a JSON object is not
// valid JSON, and a json.RawMessage holding invalid JSON makes the whole
// enclosing json.Marshal fail — which is how the former head_only mode turned
// an oversized record into an EMPTY tool result rather than a truncated one.
// See TestPayloadMaxBytes_CappedItemStaysValidJSON.
//
// Returns the number of payload bytes actually serialized, for the manifest's
// byte and token accounting.
func capPayload(item map[string]any, payload []byte, maxBytes int) int {
	if maxBytes <= 0 || len(payload) <= maxBytes {
		item["payload"] = json.RawMessage(payload)
		return len(payload)
	}
	delete(item, "payload")
	item["payload_head"] = string(payload[:maxBytes])
	item["payload_truncated"] = true
	item["payload_bytes"] = len(payload)
	return maxBytes
}

// --- Read tools ---
//
// handleContextHead and handleContextHistory are not registered as tools of
// their own. They are the `domain: "context"` arms of tesseract_get and
// tesseract_history, called from crossdomain_read_tools.go. Their shapes are
// this domain's own — a record projection and the go-mcp budget envelope — and
// differ from what the memory and knowledge arms return, which is pinned in
// TestCrossDomainGet_ContextArmShapeIsHandStated and
// TestCrossDomainHistory_ContextArmKeepsItsOwnEnvelope.

func (a *Adapter) handleContextHead(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if ns == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}
	rec, err := a.Store.Head(ctx, ns, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolError("not_found", fmt.Sprintf("no head record for %s/%s", ns, key)), nil
		}
		return toolError("internal_error", err.Error()), nil
	}
	return toolJSON(recordJSON(rec)), nil
}

func (a *Adapter) handleContextHistory(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if ns == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)
	records, err := a.Store.History(ctx, ns, key, limit)
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}
	summaries := make([]map[string]any, len(records))
	for i, rec := range records {
		summaries[i] = recordJSON(rec)
	}
	env := budget.Apply(summaries, budget.Config{Limit: limit}, "%d revisions available. Use tesseract_get with domain, namespace and key for full record content.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

// handleViewSummaries is the default arm of context_view: heads matching the
// selector, filtered by the token's namespace globs, rendered as summaries in
// the shared budget envelope. sel carries only Namespaces and RevisionScope —
// handleContextView rejects a selector asking for anything else on this arm.
func (a *Adapter) handleViewSummaries(_ context.Context, sel contextstore.Selector, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)
	records, err := a.Store.Select(ctx, sel)
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}

	// Filter by token namespace globs if a token is present.
	if claims, ok := a.loadClaims(ctx); ok {
		records = filterByGlobs(records, claims.NamespaceGlobs)
	}

	// Build summary items (omit full payload from list view).
	summaries := make([]map[string]any, len(records))
	for i, rec := range records {
		s := recordJSON(rec)
		s["status"] = "active"
		s["updated_at"] = rec.CreatedAt
		summaries[i] = s
	}
	env := budget.Apply(summaries, budget.Config{Limit: limit}, "%d records available. Use tesseract_get with domain, namespace and key for full record content.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

// --- Packet arm of context_pack ---

// handlePacket is the shape=packet arm of context_pack: namespace globs plus
// pinned records, bounded by an item and token budget, with a manifest saying
// what was included and why it stopped.
func (a *Adapter) handlePacket(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()

	namespaces := splitCommaList(req.GetString("namespaces", ""))

	includePins := req.GetBool("include_pins", true)
	maxItems := req.GetInt("max_items", 50)
	maxTokens := req.GetInt("max_tokens_estimate", 8000)
	payloadMaxBytes, errResult := resolvePayloadMaxBytes(req)
	if errResult != nil {
		return errResult, nil
	}

	// Load token globs for optional namespace filtering.
	var tokenGlobs []string
	if claims, ok := a.loadClaims(ctx); ok {
		tokenGlobs = claims.NamespaceGlobs
	}

	reqID := newMCPRequestID()
	var items []map[string]any
	pinsIncluded := 0
	bytesSoFar := 0
	tokensSoFar := 0
	itemsTotal := 0
	truncated := false
	truncationReason := ""
	sources := map[string]int{}

	budgetExceeded := func() bool {
		if maxItems > 0 && len(items) >= maxItems {
			truncationReason = "budget.max_items"
			return true
		}
		if maxTokens > 0 && tokensSoFar >= maxTokens {
			truncationReason = "budget.max_tokens_estimate"
			return true
		}
		return false
	}

	addRecord := func(rec contextstore.Record) bool {
		if budgetExceeded() {
			return false
		}
		item := recordJSON(rec)
		served := capPayload(item, rec.Payload, payloadMaxBytes)
		items = append(items, item)
		bytesSoFar += served
		tokensSoFar += contextstore.EstimateTokens(rec.Payload[:served])
		parts := strings.SplitN(rec.Namespace, "/", 3)
		nsKey := rec.Namespace
		if len(parts) >= 2 {
			nsKey = parts[0] + "/" + parts[1]
		}
		sources[nsKey]++
		return true
	}

	// Prepend pins (filtered by token globs when present).
	if includePins {
		pins, err := a.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{"user/pins/*"},
			RevisionScope: "head",
		})
		if err == nil {
			if len(tokenGlobs) > 0 {
				pins = filterByGlobs(pins, tokenGlobs)
			}
			for _, rec := range pins {
				if addRecord(rec) {
					pinsIncluded++
				}
			}
		}
	}

	// Main selector.
	candidates, err := a.Store.Select(ctx, contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
	})
	if err != nil {
		return toolError("selector_error", err.Error()), nil
	}

	// Filter by token globs when present.
	if len(tokenGlobs) > 0 {
		candidates = filterByGlobs(candidates, tokenGlobs)
	}

	itemsTotal = len(candidates)
	for _, rec := range candidates {
		if !addRecord(rec) {
			truncated = true
			break
		}
	}

	if items == nil {
		items = []map[string]any{}
	}

	manifest := map[string]any{
		"request_id":        reqID,
		"pins_included":     pinsIncluded,
		"items_total":       itemsTotal,
		"items_returned":    len(items) - pinsIncluded,
		"bytes_returned":    bytesSoFar,
		"tokens_estimate":   tokensSoFar,
		"truncated":         truncated,
		"truncation_reason": truncationReason,
		"sources":           sources,
	}

	return toolJSON(map[string]any{"items": items, "manifest": manifest}), nil
}

// --- Write tools ---

func (a *Adapter) handleWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, claims := a.checkScope(ctx, "write")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	key := req.GetString("key", "")
	payloadStr := req.GetString("payload", "")

	// Enforce token namespace globs on write target.
	if !globsPermit(claims.NamespaceGlobs, ns) {
		return toolError("namespace_not_permitted", "token namespace globs do not permit writing to: "+ns), nil
	}
	if ns == "" || key == "" || payloadStr == "" {
		return toolError("validation_error", "namespace, key, and payload are required"), nil
	}

	var payload json.RawMessage
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return toolError("validation_error", "payload must be valid JSON: "+err.Error()), nil
	}

	actor := req.GetString("actor", "mcp-agent")

	rec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: ns,
		Key:       key,
		Actor:     actor,
		Payload:   payload,
	})
	if err != nil {
		return toolError("write_failed", err.Error()), nil
	}

	_ = a.Store.EmitWrite(ctx, actor, ns, key, rec.Revision, rec.RecordID, json.RawMessage(`{"source":"mcp"}`))

	return toolJSON(map[string]any{
		"record_id": rec.RecordID,
		"revision":  rec.Revision,
		"namespace": rec.Namespace,
		"key":       rec.Key,
	}), nil
}

func (a *Adapter) handlePromoteRequest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "promote.request")
	if errResult != nil {
		return errResult, nil
	}

	srcNS := req.GetString("source_namespace", "")
	srcKey := req.GetString("source_key", "")
	tgtNS := req.GetString("target_namespace", "")
	tgtKey := req.GetString("target_key", "")
	reason := req.GetString("reason", "")
	actor := req.GetString("actor", "mcp-agent")

	if srcNS == "" || srcKey == "" || tgtNS == "" || tgtKey == "" {
		return toolError("validation_error", "source_namespace, source_key, target_namespace, and target_key are required"), nil
	}

	srcHead, err := a.Store.Head(ctx, srcNS, srcKey)
	if err != nil {
		return toolError("validation_error", fmt.Sprintf("source record not found: %v", err)), nil
	}

	requestID := "req-" + newMCPRequestID()[4:]
	now := time.Now().UTC().Format(time.RFC3339)

	pr := contextstore.PromoteRequest{
		Type:             "promote.request",
		RequestID:        requestID,
		SourceNamespace:  srcHead.Namespace,
		SourceKey:        srcHead.Key,
		SourceRevisionID: srcHead.RecordID,
		SourceChecksum:   srcHead.Checksum,
		TargetNamespace:  tgtNS,
		TargetKey:        tgtKey,
		Reason:           reason,
		Status:           "pending",
		RequestedAt:      now,
		RequestedBy:      actor,
	}
	payload, _ := json.Marshal(pr)

	namespace := "app/mcp-agent/promotions"
	_, err = a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: namespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   payload,
	})
	if err != nil {
		return toolError("promote_failed", err.Error()), nil
	}

	_ = a.Store.EmitPromote(ctx, contextstore.EventPromoteRequest, actor, srcNS, srcKey, srcHead.Revision, srcHead.RecordID,
		json.RawMessage(fmt.Sprintf(`{"request_id":%q,"target_namespace":%q,"source":"mcp"}`, requestID, tgtNS)))

	return toolJSON(map[string]any{
		"request_id": requestID,
		"status":     "pending",
	}), nil
}

// --- Promote lifecycle tools ---

func (a *Adapter) handlePromoteList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	status := req.GetString("status", "pending")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)

	recs, err := a.Store.Select(ctx, contextstore.Selector{
		Namespaces:    []string{"app/*/promotions"},
		RevisionScope: "head",
	})
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}

	var items []map[string]any
	for _, rec := range recs {
		var pr contextstore.PromoteRequest
		if err := json.Unmarshal(rec.Payload, &pr); err != nil || pr.Type != "promote.request" {
			continue
		}
		if status != "all" && pr.Status != status {
			continue
		}
		items = append(items, map[string]any{
			"request_id":       pr.RequestID,
			"status":           pr.Status,
			"source_namespace": pr.SourceNamespace,
			"source_key":       pr.SourceKey,
			"target_namespace": pr.TargetNamespace,
			"target_key":       pr.TargetKey,
			"reason":           pr.Reason,
			"requested_at":     pr.RequestedAt,
			"requested_by":     pr.RequestedBy,
		})
	}
	if items == nil {
		items = []map[string]any{}
	}
	env := budget.Apply(items, budget.Config{Limit: limit}, "%d promotion requests available. Use context_promote with stage=approve or stage=apply for specific requests.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

func (a *Adapter) handlePromoteApprove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "promote.approve")
	if errResult != nil {
		return errResult, nil
	}

	requestID := req.GetString("request_id", "")
	if requestID == "" {
		return toolError("validation_error", "request_id is required"), nil
	}
	notes := req.GetString("notes", "")
	actor := req.GetString("actor", "user")

	pr, reqNamespace, err := a.Store.GetPromoteRequest(ctx, requestID)
	if err != nil {
		return toolError("not_found", err.Error()), nil
	}
	if pr.Status != "pending" {
		return toolError("invalid_state", fmt.Sprintf("request status is %q, must be pending to approve", pr.Status)), nil
	}

	approvalID := "appr-" + newMCPRequestID()[4:]
	now := time.Now().UTC().Format(time.RFC3339)

	pa := contextstore.PromoteApproval{
		Type:             "promote.approve",
		ApprovalID:       approvalID,
		RequestID:        requestID,
		RequestNamespace: reqNamespace,
		ApprovedAt:       now,
		ApprovedBy:       actor,
		Notes:            notes,
	}
	approvalPayload, _ := json.Marshal(pa)
	if _, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: "user/promotions",
		Key:       approvalID,
		Actor:     actor,
		Payload:   approvalPayload,
	}); err != nil {
		return toolError("approve_failed", err.Error()), nil
	}

	pr.Status = "approved"
	pr.ApprovalID = approvalID
	pr.ApprovedBy = actor
	updPayload, _ := json.Marshal(pr)
	updRec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   updPayload,
	})
	if err != nil {
		return toolError("approve_failed", err.Error()), nil
	}

	_ = a.Store.EmitPromote(ctx, contextstore.EventPromoteApprove, actor, reqNamespace, requestID, updRec.Revision, updRec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"approval_id":%q,"source":"mcp"}`, approvalID)))

	return toolJSON(map[string]any{
		"approval_id": approvalID,
		"request_id":  requestID,
		"status":      "approved",
	}), nil
}

func (a *Adapter) handlePromoteApply(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "promote.apply")
	if errResult != nil {
		return errResult, nil
	}

	requestID := req.GetString("request_id", "")
	if requestID == "" {
		return toolError("validation_error", "request_id is required"), nil
	}
	actor := req.GetString("actor", "user")

	pr, reqNamespace, err := a.Store.GetPromoteRequest(ctx, requestID)
	if err != nil {
		return toolError("not_found", err.Error()), nil
	}
	if pr.Status != "approved" {
		return toolError("invalid_state", fmt.Sprintf("request status is %q, must be approved to apply", pr.Status)), nil
	}

	srcRec, err := a.Store.GetByRecordID(ctx, pr.SourceRevisionID)
	if err != nil {
		return toolError("not_found", fmt.Sprintf("source record not found: %v", err)), nil
	}

	newRec, err := a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: pr.TargetNamespace,
		Key:       pr.TargetKey,
		Actor:     actor,
		Payload:   srcRec.Payload,
	})
	if err != nil {
		return toolError("apply_failed", err.Error()), nil
	}

	pr.Status = "applied"
	appliedPayload, _ := json.Marshal(pr)
	_, _ = a.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       requestID,
		Actor:     actor,
		Payload:   appliedPayload,
	})

	_ = a.Store.EmitPromote(ctx, contextstore.EventPromote, actor, pr.TargetNamespace, pr.TargetKey, newRec.Revision, newRec.RecordID,
		json.RawMessage(fmt.Sprintf(`{"request_id":%q,"source":"mcp"}`, requestID)))

	return toolJSON(map[string]any{
		"record_id":        newRec.RecordID,
		"request_id":       requestID,
		"status":           "applied",
		"target_namespace": pr.TargetNamespace,
		"target_key":       pr.TargetKey,
	}), nil
}

// --- Helpers ---

func recordJSON(rec contextstore.Record) map[string]any {
	return map[string]any{
		"record_id":  rec.RecordID,
		"namespace":  rec.Namespace,
		"key":        rec.Key,
		"revision":   rec.Revision,
		"actor":      rec.Actor,
		"created_at": rec.CreatedAt,
		"checksum":   rec.Checksum,
	}
}

// argsMap extracts the Arguments from a CallToolRequest as map[string]any.
// Returns nil if not present or wrong type — budget helpers handle nil safely.
func argsMap(req mcp.CallToolRequest) map[string]any {
	if m, ok := req.Params.Arguments.(map[string]any); ok {
		return m
	}
	return nil
}

func newMCPRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "mcp-" + hex.EncodeToString(b)
}

// --- Context planner tools ---

func (a *Adapter) handleContextPlan(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	intent := req.GetString("intent", "custom")
	summary := req.GetString("summary", "")
	maxItems := req.GetInt("budget_items", 50)
	maxTokens := req.GetInt("budget_tokens", 4000)

	namespaces, includePins, rationale := buildContextPlan(intent, summary, maxItems, maxTokens)

	return toolJSON(map[string]any{
		"plan": map[string]any{
			"namespaces":   namespaces,
			"include_pins": includePins,
			"budget": map[string]any{
				"max_items":           maxItems,
				"max_tokens_estimate": maxTokens,
			},
		},
		"rationale": rationale,
	}), nil
}

// handleContextFetch is the execute=true arm of context_broker: build the plan,
// then run it and return the records it selects.
func (a *Adapter) handleContextFetch(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	intent := req.GetString("intent", "custom")
	summary := req.GetString("summary", "")
	maxItems := req.GetInt("budget_items", 50)
	maxTokens := req.GetInt("budget_tokens", 4000)
	payloadMaxBytes, errResult := resolvePayloadMaxBytes(req)
	if errResult != nil {
		return errResult, nil
	}

	namespaces, includePins, rationale := buildContextPlan(intent, summary, maxItems, maxTokens)

	// Load optional token globs for filtering.
	var tokenGlobs []string
	if claims, ok := a.loadClaims(ctx); ok {
		tokenGlobs = claims.NamespaceGlobs
	}

	reqID := newMCPRequestID()
	var items []map[string]any
	pinsIncluded := 0
	bytesSoFar, tokensSoFar := 0, 0
	truncated := false
	truncationReason := ""
	sources := map[string]int{}

	budgetExceeded := func() bool {
		if maxItems > 0 && len(items) >= maxItems {
			truncationReason = "budget.max_items"
			return true
		}
		if maxTokens > 0 && tokensSoFar >= maxTokens {
			truncationReason = "budget.max_tokens_estimate"
			return true
		}
		return false
	}

	addRecord := func(rec contextstore.Record) bool {
		if budgetExceeded() {
			return false
		}
		item := recordJSON(rec)
		served := capPayload(item, rec.Payload, payloadMaxBytes)
		items = append(items, item)
		bytesSoFar += served
		tokensSoFar += contextstore.EstimateTokens(rec.Payload[:served])
		parts := strings.SplitN(rec.Namespace, "/", 3)
		nsKey := rec.Namespace
		if len(parts) >= 2 {
			nsKey = parts[0] + "/" + parts[1]
		}
		sources[nsKey]++
		return true
	}

	// Pins first (when context plan includes them).
	if includePins {
		pins, err := a.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{"user/pins/*"},
			RevisionScope: "head",
		})
		if err == nil {
			if len(tokenGlobs) > 0 {
				pins = filterByGlobs(pins, tokenGlobs)
			}
			for _, rec := range pins {
				if addRecord(rec) {
					pinsIncluded++
				}
			}
		}
	}

	// Main namespaces from context plan.
	seen := map[string]bool{}
	for _, ns := range namespaces {
		if budgetExceeded() {
			truncated = true
			break
		}
		recs, err := a.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{ns},
			RevisionScope: "head",
		})
		if err != nil {
			continue
		}
		if len(tokenGlobs) > 0 {
			recs = filterByGlobs(recs, tokenGlobs)
		}
		for _, rec := range recs {
			if seen[rec.RecordID] {
				continue
			}
			seen[rec.RecordID] = true
			if !addRecord(rec) {
				truncated = true
				break
			}
		}
	}

	if items == nil {
		items = []map[string]any{}
	}

	manifest := map[string]any{
		"request_id":        reqID,
		"pins_included":     pinsIncluded,
		"items_returned":    len(items) - pinsIncluded,
		"bytes_returned":    bytesSoFar,
		"tokens_estimate":   tokensSoFar,
		"truncated":         truncated,
		"truncation_reason": truncationReason,
		"sources":           sources,
	}

	return toolJSON(map[string]any{
		"items":     items,
		"manifest":  manifest,
		"rationale": rationale,
	}), nil
}

// buildContextPlan derives namespace patterns and fetch strategy from an intent.
// This is Tesseract's internal query planner — not the universal ContextBroker.
func buildContextPlan(intent, summary string, maxItems, _ int) (namespaces []string, includePins bool, rationale string) {
	switch intent {
	case "resume_task":
		keywords := plannerExtractKeywords(summary, 3)
		for _, kw := range keywords {
			namespaces = append(namespaces, "user/memory/"+kw+"*")
		}
		namespaces = append(namespaces, "user/pins/*")
		includePins = true
		if len(keywords) > 0 {
			rationale = fmt.Sprintf("resume_task: patterns from keywords [%s] + user/pins/*", strings.Join(keywords, ", "))
		} else {
			namespaces = append(namespaces, "user/memory/*")
			rationale = "resume_task: no keywords extracted; using user/memory/* + user/pins/*"
		}
	case "boot_project":
		namespaces = []string{"user/memory/*", "user/pins/*"}
		includePins = true
		if maxItems < 100 {
			maxItems = 100
		}
		rationale = "boot_project: user/memory/* + user/pins/* for full project boot"
	case "review_session":
		namespaces = []string{"user/cache/*", "user/pins/*"}
		includePins = true
		rationale = "review_session: user/cache/* + user/pins/*"
	default:
		namespaces = []string{"user/*"}
		rationale = "custom: using user/* (no explicit constraints provided)"
	}
	return namespaces, includePins, rationale
}

// plannerExtractKeywords pulls up to n meaningful words from text, skipping stopwords.
var plannerStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "in": true, "is": true, "it": true,
	"its": true, "of": true, "on": true, "or": true, "the": true,
	"this": true, "that": true, "to": true, "was": true, "with": true,
	"we": true, "i": true, "my": true, "me": true, "our": true,
	"will": true, "not": true, "but": true, "into": true, "just": true,
	"task": true, "work": true, "previous": true, "new": true,
	"using": true, "use": true, "via": true, "which": true, "all": true,
}

func plannerExtractKeywords(text string, n int) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9') && r != '-' && r != '_'
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) < 3 || plannerStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= n {
			break
		}
	}
	return out
}

// --- Namespace management tools ---

func (a *Adapter) handleNamespaceRegister(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errResult, _ := a.checkScope(ctx, "namespace.admin")
	if errResult != nil {
		return errResult, nil
	}

	ns := req.GetString("namespace", "")
	ownerType := req.GetString("owner_type", "")
	ownerID := req.GetString("owner_id", "")

	if ns == "" || ownerType == "" || ownerID == "" {
		return toolError("validation_error", "namespace, owner_type, and owner_id are required"), nil
	}

	entry := contextstore.NamespacePolicyEntry{
		Namespace: ns,
		OwnerType: ownerType,
		OwnerID:   ownerID,
	}
	if err := a.Store.UpsertNamespacePolicy(ctx, entry); err != nil {
		return toolError("register_failed", err.Error()), nil
	}

	return toolJSON(map[string]any{
		"namespace":  ns,
		"owner_type": ownerType,
		"owner_id":   ownerID,
	}), nil
}

// namespaceShowResult is the kind=namespaces + name arm of
// context_registry_list: one namespace's ownership policy.
func (a *Adapter) namespaceShowResult(_ context.Context, ns string) *mcp.CallToolResult {
	ctx := context.Background()
	if ns == "" {
		return toolError("validation_error", "namespace is required")
	}

	entry, err := a.Store.GetNamespacePolicy(ctx, ns)
	if err != nil {
		return toolError("not_found", fmt.Sprintf("namespace policy not found: %v", err))
	}

	return toolJSON(map[string]any{
		"namespace":  entry.Namespace,
		"owner_type": entry.OwnerType,
		"owner_id":   entry.OwnerID,
		"policy":     entry.Policy,
	})
}

func (a *Adapter) handleNamespacesList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	prefix := req.GetString("prefix", "")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)

	entries, err := a.Store.ListNamespacePolicies(ctx)
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}

	// String-prefix filtering, matching the HTTP /v1/namespaces/list semantics.
	// Was using globsPermit, which treats `prefix` as a glob — divergent from
	// HTTP and surprising for callers who pass a literal path prefix like
	// `user/chrispian/project/`. CW-20260428-0005 follow-up.
	var items []map[string]any
	for _, entry := range entries {
		if prefix != "" && !strings.HasPrefix(entry.Namespace, prefix) {
			continue
		}
		items = append(items, map[string]any{
			"namespace":  entry.Namespace,
			"owner_type": entry.OwnerType,
			"owner_id":   entry.OwnerID,
			"policy":     entry.Policy,
		})
	}
	if items == nil {
		items = []map[string]any{}
	}
	env := budget.Apply(items, budget.Config{Limit: limit}, "%d namespaces available. Use context_registry_list with kind=namespaces and name=<namespace> for details on a specific one.")
	return mcp.NewToolResultText(budget.ToolJSON(env)), nil
}

// --- Audit tool ---

func (a *Adapter) handleAudit(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx := context.Background()
	ns := req.GetString("namespace", "")
	eventType := req.GetString("event_type", "")
	limit := budget.ExtractLimit(argsMap(req), budget.DefaultLimit)
	cursor := req.GetInt("cursor", 0)

	events, nextCursor, err := a.Store.QueryAuditEvents(ctx, contextstore.AuditQuery{
		Limit:     limit,
		Cursor:    int64(cursor),
		Namespace: ns,
		EventType: eventType,
	})
	if err != nil {
		return toolError("internal_error", err.Error()), nil
	}

	items := make([]map[string]any, len(events))
	for i, ev := range events {
		item := map[string]any{
			"id":         ev.ID,
			"event_type": ev.EventType,
			"actor":      ev.Actor,
			"namespace":  ev.Namespace,
			"key":        ev.Key,
			"revision":   ev.Revision,
			"created_at": ev.CreatedAt,
		}
		if ev.RecordID != "" {
			item["record_id"] = ev.RecordID
		}
		if len(ev.Metadata) > 0 {
			// Metadata is json.RawMessage; pass as-is so callers receive
			// the parsed object rather than a quoted string.
			item["metadata"] = json.RawMessage(ev.Metadata)
		}
		items[i] = item
	}

	env := budget.Apply(items, budget.Config{Limit: limit}, "%d audit events available. Use cursor parameter for pagination.")
	// Preserve cursor in the envelope for pagination continuity.
	envMap := map[string]any{}
	envJSON, _ := json.Marshal(env)
	_ = json.Unmarshal(envJSON, &envMap)
	if nextCursor != nil {
		envMap["next_cursor"] = *nextCursor
	}
	return mcp.NewToolResultText(budget.ToolJSON(envMap)), nil
}

// loadClaims returns the token claims if a token is configured and valid.
// Returns false (no error) when no token is set — callers use this for optional filtering.
func (a *Adapter) loadClaims(ctx context.Context) (contextstore.AuthToken, bool) {
	if a.Token == "" {
		return contextstore.AuthToken{}, false
	}
	claims, err := a.Store.ValidateAuthTokenWithClaims(ctx, a.Token)
	if err != nil {
		return contextstore.AuthToken{}, false
	}
	return claims, true
}

// globsPermit reports whether namespace is permitted by any of the globs.
// Matches the same logic as the HTTP API's requireNamespaceAccess:
//   - empty globs slice or glob=="*" → full access
//   - path.Match(glob, namespace) → exact glob
//   - glob ending in /* → hierarchical prefix match
func globsPermit(globs []string, namespace string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" || g == "*" || g == namespace {
			return true
		}
		if matched, _ := path.Match(g, namespace); matched {
			return true
		}
		if strings.HasSuffix(g, "/*") {
			prefix := strings.TrimSuffix(g, "*") // "app/test/"
			if strings.HasPrefix(namespace, prefix) {
				return true
			}
		}
	}
	return false
}

// filterByGlobs returns only the records whose namespace is permitted by globs.
func filterByGlobs(records []contextstore.Record, globs []string) []contextstore.Record {
	if len(globs) == 0 {
		return records
	}
	out := records[:0:0]
	for _, rec := range records {
		if globsPermit(globs, rec.Namespace) {
			out = append(out, rec)
		}
	}
	return out
}

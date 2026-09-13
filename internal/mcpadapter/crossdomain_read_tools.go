package mcpadapter

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

// ── The read domain vocabulary ───────────────────────────────────────────────

// readDomainContext addresses the context record store (the records/heads
// tables). It is deliberately NOT a domains.Domain: the domains registry covers
// the policy buckets that share memory_revisions, and adding context there
// would make the tesseract_recall `domains` filter accept a value that can only
// ever match zero rows — an empty result that reads exactly like a clean
// corpus.
const readDomainContext = "context"

// readDomainVocabulary is the closed set of values `domain` accepts on
// tesseract_get and tesseract_history, in a stable order.
//
// The revision-store values are taken from domains.All() rather than restated,
// so this vocabulary cannot advertise a domain the registry has dropped or miss
// one it has gained. `context` is prepended because it names a
// different physical store, not a domain policy.
func readDomainVocabulary() []string {
	out := []string{readDomainContext}
	for _, d := range domains.All() {
		out = append(out, string(d))
	}
	out = append(out, workspace.Domain)
	return out
}

// resolveReadDomain reads and validates the `domain` argument.
//
// An unknown value is a validation_error naming the allowed set rather than a
// silently empty read: a caller who guessed "memories" or "ctx" must be told,
// not handed a not_found that looks like a missing key.
func resolveReadDomain(req mcp.CallToolRequest) (string, *mcp.CallToolResult) {
	raw := strings.TrimSpace(req.GetString("domain", ""))
	if raw == "" {
		return "", toolError(codeValidationError,
			"domain is required; one of "+strings.Join(readDomainVocabulary(), ", "))
	}
	for _, d := range readDomainVocabulary() {
		if raw == d {
			return raw, nil
		}
	}
	return "", toolError(codeValidationError,
		"domain must be one of "+strings.Join(readDomainVocabulary(), ", ")+", got "+raw)
}

type readSelector struct {
	ItemID    string
	Domain    string
	Namespace string
	Key       string
}

func (s readSelector) ByItemID() bool { return s.ItemID != "" }

// resolveReadSelector enforces the two public selector forms. Presence is
// checked before value so an empty field cannot be silently ignored to turn a
// mixed selector into an item-only call.
func resolveReadSelector(req mcp.CallToolRequest) (readSelector, *mcp.CallToolResult) {
	args := req.GetArguments()
	_, hasItemID := args["item_id"]
	_, hasDomain := args["domain"]
	_, hasNamespace := args["namespace"]
	_, hasKey := args["key"]
	legacyCount := 0
	for _, present := range []bool{hasDomain, hasNamespace, hasKey} {
		if present {
			legacyCount++
		}
	}

	if hasItemID {
		if legacyCount != 0 {
			return readSelector{}, toolError(codeValidationError,
				"choose exactly one selector: item_id, or domain + namespace + key; do not mix them")
		}
		itemID := strings.TrimSpace(req.GetString("item_id", ""))
		if itemID == "" {
			return readSelector{}, toolError(codeValidationError, "item_id must be non-empty")
		}
		return readSelector{ItemID: itemID}, nil
	}
	if legacyCount != 3 {
		return readSelector{}, toolError(codeValidationError,
			"choose exactly one selector: item_id, or the complete domain + namespace + key form")
	}

	domain, errRes := resolveReadDomain(req)
	if errRes != nil {
		return readSelector{}, errRes
	}
	namespace := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if namespace == "" || key == "" {
		return readSelector{}, toolError(codeValidationError,
			"domain, namespace and key must all be non-empty in the legacy selector")
	}
	return readSelector{Domain: domain, Namespace: namespace, Key: key}, nil
}

// domainUnavailable is the answer when the vocabulary accepts a domain but this
// deployment has no store wired for it. It is a distinct code from not_found on
// purpose: "there is no knowledge store here" and "that key has no knowledge
// entry" are different facts, and collapsing them is how a misconfigured
// deployment reads as an empty one.
func domainUnavailable(domain string) *mcp.CallToolResult {
	return toolError(codeDomainUnavailable,
		"no store is wired for domain "+domain+" on this deployment")
}

// revisionStoreUnavailable is the answer when a revision-level op is reached on
// a deployment with no revision store.
//
// Registration already gates these ops, so nothing on the wire can reach this
// today. It exists because registration is a gate one refactor away from being
// removed, and the failure it was hiding is a nil dereference inside the store
// — a panic that takes the stdio server down rather than answering the caller.
// A named error is the cheaper failure by a wide margin.
func revisionStoreUnavailable() *mcp.CallToolResult {
	return toolError(codeDomainUnavailable,
		"no revision store is wired on this deployment")
}

// revisionStore returns the shared memory_revisions store, from whichever field
// is wired.
//
// Revision-level operations (fetch by revision_id, deprecate by revision_id)
// carry no domain filter — GetRevisionByID and Deprecate both key on
// revision_id alone — so they resolve memory and knowledge revisions alike. The
// field they arrive through is an accident of how the deployment was built, and
// gating them on MemoryStore meant a knowledge-only deployment could not fetch
// or deprecate its own revisions by ID.
func (a *Adapter) revisionStore() *memory.Store {
	if a.MemoryStore != nil {
		return a.MemoryStore
	}
	if a.KnowledgeStore != nil {
		return a.KnowledgeStore.RevisionStore()
	}
	if a.EventStore != nil {
		return a.EventStore.RevisionStore()
	}
	return nil
}

func (a *Adapter) itemDomainAvailable(domain domains.Domain) bool {
	switch domain {
	case domains.Memory:
		return a.MemoryStore != nil
	case domains.Knowledge:
		return a.KnowledgeStore != nil
	case domains.Event:
		return a.EventStore != nil
	case domains.Domain(workspace.Domain):
		return a.WorkspaceStore != nil
	default:
		return false
	}
}

// resolveItemRead resolves only the item's state row, then applies token
// namespace policy before any revision content is loaded or reinforced.
func (a *Adapter) resolveItemRead(ctx context.Context, itemID string, claims contextstore.AuthToken) (*memory.Store, memory.State, *mcp.CallToolResult, error) {
	store := a.revisionStore()
	if store == nil {
		return nil, memory.State{}, revisionStoreUnavailable(), nil
	}
	state, err := store.GetState(ctx, itemID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return nil, memory.State{}, toolError(codeNotFound, "item_id not found: "+itemID), nil
		}
		return nil, memory.State{}, nil, err
	}
	if !globsPermit(claims.NamespaceGlobs, state.Namespace) {
		return nil, memory.State{}, toolError(codeNamespaceNotPermitted,
			"token namespace globs do not permit reading: "+state.Namespace), nil
	}
	if !a.itemDomainAvailable(state.Domain) {
		return nil, memory.State{}, domainUnavailable(string(state.Domain)), nil
	}
	return store, state, nil, nil
}

// ── Registration ─────────────────────────────────────────────────────────────

// registerCrossDomainReadTools registers the reads that span every domain.
//
// tesseract_get and tesseract_history register unconditionally: the context
// store is always present on a built adapter, so `domain: "context"` always has
// a backing store, and the three revision-store domains answer domain_unavailable
// when their store is absent.
//
// tesseract_get_revision and tesseract_deprecate register whenever ANY backing
// store is wired — see revisionStore.
func (a *Adapter) registerCrossDomainReadTools(s *toolRegistrar) {
	domainList := strings.Join(readDomainVocabulary(), " | ")

	a.addTool(s, mcp.NewTool("tesseract_ref_resolve",
		mcp.WithDescription(
			"Normalize one supported reference to stable Tesseract object identity without reading content or recording use. "+
				"Pass exactly one complete selector: item_id; revision_id; domain + namespace + key; or a canonical tesseract://item/... or tesseract://revision/... URI. "+
				"Successful outcomes are resolved, deleted, not_found, ambiguous, and unsupported_reference. The current v1 selector set is unique by construction, so it does not emit ambiguous. "+
				"Unknown IDs and unsupported URI classes are outcomes; malformed selectors, authorization failures, and unavailable stores are errors. Requires memory:read. See tesseract_skills revisions."),
		mcp.WithString("item_id", mcp.Description("Stable current-item identity. Supply alone.")),
		mcp.WithString("revision_id", mcp.Description("Exact immutable revision identity. Supply alone; the result preserves revision kind.")),
		mcp.WithString("domain", mcp.Description("Legacy key selector domain: memory, knowledge, event, or workspace. Supply with namespace and key.")),
		mcp.WithString("namespace", mcp.Description("Legacy key selector namespace. Supply with domain and key.")),
		mcp.WithString("key", mcp.Description("Exact current key, preserved byte-for-byte. Supply with domain and namespace.")),
		mcp.WithString("uri", mcp.Description("Canonical tesseract://item/<item_id> or tesseract://revision/<revision_id> URI. Other URI classes return unsupported_reference.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleReferenceResolve)

	a.addTool(s, mcp.NewTool("tesseract_get",
		mcp.WithDescription(
			"**Fetch a current entry** by stable `item_id`, or by `(domain, namespace, key)`.\n"+
				"• **Kind of content:** the latest revision for a memory, knowledge or event entry; the current value for workspace; or the head record for context.\n"+
				"• **Result shape:** `memory`, `knowledge` and `event` answer a revision object; `workspace` answers a current item with `version_token` and no revision fields; `context` answers a record object and remains available only through the legacy selector.\n"+
				"• **Scope:** `memory:read` for `memory`, `knowledge`, `event` and `workspace`; `context` needs no token, matching the rest of the context read surface.\n"+
				"• **Side effect:** `memory`, `knowledge` and `workspace` reinforce activation/access_count. `context` does not reinforce, and neither does `event`, which opts out of activation.\n"+
				"• **Selectors:** pass exactly one of `item_id`, or the complete legacy `domain` + `namespace` + `key` form. `item_id` resolves its domain and namespace from storage and works for keyless items.\n"+
				"• **Use this when:** you know exactly which current item you want. Prefer `item_id` for stable references; key lookup remains supported.\n"+
				"• **Don't use this for:** revision history (`tesseract_history`), ranked search (`tesseract_recall`), or a specific revision by ID (`tesseract_get_revision`).\n"+
				"• **Errors:** `validation_error` (mixed, partial or empty selector), `namespace_not_permitted`, `domain_unavailable`, `deleted`, `not_found`.\n"+
				"• **Deeper:** `tesseract_skills memory`, `tesseract_skills knowledge`, `tesseract_skills event`, `tesseract_skills workspace`.",
		),
		mcp.WithString("item_id", mcp.Description(
			"Stable Tesseract item ID. Preferred for current-item reads; it needs no domain, namespace or key. Revisioned responses also expose it as memory_id; workspace does not.")),
		mcp.WithString("domain", mcp.Description(
			"Which store to read: "+domainList+". Legacy key selector; supply together with namespace and key, and omit all three when using item_id.")),
		mcp.WithString("namespace", mcp.Description(
			"Namespace. The first segment is the scope type ("+memory.ScopeList()+"), the second its id; `system` is a singleton with no id segment. "+
				"Memory: {scope}/{id}/memory/{type}. Knowledge: {scope}/{id}/knowledge/... (free depth). Event: {scope}/{id}/event/{type}. Context: any registered namespace path.")),
		mcp.WithString("key", mcp.Description(
			"Entry key within the namespace. This is the field memory, knowledge and event revisions carry as `memory_key`. Most event entries are keyless and are read through `event_list` instead.")),
		// The unified tool must advertise the strongest effect of any arm. The
		// memory arm reinforces activation/access_count, so this is neither
		// read-only nor idempotent even though the context and knowledge arms are.
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractGet)

	a.addTool(s, mcp.NewTool("tesseract_history",
		mcp.WithDescription(
			"**Fetch an item's revision history** by stable `item_id`, or by `(domain, namespace, key)`, newest first.\n"+
				"• **Kind of content:** every revision under the key, including superseded and deprecated ones. Workspace has no revisions and returns history_unavailable.\n"+
				"• **Result shape:** domain-dependent. `memory`, `knowledge` and `event` answer a bare array; pass `limit`, `cursor`, `budget_bytes` or `budget_tokens` and they answer `{results, manifest}` instead. `context` always answers its own budget envelope and honors `limit` only.\n"+
				"• **Scope:** `memory:read` for `memory`, `knowledge` and `event`; `context` needs no token.\n"+
				"• **Selectors:** pass exactly one of `item_id`, or the complete legacy `domain` + `namespace` + `key` form. `item_id` works for keyless entries.\n"+
				"• **Use this when:** you need to trace how an item evolved, or read superseded content. Under `event` this is also where a retracted log entry stays findable — `event_list` excludes deprecated revisions, this does not.\n"+
				"• **Don't use this for:** just the current value (`tesseract_get`).\n"+
				"• **Errors:** `validation_error` (mixed or partial selector, unusable cursor), `namespace_not_permitted`, `domain_unavailable`, `history_unavailable`, `not_found`.\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("item_id", mcp.Description("Stable Tesseract item ID. Preferred for item history and required for keyless entries.")),
		mcp.WithString("domain", mcp.Description("Which store to read: "+domainList+". Legacy key selector; supply with namespace and key.")),
		mcp.WithString("namespace", mcp.Description("Legacy key selector namespace, as for `tesseract_get`.")),
		mcp.WithString("key", mcp.Description("Legacy key selector entry key.")),
		mcp.WithNumber("limit", mcp.Description(historyLimitArgDescription+
			" Under `domain: \"context\"` this is that domain's own record limit and does not change the response shape.")),
		mcp.WithString("cursor", mcp.Description(cursorArgDescription+" Ignored under `domain: \"context\"`.")),
		mcp.WithNumber("budget_bytes", mcp.Description(budgetBytesArgDescription+" Ignored under `domain: \"context\"`.")),
		mcp.WithNumber("budget_tokens", mcp.Description(budgetTokensArgDescription+" Ignored under `domain: \"context\"`.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractHistory)

	if a.revisionStore() == nil {
		return
	}

	a.addTool(s, mcp.NewTool("tesseract_get_revision",
		mcp.WithDescription(
			"**Fetch one revision by its `revision_id`.**\n"+
				"• **Kind of content:** a single revision record, including body, facets, and lineage.\n"+
				"• **Works across domains:** memory, knowledge and event revisions share one table keyed by `revision_id`, so an ID from any of them resolves here without saying which it was.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** a `tesseract_recall` or `tesseract_history` result referenced a `revision_id` and you want the full content — the hydrate step of recall → choose → hydrate.\n"+
				"• **Don't use this for:** resolving by `(namespace, key)` — use `tesseract_get`.\n"+
				"• **Side effect:** reinforces the parent entry's activation/access_count — a deliberate read counts as use.\n"+
				"• **Errors:** `validation_error` (missing `revision_id`), `not_found`.\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to fetch (e.g. 01HX...)")),
		// Fetching by revision ID deliberately reinforces the parent entry.
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractGetRevision)

	a.addTool(s, mcp.NewTool("tesseract_deprecate",
		mcp.WithDescription(
			"**Soft-remove one revision** by its `revision_id`. The revision stays in history.\n"+
				"• **Kind of content:** none returned beyond `{status, revision_id}`.\n"+
				"• **Works across domains:** memory, knowledge and event revisions share one table keyed by `revision_id`, so an ID from any of them resolves here. Under `event` this IS the retraction path — `event_list` stops showing a deprecated entry, `tesseract_history` still returns it.\n"+
				"• **Scope:** `memory:write`.\n"+
				"• **Use this when:** a revision is wrong, outdated, or should stop appearing in current recall.\n"+
				"• **Don't use this for:** replacing content — write a new revision with `supersedes`. Hard deletes are not supported; history is canonical.\n"+
				"• **Errors:** `validation_error` (missing `revision_id`), `not_found`.\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to deprecate (e.g. 01HX...)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractDeprecate)

	// ── tesseract_touch ──────────────────────────────────────────────────────
	//
	// A tool rather than a knob on recall, against the general knobs-over-tools
	// preference, for one reason: it is a write from a read context that must
	// happen AFTER the reasoning. A `touch: true` flag on recall would reinforce
	// at the moment the ranker made its guess, which is what recall.go correctly
	// refuses to do. The timing is the whole point.
	a.addTool(s, mcp.NewTool("tesseract_touch",
		mcp.WithDescription(
			"**Report which recalled entries actually informed your work.** The closing step of `tesseract_recall` → use → touch.\n"+
				"• **Kind of content:** none returned. Answers `{touched, not_found, not_reinforced, deleted}`. Every distinct ID lands in one bucket.\n"+
				"• **Scope:** `memory:read`. It writes, but what it writes is the deliberate-read signal `tesseract_get` already emits on every memory-domain call; a read-only agent that could not close the loop would leave the loop open.\n"+
				"• **Use this when:** you have finished reasoning over a recall result and know which hits shaped the turn. **Call it after the work, not after the search.** Recall deliberately does not reinforce a hit merely for returning it. Touch supplies the use signal for projected hits you did not deliberately fetch, and may add an intentional second reinforcement for a hit already fetched through `tesseract_get` or `tesseract_get_revision`.\n"+
				"• **Touch only what genuinely shaped the turn.** Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal.\n"+
				"• **Don't use this for:** everything you recalled, everything you skimmed, anything you merely saw in a result list, or as a way to pin a memory you want ranked highly. Reinforcement has diminishing returns — each touch closes a fraction of the remaining distance to a ceiling, so the tenth touch moves a memory far less than the first and no amount of touching passes the ceiling. Inflating a report buys very little ranking and costs the ranking its ability to tell signal from noise.\n"+
				"• **Effect per distinct memory:** `activation` moves a fixed fraction of the way toward its ceiling, `access_count` increments, `last_accessed_at` is set. Naming a revision twice, or naming two revisions of the same memory, reinforces it once.\n"+
				"• **Works across domains:** pass revision hits as `revision_ids`, or current typed hits as `item_ids`. Workspace items reinforce directly; deleted workspace IDs return under `deleted`; event item IDs return under `not_reinforced`.\n"+
				"• **Deeper:** `tesseract_skills memory`, `tesseract_skills workspace`, and `tesseract_skills recall-and-ranking`.",
		),
		mcp.WithString("revision_ids", mcp.Description(
			"JSON array of `revision_id` strings from a recall, lookup, or history result "+
				"(e.g. [\"01HX...\",\"01HY...\"]). Every result carries `revision_id` under every `payload_mode`, "+
				"so this is always available without a full read. "+
				"Unknown IDs come back in `not_found` rather than failing the call, so a partly-stale set is safe to send. "+
				"At most "+strconv.Itoa(memory.MaxTouchRevisions)+" per call — a request-size bound, not a budget to spend: "+
				"a turn that genuinely used that many memories is rare, and the guidance above still applies well below the cap.")),
		mcp.WithString("item_ids", mcp.Description(
			"JSON array of stable item IDs. Workspace items are reinforced directly; revisioned items resolve through their current state. "+
				"Choose exactly one of item_ids or revision_ids. At most "+strconv.Itoa(memory.MaxTouchRevisions)+" per call.")),
		mcp.WithReadOnlyHintAnnotation(false),
		// Not idempotent: each call is a fresh report of use, and reinforcement
		// accumulates across calls even though it collapses within one.
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractTouch)
}

func referenceSelectorFromMCP(req mcp.CallToolRequest) (itemservice.ReferenceSelector, *mcp.CallToolResult) {
	args := req.GetArguments()
	_, hasItem := args["item_id"]
	_, hasRevision := args["revision_id"]
	_, hasURI := args["uri"]
	_, hasDomain := args["domain"]
	_, hasNamespace := args["namespace"]
	_, hasKey := args["key"]
	keyParts := 0
	for _, present := range []bool{hasDomain, hasNamespace, hasKey} {
		if present {
			keyParts++
		}
	}
	selectors := 0
	for _, present := range []bool{hasItem, hasRevision, hasURI, keyParts > 0} {
		if present {
			selectors++
		}
	}
	if selectors != 1 || (keyParts > 0 && keyParts != 3) {
		return itemservice.ReferenceSelector{}, toolError(codeValidationError,
			"choose exactly one of item_id, revision_id, domain + namespace + key, or uri")
	}
	selector := itemservice.ReferenceSelector{
		ItemID: req.GetString("item_id", ""), RevisionID: req.GetString("revision_id", ""),
		Domain: req.GetString("domain", ""), Namespace: req.GetString("namespace", ""),
		Key: req.GetString("key", ""), URI: req.GetString("uri", ""),
	}
	if (hasItem && selector.ItemID == "") || (hasRevision && selector.RevisionID == "") ||
		(hasURI && selector.URI == "") || (keyParts > 0 && (selector.Domain == "" || selector.Namespace == "" || selector.Key == "")) {
		return itemservice.ReferenceSelector{}, toolError(codeValidationError, "the selected reference fields must be non-empty")
	}
	return selector, nil
}

func (a *Adapter) handleReferenceResolve(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	selector, errResult := referenceSelectorFromMCP(req)
	if errResult != nil {
		return errResult, nil
	}
	res, claims := a.checkScope(ctx, "memory:read")
	if res != nil {
		return res, nil
	}
	result, err := a.itemService().ResolveReference(ctx, selector)
	if err != nil {
		switch {
		case errors.Is(err, itemservice.ErrInvalidReference):
			return toolError(codeValidationError, err.Error()), nil
		case errors.Is(err, itemservice.ErrBackendUnavailable):
			return domainUnavailable("required reference backend"), nil
		default:
			return toolError(codeInternalError, err.Error()), nil
		}
	}
	if selector.Domain != "" && !a.itemDomainAvailable(domains.Domain(selector.Domain)) {
		return domainUnavailable(selector.Domain), nil
	}
	if result.Status == itemservice.ResolutionResolved || result.Status == itemservice.ResolutionDeleted {
		if !globsPermit(claims.NamespaceGlobs, result.Namespace) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit resolving this reference"), nil
		}
		if !a.itemDomainAvailable(domains.Domain(result.Domain)) {
			return domainUnavailable("resolved reference domain"), nil
		}
	}
	return toolJSON(result), nil
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// handleTesseractGet dispatches on `domain`.
//
// Each arm is the body of the tool it replaces, with two deliberate departures:
// it reads `key` where the memory and knowledge tools read `memory_key`, and an
// empty `namespace` or `key` is a validation_error here where those tools
// reached the store and surfaced not_found. The second moves the MCP side onto
// the rule its HTTP peers already applied, so it narrows a divergence rather
// than opening one — but it IS a changed response for that input, and calling
// the arms "unchanged" would have hidden it.
//
// What is preserved is everything that differs BETWEEN arms and would be
// tempting to unify: the context arm performs no scope check (the context read
// surface never has), and the memory and knowledge arms reinforce. Both are contracts
// callers already depend on, so they are kept and documented rather than
// smoothed.
func (a *Adapter) handleTesseractGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	selector, errRes := resolveReadSelector(req)
	if errRes != nil {
		return errRes, nil
	}

	if selector.Domain == readDomainContext {
		return a.handleContextHead(ctx, req)
	}

	res, claims := a.checkScope(ctx, "memory:read")
	if res != nil {
		return res, nil
	}
	if selector.ByItemID() {
		meta, err := a.itemService().LookupMetadata(ctx, selector.ItemID)
		if err != nil {
			return workspaceToolError(err), nil
		}
		if !globsPermit(claims.NamespaceGlobs, meta.Namespace) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit reading: "+meta.Namespace), nil
		}
		if !a.itemDomainAvailable(domains.Domain(meta.Domain)) {
			return domainUnavailable(meta.Domain), nil
		}
		read, err := a.itemService().ReadCurrent(ctx, meta)
		if err != nil {
			return workspaceToolError(err), nil
		}
		if read.Item != nil {
			return toolJSON(*read.Item), nil
		}
		return toolJSON(*read.Revision), nil
	}
	domain := selector.Domain
	namespace := selector.Namespace
	key := selector.Key

	var (
		rev memory.Revision
		err error
	)
	switch domain {
	case string(domains.Memory):
		if a.MemoryStore == nil {
			return domainUnavailable(domain), nil
		}
		// Deliberate read: this bumps activation/access_count — but only after
		// the domain check, so a knowledge row resolved through domain=memory
		// is neither returned nor reinforced.
		rev, err = a.MemoryStore.GetCurrentInDomainReinforced(ctx, domains.Memory, namespace, key)
	case string(domains.Knowledge):
		if a.KnowledgeStore == nil {
			return domainUnavailable(domain), nil
		}
		// Deliberate read, same as the memory arm above: knowledge participates
		// in activation (CW-20260910-0021), so resolving a known knowledge entry
		// reinforces it. The domain check is inside the store call, before the
		// bump, for the reason spelled out on GetCurrentInDomainReinforced.
		rev, err = a.KnowledgeStore.GetCurrentReinforced(ctx, namespace, key)
	case string(domains.Event):
		if a.EventStore == nil {
			return domainUnavailable(domain), nil
		}
		// The plain getter, not a reinforcing one, and that is the domain
		// policy showing through rather than an oversight. Event opts out of
		// activation (CW-20260909-0035), so the reinforcing variant would bump
		// nothing — reinforceMemoryIDs gates on the domain in SQL — and calling
		// it here would put a use-signal decision back at a call site, which is
		// the shape CW-20260910-0021 removed. See event.Store.GetHistory.
		//
		// Note also what this arm is NOT the main way to read events. Most
		// entries are keyless, so they have no (namespace, key) to fetch; the
		// linear read is event_list.
		rev, err = a.EventStore.GetCurrent(ctx, namespace, key)
	case workspace.Domain:
		if a.WorkspaceStore == nil {
			return domainUnavailable(domain), nil
		}
		if !globsPermit(claims.NamespaceGlobs, namespace) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit reading: "+namespace), nil
		}
		item, readErr := a.itemService().ReadWorkspaceByKey(ctx, namespace, key)
		if readErr != nil {
			return workspaceToolError(readErr), nil
		}
		return toolJSON(item), nil
	default:
		// resolveReadDomain accepts whatever readDomainVocabulary offers, and
		// that is derived from domains.All(). A domain added to the registry
		// therefore becomes callable here before it has an arm. Falling through
		// would return the zero Revision with a nil error — a fabricated empty
		// record, which is exactly the silently-empty read resolveReadDomain
		// exists to prevent.
		return domainUnavailable(domain), nil
	}
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError(codeNotFound, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

// handleTesseractHistory dispatches on `domain`. See handleTesseractGet on why
// the arms are not unified.
func (a *Adapter) handleTesseractHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	selector, errRes := resolveReadSelector(req)
	if errRes != nil {
		return errRes, nil
	}

	if selector.Domain == readDomainContext {
		return a.handleContextHistory(ctx, req)
	}

	res, claims := a.checkScope(ctx, "memory:read")
	if res != nil {
		return res, nil
	}

	pr, errRes := a.resolveHistoryPageRequest(req)
	if errRes != nil {
		return errRes, nil
	}
	if selector.ByItemID() {
		meta, err := a.itemService().LookupMetadata(ctx, selector.ItemID)
		if err != nil {
			return workspaceToolError(err), nil
		}
		if !globsPermit(claims.NamespaceGlobs, meta.Namespace) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit reading: "+meta.Namespace), nil
		}
		if !a.itemDomainAvailable(domains.Domain(meta.Domain)) {
			return domainUnavailable(meta.Domain), nil
		}
		revs, err := a.itemService().History(ctx, meta)
		if err != nil {
			if errors.Is(err, itemservice.ErrHistoryUnavailable) {
				return toolError(codeHistoryUnavailable, err.Error()), nil
			}
			if errors.Is(err, memory.ErrNotFound) {
				return toolError(codeNotFound, err.Error()), nil
			}
			return nil, err
		}
		if !pr.Engaged() {
			return toolJSON(revs), nil
		}
		page, err := memory.PageRevisions(revs, pr,
			memory.ItemHistoryOrderingFingerprint(selector.ItemID))
		if err != nil {
			if errors.Is(err, memory.ErrInvalidCursor) {
				return toolError(codeValidationError, err.Error()), nil
			}
			return nil, err
		}
		return toolJSON(page), nil
	}
	domain := selector.Domain
	namespace := selector.Namespace
	key := selector.Key

	var (
		revs []memory.Revision
		err  error
	)
	switch domain {
	case string(domains.Memory):
		if a.MemoryStore == nil {
			return domainUnavailable(domain), nil
		}
		revs, err = a.MemoryStore.GetHistoryInDomain(ctx, domains.Memory, namespace, key)
	case string(domains.Knowledge):
		if a.KnowledgeStore == nil {
			return domainUnavailable(domain), nil
		}
		revs, err = a.KnowledgeStore.GetHistory(ctx, namespace, key)
	case string(domains.Event):
		if a.EventStore == nil {
			return domainUnavailable(domain), nil
		}
		// History over an event entry is the one read that still shows
		// deprecated revisions — event_list excludes them because deprecation
		// is how a log entry is retracted, and this is where the retracted
		// entry remains findable.
		revs, err = a.EventStore.GetHistory(ctx, namespace, key)
	case workspace.Domain:
		if a.WorkspaceStore == nil {
			return domainUnavailable(domain), nil
		}
		if !globsPermit(claims.NamespaceGlobs, namespace) {
			return toolError(codeNamespaceNotPermitted, "token namespace globs do not permit reading: "+namespace), nil
		}
		return toolError(codeHistoryUnavailable, "workspace items do not retain revision history"), nil
	default:
		// See handleTesseractGet: without this, an accepted-but-unhandled
		// domain answers a bare `null` instead of saying it has no arm.
		return domainUnavailable(domain), nil
	}
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError(codeNotFound, err.Error()), nil
		}
		return nil, err
	}
	if !pr.Engaged() {
		return toolJSON(revs), nil
	}
	page, err := memory.PageRevisions(revs, pr,
		memory.HistoryOrderingFingerprint(domain, namespace, key))
	if err != nil {
		if errors.Is(err, memory.ErrInvalidCursor) {
			return toolError(codeValidationError, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(page), nil
}

func (a *Adapter) handleTesseractGetRevision(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}
	revisionID := req.GetString("revision_id", "")
	if revisionID == "" {
		return toolError(codeValidationError, "revision_id is required"), nil
	}
	store := a.revisionStore()
	if store == nil {
		return revisionStoreUnavailable(), nil
	}
	// Deliberate read: GetRevisionByIDReinforced bumps activation/access_count.
	rev, err := store.GetRevisionByIDReinforced(ctx, revisionID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError(codeNotFound, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

func (a *Adapter) handleTesseractDeprecate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:write"); res != nil {
		return res, nil
	}
	revisionID := req.GetString("revision_id", "")
	if revisionID == "" {
		return toolError(codeValidationError, "revision_id is required"), nil
	}
	store := a.revisionStore()
	if store == nil {
		return revisionStoreUnavailable(), nil
	}
	if err := store.Deprecate(ctx, revisionID); err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError(codeNotFound, err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(map[string]string{"status": "deprecated", "revision_id": revisionID}), nil
}

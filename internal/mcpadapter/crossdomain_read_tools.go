package mcpadapter

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ── The read domain vocabulary ───────────────────────────────────────────────

// readDomainContext addresses the context record store (the records/heads
// tables). It is deliberately NOT a domains.Domain: the domains registry covers
// the two policy buckets that share memory_revisions, and adding a third value
// there would make the tesseract_recall `domains` filter accept a value that can
// only ever match zero rows — an empty result that reads exactly like a clean
// corpus.
const readDomainContext = "context"

// readDomainVocabulary is the closed set of values `domain` accepts on
// tesseract_get and tesseract_history, in a stable order.
//
// The two revision-store values are taken from domains.All() rather than
// restated, so this vocabulary cannot advertise a domain the registry has
// dropped or miss one it has gained. `context` is prepended because it names a
// different physical store, not a domain policy.
func readDomainVocabulary() []string {
	out := []string{readDomainContext}
	for _, d := range domains.All() {
		out = append(out, string(d))
	}
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
		return "", toolError("validation_error",
			"domain is required; one of "+strings.Join(readDomainVocabulary(), ", "))
	}
	for _, d := range readDomainVocabulary() {
		if raw == d {
			return raw, nil
		}
	}
	return "", toolError("validation_error",
		"domain must be one of "+strings.Join(readDomainVocabulary(), ", ")+", got "+raw)
}

// domainUnavailable is the answer when the vocabulary accepts a domain but this
// deployment has no store wired for it. It is a distinct code from not_found on
// purpose: "there is no knowledge store here" and "that key has no knowledge
// entry" are different facts, and collapsing them is how a misconfigured
// deployment reads as an empty one.
func domainUnavailable(domain string) *mcp.CallToolResult {
	return toolError("domain_unavailable",
		"no store is wired for domain "+domain+" on this deployment")
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
	return nil
}

// ── Registration ─────────────────────────────────────────────────────────────

// registerCrossDomainReadTools registers the reads that span every domain.
//
// tesseract_get and tesseract_history register unconditionally: the context
// store is always present on a built adapter, so `domain: "context"` always has
// a backing store, and the two revision-store domains answer domain_unavailable
// when their store is absent.
//
// tesseract_get_revision and tesseract_deprecate register whenever ANY backing
// store is wired — see revisionStore.
func (a *Adapter) registerCrossDomainReadTools(s *server.MCPServer) {
	domainList := strings.Join(readDomainVocabulary(), " | ")

	a.addTool(s, mcp.NewTool("tesseract_get",
		mcp.WithDescription(
			"**Fetch the current entry** at `(domain, namespace, key)` — one tool for all three domains.\n"+
				"• **Kind of content:** the latest revision for a memory or knowledge entry, or the head record for a context record.\n"+
				"• **Result shape:** domain-dependent, because the underlying rows are. `memory` and `knowledge` answer a revision object; `context` answers a record object. Read `domain` back off your own call, not off the response.\n"+
				"• **Scope:** `memory:read` for `memory` and `knowledge`; `context` needs no token, matching the rest of the context read surface.\n"+
				"• **Side effect:** under `memory`, reinforces the entry's activation/access_count — a deliberate read counts as use, unlike `tesseract_recall`. `knowledge` and `context` do not reinforce.\n"+
				"• **Use this when:** you know exactly which entry you want.\n"+
				"• **Don't use this for:** revision history (`tesseract_history`), ranked search (`tesseract_recall`), or a specific revision by ID (`tesseract_get_revision`).\n"+
				"• **Errors:** `validation_error` (bad or missing `domain`, missing `namespace`/`key`), `domain_unavailable` (no store wired for that domain here), `not_found`.\n"+
				"• **Deeper:** `tesseract_skills memory`, `tesseract_skills knowledge`.",
		),
		mcp.WithString("domain", mcp.Required(), mcp.Description(
			"Which store to read: "+domainList+". Required — there is no default, because guessing it from the namespace would answer the wrong question silently.")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description(
			"Namespace. Memory: user/{id}/memory/{type}. Knowledge: {user|app}/{id}/knowledge/... . Context: any registered namespace path.")),
		mcp.WithString("key", mcp.Required(), mcp.Description(
			"Entry key within the namespace. This is the field memory and knowledge revisions carry as `memory_key`.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractGet)

	a.addTool(s, mcp.NewTool("tesseract_history",
		mcp.WithDescription(
			"**Fetch the revision history** at `(domain, namespace, key)`, newest first — one tool for all three domains.\n"+
				"• **Kind of content:** every revision under the key, including superseded and deprecated ones.\n"+
				"• **Result shape:** domain-dependent. `memory` and `knowledge` answer a bare array; pass `limit`, `cursor`, `budget_bytes` or `budget_tokens` and they answer `{results, manifest}` instead. `context` always answers its own budget envelope and honors `limit` only.\n"+
				"• **Scope:** `memory:read` for `memory` and `knowledge`; `context` needs no token.\n"+
				"• **Use this when:** you need to trace how an entry evolved, or read superseded content.\n"+
				"• **Don't use this for:** just the current value (`tesseract_get`).\n"+
				"• **Errors:** `validation_error` (bad or missing `domain`, missing `namespace`/`key`, unusable `cursor`), `domain_unavailable`, `not_found`.\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("domain", mcp.Required(), mcp.Description(
			"Which store to read: "+domainList+". Required — there is no default.")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace, as for `tesseract_get`.")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Entry key within the namespace.")),
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
				"• **Works across domains:** memory and knowledge revisions share one table keyed by `revision_id`, so an ID from either resolves here without saying which it was.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** a `tesseract_recall` or `tesseract_history` result referenced a `revision_id` and you want the full content — the hydrate step of recall → choose → hydrate.\n"+
				"• **Don't use this for:** resolving by `(namespace, key)` — use `tesseract_get`.\n"+
				"• **Side effect:** reinforces the parent entry's activation/access_count — a deliberate read counts as use.\n"+
				"• **Errors:** `validation_error` (missing `revision_id`), `not_found`.\n"+
				"• **Deeper:** `tesseract_skills revisions`.",
		),
		mcp.WithString("revision_id", mcp.Required(), mcp.Description("Revision ID to fetch (e.g. 01HX...)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractGetRevision)

	a.addTool(s, mcp.NewTool("tesseract_deprecate",
		mcp.WithDescription(
			"**Soft-remove one revision** by its `revision_id`. The revision stays in history.\n"+
				"• **Kind of content:** none returned beyond `{status, revision_id}`.\n"+
				"• **Works across domains:** memory and knowledge revisions share one table keyed by `revision_id`, so an ID from either resolves here.\n"+
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
				"• **Kind of content:** none returned. Answers `{touched, not_found}` — `touched` is how many distinct memories were reinforced, `not_found` lists revision IDs that resolved to nothing.\n"+
				"• **Scope:** `memory:read`. It writes, but what it writes is the deliberate-read signal `tesseract_get` already emits on every memory-domain call; a read-only agent that could not close the loop would leave the loop open.\n"+
				"• **Use this when:** you have finished reasoning over a recall result and know which hits shaped the turn. **Call it after the work, not after the search.** Recall deliberately does not reinforce — being returned is the ranker's guess. Touch is you telling it the guess was right, and it is the only input activation has.\n"+
				"• **Touch only what genuinely shaped the turn.** Under-reporting is fine; over-reporting is worse than silence, because it teaches the ranking that noise is signal.\n"+
				"• **Don't use this for:** everything you recalled, everything you skimmed, anything you merely saw in a result list, or as a way to pin a memory you want ranked highly. Reinforcement has diminishing returns — each touch closes a fraction of the remaining distance to a ceiling, so the tenth touch moves a memory far less than the first and no amount of touching passes the ceiling. Inflating a report buys very little ranking and costs the ranking its ability to tell signal from noise.\n"+
				"• **Effect per distinct memory:** `activation` moves a fixed fraction of the way toward its ceiling, `access_count` increments, `last_accessed_at` is set. Naming a revision twice, or naming two revisions of the same memory, reinforces it once.\n"+
				"• **Works across domains:** memory and knowledge revision IDs both resolve, so a mixed `tesseract_recall` result can be reported in one call.\n"+
				"• **Deeper:** `tesseract_skills memory` for the worked loop; `tesseract_skills recall-and-ranking` for how activation ranks.",
		),
		mcp.WithString("revision_ids", mcp.Required(), mcp.Description(
			"JSON array of `revision_id` strings from a recall, lookup, or history result "+
				"(e.g. [\"01HX...\",\"01HY...\"]). Every result carries `revision_id` under every `payload_mode`, "+
				"so this is always available without a full read. "+
				"Unknown IDs come back in `not_found` rather than failing the call, so a partly-stale set is safe to send. "+
				"At most "+strconv.Itoa(memory.MaxTouchRevisions)+" per call — a request-size bound, not a budget to spend: "+
				"a turn that genuinely used that many memories is rare, and the guidance above still applies well below the cap.")),
		mcp.WithReadOnlyHintAnnotation(false),
		// Not idempotent: each call is a fresh report of use, and reinforcement
		// accumulates across calls even though it collapses within one.
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractTouch)
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// handleTesseractGet dispatches on `domain`.
//
// Each arm is the body of the tool it replaces, unchanged apart from reading
// `key` where the memory and knowledge tools read `memory_key`. That includes
// the parts that differ between arms and would be tempting to unify: the
// context arm performs no scope check (the context read surface never has), and
// only the memory arm reinforces. Both differences are contracts callers
// already depend on, so they are preserved and documented rather than smoothed.
func (a *Adapter) handleTesseractGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	domain, errRes := resolveReadDomain(req)
	if errRes != nil {
		return errRes, nil
	}

	if domain == readDomainContext {
		return a.handleContextHead(ctx, req)
	}

	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}
	namespace := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if namespace == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}

	var (
		rev memory.Revision
		err error
	)
	switch domain {
	case string(domains.Memory):
		if a.MemoryStore == nil {
			return domainUnavailable(domain), nil
		}
		// Deliberate read: GetCurrentReinforced bumps activation/access_count.
		rev, err = a.MemoryStore.GetCurrentReinforced(ctx, namespace, key)
	case string(domains.Knowledge):
		if a.KnowledgeStore == nil {
			return domainUnavailable(domain), nil
		}
		rev, err = a.KnowledgeStore.GetCurrent(ctx, namespace, key)
	}
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(rev), nil
}

// handleTesseractHistory dispatches on `domain`. See handleTesseractGet on why
// the arms are not unified.
func (a *Adapter) handleTesseractHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	domain, errRes := resolveReadDomain(req)
	if errRes != nil {
		return errRes, nil
	}

	if domain == readDomainContext {
		return a.handleContextHistory(ctx, req)
	}

	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}
	namespace := req.GetString("namespace", "")
	key := req.GetString("key", "")
	if namespace == "" || key == "" {
		return toolError("validation_error", "namespace and key are required"), nil
	}

	pr, errRes := a.resolveHistoryPageRequest(req)
	if errRes != nil {
		return errRes, nil
	}

	var (
		revs []memory.Revision
		err  error
	)
	switch domain {
	case string(domains.Memory):
		if a.MemoryStore == nil {
			return domainUnavailable(domain), nil
		}
		revs, err = a.MemoryStore.GetHistory(ctx, namespace, key)
	case string(domains.Knowledge):
		if a.KnowledgeStore == nil {
			return domainUnavailable(domain), nil
		}
		revs, err = a.KnowledgeStore.GetHistory(ctx, namespace, key)
	}
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
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
			return toolError("validation_error", err.Error()), nil
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
		return toolError("validation_error", "revision_id is required"), nil
	}
	// Deliberate read: GetRevisionByIDReinforced bumps activation/access_count.
	rev, err := a.revisionStore().GetRevisionByIDReinforced(ctx, revisionID)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
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
		return toolError("validation_error", "revision_id is required"), nil
	}
	if err := a.revisionStore().Deprecate(ctx, revisionID); err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			return toolError("not_found", err.Error()), nil
		}
		return nil, err
	}
	return toolJSON(map[string]string{"status": "deprecated", "revision_id": revisionID}), nil
}

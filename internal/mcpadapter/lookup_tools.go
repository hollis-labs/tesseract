package mcpadapter

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerLookupTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("tesseract_lookup",
		mcp.WithDescription(
			"**Unified search across memory + knowledge.** Returns ranked results + facet histograms.\n"+
				"• **Kind of content:** mixed memory and knowledge revisions matching query + filters, with a uniform shape.\n"+
				"• **Result shape:** `{results: [{revision, score}], facets: {domains, kinds, sources}}`, best first. `state` rides only on `payload_mode=full`; projected results carry `payload_mode` instead.\n"+
				"• **`score`:** ranking-relative, comparable only within one response. `activation` → activation strength; `similarity` → cosine similarity (can be 0 or negative); `relevance` → RRF-fused BM25 + cosine. **Absent under `chronological`** — order is carried by array order plus `revision.created_at`.\n"+
				"• **Just-in-time pattern — recall → choose → hydrate.** Look up at the default `payload_mode` to see what exists, **choose** the few hits worth reading, then **hydrate** each by passing its `revision_id` to `memory_get_revision`. Reaching for `payload_mode=full` to skip the third step is how a single lookup eats a context window.\n"+
				"• **`payload_mode`:** `keys` | `summary` | `full`; server-configured default. Every result carries `revision_id` in every mode. Under `keys` and `summary` each result also carries `payload_mode` — a missing `payload.body` there means **withheld**, never **empty**, so never write back a body you looked up without it.\n"+
				"• **`pointer_health`:** on each knowledge result under `summary` and `full` (not `keys`). Says whether the entry's pointer was actually resolved, and when — the body is the durable half of a knowledge entry, the pointer is the half that rots. **Absent means the revision has no pointer at all**, never that it is healthy. Filter with the `pointer_health` argument to enumerate suspect entries by query instead of discovering them by failure.\n"+
				"• **`facets`:** counted from the returned rows before projection, so changing `payload_mode` never changes them. They describe **only what `limit` returned**, not the full match set — the counts sum to the number of results, so do not read them as a corpus histogram.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you don't know whether the content is memory or knowledge, or you want both. **Prefer this BEFORE filesystem or web exploration.**\n"+
				"• **Don't use this for:** memory-only recall (`memory_recall`), deterministic selection (`views_evaluate`).\n"+
				"• **Deeper:** `tesseract_skills recall-and-ranking` for ranking modes; `tesseract_skills facets-and-kinds` for facet filters.",
		),
		mcp.WithString("namespaces", mcp.Required(), mcp.Description("JSON array of namespace strings. Memory namespaces use typed form user/{id}/memory/{type} or the prefix form user/{id}/memory (matches every type). Knowledge namespaces use user/{id}/knowledge/... (e.g. [\"user/chrispian/memory/decisions\",\"user/chrispian/knowledge/portfolio\"]).")),
		mcp.WithString("query", mcp.Description("Semantic query (required for similarity or relevance ranking)")),
		mcp.WithString("ranking", mcp.Description("activation|chronological|similarity|relevance (default: relevance when query is set, else activation)")),
		mcp.WithString("revision_scope", mcp.Description("current|timeline (default: current)")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 30, max 500)")),
		mcp.WithString("domains", mcp.Description("JSON array of domain filters, e.g. [\"memory\",\"knowledge\"]")),
		mcp.WithString("facet_kinds", mcp.Description("JSON array of facet kind filters (knowledge), e.g. [\"package\",\"doc\"]")),
		mcp.WithString("facet_sources", mcp.Description("JSON array of facet source filters (knowledge), e.g. [\"filesystem\",\"obsidian\"]")),
		// Rendered from the vocabulary rather than restated, so this cannot
		// advertise a status the filter does not accept.
		mcp.WithString("pointer_health", mcp.Description(
			"JSON array of pointer verification statuses (knowledge). Allowed: "+
				strings.Join(memory.PointerHealthStatusVocabulary(), ", ")+
				". `unresolvable` = a resolver got a definitive negative (missing file, HTTP 404/410). "+
				"`unverifiable` = it could not tell (timeout, 403, rate limit, or a scheme with no resolver) — NOT evidence of death. "+
				"`unchecked` = the pointer names something external and nobody has looked yet. "+
				"`not_applicable` = scheme `nil`, the record declares it has no external source. "+
				"Filtering happens in SQL before `limit`, so [\"unresolvable\"] enumerates the dead set rather than sampling it.")),
		mcp.WithString("origins", mcp.Description("JSON array of origin filters")),
		mcp.WithString("statuses", mcp.Description("JSON array of status filters")),
		mcp.WithString("tags", mcp.Description("JSON array of tag filters")),
		mcp.WithNumber("confidence_min", mcp.Description("Minimum confidence")),
		mcp.WithString("since", mcp.Description("RFC3339 lower bound")),
		mcp.WithString("until", mcp.Description("RFC3339 upper bound")),
		mcp.WithString("payload_mode", mcp.Description(payloadModeArgDescription)),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleTesseractLookup)
}

func (a *Adapter) handleTesseractLookup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	payloadMode, modeErr := a.resolvePayloadMode(req)
	if modeErr != nil {
		return modeErr, nil
	}

	unmarshalStrings := func(field string) ([]string, *mcp.CallToolResult) {
		out, _, err := parseStringArrayArg(req, field)
		if err != nil {
			return nil, toolError("validation_error", field+" "+err.Error())
		}
		return out, nil
	}

	namespaces, errRes := unmarshalStrings("namespaces")
	if errRes != nil {
		return errRes, nil
	}

	domainStrs, errRes := unmarshalStrings("domains")
	if errRes != nil {
		return errRes, nil
	}
	var doms []domains.Domain
	for _, d := range domainStrs {
		doms = append(doms, domains.Domain(d))
	}

	kinds, errRes := unmarshalStrings("facet_kinds")
	if errRes != nil {
		return errRes, nil
	}
	sources, errRes := unmarshalStrings("facet_sources")
	if errRes != nil {
		return errRes, nil
	}

	pointerHealth, errRes := unmarshalStrings("pointer_health")
	if errRes != nil {
		return errRes, nil
	}
	// Reject an unknown status rather than letting it filter to nothing. An
	// empty result set from a typo reads exactly like a clean corpus, which is
	// the failure this ticket exists to remove.
	for _, h := range pointerHealth {
		if !memory.PointerHealthStatus(h).Valid() {
			return toolError("validation_error",
				"pointer_health must be one of "+strings.Join(memory.PointerHealthStatusVocabulary(), ", ")+", got "+h), nil
		}
	}

	originStrs, errRes := unmarshalStrings("origins")
	if errRes != nil {
		return errRes, nil
	}
	var origins []memory.Origin
	for _, o := range originStrs {
		origins = append(origins, memory.Origin(o))
	}

	statusStrs, errRes := unmarshalStrings("statuses")
	if errRes != nil {
		return errRes, nil
	}
	var statuses []memory.Status
	for _, st := range statusStrs {
		statuses = append(statuses, memory.Status(st))
	}

	tags, errRes := unmarshalStrings("tags")
	if errRes != nil {
		return errRes, nil
	}

	var since, until *time.Time
	if raw := req.GetString("since", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError("validation_error", "since must be RFC3339: "+err.Error()), nil
		}
		since = &t
	}
	if raw := req.GetString("until", ""); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return toolError("validation_error", "until must be RFC3339: "+err.Error()), nil
		}
		until = &t
	}

	in := memory.RecallInput{
		Namespaces:    namespaces,
		RevisionScope: memory.RevisionScope(req.GetString("revision_scope", "")),
		Ranking:       memory.Ranking(req.GetString("ranking", "")),
		Query:         req.GetString("query", ""),
		Limit:         int(req.GetFloat("limit", 0)),
		Filters: memory.RecallFilters{
			Origins:       origins,
			Statuses:      statuses,
			Tags:          tags,
			ConfidenceMin: req.GetFloat("confidence_min", 0),
			Since:         since,
			Until:         until,
			Domains:       doms,
			FacetKinds:    kinds,
			FacetSources:  sources,
			PointerHealth: pointerHealth,
		},
	}

	results, err := a.MemoryStore.Recall(ctx, in)
	if err != nil {
		if errors.Is(err, memory.ErrSimilarityUnavailable) {
			return toolError("similarity_unavailable", err.Error()), nil
		}
		if errors.Is(err, memory.ErrInvalidInput) {
			return toolError("validation_error", err.Error()), nil
		}
		return nil, err
	}

	// Facets are counted from the unprojected results, so payload_mode never
	// changes them. They are still best-effort: Recall truncates to Limit
	// before returning (recall.go, step 6), so these counts describe the
	// RETURNED rows, not the full match set — same caveat the HTTP peer
	// documents at lookup_handler.go.
	facets := map[string]map[string]int{
		"domains": {},
		"kinds":   {},
		"sources": {},
	}
	for _, r := range results {
		if d := string(r.Revision.Domain); d != "" {
			facets["domains"][d]++
		}
		if k := r.Revision.Facets.Kind; k != "" {
			facets["kinds"][k]++
		}
		if s := r.Revision.Facets.Source; s != "" {
			facets["sources"][s]++
		}
	}

	return toolJSON(map[string]any{
		"results": memory.ProjectResults(results, payloadMode),
		"facets":  facets,
	}), nil
}

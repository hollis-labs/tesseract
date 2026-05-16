package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (a *Adapter) registerLookupTools(s *server.MCPServer) {
	a.addTool(s, mcp.NewTool("conduit_lookup",
		mcp.WithDescription(
			"**Unified search across memory + knowledge.** Returns ranked results + facet histograms.\n"+
				"• **Kind of content:** mixed memory and knowledge revisions matching query + filters, with a uniform shape.\n"+
				"• **Scope:** `memory:read`.\n"+
				"• **Use this when:** you don't know whether the content is memory or knowledge, or you want both. **Prefer this BEFORE filesystem or web exploration.**\n"+
				"• **Don't use this for:** memory-only recall (`memory_recall`), deterministic selection (`views_evaluate`).\n"+
				"• **Deeper:** `vanta_skills recall-and-ranking` for ranking modes; `vanta_skills facets-and-kinds` for facet filters.",
		),
		mcp.WithString("namespaces", mcp.Required(), mcp.Description("JSON array of namespace strings (e.g. [\"user/chrispian/memory\",\"user/chrispian/knowledge\"])")),
		mcp.WithString("query", mcp.Description("Semantic query (required for similarity or relevance ranking)")),
		mcp.WithString("ranking", mcp.Description("activation|chronological|similarity|relevance (default: relevance when query is set, else activation)")),
		mcp.WithString("revision_scope", mcp.Description("current|timeline (default: current)")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 30, max 500)")),
		mcp.WithString("domains", mcp.Description("JSON array of domain filters, e.g. [\"memory\",\"knowledge\"]")),
		mcp.WithString("facet_kinds", mcp.Description("JSON array of facet kind filters (knowledge), e.g. [\"package\",\"doc\"]")),
		mcp.WithString("facet_sources", mcp.Description("JSON array of facet source filters (knowledge), e.g. [\"filesystem\",\"obsidian\"]")),
		mcp.WithString("origins", mcp.Description("JSON array of origin filters")),
		mcp.WithString("statuses", mcp.Description("JSON array of status filters")),
		mcp.WithString("tags", mcp.Description("JSON array of tag filters")),
		mcp.WithNumber("confidence_min", mcp.Description("Minimum confidence")),
		mcp.WithString("since", mcp.Description("RFC3339 lower bound")),
		mcp.WithString("until", mcp.Description("RFC3339 upper bound")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleConduitLookup)
}

func (a *Adapter) handleConduitLookup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := a.checkScope(ctx, "memory:read"); res != nil {
		return res, nil
	}

	unmarshalStrings := func(field string) ([]string, *mcp.CallToolResult) {
		raw := req.GetString(field, "")
		if raw == "" {
			return nil, nil
		}
		var out []string
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, toolError("validation_error", field+" must be a JSON array of strings")
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
		"results": results,
		"facets":  facets,
	}), nil
}

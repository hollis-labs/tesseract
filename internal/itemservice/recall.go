package itemservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

type RecallResult struct {
	Revision      *memory.Revision      `json:"revision,omitempty"`
	Item          *WorkspaceItem        `json:"item,omitempty"`
	Score         *float64              `json:"score,omitempty"`
	State         *memory.State         `json:"state,omitempty"`
	PointerHealth *memory.PointerHealth `json:"pointer_health,omitempty"`
}

type ProjectedWorkspaceItem struct {
	ItemID        string             `json:"item_id"`
	Domain        string             `json:"domain"`
	Namespace     string             `json:"namespace"`
	Key           string             `json:"key,omitempty"`
	WorkstreamID  string             `json:"workstream_id,omitempty"`
	Provenance    *memory.Provenance `json:"provenance,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Payload       *WorkspacePayload  `json:"payload,omitempty"`
	Tags          []string           `json:"tags,omitempty"`
	ConsumerState json.RawMessage    `json:"consumer_state,omitempty"`
}

type ProjectedResult struct {
	Revision      *memory.ProjectedRevision `json:"revision,omitempty"`
	Item          *ProjectedWorkspaceItem   `json:"item,omitempty"`
	Score         *float64                  `json:"score,omitempty"`
	PayloadMode   memory.PayloadMode        `json:"payload_mode"`
	PointerHealth *memory.PointerHealth     `json:"pointer_health,omitempty"`
}

type PagedRecall struct {
	Results  any             `json:"results"`
	Manifest memory.Manifest `json:"manifest"`
	Kept     []RecallResult  `json:"-"`
}

func (s *Service) RecallPaged(ctx context.Context, in memory.RecallInput, pr memory.PageRequest) (PagedRecall, error) {
	withWorkspace, revisionDomains, err := splitDomains(in.Filters.Domains)
	if err != nil {
		return PagedRecall{}, err
	}
	if !withWorkspace {
		if s.Revisions == nil {
			return PagedRecall{}, fmt.Errorf("revision store unavailable")
		}
		page, recallErr := s.Revisions.RecallPaged(ctx, in, pr)
		if recallErr != nil {
			return PagedRecall{}, recallErr
		}
		return convertRevisionPage(page, pr.PayloadMode), nil
	}
	if s.Workspace == nil {
		return PagedRecall{}, fmt.Errorf("workspace store unavailable")
	}
	if validationErr := validateWorkspaceRecall(&in); validationErr != nil {
		return PagedRecall{}, validationErr
	}
	workspaceNamespaces, err := selectWorkspaceNamespaces(in.Namespaces)
	if err != nil {
		return PagedRecall{}, err
	}
	if len(workspaceNamespaces) == 0 && len(revisionDomains) == 0 {
		return PagedRecall{}, fmt.Errorf("%w: workspace recall requires a workspace namespace", memory.ErrInvalidInput)
	}

	ranking, searchMode := resolveRecallModes(in)
	all := make([]RecallResult, 0)
	if len(revisionDomains) > 0 {
		if s.Revisions == nil {
			return PagedRecall{}, fmt.Errorf("revision store unavailable")
		}
		revIn := in
		revIn.Filters.Domains = revisionDomains
		revIn.Offset = 0
		revPage, recallErr := s.Revisions.RecallAll(ctx, revIn)
		if recallErr != nil {
			return PagedRecall{}, recallErr
		}
		for i := range revPage.Results {
			r := revPage.Results[i]
			state := r.State
			rev := r.Revision
			all = append(all, RecallResult{Revision: &rev, Score: r.Score, State: &state, PointerHealth: r.PointerHealth})
		}
	}
	var workspaceResults []workspace.RecallResult
	if len(workspaceNamespaces) > 0 {
		workspaceResults, err = s.Workspace.RecallAll(ctx, workspace.RecallInput{
			Namespaces: workspaceNamespaces, Query: queryForWorkspace(in, ranking), Ranking: ranking,
			WorkstreamID: in.Filters.WorkstreamID,
			Tags:         in.Filters.Tags, StateFilters: in.Filters.StateFilters,
			Since: in.Filters.Since, Until: in.Filters.Until,
		})
		if err != nil {
			return PagedRecall{}, err
		}
	}
	workspaceStart := len(all)
	for _, r := range workspaceResults {
		item := PublicWorkspaceItem(r.Item)
		all = append(all, RecallResult{Item: &item, Score: r.Score})
	}
	orderCombined(all, workspaceStart, len(revisionDomains) > 0, ranking, searchMode, strings.TrimSpace(in.Query) != "")

	fingerprint := memory.RecallOrderingFingerprint(in)
	offset, err := memory.DecodeCursor(pr.Cursor, fingerprint)
	if err != nil {
		return PagedRecall{}, err
	}
	limit, capped := memory.ClampRecallLimit(pr.Limit, pr.PayloadMode)
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	window := all[offset:end]
	projected := projectResults(window, pr.PayloadMode)
	keptCount, bytes, reason := cutBudget(projected, pr.Budget)
	window = window[:keptCount]
	projected = projectResults(window, pr.PayloadMode)
	manifest := combinedManifest(len(all), offset, keptCount, bytes, fingerprint, reason, capped)
	return PagedRecall{Results: projected, Manifest: manifest, Kept: window}, nil
}

func selectWorkspaceNamespaces(requested []string) ([]string, error) {
	out := make([]string, 0, len(requested))
	for _, namespace := range requested {
		validationErr := memory.ValidateWorkspaceRecallNamespace(namespace)
		if validationErr == nil {
			out = append(out, namespace)
			continue
		}
		for _, segment := range strings.Split(strings.TrimSuffix(namespace, "/*"), "/") {
			if segment == workspace.Domain {
				return nil, fmt.Errorf("%w: %w", memory.ErrInvalidInput, validationErr)
			}
		}
	}
	return out, nil
}

func splitDomains(requested []domains.Domain) (bool, []domains.Domain, error) {
	if len(requested) == 0 {
		return false, nil, nil
	}
	seen := map[string]bool{}
	var revision []domains.Domain
	withWorkspace := false
	for _, d := range requested {
		name := string(d)
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == workspace.Domain {
			withWorkspace = true
			continue
		}
		if !d.Valid() {
			return false, nil, fmt.Errorf("%w: domains must contain memory, knowledge, event, or workspace; got %q", memory.ErrInvalidInput, name)
		}
		revision = append(revision, d)
	}
	return withWorkspace, revision, nil
}

func validateWorkspaceRecall(in *memory.RecallInput) error {
	if len(in.Namespaces) == 0 {
		return fmt.Errorf("%w: at least one namespace is required", memory.ErrInvalidInput)
	}
	if in.RevisionScope == memory.RevisionScopeTimeline {
		return fmt.Errorf("%w: history_unavailable: workspace has no immutable revisions", memory.ErrInvalidInput)
	}
	if in.RevisionScope != "" && in.RevisionScope != memory.RevisionScopeCurrent {
		return fmt.Errorf("%w: revision_scope must be current when workspace is included", memory.ErrInvalidInput)
	}
	switch in.Ranking {
	case "", memory.RankingActivation, memory.RankingChronological, memory.RankingRelevance:
	default:
		return fmt.Errorf("%w: ranking must be activation, chronological, or relevance when workspace is included", memory.ErrInvalidInput)
	}
	switch in.SearchMode {
	case "", memory.SearchModeHybrid, memory.SearchModeLexical:
	default:
		return fmt.Errorf("%w: workspace supports search_mode=lexical or hybrid; got %q", memory.ErrInvalidInput, in.SearchMode)
	}
	ranking, mode := resolveRecallModes(*in)
	if mode != memory.SearchModeHybrid && ranking != memory.RankingRelevance {
		return fmt.Errorf("%w: search_mode=%s requires ranking=relevance", memory.ErrInvalidInput, mode)
	}
	if ranking == memory.RankingRelevance && strings.TrimSpace(in.Query) == "" {
		return fmt.Errorf("%w: query is required for relevance ranking", memory.ErrInvalidInput)
	}
	if in.Ranking == memory.RankingSimilarity || in.Filters.SimilarityMin != nil {
		return fmt.Errorf("%w: workspace has no embeddings; remove workspace or use lexical, activation, or chronological recall", memory.ErrInvalidInput)
	}
	if in.SearchMode == memory.SearchModeSemantic {
		return fmt.Errorf("%w: workspace cannot use search_mode=semantic because it has no embeddings", memory.ErrInvalidInput)
	}
	unsupported := []struct {
		name string
		used bool
	}{
		{"derived_from", len(in.Filters.DerivedFrom) > 0}, {"statuses", len(in.Filters.Statuses) > 0},
		{"confidence_min", in.Filters.ConfidenceMin != 0}, {"facet_kinds", len(in.Filters.FacetKinds) > 0},
		{"facet_sources", len(in.Filters.FacetSources) > 0}, {"pointer_health", len(in.Filters.PointerHealth) > 0},
		{"related_to", len(in.Filters.RelatedTo) > 0}, {"related_relations", len(in.Filters.RelatedRelations) > 0},
	}
	for _, f := range unsupported {
		if f.used {
			return fmt.Errorf("%w: filter %s is not supported for workspace recall", memory.ErrInvalidInput, f.name)
		}
	}
	if in.Reranker != "" {
		return fmt.Errorf("%w: reranker is not supported when workspace is included", memory.ErrInvalidInput)
	}
	return nil
}

func resolveRecallModes(in memory.RecallInput) (memory.Ranking, memory.SearchMode) {
	ranking := in.Ranking
	if ranking == "" {
		if strings.TrimSpace(in.Query) != "" {
			ranking = memory.RankingRelevance
		} else {
			ranking = memory.RankingActivation
		}
	}
	mode := in.SearchMode
	if mode == "" {
		mode = memory.SearchModeHybrid
	}
	return ranking, mode
}

func queryForWorkspace(in memory.RecallInput, ranking memory.Ranking) string {
	if ranking == memory.RankingRelevance {
		return in.Query
	}
	return ""
}

func orderCombined(results []RecallResult, workspaceStart int, mixed bool, ranking memory.Ranking, _ memory.SearchMode, queried bool) {
	if ranking == memory.RankingRelevance && queried {
		if !mixed {
			// The workspace query is already ordered by BM25. Keep the public
			// lexical contract: order is the signal and score stays absent,
			// because SQLite's lower-is-better value would invert every other
			// score on this surface.
			for i := workspaceStart; i < len(results); i++ {
				results[i].Score = nil
			}
			return
		}
		// Rank fusion compares positions from the separate stores. Raw BM25 and
		// revision-store relevance scores are deliberately not compared.
		for i := 0; i < workspaceStart; i++ {
			v := 1.0 / float64(60+i+1)
			results[i].Score = &v
		}
		for i := workspaceStart; i < len(results); i++ {
			v := 1.0 / float64(60+(i-workspaceStart)+1)
			results[i].Score = &v
		}
		sort.SliceStable(results, func(i, j int) bool {
			if *results[i].Score == *results[j].Score {
				return resultID(results[i]) < resultID(results[j])
			}
			return *results[i].Score > *results[j].Score
		})
		return
	}
	if ranking == memory.RankingActivation {
		sort.SliceStable(results, func(i, j int) bool {
			a, b := score(results[i]), score(results[j])
			if a == b {
				return resultID(results[i]) < resultID(results[j])
			}
			return a > b
		})
		return
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := resultTime(results[i]), resultTime(results[j])
		if a.Equal(b) {
			return resultID(results[i]) > resultID(results[j])
		}
		return a.After(b)
	})
}

func resultID(r RecallResult) string {
	if r.Item != nil {
		return r.Item.ItemID
	}
	return r.Revision.RevisionID
}
func resultTime(r RecallResult) time.Time {
	if r.Item != nil {
		return r.Item.UpdatedAt
	}
	return r.Revision.CreatedAt
}
func score(r RecallResult) float64 {
	if r.Score == nil {
		return 0
	}
	return *r.Score
}

func projectResults(results []RecallResult, mode memory.PayloadMode) any {
	if mode == memory.PayloadModeFull {
		return results
	}
	if !mode.Valid() {
		mode = memory.DefaultPayloadMode
	}
	out := make([]ProjectedResult, 0, len(results))
	for _, r := range results {
		p := ProjectedResult{Score: r.Score, PayloadMode: mode, PointerHealth: r.PointerHealth}
		if r.Revision != nil {
			projected := memory.ProjectResults([]memory.RecallResult{{Revision: *r.Revision, Score: r.Score, State: derefState(r.State), PointerHealth: r.PointerHealth}}, mode).([]memory.ProjectedResult)[0]
			p.Revision = &projected.Revision
			p.PointerHealth = projected.PointerHealth
		} else {
			item := &ProjectedWorkspaceItem{ItemID: r.Item.ItemID, Domain: workspace.Domain, Namespace: r.Item.Namespace, Key: r.Item.Key, WorkstreamID: r.Item.WorkstreamID, Provenance: r.Item.Provenance, UpdatedAt: r.Item.UpdatedAt}
			if mode == memory.PayloadModeSummary {
				item.Payload = &WorkspacePayload{Summary: r.Item.Payload.Summary}
				item.Tags = r.Item.Tags
				item.ConsumerState = r.Item.ConsumerState
			}
			p.Item = item
		}
		out = append(out, p)
	}
	return out
}

func derefState(state *memory.State) memory.State {
	if state == nil {
		return memory.State{}
	}
	return *state
}

func convertRevisionPage(page memory.PagedRecall, mode memory.PayloadMode) PagedRecall {
	kept := make([]RecallResult, 0, len(page.Kept))
	for i := range page.Kept {
		r := page.Kept[i]
		rev := r.Revision
		state := r.State
		kept = append(kept, RecallResult{Revision: &rev, Score: r.Score, State: &state, PointerHealth: r.PointerHealth})
	}
	return PagedRecall{Results: projectResults(kept, mode), Manifest: page.Manifest, Kept: kept}
}

func cutBudget(projected any, budget memory.Budget) (int, int, string) {
	raw, _ := json.Marshal(projected)
	if !budget.Set() {
		return sliceLen(projected), len(raw), ""
	}
	n := sliceLen(projected)
	for keep := 1; keep <= n; keep++ {
		candidate := slicePrefix(projected, keep)
		b, _ := json.Marshal(candidate)
		tokens := memory.EstimateTokens(len(b))
		if (budget.Bytes > 0 && len(b) > budget.Bytes) || (budget.Tokens > 0 && tokens > budget.Tokens) {
			if keep == 1 {
				reason := memory.TruncationBudgetBytes
				if budget.Tokens > 0 && (budget.Bytes == 0 || tokens > budget.Tokens) {
					reason = memory.TruncationBudgetTokens
				}
				return 1, len(b), reason
			}
			prev, _ := json.Marshal(slicePrefix(projected, keep-1))
			reason := memory.TruncationBudgetBytes
			if budget.Tokens > 0 && (budget.Bytes == 0 || memory.EstimateTokens(len(b)) > budget.Tokens) {
				reason = memory.TruncationBudgetTokens
			}
			return keep - 1, len(prev), reason
		}
	}
	return n, len(raw), ""
}

func sliceLen(v any) int {
	switch x := v.(type) {
	case []RecallResult:
		return len(x)
	case []ProjectedResult:
		return len(x)
	}
	return 0
}
func slicePrefix(v any, n int) any {
	switch x := v.(type) {
	case []RecallResult:
		return x[:n]
	case []ProjectedResult:
		return x[:n]
	}
	return v
}

func combinedManifest(total, offset, returned, bytes int, fingerprint, reason string, capped bool) memory.Manifest {
	hasMore := offset+returned < total
	m := memory.Manifest{ResultsTotal: total, ResultsReturned: returned, BytesReturned: bytes, TokensEstimate: memory.EstimateTokens(bytes), Truncated: hasMore}
	if hasMore {
		if reason != "" {
			m.TruncationReason = reason
		} else if capped {
			m.TruncationReason = memory.TruncationPayloadModeLimitCap
		} else {
			m.TruncationReason = memory.TruncationLimit
		}
		next := memory.EncodeCursor(offset+returned, fingerprint)
		m.NextCursor = &next
	}
	return m
}

func IsInvalidCursor(err error) bool { return errors.Is(err, memory.ErrInvalidCursor) }

package contextstore

import (
	"context"
	"strings"
)

// EvaluateResult is the shared return shape for view-evaluate operations.
// HTTP and MCP callers marshal this identically; HTTP additionally wraps the
// body with a stable JSON shape (`items` + `evaluation_meta`).
type EvaluateResult struct {
	Items           []Record `json:"items"`
	SortKeys        []string `json:"sort_keys"`
	MatchedCount    int      `json:"matched_count"`
	Truncated       bool     `json:"truncated"`
	NormalizedScope string   `json:"normalized_scope"`
}

// Evaluate runs Select with deterministic defaults matching the HTTP
// /v1/views/evaluate contract: applies the caller-provided limit when > 0,
// falls back to DefaultSelectLimit, sorts by namespace/key/revision when no
// order is supplied, and optionally strips payloads.
func (s *Store) Evaluate(ctx context.Context, sel Selector, includePayload bool, limit int) (EvaluateResult, error) {
	if limit > 0 {
		sel.Limit = limit
	}
	if sel.Limit == 0 {
		sel.Limit = DefaultSelectLimit
	}
	if len(sel.Order) == 0 {
		sel.Order = []string{"namespace", "key", "revision"}
	}
	items, err := s.Select(ctx, sel)
	if err != nil {
		return EvaluateResult{}, err
	}
	if !includePayload {
		for i := range items {
			items[i].Payload = nil
		}
	}
	return EvaluateResult{
		Items:           items,
		SortKeys:        sel.Order,
		MatchedCount:    len(items),
		Truncated:       len(items) >= sel.Limit,
		NormalizedScope: NormalizedScope(sel.RevisionScope),
	}, nil
}

// NormalizedScope canonicalizes a revision_scope string: returns "all" when
// the trimmed, case-insensitive value is "all", otherwise "head".
func NormalizedScope(scope string) string {
	if strings.EqualFold(strings.TrimSpace(scope), "all") {
		return "all"
	}
	return "head"
}

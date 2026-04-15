package contextstore

import "context"

// EstimateResult summarizes a Select without returning the full record set.
// It is the shared shape returned by both MCP and HTTP estimate operations.
type EstimateResult struct {
	RecordCount         int `json:"record_count"`
	TotalBytes          int `json:"total_bytes"`
	TotalTokensEstimate int `json:"total_tokens_estimate"`
}

// Estimate runs Select with the given selector and returns aggregate counts
// (records, payload bytes, rough token estimate) without the records themselves.
// Token estimate is bytes/4 rounded up — a coarse but deterministic proxy.
func (s *Store) Estimate(ctx context.Context, sel Selector) (EstimateResult, error) {
	records, err := s.Select(ctx, sel)
	if err != nil {
		return EstimateResult{}, err
	}
	total := 0
	for _, rec := range records {
		total += len(rec.Payload)
	}
	return EstimateResult{
		RecordCount:         len(records),
		TotalBytes:          total,
		TotalTokensEstimate: (total + 3) / 4,
	}, nil
}

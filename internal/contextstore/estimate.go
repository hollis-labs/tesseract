package contextstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EstimateResult summarizes a Select without returning the full record set.
// It is the shared shape returned by both MCP and HTTP estimate operations.
type EstimateResult struct {
	RecordCount         int `json:"record_count"`
	TotalBytes          int `json:"total_bytes"`
	TotalTokensEstimate int `json:"total_tokens_estimate"`
}

// Estimate counts records matching the selector and totals their payload
// bytes (with a coarse bytes/4 token proxy). It bypasses the default
// SelectLimit so callers get an honest count for "will this selector
// overflow my budget?" — the limit cap that protects Select's payload
// loading is irrelevant here because Estimate stat()s files instead of
// reading them.
func (s *Store) Estimate(ctx context.Context, sel Selector) (EstimateResult, error) {
	// Mirror the reads that Select performs but project only the columns
	// needed for counting + sizing, and skip the limit cap entirely.
	if err := validateSelectorForEstimate(&sel); err != nil {
		return EstimateResult{}, err
	}
	scope := NormalizedScope(sel.RevisionScope)

	query := `
SELECT DISTINCT r.namespace, r.file_path
FROM records r`
	if scope == "head" {
		query += `
JOIN heads h ON h.head_record_id = r.record_id`
	}
	if len(sel.TagsAny) > 0 {
		query += `
JOIN record_tags rt ON rt.record_id = r.record_id AND rt.tag IN (` + placeholders(len(sel.TagsAny)) + `)`
	}
	query += "\nWHERE 1=1"

	var args []any
	for _, tag := range sel.TagsAny {
		args = append(args, strings.TrimSpace(tag))
	}
	if len(sel.Keys) > 0 {
		query += " AND r.key_name IN (" + placeholders(len(sel.Keys)) + ")"
		for _, key := range sel.Keys {
			args = append(args, strings.TrimSpace(key))
		}
	}
	if len(sel.Types) > 0 {
		query += " AND r.record_type IN (" + placeholders(len(sel.Types)) + ")"
		for _, t := range sel.Types {
			args = append(args, strings.TrimSpace(t))
		}
	}
	if len(sel.Statuses) > 0 {
		query += " AND r.status IN (" + placeholders(len(sel.Statuses)) + ")"
		for _, st := range sel.Statuses {
			args = append(args, strings.TrimSpace(st))
		}
	}

	// Query is built from static fragments + placeholders; every dynamic
	// value flows through args via parameterized binds. Same shape as the
	// pre-existing Select() construction in store.go.
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return EstimateResult{}, err
	}
	defer func() { _ = rows.Close() }()

	var (
		count int
		total int64
	)
	for rows.Next() {
		var namespace, relPath string
		if err := rows.Scan(&namespace, &relPath); err != nil {
			return EstimateResult{}, err
		}
		if !matchNamespace(sel.Namespaces, namespace) {
			continue
		}
		info, err := os.Stat(filepath.Join(s.recordsDir, relPath))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return EstimateResult{}, fmt.Errorf("%w: payload missing for %s", ErrConsistencyFault, relPath)
			}
			return EstimateResult{}, err
		}
		count++
		total += info.Size()
	}
	if err := rows.Err(); err != nil {
		return EstimateResult{}, err
	}
	return EstimateResult{
		RecordCount:         count,
		TotalBytes:          int(total),
		TotalTokensEstimate: int((total + 3) / 4),
	}, nil
}

// validateSelectorForEstimate runs the same shape checks validateSelector
// applies, but skips the Limit defaulting/cap — Estimate must count the
// full matching set regardless of how callers would page Select results.
func validateSelectorForEstimate(sel *Selector) error {
	saved := sel.Limit
	sel.Limit = 1 // bypass the "0 → DefaultSelectLimit" + MaxSelectLimit checks
	err := validateSelector(sel)
	sel.Limit = saved
	return err
}

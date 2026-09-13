package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/memorytime"
)

const defaultSearchLimit = 50
const maxSearchLimit = 200

// SearchLexical searches one exact workspace namespace without recording use.
func (s *Store) SearchLexical(ctx context.Context, namespace, query string, limit int) ([]SearchResult, error) {
	if err := memory.ValidateWorkspaceNamespace(namespace); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	match, err := lexicalMatch(query)
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = defaultSearchLimit
	}
	if limit < 0 || limit > maxSearchLimit {
		return nil, fmt.Errorf("%w: search limit must be between 1 and %d", ErrInvalidInput, maxSearchLimit)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.item_id, w.namespace, w.key_name, w.summary, w.updated_at,
		       bm25(workspace_items_fts)
		FROM workspace_items_fts
		JOIN workspace_items w ON w.rowid = workspace_items_fts.rowid
		WHERE workspace_items_fts MATCH ? AND w.namespace = ?
		ORDER BY bm25(workspace_items_fts), w.item_id
		LIMIT ?`, match, namespace, limit)
	if err != nil {
		return nil, fmt.Errorf("workspace lexical search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	results := make([]SearchResult, 0)
	for rows.Next() {
		var result SearchResult
		var key sql.NullString
		var updatedRaw string
		if scanErr := rows.Scan(&result.ItemID, &result.Namespace, &key, &result.Summary, &updatedRaw, &result.Score); scanErr != nil {
			return nil, scanErr
		}
		result.Key = key.String
		result.UpdatedAt, err = memorytime.Parse(updatedRaw)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func lexicalMatch(query string) (string, error) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return "", fmt.Errorf("%w: lexical query is required", ErrInvalidInput)
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND "), nil
}

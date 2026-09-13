package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/memorytime"
)

const defaultSearchLimit = 50
const maxSearchLimit = 200

const recallItemColumns = `w.item_id, w.version_token, w.namespace, w.key_name, w.summary, w.body,
	w.data, w.data_schema_hash, w.tags, w.consumer_state, w.author_agent_id, w.author_version,
	w.session_id, w.created_at, w.updated_at, w.activation, w.access_count, w.last_used_at, w.last_decayed_at,
	w.workstream_id, w.write_context`

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

var recallStateFieldRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Recall returns current workspace items under lexical, activation, or
// chronological ordering. It never records use.
func (s *Store) Recall(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	return s.recall(ctx, in, true)
}

// RecallAll returns every matching current item in the requested ordering.
// Public cross-store paging needs the complete sequence before it fuses ranks
// and applies its own cursor, projection and budget window.
func (s *Store) RecallAll(ctx context.Context, in RecallInput) ([]RecallResult, error) {
	return s.recall(ctx, in, false)
}

func (s *Store) recall(ctx context.Context, in RecallInput, bounded bool) ([]RecallResult, error) {
	if err := memory.ValidateWorkstreamID(in.WorkstreamID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if len(in.Namespaces) == 0 {
		return nil, fmt.Errorf("%w: at least one namespace is required", ErrInvalidInput)
	}
	if bounded && in.Limit <= 0 {
		in.Limit = maxSearchLimit
	}
	if bounded && in.Limit > maxSearchLimit {
		in.Limit = maxSearchLimit
	}

	var where []string
	var args []any
	var namespaceClauses []string
	for _, ns := range in.Namespaces {
		if strings.HasSuffix(ns, "/*") {
			prefix := strings.TrimSuffix(ns, "/*")
			if err := memory.ValidateWorkspaceRecallNamespace(ns); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
			}
			namespaceClauses = append(namespaceClauses, `instr(w.namespace, ?) = 1`)
			args = append(args, prefix+"/")
			continue
		}
		if err := memory.ValidateWorkspaceNamespace(ns); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
		namespaceClauses = append(namespaceClauses, "w.namespace = ?")
		args = append(args, ns)
	}
	where = append(where, "("+strings.Join(namespaceClauses, " OR ")+")")
	if in.WorkstreamID != "" {
		where = append(where, "w.workstream_id = ?")
		args = append(args, in.WorkstreamID)
	}

	if len(in.Tags) > 0 {
		where = append(where, `w.tags IS NOT NULL AND EXISTS (
			SELECT 1 FROM json_each(w.tags) jt WHERE jt.value IN (`+placeholders(len(in.Tags))+`))`)
		for _, tag := range in.Tags {
			args = append(args, tag)
		}
	}
	stateWhere, stateArgs, err := workspaceStateClauses(in.StateFilters)
	if err != nil {
		return nil, err
	}
	where = append(where, stateWhere...)
	args = append(args, stateArgs...)
	if in.Since != nil {
		where = append(where, "w.updated_at >= ?")
		args = append(args, memorytime.Format(in.Since.UTC()))
	}
	if in.Until != nil {
		where = append(where, "w.updated_at <= ?")
		args = append(args, memorytime.Format(in.Until.UTC()))
	}

	from := "workspace_items w"
	order := "w.updated_at DESC, w.item_id DESC"
	scoreExpr := "NULL"
	if strings.TrimSpace(in.Query) != "" {
		match, matchErr := lexicalMatch(in.Query)
		if matchErr != nil {
			return nil, matchErr
		}
		from = "workspace_items_fts JOIN workspace_items w ON w.rowid = workspace_items_fts.rowid"
		where = append([]string{"workspace_items_fts MATCH ?"}, where...)
		args = append([]any{match}, args...)
		order = "bm25(workspace_items_fts), w.item_id"
		scoreExpr = "bm25(workspace_items_fts)"
	} else if in.Ranking == memory.RankingActivation {
		order = "w.activation DESC, w.item_id"
		scoreExpr = "w.activation"
	}
	// #nosec G202 -- every SQL fragment is selected from constants above;
	// caller values remain placeholders in args.
	query := `SELECT ` + recallItemColumns + `, ` + scoreExpr + ` FROM ` + from +
		` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order
	if bounded {
		query += ` LIMIT ?`
		args = append(args, in.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("workspace recall: %w", err)
	}
	defer func() { _ = rows.Close() }()
	results := make([]RecallResult, 0)
	for rows.Next() {
		var score sql.NullFloat64
		item, scanErr := scanItemWithScore(rows, &score)
		if scanErr != nil {
			return nil, scanErr
		}
		var scorePtr *float64
		if score.Valid {
			v := score.Float64
			scorePtr = &v
		}
		results = append(results, RecallResult{Item: item, Score: scorePtr})
	}
	return results, rows.Err()
}

func scanItemWithScore(row scanner, score *sql.NullFloat64) (Item, error) {
	var item Item
	var key, body, data, dataHash, tags, consumer, workstreamID, writeContext sql.NullString
	var createdRaw, updatedRaw, usedRaw, decayedRaw string
	err := row.Scan(&item.ItemID, &item.VersionToken, &item.Namespace, &key, &item.Summary, &body,
		&data, &dataHash, &tags, &consumer, &item.Author.AgentID, &item.Author.AgentVersion,
		&item.SessionID, &createdRaw, &updatedRaw, &item.Activation, &item.AccessCount, &usedRaw, &decayedRaw,
		&workstreamID, &writeContext, score)
	if err != nil {
		return Item{}, err
	}
	item.Domain = Domain
	item.Key, item.Body, item.DataSchemaHash = key.String, body.String, dataHash.String
	item.WorkstreamID = workstreamID.String
	if writeContext.Valid {
		item.Provenance, err = memory.DecodeWriteContext(writeContext.String)
		if err != nil {
			return Item{}, err
		}
	}
	if data.Valid {
		item.Data = json.RawMessage(data.String)
	}
	if consumer.Valid {
		item.ConsumerState = json.RawMessage(consumer.String)
	}
	if tags.Valid {
		if err := json.Unmarshal([]byte(tags.String), &item.Tags); err != nil {
			return Item{}, err
		}
	}
	var parseErr error
	if item.CreatedAt, parseErr = memorytime.Parse(createdRaw); parseErr != nil {
		return Item{}, parseErr
	}
	if item.UpdatedAt, parseErr = memorytime.Parse(updatedRaw); parseErr != nil {
		return Item{}, parseErr
	}
	if item.LastUsedAt, parseErr = memorytime.Parse(usedRaw); parseErr != nil {
		return Item{}, parseErr
	}
	if item.LastDecayedAt, parseErr = memorytime.Parse(decayedRaw); parseErr != nil {
		return Item{}, parseErr
	}
	return item, nil
}

func workspaceStateClauses(filters []memory.StateFilter) ([]string, []any, error) {
	var where []string
	var args []any
	seen := map[string]bool{}
	for _, filter := range filters {
		if !recallStateFieldRE.MatchString(filter.Field) {
			return nil, nil, fmt.Errorf("%w: state filter field %q must be a lowercase identifier matching ^[a-z][a-z0-9_]*$", ErrInvalidInput, filter.Field)
		}
		if seen[filter.Field] {
			return nil, nil, fmt.Errorf("%w: state filter field %q named twice", ErrInvalidInput, filter.Field)
		}
		seen[filter.Field] = true
		if len(filter.Values) == 0 {
			return nil, nil, fmt.Errorf("%w: state filter %q must have at least one value", ErrInvalidInput, filter.Field)
		}
		where = append(where, "w.consumer_state IS NOT NULL AND json_extract(w.consumer_state, '$."+filter.Field+"') IN ("+placeholders(len(filter.Values))+")")
		for _, value := range filter.Values {
			normalized, err := normalizeWorkspaceStateValue(filter.Field, value)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, normalized)
		}
	}
	return where, args, nil
}

func normalizeWorkspaceStateValue(field string, value any) (any, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return int64(1), nil
		}
		return int64(0), nil
	case float64:
		return v, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, fmt.Errorf("%w: state filter %q has invalid number", ErrInvalidInput, field)
		}
		return f, nil
	case nil:
		return nil, fmt.Errorf("%w: state filter %q cannot match null", ErrInvalidInput, field)
	default:
		return nil, fmt.Errorf("%w: state filter %q values must be strings, numbers or booleans", ErrInvalidInput, field)
	}
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

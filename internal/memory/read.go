package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// revisionColumns is the shared SELECT column list for revision queries.
const revisionColumns = `revision_id, memory_id, namespace, COALESCE(memory_key, ''),
       status, COALESCE(supersedes, ''), created_at,
       author_agent_id, author_version, trigger, session_id, origin,
       confidence, tags, COALESCE(ttl_seconds, 0), expires_at,
       COALESCE(payload_summary, ''), COALESCE(payload_body, '')`

// scanRevision scans a single revision row from the shared column list.
func scanRevision(r rowScanner) (Revision, error) {
	var rev Revision
	var createdAt string
	var expiresAt sql.NullString
	var tagsJSON string
	err := r.Scan(
		&rev.RevisionID, &rev.MemoryID, &rev.Namespace, &rev.MemoryKey,
		&rev.Status, &rev.Supersedes, &createdAt,
		&rev.Author.AgentID, &rev.Author.AgentVersion, &rev.Trigger, &rev.SessionID, &rev.Origin,
		&rev.Confidence, &tagsJSON, &rev.TTLSeconds, &expiresAt,
		&rev.Payload.Summary, &rev.Payload.Body,
	)
	if err != nil {
		return Revision{}, err
	}
	rev.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	if expiresAt.Valid {
		t, _ := time.Parse(time.DateTime, expiresAt.String)
		rev.ExpiresAt = &t
	}
	if tagsJSON != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &rev.Tags)
	}
	if rev.Tags == nil {
		rev.Tags = []string{}
	}
	return rev, nil
}

// GetState reads the memory_state row for the given memory_id.
func (s *Store) GetState(ctx context.Context, memoryID string) (State, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT memory_id, namespace, COALESCE(memory_key, ''), COALESCE(current_revision, ''),
       activation, access_count, last_accessed_at, created_at
FROM memory_state WHERE memory_id = ?`, memoryID)

	var st State
	var lastAccessed, created sql.NullString
	err := row.Scan(
		&st.MemoryID, &st.Namespace, &st.MemoryKey, &st.CurrentRevision,
		&st.Activation, &st.AccessCount, &lastAccessed, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, fmt.Errorf("%w: memory_id %s", ErrNotFound, memoryID)
	}
	if err != nil {
		return State{}, err
	}
	if lastAccessed.Valid {
		t, _ := time.Parse(time.DateTime, lastAccessed.String)
		st.LastAccessedAt = &t
	}
	if created.Valid {
		st.CreatedAt, _ = time.Parse(time.DateTime, created.String)
	}
	return st, nil
}

// GetRevisionByID reads a single memory_revisions row by revision_id.
func (s *Store) GetRevisionByID(ctx context.Context, revisionID string) (Revision, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+revisionColumns+` FROM memory_revisions WHERE revision_id = ?`,
		revisionID,
	)
	rev, err := scanRevision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, fmt.Errorf("%w: revision_id %s", ErrNotFound, revisionID)
	}
	return rev, err
}

// GetCurrent returns the current (latest) revision for a logical memory
// identified by (namespace, memory_key). Returns ErrNotFound if no memory
// exists or current_revision is empty.
func (s *Store) GetCurrent(ctx context.Context, namespace, memoryKey string) (Revision, error) {
	var currentRevision string
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(current_revision, '') FROM memory_state WHERE namespace = ? AND memory_key = ?`,
		namespace, memoryKey,
	)
	err := row.Scan(&currentRevision)
	if errors.Is(err, sql.ErrNoRows) || currentRevision == "" {
		return Revision{}, fmt.Errorf("%w: no current revision for %s/%s", ErrNotFound, namespace, memoryKey)
	}
	if err != nil {
		return Revision{}, err
	}
	return s.GetRevisionByID(ctx, currentRevision)
}

// GetHistory returns all revisions for a logical memory identified by
// (namespace, memory_key), ordered newest-first. Returns ErrNotFound if no
// memory exists for the given key.
func (s *Store) GetHistory(ctx context.Context, namespace, memoryKey string) ([]Revision, error) {
	var memoryID string
	row := s.db.QueryRowContext(ctx,
		`SELECT memory_id FROM memory_state WHERE namespace = ? AND memory_key = ?`,
		namespace, memoryKey,
	)
	err := row.Scan(&memoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no memory for %s/%s", ErrNotFound, namespace, memoryKey)
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+revisionColumns+` FROM memory_revisions WHERE memory_id = ? ORDER BY created_at DESC, revision_id DESC`,
		memoryID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var revs []Revision
	for rows.Next() {
		rev, scanErr := scanRevision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		revs = append(revs, rev)
	}
	return revs, rows.Err()
}

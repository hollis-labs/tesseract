package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tesseract/domains"
)

// memoryTimeFormat is the canonical timestamp format for memory tables.
const memoryTimeFormat = time.RFC3339Nano

// parseMemoryTime parses a timestamp stored in memory tables. It tries
// RFC3339Nano first, then falls back to time.DateTime for backward
// compatibility with data written before the precision upgrade.
func parseMemoryTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return t, nil
	}
	return time.Parse(time.DateTime, s)
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// revisionColumns is the shared SELECT column list for revision queries.
const revisionColumns = `revision_id, memory_id, domain, namespace, COALESCE(memory_key, ''),
       status, COALESCE(supersedes, ''), created_at,
       author_agent_id, author_version, trigger, session_id, origin,
       confidence, tags, COALESCE(ttl_seconds, 0), expires_at,
       COALESCE(payload_summary, ''), COALESCE(payload_body, ''),
       COALESCE(embedding_model, ''), embedding_vector,
       facet_kind, facet_source,
       facet_pointer_scheme, facet_pointer_locator, facet_pointer_resolved_at`

// scanRevision scans a single revision row from the shared column list.
func scanRevision(r rowScanner) (Revision, error) {
	var rev Revision
	var domain string
	var createdAt string
	var expiresAt sql.NullString
	var tagsJSON string
	var embeddingBlob []byte
	var facetKind, facetSource sql.NullString
	var pointerScheme, pointerLocator, pointerResolvedAt sql.NullString
	err := r.Scan(
		&rev.RevisionID, &rev.MemoryID, &domain, &rev.Namespace, &rev.MemoryKey,
		&rev.Status, &rev.Supersedes, &createdAt,
		&rev.Author.AgentID, &rev.Author.AgentVersion, &rev.Trigger, &rev.SessionID, &rev.Origin,
		&rev.Confidence, &tagsJSON, &rev.TTLSeconds, &expiresAt,
		&rev.Payload.Summary, &rev.Payload.Body,
		&rev.EmbeddingModel, &embeddingBlob,
		&facetKind, &facetSource,
		&pointerScheme, &pointerLocator, &pointerResolvedAt,
	)
	if err != nil {
		return Revision{}, err
	}
	rev.Domain = domains.Domain(domain)
	rev.CreatedAt, _ = parseMemoryTime(createdAt)
	if expiresAt.Valid {
		t, _ := parseMemoryTime(expiresAt.String)
		rev.ExpiresAt = &t
	}
	if tagsJSON != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &rev.Tags)
	}
	if rev.Tags == nil {
		rev.Tags = []string{}
	}
	rev.EmbeddingVector = blobToFloat32(embeddingBlob)
	if facetKind.Valid {
		rev.Facets.Kind = facetKind.String
	}
	if facetSource.Valid {
		rev.Facets.Source = facetSource.String
	}
	if pointerScheme.Valid || pointerLocator.Valid {
		p := &Pointer{Scheme: pointerScheme.String, Locator: pointerLocator.String}
		if pointerResolvedAt.Valid {
			t, _ := parseMemoryTime(pointerResolvedAt.String)
			p.ResolvedAt = &t
		}
		rev.Facets.Pointer = p
	}
	return rev, nil
}

// GetState reads the memory_state row for the given memory_id.
func (s *Store) GetState(ctx context.Context, memoryID string) (State, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT memory_id, domain, namespace, COALESCE(memory_key, ''), COALESCE(current_revision, ''),
       activation, access_count, last_accessed_at, created_at
FROM memory_state WHERE memory_id = ?`, memoryID)

	var st State
	var domain string
	var lastAccessed, created sql.NullString
	err := row.Scan(
		&st.MemoryID, &domain, &st.Namespace, &st.MemoryKey, &st.CurrentRevision,
		&st.Activation, &st.AccessCount, &lastAccessed, &created,
	)
	st.Domain = domains.Domain(domain)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, fmt.Errorf("%w: memory_id %s", ErrNotFound, memoryID)
	}
	if err != nil {
		return State{}, err
	}
	if lastAccessed.Valid {
		t, _ := parseMemoryTime(lastAccessed.String)
		st.LastAccessedAt = &t
	}
	if created.Valid {
		st.CreatedAt, _ = parseMemoryTime(created.String)
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

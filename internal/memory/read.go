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

// GetCurrentReinforced is GetCurrent plus an activation-reinforcement bump.
// It is the deliberate-read entry point behind tesseract_get's memory arm / the
// /v1/memory/current route: an agent resolving a known (namespace, memory_key)
// is a genuine "this memory mattered" signal, so it reinforces activation,
// access_count, and last_accessed_at. Reinforcement is best-effort — a
// failure to bump never fails the read. Internal callers that resolve the
// head for non-agent reasons (promotion, embedding) must keep using the
// plain GetCurrent so they don't spuriously reinforce.
func (s *Store) GetCurrentReinforced(ctx context.Context, namespace, memoryKey string) (Revision, error) {
	rev, err := s.GetCurrent(ctx, namespace, memoryKey)
	if err != nil {
		return rev, err
	}
	_ = s.reinforceAccess(ctx, rev.MemoryID)
	return rev, nil
}

// GetRevisionByIDReinforced is GetRevisionByID plus an activation-reinforcement
// bump. It backs the tesseract_get_revision tool / the /v1/memory/revisions/{id}
// route. Pulling up a specific revision by ID is an explicit, deliberate
// consultation of that memory, so it reinforces the parent memory_state the
// same way GetCurrentReinforced does. Best-effort: a reinforcement failure
// never fails the read. Internal callers reading a revision for non-agent
// reasons must keep using the plain GetRevisionByID.
func (s *Store) GetRevisionByIDReinforced(ctx context.Context, revisionID string) (Revision, error) {
	rev, err := s.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return rev, err
	}
	_ = s.reinforceAccess(ctx, rev.MemoryID)
	return rev, nil
}

// GetCurrentInDomain is GetCurrent restricted to one domain.
//
// (namespace, memory_key) does not identify a domain — memory_state has no
// domain column, and memory and knowledge revisions share memory_revisions —
// so an unfiltered resolve returns whatever was written at that key regardless
// of which domain wrote it. A caller that named a domain must not be handed
// another one's row: knowledge/store.go states that contract for its side
// ("callers should not see cross-domain reads"), and this is the same contract
// for every side, in one place, so the two cannot implement it differently.
//
// The filter is applied to the resolved revision rather than in SQL because the
// head pointer lives in memory_state and the domain lives on the revision; there
// is one row to check and no index to gain.
func (s *Store) GetCurrentInDomain(ctx context.Context, domain domains.Domain, namespace, memoryKey string) (Revision, error) {
	rev, err := s.GetCurrent(ctx, namespace, memoryKey)
	if err != nil {
		return Revision{}, err
	}
	if rev.Domain != domain {
		return Revision{}, fmt.Errorf("%w: revision at %s/%s is not a %s entry",
			ErrNotFound, namespace, memoryKey, domain)
	}
	return rev, nil
}

// GetCurrentInDomainReinforced is GetCurrentInDomain plus the deliberate-read
// activation bump.
//
// The order is the whole point and is not interchangeable with reinforcing
// first and filtering after: reinforcement is a WRITE to ranking state, so
// bumping a row that is then withheld as not_found would teach the ranking that
// a memory mattered on the strength of a read that never returned it — and
// would do it to the wrong domain's rows. The check happens before the write.
func (s *Store) GetCurrentInDomainReinforced(ctx context.Context, domain domains.Domain, namespace, memoryKey string) (Revision, error) {
	rev, err := s.GetCurrentInDomain(ctx, domain, namespace, memoryKey)
	if err != nil {
		return rev, err
	}
	_ = s.reinforceAccess(ctx, rev.MemoryID)
	return rev, nil
}

// GetHistoryInDomain is GetHistory restricted to one domain, newest-first.
//
// Returns ErrNotFound when the key exists but holds no revision of this domain,
// rather than an empty slice: an empty history and a wrong-domain history are
// different facts, and only one of them means "nothing has been written here".
//
// Filtering here also keeps the paging cursor honest. HistoryOrderingFingerprint
// takes the domain as part of its key, so an unfiltered memory-domain read of a
// knowledge entry would issue a cursor stamped "memory" over rows the knowledge
// door stamps "knowledge" — two doors paging identical rows with mutually
// unusable cursors.
func (s *Store) GetHistoryInDomain(ctx context.Context, domain domains.Domain, namespace, memoryKey string) ([]Revision, error) {
	revs, err := s.GetHistory(ctx, namespace, memoryKey)
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(revs))
	for _, rev := range revs {
		if rev.Domain == domain {
			out = append(out, rev)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no %s revisions for %s/%s", ErrNotFound, domain, namespace, memoryKey)
	}
	return out, nil
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

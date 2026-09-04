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

// Sentinels for the memory write path.
var (
	ErrInvalidInput = errors.New("invalid memory input")
	ErrNotFound     = errors.New("memory not found")
)

// WriteInput carries all fields for a new revision write.
type WriteInput struct {
	// Domain selects the revision's policy bucket. Empty defaults to
	// domains.Memory to preserve existing call sites.
	Domain         domains.Domain
	Namespace      string
	MemoryKey      string
	Supersedes     string
	Status         Status
	Author         Author
	Trigger        Trigger
	SessionID      string
	Origin         Origin
	Confidence     float64
	Tags           []string
	TTL            time.Duration
	Payload        Payload
	Facets         Facets
	Dedup          string  // "none" (default), "semantic"
	DedupThreshold float64 // optional per-call override; 0 = use store default
}

// WriteRevision creates a new revision in the memory store, handling keyed,
// keyless, and supersedes cases within a single transaction.
func (s *Store) WriteRevision(ctx context.Context, in WriteInput) (Revision, error) {
	if in.Domain == "" {
		in.Domain = domains.Memory
	}
	if err := validateWriteInput(in); err != nil {
		return Revision{}, err
	}

	// Make sure the namespace is in the policy registry before we write data
	// for it. Idempotent — only inserts the first time the namespace is seen.
	// See CW-20260428-0005 for the bug this closes (registry-vs-data drift).
	if s.namespaceRegistrar != nil {
		if err := s.namespaceRegistrar.EnsureNamespaceRegistered(ctx, in.Namespace); err != nil {
			return Revision{}, fmt.Errorf("ensure namespace registered: %w", err)
		}
	}

	// Semantic dedup: if requested, search for similar existing revisions.
	// NOTE: This runs outside the transaction (before BeginTx). There is a
	// TOCTOU window where a concurrent write could create a matching revision
	// between the dedup check and the INSERT. This is acceptable for single-
	// writer SQLite but should be revisited for multi-writer backends.
	var dedupMatch string
	if in.Dedup == "semantic" {
		if s.embedder == nil {
			return Revision{}, ErrEmbedderUnavailable
		}
		threshold := in.DedupThreshold
		if threshold == 0 {
			threshold = s.dedupThreshold
		}
		if threshold == 0 {
			threshold = 0.85
		}
		text := revisionEmbedText(Revision{Payload: in.Payload})
		matchID, sameKey, matchErr := s.findSemanticMatch(ctx, in.Namespace, in.MemoryKey, text, threshold)
		if matchErr != nil {
			return Revision{}, fmt.Errorf("semantic dedup: %w", matchErr)
		}
		if matchID != "" {
			dedupMatch = matchID
			if sameKey && in.Supersedes == "" {
				in.Supersedes = matchID
			}
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	memoryID, err := resolveOrCreateMemory(ctx, tx, in.Domain, in.Namespace, in.MemoryKey)
	if err != nil {
		return Revision{}, fmt.Errorf("resolve memory: %w", err)
	}

	status := in.Status
	if status == "" {
		status = StatusDraft
	}

	revisionID := NewULID()
	now := time.Now().UTC()

	var expiresAt *time.Time
	var ttlSeconds int64
	if in.TTL > 0 {
		ttlSeconds = int64(in.TTL.Seconds())
		exp := now.Add(in.TTL)
		expiresAt = &exp
	}

	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return Revision{}, fmt.Errorf("marshal tags: %w", err)
	}

	// Nullable string helpers for the INSERT.
	nullStr := func(s string) sql.NullString {
		if s == "" {
			return sql.NullString{}
		}
		return sql.NullString{String: s, Valid: true}
	}
	nullTime := func(t *time.Time) sql.NullString {
		if t == nil {
			return sql.NullString{}
		}
		return sql.NullString{String: t.UTC().Format(memoryTimeFormat), Valid: true}
	}
	nullInt := func(v int64) sql.NullInt64 {
		if v == 0 {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: v, Valid: true}
	}

	var pointerScheme, pointerLocator string
	var pointerResolvedAt *time.Time
	if in.Facets.Pointer != nil {
		pointerScheme = in.Facets.Pointer.Scheme
		pointerLocator = in.Facets.Pointer.Locator
		pointerResolvedAt = in.Facets.Pointer.ResolvedAt
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO memory_revisions (
    revision_id, memory_id, domain, namespace, memory_key, status, supersedes,
    created_at, author_agent_id, author_version, trigger, session_id, origin,
    confidence, tags, ttl_seconds, expires_at, payload_summary, payload_body,
    facet_kind, facet_source, facet_pointer_scheme, facet_pointer_locator, facet_pointer_resolved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revisionID,
		memoryID,
		string(in.Domain),
		in.Namespace,
		nullStr(in.MemoryKey),
		string(status),
		nullStr(in.Supersedes),
		now.Format(memoryTimeFormat),
		in.Author.AgentID,
		in.Author.AgentVersion,
		string(in.Trigger),
		in.SessionID,
		string(in.Origin),
		in.Confidence,
		string(tagsJSON),
		nullInt(ttlSeconds),
		nullTime(expiresAt),
		nullStr(in.Payload.Summary),
		nullStr(in.Payload.Body),
		nullStr(in.Facets.Kind),
		nullStr(in.Facets.Source),
		nullStr(pointerScheme),
		nullStr(pointerLocator),
		nullTime(pointerResolvedAt),
	)
	if err != nil {
		return Revision{}, fmt.Errorf("insert revision: %w", err)
	}

	// If supersedes is set, verify it belongs to the same logical memory
	// and auto-deprecate it. Cross-memory deprecation is rejected to prevent
	// corrupting unrelated memory_state.current_revision pointers.
	if in.Supersedes != "" {
		var supersededMemID string
		supErr := tx.QueryRowContext(ctx,
			`SELECT memory_id FROM memory_revisions WHERE revision_id = ?`,
			in.Supersedes,
		).Scan(&supersededMemID)
		if supErr != nil {
			if errors.Is(supErr, sql.ErrNoRows) {
				return Revision{}, fmt.Errorf("%w: supersedes revision %s not found", ErrInvalidInput, in.Supersedes)
			}
			return Revision{}, fmt.Errorf("verify supersedes: %w", supErr)
		}
		if supersededMemID != memoryID {
			return Revision{}, fmt.Errorf("%w: supersedes revision %s belongs to a different memory", ErrInvalidInput, in.Supersedes)
		}
		if depErr := deprecateRevisionTx(ctx, tx, in.Supersedes); depErr != nil {
			return Revision{}, fmt.Errorf("deprecate superseded: %w", depErr)
		}
	}

	// Point the memory_state current_revision to this new revision.
	_, err = tx.ExecContext(ctx,
		`UPDATE memory_state SET current_revision = ? WHERE memory_id = ?`,
		revisionID, memoryID,
	)
	if err != nil {
		return Revision{}, fmt.Errorf("update state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Revision{}, fmt.Errorf("commit: %w", err)
	}

	// Fire-and-forget embed job enqueue; errors are non-fatal.
	_ = s.queue.Enqueue(ctx, Job{
		Kind:    "embed",
		Payload: []byte(fmt.Sprintf(`{"revision_id":%q}`, revisionID)),
	})

	if s.auditSink != nil {
		actor := in.Author.AgentID
		key := in.MemoryKey
		if key == "" {
			key = memoryID // logical memory identity for keyless writes
		}
		switch {
		case in.Domain == domains.Knowledge && in.Supersedes != "":
			_ = s.auditSink.EmitKnowledgeSupersede(ctx, actor, in.Namespace, key, revisionID, nil)
		case in.Domain == domains.Knowledge:
			_ = s.auditSink.EmitKnowledgeWrite(ctx, actor, in.Namespace, key, revisionID, nil)
		case in.Supersedes != "":
			_ = s.auditSink.EmitMemorySupersede(ctx, actor, in.Namespace, key, revisionID, nil)
		default:
			_ = s.auditSink.EmitMemoryWrite(ctx, actor, in.Namespace, key, revisionID, nil)
		}
	}

	rev := Revision{
		RevisionID: revisionID,
		MemoryID:   memoryID,
		Domain:     in.Domain,
		Namespace:  in.Namespace,
		MemoryKey:  in.MemoryKey,
		Status:     status,
		Supersedes: in.Supersedes,
		CreatedAt:  now,
		Author:     in.Author,
		Trigger:    in.Trigger,
		SessionID:  in.SessionID,
		Origin:     in.Origin,
		Confidence: in.Confidence,
		Tags:       tags,
		TTLSeconds: ttlSeconds,
		ExpiresAt:  expiresAt,
		Payload:    in.Payload,
		Facets:     in.Facets,
		DedupMatch: dedupMatch,
	}
	return rev, nil
}

// resolveOrCreateMemory finds an existing memory_state by (namespace, key)
// or creates a new one. For keyless writes (key == ""), always creates new.
// Domain is stamped on creation and never changes for a given memory_id.
func resolveOrCreateMemory(ctx context.Context, tx *sql.Tx, domain domains.Domain, namespace, key string) (string, error) {
	if key != "" {
		var existing, existingDomain string
		row := tx.QueryRowContext(ctx,
			`SELECT memory_id, domain FROM memory_state WHERE namespace = ? AND memory_key = ?`,
			namespace, key,
		)
		err := row.Scan(&existing, &existingDomain)
		if err == nil {
			if existingDomain != string(domain) {
				return "", fmt.Errorf("%w: memory %s/%s exists under domain %q, cannot write as %q",
					ErrInvalidInput, namespace, key, existingDomain, domain)
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		// Fall through to create.
	}

	memoryID := NewULID()
	var keyVal sql.NullString
	if key != "" {
		keyVal = sql.NullString{String: key, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO memory_state (memory_id, domain, namespace, memory_key, activation, access_count, created_at)
VALUES (?, ?, ?, ?, 1.0, 0, ?)`,
		memoryID, string(domain), namespace, keyVal, time.Now().UTC().Format(memoryTimeFormat),
	)
	if err != nil {
		return "", fmt.Errorf("insert memory_state: %w", err)
	}
	return memoryID, nil
}

// deprecateRevisionTx is the ONE authorized mutation of
// memory_revisions.status. Guard test enforces this constraint.
func deprecateRevisionTx(ctx context.Context, tx *sql.Tx, revisionID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE memory_revisions SET status = ? WHERE revision_id = ? AND status != ?`,
		string(StatusDeprecated), revisionID, string(StatusDeprecated),
	)
	// RowsAffected == 0 is OK (idempotent — already deprecated or does not exist).
	return err
}

// validateWriteInput checks all required fields before a revision is written.
func validateWriteInput(in WriteInput) error {
	if !in.Domain.Valid() {
		return fmt.Errorf("%w: invalid domain %q", ErrInvalidInput, in.Domain)
	}
	if err := validateFacets(in.Domain, in.Facets); err != nil {
		return err
	}
	policy, err := in.Domain.Policy()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if in.Namespace == "" {
		return fmt.Errorf("%w: namespace is required", ErrInvalidInput)
	}
	if err := policy.ValidateNamespace(in.Namespace); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	// Legacy parser enforces the user/{id}[/project|session/{id}]/memory
	// shape; only the memory domain is required to satisfy it. Knowledge and
	// future domains have their own shape policy above.
	if in.Domain == domains.Memory {
		if err := ValidateNamespace(in.Namespace); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	// Memory-domain keys follow dot-notation lowercase rules. Other domains
	// may carry keys that violate these constraints (e.g. knowledge pointers
	// using hyphens or slugs from external sources).
	if in.MemoryKey != "" && in.Domain == domains.Memory {
		if err := ValidateKey(in.MemoryKey); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	if in.SessionID == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidInput)
	}
	if in.Author.AgentID == "" {
		return fmt.Errorf("%w: author.agent_id is required", ErrInvalidInput)
	}
	if in.Origin == "" {
		return fmt.Errorf("%w: origin is required", ErrInvalidInput)
	}
	if !in.Origin.Valid() {
		return fmt.Errorf("%w: invalid origin %q", ErrInvalidInput, in.Origin)
	}
	if in.Trigger == "" {
		return fmt.Errorf("%w: trigger is required", ErrInvalidInput)
	}
	if !in.Trigger.Valid() {
		return fmt.Errorf("%w: invalid trigger %q", ErrInvalidInput, in.Trigger)
	}
	if in.Confidence < 0 || in.Confidence > 1.0 {
		return fmt.Errorf("%w: confidence must be in [0, 1.0], got %f", ErrInvalidInput, in.Confidence)
	}
	if in.Status != "" && !in.Status.Valid() {
		return fmt.Errorf("%w: invalid status %q", ErrInvalidInput, in.Status)
	}
	if in.Payload.Summary == "" {
		return fmt.Errorf("%w: payload.summary is required", ErrInvalidInput)
	}
	if in.Dedup != "" && in.Dedup != "none" && in.Dedup != "semantic" {
		return fmt.Errorf("%w: invalid dedup mode %q (must be none or semantic)", ErrInvalidInput, in.Dedup)
	}
	if in.DedupThreshold < 0 || in.DedupThreshold > 1.0 {
		return fmt.Errorf("%w: dedup_threshold must be in [0, 1.0], got %f", ErrInvalidInput, in.DedupThreshold)
	}
	return nil
}

// validateFacets is the authoritative persistence-boundary check for the
// domain/facet contract. Every production writer ultimately reaches
// WriteRevision, including the root facade, knowledge wrapper, HTTP, MCP, and
// memory promotion paths, so no outer adapter can bypass these invariants.
func validateFacets(domain domains.Domain, facets Facets) error {
	switch domain {
	case domains.Memory:
		if !facets.IsZero() {
			return fmt.Errorf("%w: memory revisions must not carry knowledge facets", ErrInvalidInput)
		}
	case domains.Knowledge:
		if facets.Kind == "" {
			return fmt.Errorf("%w: facet.kind is required (allowed kinds: %s)",
				ErrInvalidInput, KnowledgeKindList())
		}
		if !IsCanonicalKnowledgeKind(facets.Kind) {
			return fmt.Errorf("%w: facet.kind %q is not a canonical knowledge kind (allowed kinds: %s)",
				ErrInvalidInput, facets.Kind, KnowledgeKindList())
		}
		if facets.Source == "" {
			return fmt.Errorf("%w: facet.source is required", ErrInvalidInput)
		}
		if facets.Pointer == nil || facets.Pointer.Scheme == "" || facets.Pointer.Locator == "" {
			return fmt.Errorf("%w: facet.pointer.scheme and facet.pointer.locator are required", ErrInvalidInput)
		}
	}
	return nil
}

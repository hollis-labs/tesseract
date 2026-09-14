package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tesseract/internal/memorylinks"
)

// ValidateWriteInput applies the complete persistence-boundary validation for
// a revision without writing it. Transactional workflows use it after loading
// the content they are about to copy and before calling WriteRevisionInTx.
func ValidateWriteInput(in WriteInput) error {
	if err := validateWriteInput(in); err != nil {
		return err
	}
	if err := validateConsumerStateFor(in); err != nil {
		return err
	}
	return validatePayloadData(Payload{
		Summary: in.Summary, Body: in.Body, Data: in.Data, DataSchemaHash: in.DataSchemaHash,
	})
}

// EnsureNamespaceRegistered performs the ordinary write path's idempotent
// namespace registration. It is separate from WriteRevisionInTx because a
// registrar may use the same database and cannot safely open a sibling write
// transaction while the caller's transaction is active.
func (s *Store) EnsureNamespaceRegistered(ctx context.Context, namespace string) error {
	if s.namespaceRegistrar == nil {
		return nil
	}
	if err := s.namespaceRegistrar.EnsureNamespaceRegistered(ctx, namespace); err != nil {
		return fmt.Errorf("ensure namespace registered: %w", err)
	}
	return nil
}

// WriteRevisionInTx writes one fully validated revision using the caller's
// transaction. The caller owns commit and rollback. It performs no semantic
// deduplication, namespace registration, queue work, or audit emission.
func (s *Store) WriteRevisionInTx(ctx context.Context, tx *sql.Tx, in WriteInput) (Revision, error) {
	return s.writeRevisionInTx(ctx, tx, in, "")
}

// WriteRevisionToItemInTx is WriteRevisionInTx for a previously resolved
// logical item. It is the transaction-safe path for keyless existing targets;
// callers must still provide Supersedes when the new revision replaces one.
func (s *Store) WriteRevisionToItemInTx(ctx context.Context, tx *sql.Tx, in WriteInput, itemID string) (Revision, error) {
	if itemID == "" {
		return Revision{}, fmt.Errorf("%w: target item_id is required", ErrInvalidInput)
	}
	return s.writeRevisionInTx(ctx, tx, in, itemID)
}

func (s *Store) writeRevisionInTx(ctx context.Context, tx *sql.Tx, in WriteInput, fixedItemID string) (Revision, error) {
	if tx == nil {
		return Revision{}, fmt.Errorf("%w: transaction is required", ErrInvalidInput)
	}
	if in.Dedup == "semantic" {
		return Revision{}, fmt.Errorf("%w: semantic dedup is unavailable inside a caller transaction", ErrInvalidInput)
	}
	if err := ValidateWriteInput(in); err != nil {
		return Revision{}, err
	}
	if err := s.validateTierPolicy(ctx, tx, in); err != nil {
		return Revision{}, err
	}

	payload := Payload{Summary: in.Summary, Body: in.Body, Data: in.Data, DataSchemaHash: in.DataSchemaHash}
	memoryID := fixedItemID
	if memoryID == "" {
		var err error
		memoryID, err = resolveOrCreateMemory(ctx, tx, in.Domain, in.Namespace, in.MemoryKey)
		if err != nil {
			return Revision{}, fmt.Errorf("resolve memory: %w", err)
		}
	} else {
		var storedDomain, namespace, key string
		err := tx.QueryRowContext(ctx, `SELECT domain, namespace, COALESCE(memory_key, '') FROM memory_state WHERE memory_id = ?`, memoryID).
			Scan(&storedDomain, &namespace, &key)
		if errors.Is(err, sql.ErrNoRows) {
			return Revision{}, fmt.Errorf("%w: item_id %s", ErrNotFound, memoryID)
		}
		if err != nil {
			return Revision{}, fmt.Errorf("resolve target item: %w", err)
		}
		if storedDomain != string(in.Domain) || namespace != in.Namespace || key != in.MemoryKey {
			return Revision{}, fmt.Errorf("%w: target item selector no longer matches", ErrInvalidInput)
		}
	}
	workstreamID, err := resolveRevisionWorkstream(ctx, tx, memoryID, in.WorkstreamID)
	if err != nil {
		return Revision{}, err
	}
	writeContextValue, provenance, err := EncodeWriteContext(ctx)
	if err != nil {
		return Revision{}, err
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

	nullStr := func(value string) sql.NullString {
		return sql.NullString{String: value, Valid: value != ""}
	}
	nullTime := func(value *time.Time) sql.NullString {
		if value == nil {
			return sql.NullString{}
		}
		return sql.NullString{String: value.UTC().Format(memoryTimeFormat), Valid: true}
	}
	nullInt := func(value int64) sql.NullInt64 {
		return sql.NullInt64{Int64: value, Valid: value != 0}
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
    created_at, author_agent_id, author_version, trigger, session_id, derived_from,
    confidence, tags, ttl_seconds, expires_at, payload_summary, payload_body,
    payload_data, payload_data_schema_hash,
    facet_kind, facet_source, facet_pointer_scheme, facet_pointer_locator, facet_pointer_resolved_at,
    consumer_state, workstream_id, write_context
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revisionID, memoryID, string(in.Domain), in.Namespace, nullStr(in.MemoryKey), string(status), nullStr(in.Supersedes),
		now.Format(memoryTimeFormat), in.Author.AgentID, in.Author.AgentVersion, string(in.Trigger), in.SessionID,
		string(in.DerivedFrom), in.Confidence, string(tagsJSON), nullInt(ttlSeconds), nullTime(expiresAt),
		nullStr(payload.Summary), nullStr(payload.Body), nullStr(string(payload.Data)), nullStr(payload.DataSchemaHash),
		nullStr(in.Facets.Kind), nullStr(in.Facets.Source), nullStr(pointerScheme), nullStr(pointerLocator),
		nullTime(pointerResolvedAt), nullStr(string(in.ConsumerState)), optionalWorkstream(workstreamID), writeContextValue,
	)
	if err != nil {
		return Revision{}, fmt.Errorf("insert revision: %w", err)
	}

	if in.Supersedes != "" {
		var supersededMemoryID string
		err := tx.QueryRowContext(ctx, `SELECT memory_id FROM memory_revisions WHERE revision_id = ?`, in.Supersedes).Scan(&supersededMemoryID)
		if errors.Is(err, sql.ErrNoRows) {
			return Revision{}, fmt.Errorf("%w: supersedes revision %s not found", ErrInvalidInput, in.Supersedes)
		}
		if err != nil {
			return Revision{}, fmt.Errorf("verify supersedes: %w", err)
		}
		if supersededMemoryID != memoryID {
			return Revision{}, fmt.Errorf("%w: supersedes revision %s belongs to a different memory", ErrInvalidInput, in.Supersedes)
		}
		if err := deprecateRevisionTx(ctx, tx, in.Supersedes); err != nil {
			return Revision{}, fmt.Errorf("deprecate superseded: %w", err)
		}
	}

	if err := memorylinks.WriteReferences(ctx, tx, memorylinks.TxResolver{Tx: tx}, memorylinks.RevisionRef{
		RevisionID: revisionID, MemoryID: memoryID, Namespace: in.Namespace, CreatedAt: now.Format(memoryTimeFormat),
	}, memorylinks.LinkText(payload.Summary, payload.Body)); err != nil {
		return Revision{}, fmt.Errorf("index links: %w", err)
	}
	if err := memorylinks.ResolvePending(ctx, tx, memorylinks.TxResolver{Tx: tx}, in.MemoryKey); err != nil {
		return Revision{}, fmt.Errorf("resolve pending links: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_state SET current_revision = ? WHERE memory_id = ?`, revisionID, memoryID); err != nil {
		return Revision{}, fmt.Errorf("update state: %w", err)
	}

	return Revision{
		RevisionID: revisionID, ItemID: memoryID, MemoryID: memoryID, Domain: in.Domain,
		Namespace: in.Namespace, MemoryKey: in.MemoryKey, WorkstreamID: workstreamID, Provenance: provenance,
		Status: status, Supersedes: in.Supersedes, CreatedAt: now, Author: in.Author, Trigger: in.Trigger,
		SessionID: in.SessionID, DerivedFrom: in.DerivedFrom, Confidence: in.Confidence, Tags: tags,
		TTLSeconds: ttlSeconds, ExpiresAt: expiresAt, Payload: payload, Facets: in.Facets,
		ConsumerState: in.ConsumerState,
	}, nil
}

// FinishRevisionWrite performs the established best-effort effects for a
// revision after the transaction that created it has committed.
func (s *Store) FinishRevisionWrite(ctx context.Context, rev Revision, in WriteInput) {
	postCommitCtx := context.WithoutCancel(ctx)
	if err := s.queue.Enqueue(postCommitCtx, Job{
		Kind: EmbedJobKind, Payload: []byte(fmt.Sprintf(`{"revision_id":%q}`, rev.RevisionID)),
	}); err != nil {
		s.enqueueFailures.Add(1)
		s.log().WarnContext(postCommitCtx, "embed enqueue failed",
			"job_kind", EmbedJobKind, "revision_id", rev.RevisionID, "memory_id", rev.MemoryID,
			"namespace", in.Namespace, "domain", string(in.Domain), "queue", fmt.Sprintf("%T", s.queue), "err", err)
	}
	if s.auditSink != nil {
		key := in.MemoryKey
		if key == "" {
			key = rev.MemoryID
		}
		op := auditOpWrite
		if in.Supersedes != "" {
			op = auditOpSupersede
		}
		_ = s.auditSink.EmitRevision(postCommitCtx, string(in.Domain), op, in.Author.AgentID, in.Namespace, key, rev.RevisionID, nil)
	}
}

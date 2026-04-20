package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PromoteInput carries parameters for promoting a session-scoped memory
// revision into a user or project scope.
type PromoteInput struct {
	SourceNamespace string // must be a session-scoped namespace
	SourceMemoryID  string
	TargetNamespace string // user or project scope
	ActorAgentID    string
	ActorVersion    string
}

// Promote moves a session-scoped memory to user/project scope with key-based
// deduplication. It returns the new promoted revision in the target namespace.
func (s *Store) Promote(ctx context.Context, in PromoteInput) (Revision, error) {
	// Validate source namespace must be session-scoped.
	srcNS, parseErr := ParseNamespace(in.SourceNamespace)
	if parseErr != nil {
		return Revision{}, fmt.Errorf("%w: invalid source namespace: %w", ErrInvalidInput, parseErr)
	}
	if srcNS.Scope != ScopeSession {
		return Revision{}, fmt.Errorf("%w: source namespace must be session-scoped, got scope %d", ErrInvalidInput, srcNS.Scope)
	}

	// Validate target namespace must be user or project scope.
	tgtNS, parseErr := ParseNamespace(in.TargetNamespace)
	if parseErr != nil {
		return Revision{}, fmt.Errorf("%w: invalid target namespace: %w", ErrInvalidInput, parseErr)
	}
	if tgtNS.Scope != ScopeUser && tgtNS.Scope != ScopeProject {
		return Revision{}, fmt.Errorf("%w: target namespace must be user or project scope, got scope %d", ErrInvalidInput, tgtNS.Scope)
	}

	// Load source memory state and current revision.
	state, err := s.GetState(ctx, in.SourceMemoryID)
	if err != nil {
		return Revision{}, fmt.Errorf("load source memory state: %w", err)
	}

	// Verify that the memory_id belongs to the source namespace.
	if state.Namespace != in.SourceNamespace {
		return Revision{}, fmt.Errorf("%w: memory_id %s does not belong to namespace %s (belongs to %s)",
			ErrInvalidInput, in.SourceMemoryID, in.SourceNamespace, state.Namespace)
	}

	if state.CurrentRevision == "" {
		return Revision{}, fmt.Errorf("%w: source memory %s has no current revision", ErrNotFound, in.SourceMemoryID)
	}

	srcRev, err := s.GetRevisionByID(ctx, state.CurrentRevision)
	if err != nil {
		return Revision{}, fmt.Errorf("load source revision: %w", err)
	}

	// Build the WriteInput for the target namespace.
	writeIn := WriteInput{
		Namespace:  in.TargetNamespace,
		MemoryKey:  srcRev.MemoryKey,
		Author:     Author{AgentID: in.ActorAgentID, AgentVersion: in.ActorVersion},
		Trigger:    TriggerPromotion,
		SessionID:  srcRev.SessionID,
		Origin:     srcRev.Origin,
		Confidence: srcRev.Confidence,
		Tags:       srcRev.Tags,
		TTL:        0, // TTL not carried over on promotion
		Status:     StatusReviewed,
		Payload:    srcRev.Payload,
	}

	// If keyed and same key exists in target, set supersedes to that revision.
	if srcRev.MemoryKey != "" {
		existing, getCurErr := s.GetCurrent(ctx, in.TargetNamespace, srcRev.MemoryKey)
		if getCurErr == nil {
			writeIn.Supersedes = existing.RevisionID
		} else if !errors.Is(getCurErr, ErrNotFound) {
			return Revision{}, fmt.Errorf("check target namespace for key %s: %w", srcRev.MemoryKey, getCurErr)
		}
	}

	// Write the promoted revision into the target namespace.
	promoted, err := s.WriteRevision(ctx, writeIn)
	if err != nil {
		return Revision{}, fmt.Errorf("write promoted revision: %w", err)
	}

	// Deprecate the source revision.
	if depErr := s.Deprecate(ctx, srcRev.RevisionID); depErr != nil {
		return Revision{}, fmt.Errorf("deprecate source revision: %w", depErr)
	}

	// Umbrella promote event. Nested WriteRevision (for target) and Deprecate
	// (for source) emit their own events — callers see three events per promote.
	if s.auditSink != nil {
		key := promoted.MemoryKey
		if key == "" {
			key = promoted.MemoryID
		}
		_ = s.auditSink.EmitMemoryPromote(ctx, in.ActorAgentID, promoted.Namespace, key, promoted.RevisionID, nil)
	}

	return promoted, nil
}

// Deprecate marks a revision as deprecated without writing a replacement.
// If the deprecated revision was the current revision, memory_state.current_revision
// is updated to the next non-deprecated revision, or NULL if none remains.
// The operation is idempotent: deprecating an already-deprecated revision is a no-op.
func (s *Store) Deprecate(ctx context.Context, revisionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Load the revision's memory_id and current status.
	var memoryID string
	var status Status
	row := tx.QueryRowContext(ctx,
		`SELECT memory_id, status FROM memory_revisions WHERE revision_id = ?`,
		revisionID,
	)
	scanErr := row.Scan(&memoryID, &status)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: revision_id %s", ErrNotFound, revisionID)
		}
		return fmt.Errorf("scan revision: %w", scanErr)
	}

	// Idempotent no-op if already deprecated.
	if status == StatusDeprecated {
		return tx.Commit()
	}

	// Deprecate via the one authorized status mutation in write.go.
	if depErr := deprecateRevisionTx(ctx, tx, revisionID); depErr != nil {
		return fmt.Errorf("deprecate revision: %w", depErr)
	}

	// Recompute memory_state.current_revision: find next non-deprecated revision.
	var nextRevision sql.NullString
	nextRow := tx.QueryRowContext(ctx, `
SELECT revision_id FROM memory_revisions
WHERE memory_id = ? AND status != ? AND revision_id != ?
ORDER BY created_at DESC, revision_id DESC
LIMIT 1`,
		memoryID, string(StatusDeprecated), revisionID,
	)
	scanErr = nextRow.Scan(&nextRevision)
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return fmt.Errorf("scan next revision: %w", scanErr)
	}
	// If sql.ErrNoRows, nextRevision stays as sql.NullString{} (NULL).

	// Update memory_state.current_revision (NULL if no non-deprecated revision remains).
	_, err = tx.ExecContext(ctx,
		`UPDATE memory_state SET current_revision = ? WHERE memory_id = ?`,
		nextRevision, memoryID,
	)
	if err != nil {
		return fmt.Errorf("update current_revision: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if s.auditSink != nil {
		// Namespace/key not directly available on this signature — reload
		// memory_state for observability. If that read fails, skip the emit
		// rather than fail Deprecate (emit is best-effort observability).
		if state, stErr := s.GetState(ctx, memoryID); stErr == nil {
			key := state.MemoryKey
			if key == "" {
				key = memoryID
			}
			_ = s.auditSink.EmitMemoryDeprecate(ctx, "system", state.Namespace, key, revisionID, nil)
		}
	}

	return nil
}

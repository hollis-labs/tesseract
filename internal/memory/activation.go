package memory

import (
	"context"
	"fmt"
	"time"
)

// reinforceMemoryIDs is the shared activation-reinforcement primitive: it
// bumps activation, access_count, and last_accessed_at on memory_state for
// every memory_id in the set. Reinforcement is a "deliberate read" signal —
// it must be driven only by the get paths (memory_get / memory_get_revision),
// never by search/recall (which would let the system's own guesses
// self-reinforce). Best-effort: errors here do not fail the caller's read.
func (s *Store) reinforceMemoryIDs(ctx context.Context, memoryIDs []string) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reinforce tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(memoryTimeFormat)
	// Diminishing-returns formula: new = old + 0.1 * (2.0 - old)
	stmt, err := tx.PrepareContext(ctx, `
		UPDATE memory_state
		SET activation = activation + 0.1 * (2.0 - activation),
		    access_count = access_count + 1,
		    last_accessed_at = ?
		WHERE memory_id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare reinforce: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, id := range memoryIDs {
		if _, err := stmt.ExecContext(ctx, now, id); err != nil {
			return fmt.Errorf("reinforce %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// reinforceAccess reinforces activation + last_accessed_at + access_count for
// a single deliberately-read memory. Best-effort: errors do not fail the read.
func (s *Store) reinforceAccess(ctx context.Context, memoryID string) error {
	if memoryID == "" {
		return nil
	}
	return s.reinforceMemoryIDs(ctx, []string{memoryID})
}

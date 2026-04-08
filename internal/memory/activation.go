package memory

import (
	"context"
	"fmt"
	"time"
)

// reinforceAccess updates activation + last_accessed_at + access_count for
// every memory in results. Called from Recall after a successful activation-
// ranked query. Best-effort: errors here do not fail the recall.
func (s *Store) reinforceAccess(ctx context.Context, results []RecallResult) error {
	if len(results) == 0 {
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

	for _, r := range results {
		if _, err := stmt.ExecContext(ctx, now, r.Revision.MemoryID); err != nil {
			return fmt.Errorf("reinforce %s: %w", r.Revision.MemoryID, err)
		}
	}
	return tx.Commit()
}

package contextstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hollis-labs/tesseract/internal/memorytime"
)

type memoryTimestampUpdate struct {
	key   any
	value string
}

// normalizeMemoryTimestamps rewrites every memory-owned timestamp into the
// fixed-width UTC codec used by indexed TEXT comparisons. Its caller owns the
// schema-migration transaction, so an invalid nonempty value aborts the whole
// normalization rather than leaving a database with mixed encodings.
func normalizeMemoryTimestamps(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name      string
		selectSQL string
		updateSQL string
	}{
		{
			name:      "memory_revisions.created_at",
			selectSQL: `SELECT rowid, created_at FROM memory_revisions WHERE created_at IS NOT NULL AND created_at != ''`,
			updateSQL: `UPDATE memory_revisions SET created_at = ? WHERE rowid = ?`,
		},
		{
			name:      "memory_revisions.expires_at",
			selectSQL: `SELECT rowid, expires_at FROM memory_revisions WHERE expires_at IS NOT NULL AND expires_at != ''`,
			updateSQL: `UPDATE memory_revisions SET expires_at = ? WHERE rowid = ?`,
		},
		{
			name:      "memory_revisions.facet_pointer_resolved_at",
			selectSQL: `SELECT rowid, facet_pointer_resolved_at FROM memory_revisions WHERE facet_pointer_resolved_at IS NOT NULL AND facet_pointer_resolved_at != ''`,
			updateSQL: `UPDATE memory_revisions SET facet_pointer_resolved_at = ? WHERE rowid = ?`,
		},
		{
			name:      "memory_state.created_at",
			selectSQL: `SELECT memory_id, created_at FROM memory_state WHERE created_at IS NOT NULL AND created_at != ''`,
			updateSQL: `UPDATE memory_state SET created_at = ? WHERE memory_id = ?`,
		},
		{
			name:      "memory_state.last_accessed_at",
			selectSQL: `SELECT memory_id, last_accessed_at FROM memory_state WHERE last_accessed_at IS NOT NULL AND last_accessed_at != ''`,
			updateSQL: `UPDATE memory_state SET last_accessed_at = ? WHERE memory_id = ?`,
		},
		{
			name:      "memory_state.last_decayed_at",
			selectSQL: `SELECT memory_id, last_decayed_at FROM memory_state WHERE last_decayed_at IS NOT NULL AND last_decayed_at != ''`,
			updateSQL: `UPDATE memory_state SET last_decayed_at = ? WHERE memory_id = ?`,
		},
		{
			name:      "pointer_verifications.checked_at",
			selectSQL: `SELECT id, checked_at FROM pointer_verifications WHERE checked_at IS NOT NULL AND checked_at != ''`,
			updateSQL: `UPDATE pointer_verifications SET checked_at = ? WHERE id = ?`,
		},
	}

	for _, column := range columns {
		if err := normalizeMemoryTimestampColumn(ctx, tx, column.name, column.selectSQL, column.updateSQL); err != nil {
			return err
		}
	}
	return nil
}

func normalizeMemoryTimestampColumn(
	ctx context.Context,
	tx *sql.Tx,
	name, selectSQL, updateSQL string,
) error {
	rows, err := tx.QueryContext(ctx, selectSQL)
	if err != nil {
		return fmt.Errorf("normalize %s: select values: %w", name, err)
	}

	updates := make([]memoryTimestampUpdate, 0)
	for rows.Next() {
		var key any
		var raw string
		if scanErr := rows.Scan(&key, &raw); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("normalize %s: scan value: %w", name, scanErr)
		}
		parsed, parseErr := memorytime.Parse(raw)
		if parseErr != nil {
			_ = rows.Close()
			return fmt.Errorf("normalize %s key %v: invalid timestamp %q: %w", name, key, raw, parseErr)
		}
		canonical := memorytime.Format(parsed)
		if raw != canonical {
			updates = append(updates, memoryTimestampUpdate{key: key, value: canonical})
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return fmt.Errorf("normalize %s: iterate values: %w", name, rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return fmt.Errorf("normalize %s: close values: %w", name, closeErr)
	}

	for _, update := range updates {
		if _, execErr := tx.ExecContext(ctx, updateSQL, update.value, update.key); execErr != nil {
			return fmt.Errorf("normalize %s key %v: update value: %w", name, update.key, execErr)
		}
	}
	return nil
}

package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"time"
)

const (
	activationFloor = 0.05
	halfLifeHours   = 14 * 24 // 336 hours
)

// DecayJob runs periodic activation decay and TTL expiry against the memory
// store. Interval defaults to 1 hour if not set.
type DecayJob struct {
	Store    *Store
	Interval time.Duration
	Logger   func(format string, args ...interface{})
}

// Run starts the decay loop and blocks until ctx is canceled.
func (j *DecayJob) Run(ctx context.Context) {
	if j.Interval <= 0 {
		j.Interval = 1 * time.Hour
	}
	if j.Logger == nil {
		j.Logger = log.Printf
	}
	ticker := time.NewTicker(j.Interval)
	defer ticker.Stop()
	j.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.runOnce(ctx)
		}
	}
}

func (j *DecayJob) runOnce(ctx context.Context) {
	if runErr := j.Store.applyActivationDecay(ctx); runErr != nil {
		if !errors.Is(runErr, context.Canceled) {
			j.Logger("decay: applyActivationDecay: %v", runErr)
		}
	}
	if runErr := j.Store.expireTTLRevisions(ctx); runErr != nil {
		if !errors.Is(runErr, context.Canceled) {
			j.Logger("decay: expireTTLRevisions: %v", runErr)
		}
	}
}

// applyActivationDecay applies exponential decay to all memory_state rows.
// Half-life is 14 days. Floor is 0.05. Updates that would change activation
// by less than 0.001 are skipped.
//
// The decay is relative to the current stored activation (per plan-time
// decision 1):
//
//	new_activation = activation * exp(-elapsed_hours * ln(2) / halfLifeHours)
func (s *Store) applyActivationDecay(ctx context.Context) error {
	updates, err := s.collectDecayUpdates(ctx)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}

	tx, txErr := s.db.BeginTx(ctx, nil)
	if txErr != nil {
		return fmt.Errorf("begin tx: %w", txErr)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, stmtErr := tx.PrepareContext(ctx,
		`UPDATE memory_state SET activation = ? WHERE memory_id = ?`,
	)
	if stmtErr != nil {
		return fmt.Errorf("prepare stmt: %w", stmtErr)
	}
	defer func() { _ = stmt.Close() }()

	for _, u := range updates {
		if _, execErr := stmt.ExecContext(ctx, u.newActivation, u.memoryID); execErr != nil {
			return fmt.Errorf("update activation for %s: %w", u.memoryID, execErr)
		}
	}

	return tx.Commit()
}

type decayUpdate struct {
	memoryID      string
	newActivation float64
}

func (s *Store) collectDecayUpdates(ctx context.Context) ([]decayUpdate, error) {
	rows, queryErr := s.db.QueryContext(ctx,
		`SELECT memory_id, activation, last_accessed_at, created_at FROM memory_state`,
	)
	if queryErr != nil {
		return nil, fmt.Errorf("query memory_state: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	now := time.Now().UTC()
	var updates []decayUpdate

	for rows.Next() {
		var memoryID string
		var activation float64
		var lastAccessedAt sql.NullString
		var createdAt string
		if scanErr := rows.Scan(&memoryID, &activation, &lastAccessedAt, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("scan row: %w", scanErr)
		}

		// Determine baseline: last_accessed_at if set, else created_at.
		baseline := createdAt
		if lastAccessedAt.Valid && lastAccessedAt.String != "" {
			baseline = lastAccessedAt.String
		}
		baseTime, parseErr := parseMemoryTime(baseline)
		if parseErr != nil {
			// Skip rows with unparseable timestamps.
			continue
		}

		elapsedHours := now.Sub(baseTime).Hours()
		if elapsedHours < 0 {
			elapsedHours = 0
		}

		// Relative exponential decay: preserves reinforcement boosts.
		newActivation := activation * math.Exp(-elapsedHours*math.Ln2/halfLifeHours)

		// Apply floor.
		if newActivation < activationFloor {
			newActivation = activationFloor
		}

		// Skip if change is negligible.
		if math.Abs(newActivation-activation) < 0.001 {
			continue
		}

		updates = append(updates, decayUpdate{memoryID: memoryID, newActivation: newActivation})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate rows: %w", rowsErr)
	}

	return updates, nil
}

// ExportApplyActivationDecay is exported for testing only.
func (s *Store) ExportApplyActivationDecay(ctx context.Context) error {
	return s.applyActivationDecay(ctx)
}

// ExportExpireTTLRevisions is exported for testing only.
func (s *Store) ExportExpireTTLRevisions(ctx context.Context) error {
	return s.expireTTLRevisions(ctx)
}

// expireTTLRevisions marks as deprecated all revisions whose expires_at has
// passed. It reuses the authorized Deprecate path which also recomputes
// current_revision.
func (s *Store) expireTTLRevisions(ctx context.Context) error {
	now := time.Now().UTC().Format(memoryTimeFormat)
	rows, queryErr := s.db.QueryContext(ctx,
		`SELECT revision_id FROM memory_revisions
		 WHERE expires_at IS NOT NULL AND expires_at <= ? AND status != ?`,
		now, string(StatusDeprecated),
	)
	if queryErr != nil {
		return fmt.Errorf("query expired revisions: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	var revisionIDs []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return fmt.Errorf("scan revision_id: %w", scanErr)
		}
		revisionIDs = append(revisionIDs, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("iterate expired revisions: %w", rowsErr)
	}

	for _, id := range revisionIDs {
		if depErr := s.Deprecate(ctx, id); depErr != nil {
			return fmt.Errorf("deprecate expired revision %s: %w", id, depErr)
		}
	}
	return nil
}

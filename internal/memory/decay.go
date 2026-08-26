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

// applyActivationDecay applies exponential decay to all memory_state rows
// against the current wall clock. Half-life is 14 days. Floor is 0.05. Updates
// that would change activation by less than decayWriteThreshold are skipped.
//
// The decay is relative to the current stored activation (per plan-time
// decision 1):
//
//	new_activation = activation * exp(-elapsed_hours * ln(2) / halfLifeHours)
func (s *Store) applyActivationDecay(ctx context.Context) error {
	return s.applyActivationDecayAt(ctx, time.Now().UTC())
}

// applyActivationDecayAt is applyActivationDecay with the clock injected. The
// whole pass shares one `now`: the same instant that computes elapsed is the
// instant written back as the new baseline, so no sliver of time can be
// double-counted or lost between the read and the write.
//
// THE INVARIANT: activation and last_decayed_at move together, always, in one
// UPDATE. A row's stored activation is exactly its value at last_decayed_at.
// Elapsed is measured from that column and from nothing else — in particular
// never from last_accessed_at, which belongs to reads (see activation.go).
//
// This is what makes the pass idempotent in the only sense that matters:
// running it twice with no time passing computes elapsed ~0 the second time and
// writes nothing, instead of re-applying an interval it has already applied.
func (s *Store) applyActivationDecayAt(ctx context.Context, now time.Time) error {
	now = now.UTC()
	updates, err := s.collectDecayUpdates(ctx, now)
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

	// Both columns, or neither. Writing activation without advancing the
	// baseline is the defect this ticket fixed (CW-20260826-0001); advancing
	// the baseline without writing activation would silently discard decay.
	stmt, stmtErr := tx.PrepareContext(ctx,
		`UPDATE memory_state SET activation = ?, last_decayed_at = ? WHERE memory_id = ?`,
	)
	if stmtErr != nil {
		return fmt.Errorf("prepare stmt: %w", stmtErr)
	}
	defer func() { _ = stmt.Close() }()

	nowStr := now.Format(memoryTimeFormat)
	for _, u := range updates {
		if _, execErr := stmt.ExecContext(ctx, u.newActivation, nowStr, u.memoryID); execErr != nil {
			return fmt.Errorf("update activation for %s: %w", u.memoryID, execErr)
		}
	}

	return tx.Commit()
}

type decayUpdate struct {
	memoryID      string
	newActivation float64
}

// decayWriteThreshold is the smallest activation change worth a row write. It
// is a write-churn guard, not a decay rule, and the distinction is load-bearing:
// a row below it is skipped WITHOUT advancing last_decayed_at, so the elapsed
// time keeps accruing and lands whole in a later pass. Advancing the baseline
// on a skipped row would discard that time permanently, which at hourly passes
// silently converts the threshold into an activation floor near 0.485 —
// 0.485 * (1 - exp(-1*ln2/336)) is almost exactly 0.001 — and no row below it
// would ever decay again.
const decayWriteThreshold = 0.001

func (s *Store) collectDecayUpdates(ctx context.Context, now time.Time) ([]decayUpdate, error) {
	rows, queryErr := s.db.QueryContext(ctx,
		`SELECT memory_id, activation, last_decayed_at, created_at FROM memory_state`,
	)
	if queryErr != nil {
		return nil, fmt.Errorf("query memory_state: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	var updates []decayUpdate

	for rows.Next() {
		var memoryID string
		var activation float64
		var lastDecayedAt sql.NullString
		var createdAt string
		if scanErr := rows.Scan(&memoryID, &activation, &lastDecayedAt, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("scan row: %w", scanErr)
		}

		// Baseline: last_decayed_at if set, else created_at. NULL means the row
		// has never been decayed, and created_at is then the honest answer to
		// "as of when is this activation current" — it is the 1.0 insert
		// default, established at insert. last_accessed_at is deliberately not
		// consulted; decay must not read the reinforcement signal.
		baseline := createdAt
		if lastDecayedAt.Valid && lastDecayedAt.String != "" {
			baseline = lastDecayedAt.String
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

		// Skip if change is negligible. See decayWriteThreshold: skipping here
		// leaves last_decayed_at untouched on purpose.
		if math.Abs(newActivation-activation) < decayWriteThreshold {
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

// ExportApplyActivationDecayAt is exported for testing only. It runs one decay
// pass against an injected clock, which is how tests state elapsed time exactly
// instead of back-dating a column and hoping the two agree.
func (s *Store) ExportApplyActivationDecayAt(ctx context.Context, now time.Time) error {
	return s.applyActivationDecayAt(ctx, now)
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

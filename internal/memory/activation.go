package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// activationCeiling is the asymptote reinforcement approaches. It is a
	// bound, not a clamp: `activation + k*(ceiling - activation)` gets closer
	// to it with every reinforcement and never arrives, so frequently-read
	// memories stay distinguishable from each other instead of piling at one
	// value — which is the discrimination activation exists to provide, at the
	// top of the range where it matters most.
	//
	// It sits ABOVE memory_state.activation's insert default of 1.0
	// (internal/contextstore/store.go), and that headroom is the point. A new
	// memory lands mid-range with room to climb and room to fall. A ceiling AT
	// the insert default would make 1.0 a fixed point of the curve, so touching
	// a memory written this session would move activation by exactly zero —
	// the case the touch loop exists for. See TestFreshMemoryMovesUnderTouch.
	activationCeiling = 2.0

	// reinforcementRate is k: the fraction of the remaining distance to
	// activationCeiling that one reinforcement closes. From the floor it
	// yields 0.05 → 0.245 → 0.4205 → 0.57845 → 0.720605 → …
	//
	// It is a package constant rather than config on purpose. Per D4 an app
	// default with no per-call override is the shape any tuning must take,
	// because a caller that chooses its own reinforcement weight is exactly
	// the incentive problem the touch loop exists to avoid.
	//
	// Do not tune it against observed activation levels while CW-20260826-0001
	// is open: decay currently re-applies its factor from a baseline it never
	// advances, so the levels a rate would be fitted to are set by that bug
	// rather than by the rate.
	reinforcementRate = 0.1

	// MaxTouchRevisions bounds one TouchRevisions request. It is a request-size
	// guard, not an incentive mechanism — the incentive work is done by the
	// curve above, which cannot be gamed by touching more. Callers wanting to
	// report more than this in one turn are almost certainly reporting things
	// that did not shape the turn, but the cap exists so one malformed call
	// cannot rewrite a corpus.
	MaxTouchRevisions = 100
)

// reinforceMemoryIDs is the shared activation-reinforcement primitive: it
// bumps activation, access_count, and last_accessed_at on memory_state for
// every memory_id in the set. Reinforcement is a "deliberate read" signal —
// it must be driven only by the get paths (memory_get / memory_get_revision)
// or by an explicit TouchRevisions report, never by search/recall (which would
// let the system's own guesses self-reinforce).
//
// The curve is `activation + k*(ceiling - activation)`, with k and the ceiling
// bound as parameters rather than written into the statement text, so the
// constants above are the single definition of the rule.
//
// The increment is negative for a row already above activationCeiling, which
// walks such a row back down toward it. No row in the live corpus is above 2.0
// and no path writes one, so this is a property of the curve rather than a case
// anything currently hits — which is exactly why it is tested rather than
// assumed. See TestTouchPullsOutOfRangeRowsTowardCeiling.
//
// Callers decide whether a failure here is fatal. The get paths swallow it —
// a reinforcement failure must not fail a read. TouchRevisions does not, because
// there the reinforcement IS the operation.
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
	// last_decayed_at is stamped alongside because reinforcement WRITES
	// activation, and the stamp is what every writer of activation owes so no
	// interval is applied twice (decay.go). The two columns say different
	// things and are written for different reasons: last_accessed_at is the
	// read signal, and nothing but a real read ever writes it; last_decayed_at
	// is bookkeeping for the decay pass.
	//
	// Note what this does NOT do: the new activation is computed from the value
	// current as of the OLD baseline, and the decay owed since then is
	// discarded rather than applied first. A year-old never-decayed row at 1.0
	// lands at 1.1 here, where decaying first would give 0.05 -> 0.245. That is
	// pre-existing, bounded by the pass interval in normal operation (~0.2% at
	// hourly passes) and unbounded across job downtime. See the note on
	// applyActivationDecayAt: fixing it changes the reinforcement curve's
	// inputs and belongs with the equilibrium retune.
	//
	// Without it a touch is annihilated. A floored row is skipped by every
	// decay pass (its change is 0, under decayWriteThreshold), so its
	// last_decayed_at stays wherever it last landed — months back, for most of
	// the corpus. Reinforce that row to 0.245 and the very next pass would see
	// months of elapsed against a value one second old and slam it straight
	// back to the floor. That is the compounding defect coming back through a
	// different door, and it would hit precisely the memories the touch loop
	// exists to rescue.
	stmt, err := tx.PrepareContext(ctx, `
		UPDATE memory_state
		SET activation = activation + ? * (? - activation),
		    access_count = access_count + 1,
		    last_accessed_at = ?,
		    last_decayed_at = ?
		WHERE memory_id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare reinforce: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, id := range memoryIDs {
		if _, err := stmt.ExecContext(ctx, reinforcementRate, activationCeiling, now, now, id); err != nil {
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

// TouchResult reports what a TouchRevisions call did. Touched counts distinct
// memories reinforced, not revision IDs accepted: two revisions of one memory
// reinforce it once. NotFound is always a slice, never nil, so a caller reading
// JSON never has to tell an empty list from a missing key.
type TouchResult struct {
	Touched  int      `json:"touched"`
	NotFound []string `json:"not_found"`
}

// TouchRevisions reinforces the memories behind the given revision IDs: the
// write half of the recall → use → touch loop.
//
// Recall deliberately does not reinforce, because being returned by a search is
// the ranker's own guess (recall.go). Touch is the caller reporting, after the
// reasoning, which of those guesses were right — the one input that feeds
// activation information rather than more guesses. The timing is the point:
// a `touch: true` flag on recall would reinforce the guess, at the moment the
// guess was made.
//
// It works across domains — a revision ID names a row in memory_revisions
// whether it was written as memory or as knowledge — which is why the tool that
// fronts it is named tesseract_touch rather than memory_touch.
//
// Duplicate revision IDs, and distinct revisions of the same memory, collapse to
// one reinforcement. Reporting the same thing twice must not be worth more than
// reporting it once.
//
// Unknown revision IDs are reported in TouchResult.NotFound rather than raised:
// a partly-stale set of IDs is a normal thing for a caller to hold, and failing
// the whole call would cost the reinforcements that were valid.
//
// The accounting invariant that makes NotFound usable: every DISTINCT ID in the
// request is accounted for exactly once, either by contributing to Touched or by
// appearing in NotFound. Nothing is silently dropped — including the empty
// string. Touched counts memories rather than IDs, so it is <= the number of
// distinct IDs that resolved, never a per-ID tally.
func (s *Store) TouchRevisions(ctx context.Context, revisionIDs []string) (TouchResult, error) {
	res := TouchResult{NotFound: []string{}}
	if len(revisionIDs) == 0 {
		return res, nil
	}
	if len(revisionIDs) > MaxTouchRevisions {
		return res, fmt.Errorf("%w: at most %d revision_ids per touch, got %d",
			ErrInvalidInput, MaxTouchRevisions, len(revisionIDs))
	}

	seenRevision := make(map[string]struct{}, len(revisionIDs))
	seenMemory := make(map[string]struct{}, len(revisionIDs))
	var memoryIDs []string

	// The empty string is deliberately NOT skipped here. Every distinct ID a
	// caller sends must come back either counted in Touched or listed in
	// NotFound — a caller diffing NotFound against what it sent should find a
	// hole nowhere. "" resolves to no row and lands in NotFound like any other
	// ID that names nothing. See TestTouchRevisions_AccountsForEveryDistinctID.
	for _, revID := range revisionIDs {
		if _, dup := seenRevision[revID]; dup {
			continue
		}
		seenRevision[revID] = struct{}{}

		var memoryID string
		row := s.db.QueryRowContext(ctx,
			`SELECT memory_id FROM memory_revisions WHERE revision_id = ?`, revID)
		if err := row.Scan(&memoryID); err != nil {
			// A missing row is the expected not-found case; any other scan
			// error is a real failure and must not be reported as not-found.
			if errors.Is(err, sql.ErrNoRows) {
				res.NotFound = append(res.NotFound, revID)
				continue
			}
			return res, fmt.Errorf("resolve revision %s: %w", revID, err)
		}
		if _, dup := seenMemory[memoryID]; dup {
			continue
		}
		seenMemory[memoryID] = struct{}{}
		memoryIDs = append(memoryIDs, memoryID)
	}

	if err := s.reinforceMemoryIDs(ctx, memoryIDs); err != nil {
		return res, err
	}
	res.Touched = len(memoryIDs)
	return res, nil
}

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
// every memory_id in the set whose domain participates in activation.
//
// The domain filter is applied in the UPDATE itself (see below), so a
// memory_id naming a non-participating domain is a no-op rather than an error.
// That is the right shape for the read paths, which reinforce best-effort and
// must not fail a read over it.
//
// It returns the memory IDs it ACTUALLY moved, in the order they were given.
// Until CW-20260909-0035 it returned only an error and TouchRevisions counted
// its own request, which was exact for as long as every domain participated —
// the ask and the effect were the same set. Event opts out, so from the moment
// it exists the two diverge, and a count of the ask would report reinforcement
// of rows the predicate excluded. Deriving the answer from RowsAffected rather
// than from a second policy lookup at the call site is deliberate: the SQL
// predicate is the single decider of participation (CW-20260910-0021), and a
// caller that re-derived the same answer in Go would be a second source free to
// drift from it.
//
// Reinforcement is a "deliberate read" signal —
// it must be driven only by the get paths (tesseract_get / tesseract_get_revision)
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
func (s *Store) reinforceMemoryIDs(ctx context.Context, memoryIDs []string) ([]string, error) {
	if len(memoryIDs) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin reinforce tx: %w", err)
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
	// The domain predicate is part of the statement, not a check some caller
	// runs first, and that placement is the fix CW-20260910-0021 landed. Every
	// reinforcement path in the tree funnels through here — the deliberate-read
	// getters, tesseract_get_revision and tesseract_touch, the last two already
	// domain-blind — so gating in SQL makes participation a property of the row
	// rather than of whoever wrote the call site. The defect being repaired was
	// exactly a call site holding this choice: knowledge's read path called the
	// plain getter where memory's called the reinforcing one, and nothing in
	// the type system or the tests noticed for four months.
	domainFilter, filterArgs := activationDomainPredicate("domain")
	stmt, err := tx.PrepareContext(ctx, `
		UPDATE memory_state
		SET activation = activation + ? * (? - activation),
		    access_count = access_count + 1,
		    last_accessed_at = ?,
		    last_decayed_at = ?
		WHERE memory_id = ? AND `+domainFilter+`
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare reinforce: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	var moved []string
	for _, id := range memoryIDs {
		args := append([]any{reinforcementRate, activationCeiling, now, now, id}, filterArgs...)
		res, execErr := stmt.ExecContext(ctx, args...)
		if execErr != nil {
			return nil, fmt.Errorf("reinforce %s: %w", id, execErr)
		}
		// A driver that cannot report RowsAffected is treated as "did not
		// move", which under-reports rather than over-reports. That is the
		// safer direction for a number a caller may trust: the whole reason
		// this returns a set is that a count of the ask was optimistic.
		// modernc.org/sqlite reports it, so this is a guard rather than a live
		// path.
		n, raErr := res.RowsAffected()
		if raErr != nil || n == 0 {
			continue
		}
		moved = append(moved, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return moved, nil
}

// reinforceAccess reinforces activation + last_accessed_at + access_count for
// a single deliberately-read memory. Best-effort: errors do not fail the read.
func (s *Store) reinforceAccess(ctx context.Context, memoryID string) error {
	if memoryID == "" {
		return nil
	}
	_, err := s.reinforceMemoryIDs(ctx, []string{memoryID})
	return err
}

// TouchResult reports what a TouchRevisions call did. Touched counts distinct
// memories ACTUALLY reinforced, not revision IDs accepted: two revisions of one
// memory reinforce it once, and a revision whose domain sits outside activation
// reinforces nothing. Both slices are always slices, never nil, so a caller
// reading JSON never has to tell an empty list from a missing key.
type TouchResult struct {
	Touched  int      `json:"touched"`
	NotFound []string `json:"not_found"`

	// NotReinforced lists the revision IDs that resolved to a real memory whose
	// domain does not participate in activation — event revisions, today. They
	// are neither counted nor an error: touching one is a well-formed request
	// with no effect, and saying so is the difference between a caller learning
	// that and a caller believing a number.
	//
	// It is a third bucket rather than an entry in NotFound because the two are
	// different facts. NotFound means the ID names nothing; this means the ID
	// names something the activation system does not move. Collapsing them
	// would send a caller looking for a revision that is sitting right there.
	NotReinforced []string `json:"not_reinforced"`
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
// The accounting invariant that makes the report usable: every DISTINCT ID in
// the request is accounted for exactly once, by contributing to Touched, by
// appearing in NotFound, or by appearing in NotReinforced. Nothing is silently
// dropped — including the empty string. Touched counts memories rather than IDs,
// so it is <= the number of distinct IDs that resolved, never a per-ID tally.
//
// Touched reports what the UPDATE moved, not what this asked it to move. The
// two were the same set for as long as every domain participated in activation;
// Event is the first that does not (CW-20260909-0035), and counting the ask
// from that point on would have reported reinforcement of rows the SQL
// predicate excluded. The distinction is drawn from RowsAffected rather than
// from a policy lookup here — see reinforceMemoryIDs for why the statement has
// to be the one that answers.
func (s *Store) TouchRevisions(ctx context.Context, revisionIDs []string) (TouchResult, error) {
	res := TouchResult{NotFound: []string{}, NotReinforced: []string{}}
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
	// Every distinct revision ID that resolved, grouped by the memory it
	// resolved to. Kept for ALL of them, not only the first per memory: if the
	// memory turns out not to move, a caller diffing NotReinforced against what
	// it sent must find every ID it sent, not a representative of each.
	revisionsByMemory := make(map[string][]string, len(revisionIDs))

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
		revisionsByMemory[memoryID] = append(revisionsByMemory[memoryID], revID)
		if _, dup := seenMemory[memoryID]; dup {
			continue
		}
		seenMemory[memoryID] = struct{}{}
		memoryIDs = append(memoryIDs, memoryID)
	}

	moved, err := s.reinforceMemoryIDs(ctx, memoryIDs)
	if err != nil {
		return res, err
	}
	movedSet := make(map[string]struct{}, len(moved))
	for _, id := range moved {
		movedSet[id] = struct{}{}
	}
	// memoryIDs preserves request order, so NotReinforced comes back in the
	// order the caller sent rather than in map order.
	for _, id := range memoryIDs {
		if _, ok := movedSet[id]; ok {
			continue
		}
		res.NotReinforced = append(res.NotReinforced, revisionsByMemory[id]...)
	}
	res.Touched = len(moved)
	return res, nil
}

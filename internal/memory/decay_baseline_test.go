package memory_test

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// Decay baseline properties — CW-20260826-0001.
//
// Every expectation in this file is a LITERAL, and the comment beside it gives
// the arithmetic that produced it. None of them is computed from
// math.Exp(-elapsed*math.Ln2/halfLifeHours), because an expectation written
// that way asserts the formula against itself: it holds for any half-life,
// including the compounding one this ticket removed, and would have passed
// against the defect unchanged.
//
// The N values are chosen so the literal follows from the half-life PROPERTY
// rather than from the exponential: after one half-life a value halves, after
// two it quarters, and a half-step is 1/sqrt(2) because two half-steps have to
// compose into one halving. A wrong half-life, or a second application of the
// right one, fails all four.

// decayEpoch is an arbitrary fixed instant. Every pass in this file is driven
// by an injected clock, so no test here depends on the wall clock or on how
// long it takes to run.
var decayEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// setDecayBaseline puts memory_state.last_decayed_at at an exact instant so
// elapsed is stated rather than approximated.
func setDecayBaseline(t *testing.T, ms *memory.Store, memoryID string, at time.Time) {
	t.Helper()
	if _, err := ms.DB().ExecContext(context.Background(),
		`UPDATE memory_state SET last_decayed_at = ? WHERE memory_id = ?`,
		at.UTC().Format(time.RFC3339Nano), memoryID,
	); err != nil {
		t.Fatalf("set last_decayed_at for %s: %v", memoryID, err)
	}
}

func decayBaselineOf(t *testing.T, ms *memory.Store, memoryID string) (string, bool) {
	t.Helper()
	var v sql.NullString
	if err := ms.DB().QueryRowContext(context.Background(),
		`SELECT last_decayed_at FROM memory_state WHERE memory_id = ?`, memoryID,
	).Scan(&v); err != nil {
		t.Fatalf("read last_decayed_at for %s: %v", memoryID, err)
	}
	return v.String, v.Valid && v.String != ""
}

func lastAccessedOf(t *testing.T, ms *memory.Store, memoryID string) (string, bool) {
	t.Helper()
	var v sql.NullString
	if err := ms.DB().QueryRowContext(context.Background(),
		`SELECT last_accessed_at FROM memory_state WHERE memory_id = ?`, memoryID,
	).Scan(&v); err != nil {
		t.Fatalf("read last_accessed_at for %s: %v", memoryID, err)
	}
	return v.String, v.Valid && v.String != ""
}

// TestMigrationStampsExistingRowsRatherThanBackfilling pins what schema
// migration 14 decided to do with rows that predate it, because the diff alone
// would not say and the two options are not close.
//
// Backfilling last_decayed_at from the old baseline — last_accessed_at, else
// created_at — would hand the first pass after the upgrade an elapsed of the
// row's whole lifetime. For a corpus months old that is many half-lives in one
// transaction, and every row lands on the floor: the upgrade would finish the
// destruction rather than stop it, and it would take the recently-reinforced
// rows that still carry signal with it.
//
// Stamping `now` freezes each row where it is, correct or not, and starts the
// clock from the upgrade. It reconstructs nothing: activation is overwritten in
// place with no history anywhere in the schema, so pre-existing levels are not
// recoverable and any curve fitted to them would be invented.
func TestMigrationStampsExistingRowsRatherThanBackfilling(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	rev, err := ms.WriteRevision(ctx, sampleInput("decay.migration"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	// Age the row a year and put the schema back to where it was before the
	// column existed, so reopening replays the migration over a mature row.
	yearAgo := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, ageErr := cs.DB().ExecContext(ctx,
		`UPDATE memory_state SET created_at = ?, last_accessed_at = ? WHERE memory_id = ?`,
		yearAgo, yearAgo, rev.MemoryID); ageErr != nil {
		t.Fatalf("age the row: %v", ageErr)
	}
	if _, dropErr := cs.DB().ExecContext(ctx, `ALTER TABLE memory_state DROP COLUMN last_decayed_at`); dropErr != nil {
		t.Fatalf("drop last_decayed_at: %v", dropErr)
	}
	if _, rollErr := cs.DB().ExecContext(ctx, `DELETE FROM schema_version WHERE version >= 14`); rollErr != nil {
		t.Fatalf("roll schema_version back: %v", rollErr)
	}
	if closeErr := cs.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	// Reopen: migration 14 runs against a row a year old.
	upgradeAt := time.Now().UTC()
	cs2, err := contextstore.Open(ctx, contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("reopen contextstore: %v", err)
	}
	defer func() { _ = cs2.Close() }()
	ms2 := memory.NewStore(cs2.DB(), nil, "", 0, memory.NoopQueue{})

	stamped, ok := decayBaselineOf(t, ms2, rev.MemoryID)
	if !ok {
		t.Fatal("migration left last_decayed_at unset on an existing row")
	}
	got, parseErr := time.Parse(time.RFC3339Nano, stamped)
	if parseErr != nil {
		t.Fatalf("migration wrote an unparseable last_decayed_at %q: %v", stamped, parseErr)
	}
	if got.Before(upgradeAt.Add(-1 * time.Minute)) {
		t.Fatalf("migration backfilled last_decayed_at to %v, roughly the row's own age; "+
			"it must stamp the upgrade instant (~%v) or the first pass applies a year at once",
			got, upgradeAt)
	}

	// The consequence, stated as behavior rather than inferred: one pass an
	// hour after the upgrade costs one hour, not a year. A year would be
	// 365*24/336 = 26 half-lives, i.e. the floor.
	if decayErr := ms2.ExportApplyActivationDecayAt(ctx, upgradeAt.Add(1*time.Hour)); decayErr != nil {
		t.Fatalf("post-migration decay pass: %v", decayErr)
	}
	if act := activationOf(t, ms2, rev.MemoryID); act < 0.99 {
		t.Errorf("an hour after the upgrade a year-old row is at %v, want ~1.0 — "+
			"the migration handed the pass the row's whole lifetime", act)
	}
}

// TestDecayIsOneApplicationOverElapsed is the headline acceptance property: a
// memory unread for N hours holds activation equal to ONE application of the
// half-life over N hours — asserted at four values of N, not one.
func TestDecayIsOneApplicationOverElapsed(t *testing.T) {
	cases := []struct {
		name       string
		elapsed    time.Duration
		want       float64
		derivation string
	}{
		{
			name:       "half_of_one_half_life",
			elapsed:    168 * time.Hour,
			want:       0.7071067811865476,
			derivation: "168h is half of the 336h half-life; two such steps must compose to one halving, so each is 1/sqrt(2)",
		},
		{
			name:       "one_half_life",
			elapsed:    336 * time.Hour,
			want:       0.5,
			derivation: "336h is one half-life by definition: 1.0 halves to 0.5",
		},
		{
			name:       "two_half_lives",
			elapsed:    672 * time.Hour,
			want:       0.25,
			derivation: "672h is two half-lives: 0.5 * 0.5",
		},
		{
			name:       "three_half_lives",
			elapsed:    1008 * time.Hour,
			want:       0.125,
			derivation: "1008h is three half-lives: 0.5 * 0.5 * 0.5, still clear of the 0.05 floor",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ms, cleanup := newTestStore(t)
			defer cleanup()

			rev, err := ms.WriteRevision(ctx, sampleInput("decay.one_application."+tc.name))
			if err != nil {
				t.Fatalf("WriteRevision: %v", err)
			}
			// The literals above are all relative to a start of 1.0. If the
			// insert default ever moves, they are wrong rather than merely
			// stale, so this is asserted and not assumed.
			if start := activationOf(t, ms, rev.MemoryID); start != insertDefaultActivation {
				t.Fatalf("fresh memory starts at %v, want %v — the expectations below are relative to it",
					start, insertDefaultActivation)
			}

			setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)
			if err := ms.ExportApplyActivationDecayAt(ctx, decayEpoch.Add(tc.elapsed)); err != nil {
				t.Fatalf("decay pass: %v", err)
			}

			got := activationOf(t, ms, rev.MemoryID)
			if math.Abs(got-tc.want) > 1e-12 {
				t.Errorf("after %v unread: activation = %v, want %v (%s)",
					tc.elapsed, got, tc.want, tc.derivation)
			}
		})
	}
}

// TestDecayRepeatedWithNoTimePassingChangesNothing is the cheapest possible
// regression test for CW-20260826-0001 and the property that failed before it:
// the pass re-derived elapsed from a baseline it never advanced, so running it
// again immediately re-applied an interval it had already applied.
func TestDecayRepeatedWithNoTimePassingChangesNothing(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.idempotent"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)

	// One honest application of the half-life over 336h: 1.0 halves to 0.5.
	at := decayEpoch.Add(336 * time.Hour)
	if err := ms.ExportApplyActivationDecayAt(ctx, at); err != nil {
		t.Fatalf("first decay pass: %v", err)
	}
	after := activationOf(t, ms, rev.MemoryID)
	if math.Abs(after-0.5) > 1e-12 {
		t.Fatalf("first pass: activation = %v, want 0.5 (one half-life from the 1.0 insert default)", after)
	}
	baseline, ok := decayBaselineOf(t, ms, rev.MemoryID)
	if !ok {
		t.Fatal("first pass did not stamp last_decayed_at; the baseline never advances and decay will compound")
	}

	// Twenty more passes at the SAME instant. Under the defect each would have
	// multiplied by another exp(-336*ln2/336) = 0.5, leaving 0.5^21 = 4.8e-7,
	// which the floor would then clamp to 0.05.
	for i := 0; i < 20; i++ {
		if err := ms.ExportApplyActivationDecayAt(ctx, at); err != nil {
			t.Fatalf("repeat decay pass %d: %v", i+1, err)
		}
	}

	// Bit-identical, not merely close: with elapsed 0 the pass must write
	// nothing at all, so there is no rounding to tolerate.
	if got := activationOf(t, ms, rev.MemoryID); got != after {
		t.Errorf("21 passes at one instant moved activation %v -> %v; a pass with no time elapsed must be a no-op",
			after, got)
	}
	if got, _ := decayBaselineOf(t, ms, rev.MemoryID); got != baseline {
		t.Errorf("repeat passes rewrote last_decayed_at %q -> %q", baseline, got)
	}
}

// TestDecayBaselineSurvivesRestart closes the store and reopens it from the
// same directory between the two halves of a single half-life. A baseline held
// in process memory would reset here; a baseline held in the row does not, and
// the two half-steps compose to exactly one halving.
func TestDecayBaselineSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.restart"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)

	// First half-step: 168h is half of the 336h half-life, so 1.0 -> 1/sqrt(2).
	if decayErr := ms.ExportApplyActivationDecayAt(ctx, decayEpoch.Add(168*time.Hour)); decayErr != nil {
		t.Fatalf("pre-restart decay pass: %v", decayErr)
	}
	if got := activationOf(t, ms, rev.MemoryID); math.Abs(got-0.7071067811865476) > 1e-12 {
		t.Fatalf("pre-restart: activation = %v, want 0.7071067811865476 (1/sqrt(2))", got)
	}

	// Restart: everything the process knew is gone.
	if closeErr := cs.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}
	cs2, err := contextstore.Open(ctx, contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("reopen contextstore: %v", err)
	}
	defer func() { _ = cs2.Close() }()
	ms2 := memory.NewStore(cs2.DB(), nil, "", 0, memory.NoopQueue{})

	// Second half-step, another 168h on from the first. Two half-steps compose
	// to one half-life: 1/sqrt(2) * 1/sqrt(2) = 0.5.
	if err := ms2.ExportApplyActivationDecayAt(ctx, decayEpoch.Add(336*time.Hour)); err != nil {
		t.Fatalf("post-restart decay pass: %v", err)
	}
	got := activationOf(t, ms2, rev.MemoryID)
	if math.Abs(got-0.5) > 1e-12 {
		t.Errorf("two 168h steps across a restart: activation = %v, want 0.5 — "+
			"the halves must compose to exactly one half-life", got)
	}
}

// TestDecayDoesNotCompoundAcrossHourlyPasses runs the job at its production
// cadence for one half-life. 336 hourly passes must total ONE halving.
//
// This is the direct inverse of the defect. Under the old baseline the pass at
// hour i saw elapsed = i, so the cumulative exponent was the triangular number
// 336*337/2 = 56616 hours instead of 336 — a factor of exp(-56616*ln2/336) =
// exp(-116.8), which is 3e-51 before the floor clamps it to 0.05.
func TestDecayDoesNotCompoundAcrossHourlyPasses(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.hourly"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)

	const passes = 336
	for i := 1; i <= passes; i++ {
		if err := ms.ExportApplyActivationDecayAt(ctx, decayEpoch.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("hourly pass %d: %v", i, err)
		}
	}

	// 1e-9 rather than 1e-12: 336 chained multiplications accumulate a few
	// hundred ULP, which is ~1e-13 relative and still four orders inside this.
	got := activationOf(t, ms, rev.MemoryID)
	if math.Abs(got-0.5) > 1e-9 {
		t.Errorf("336 hourly passes over one half-life: activation = %v, want 0.5", got)
	}

	// No unapplied tail: the last pass must have written, so the whole 336h is
	// accounted for. Without this, a run that skipped its final passes could
	// land near 0.5 for the wrong reason.
	wantBaseline := decayEpoch.Add(passes * time.Hour).Format(time.RFC3339Nano)
	if gotBaseline, _ := decayBaselineOf(t, ms, rev.MemoryID); gotBaseline != wantBaseline {
		t.Errorf("last_decayed_at = %q, want %q — some elapsed time was never applied",
			gotBaseline, wantBaseline)
	}
}

// TestDecayDoesNotStallBelowTheWriteThreshold covers the interaction that the
// write threshold creates and that a careless fix would turn into a second
// floor ten times higher than the real one.
//
// collectDecayUpdates skips a row whose change is under 0.001. At hourly passes
// one hour moves a row by activation*(1-exp(-ln2/336)) = activation*0.0020608,
// which is under 0.001 for any activation below 0.485. If a skipped pass
// advanced last_decayed_at anyway, that hour would be discarded, every
// subsequent hour would be discarded too, and nothing below 0.485 would ever
// decay again.
//
// Skipping without advancing makes the deferred time accrue and land whole in a
// later pass, so the total is unchanged: a row at 0.2 still halves to 0.1 over
// one half-life even though not one of its 336 individual hours was, on its
// own, worth a write.
func TestDecayDoesNotStallBelowTheWriteThreshold(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.subthreshold"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	setActivation(t, ms, rev.MemoryID, 0.2)
	setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)

	for i := 1; i <= 336; i++ {
		if err := ms.ExportApplyActivationDecayAt(ctx, decayEpoch.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("hourly pass %d: %v", i, err)
		}
	}

	// One half-life from 0.2 is 0.1. The result can sit slightly ABOVE that by
	// whatever tail was still accruing when the run stopped, and never below.
	//
	// Bounding the tail: near 0.1 a write needs 0.1*(1-exp(-e*ln2/336)) >= 0.001,
	// so 1-exp(-e*ln2/336) >= 0.01, so e >= 4.87h. Passes are hourly, so the
	// row writes on the 5th accrued hour and the largest tail ever outstanding
	// is 4h. Four unapplied hours are worth a factor of exp(4*ln2/336) =
	// 1.00827, so the ceiling on the result is 0.1*1.00827 = 0.100827; 0.1009
	// is that rounded up.
	got := activationOf(t, ms, rev.MemoryID)
	if got < 0.1 || got > 0.1009 {
		t.Errorf("a row at 0.2 after one half-life of hourly passes = %v, want within [0.1, 0.1009] — "+
			"0.2 means it froze at the write threshold; below 0.1 means time was applied twice", got)
	}
}

// TestDecayNeverWritesLastAccessedAt holds the line the whole ticket turns on.
// The cheap fix for compounding was to advance last_accessed_at on decay, which
// works arithmetically and destroys the only signal tesseract_touch records:
// every untouched memory in the corpus would report itself as read this hour.
func TestDecayNeverWritesLastAccessedAt(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.reads_only"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if _, set := lastAccessedOf(t, ms, rev.MemoryID); set {
		t.Fatal("a freshly written memory already has last_accessed_at set")
	}
	setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)

	// Enough passes, over enough time, that activation actually moves — a run
	// in which decay wrote nothing would prove nothing about what it writes.
	for i := 1; i <= 12; i++ {
		if err := ms.ExportApplyActivationDecayAt(ctx, decayEpoch.Add(time.Duration(i)*168*time.Hour)); err != nil {
			t.Fatalf("decay pass %d: %v", i, err)
		}
	}
	if got := activationOf(t, ms, rev.MemoryID); got >= insertDefaultActivation {
		t.Fatalf("activation did not move (%v); this test needs decay to have written", got)
	}

	if got, set := lastAccessedOf(t, ms, rev.MemoryID); set {
		t.Errorf("decay wrote last_accessed_at = %q; that column is the read signal and decay is not a read", got)
	}
	var accessCount int64
	if err := ms.DB().QueryRowContext(ctx,
		`SELECT access_count FROM memory_state WHERE memory_id = ?`, rev.MemoryID).Scan(&accessCount); err != nil {
		t.Fatalf("read access_count: %v", err)
	}
	if accessCount != 0 {
		t.Errorf("decay moved access_count to %d; decay is not an access", accessCount)
	}

	// Positive control: a real read does set it. Without this the assertions
	// above would also pass against a build where nothing writes the column.
	touchOnce(t, ms, rev.RevisionID)
	if _, set := lastAccessedOf(t, ms, rev.MemoryID); !set {
		t.Error("a touch did not set last_accessed_at; the assertions above are then vacuous")
	}
}

// TestTouchRestampsTheDecayBaseline is the other half of the invariant that
// last_decayed_at means "as of when is this activation current". Reinforcement
// writes activation, so it owes the stamp.
//
// Without it a touch on a long-floored row is annihilated: floored rows are
// skipped by every pass (their change is 0), so their baseline sits wherever it
// last landed, and the first pass after a touch would apply that entire
// interval to a value one instant old.
func TestTouchRestampsTheDecayBaseline(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.touch_restamps"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	// A row at the floor with a baseline a year stale — the state most of the
	// live corpus is in.
	setActivation(t, ms, rev.MemoryID, 0.05)
	setDecayBaseline(t, ms, rev.MemoryID, decayEpoch)

	touchOnce(t, ms, rev.RevisionID)

	// From the floor, one reinforcement is 0.05 + 0.1*(2.0-0.05) = 0.245.
	reinforced := activationOf(t, ms, rev.MemoryID)
	if math.Abs(reinforced-0.245) > 1e-9 {
		t.Fatalf("one touch from the floor = %v, want 0.245", reinforced)
	}

	// An hour later the row must have decayed by ONE hour, not by the year that
	// preceded the touch. One hour costs a factor of 2^(-1/336), and the change
	// 0.245*0.0020608 = 0.000505 is under the 0.001 write threshold, so the
	// pass correctly writes nothing at all and the value is unchanged.
	if err := ms.ExportApplyActivationDecayAt(ctx, time.Now().UTC().Add(1*time.Hour)); err != nil {
		t.Fatalf("decay pass: %v", err)
	}
	if got := activationOf(t, ms, rev.MemoryID); got != reinforced {
		t.Errorf("an hour after a touch: activation = %v, want %v unchanged — "+
			"a stale baseline survived the reinforcement", got, reinforced)
	}

	// And a month later it must be one month of decay, not a year and a month.
	// 720h is 720/336 half-lives; the check that matters is that the row is
	// still far above the floor, which a year of applied decay would not be.
	if err := ms.ExportApplyActivationDecayAt(ctx, time.Now().UTC().Add(720*time.Hour)); err != nil {
		t.Fatalf("decay pass: %v", err)
	}
	if got := activationOf(t, ms, rev.MemoryID); got <= 0.05 {
		t.Errorf("a month after a touch: activation = %v, at or under the floor — "+
			"the pre-touch interval was applied to a post-touch value", got)
	}
}

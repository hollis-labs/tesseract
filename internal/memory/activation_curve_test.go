package memory_test

// The reinforcement curve, verified rather than assumed.
//
// The rule is `activation += k * (ceiling - activation)` with ceiling = 1.0.
// Every expected value in this file is written as a literal, derived by hand
// from k = 0.2 and ceiling = 1.0 and stated here independently of the constants
// in internal/memory/activation.go. That is deliberate: a test that recomputes
// the expectation from the same constants the production code reads asserts
// only that arithmetic is deterministic. These break when either constant moves,
// which is what makes them a guard rather than a mirror.
//
// The four properties correspond one-to-one to the planner ruling on
// CW-20260825-0008: monotonicity, asymptote, out-of-range self-correction, and
// the equilibrium against decay.

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// setActivation puts a memory_state row at an exact activation so a property can
// be probed at a chosen point on the curve rather than only where a write
// happens to leave it.
func setActivation(t *testing.T, ms *memory.Store, memoryID string, v float64) {
	t.Helper()
	if _, err := ms.DB().ExecContext(context.Background(),
		`UPDATE memory_state SET activation = ? WHERE memory_id = ?`, v, memoryID); err != nil {
		t.Fatalf("seed activation %v for %s: %v", v, memoryID, err)
	}
}

func activationOf(t *testing.T, ms *memory.Store, memoryID string) float64 {
	t.Helper()
	st, err := ms.GetState(context.Background(), memoryID)
	if err != nil {
		t.Fatalf("GetState %s: %v", memoryID, err)
	}
	return st.Activation
}

// touchOnce reinforces exactly one memory through the production touch path.
func touchOnce(t *testing.T, ms *memory.Store, revisionID string) {
	t.Helper()
	res, err := ms.TouchRevisions(context.Background(), []string{revisionID})
	if err != nil {
		t.Fatalf("TouchRevisions: %v", err)
	}
	if res.Touched != 1 {
		t.Fatalf("TouchRevisions touched %d memories, want 1", res.Touched)
	}
}

// ── Property 1: monotonicity ────────────────────────────────────────────────

// TestReinforcementPreservesOrdering checks the claim that ordering between two
// memories survives reinforcement, because d/da[a + k(1-a)] = 1-k > 0 for k < 1.
//
// It is checked across the whole range rather than at one pair, including pairs
// that straddle the ceiling, because the derivative argument does not care which
// side of the ceiling a value sits on and the test should not either.
func TestReinforcementPreservesOrdering(t *testing.T) {
	pairs := []struct{ lower, higher float64 }{
		{0.05, 0.051},  // the crowded floor: the smallest gap the corpus has
		{0.05, 1.0},    // floor against a freshly written memory
		{0.4, 0.6},     // mid-range
		{0.99, 0.999},  // just under the ceiling
		{1.0, 1.1608},  // ceiling against a legacy out-of-range row
		{1.05, 1.1608}, // two out-of-range rows
	}

	for _, p := range pairs {
		t.Run(fmt.Sprintf("%v_lt_%v", p.lower, p.higher), func(t *testing.T) {
			ms, cleanup := newTestStore(t)
			defer cleanup()
			ctx := context.Background()

			lo, err := ms.WriteRevision(ctx, sampleInput("order.lower"))
			if err != nil {
				t.Fatal(err)
			}
			hi, err := ms.WriteRevision(ctx, sampleInput("order.higher"))
			if err != nil {
				t.Fatal(err)
			}
			setActivation(t, ms, lo.MemoryID, p.lower)
			setActivation(t, ms, hi.MemoryID, p.higher)

			touchOnce(t, ms, lo.RevisionID)
			touchOnce(t, ms, hi.RevisionID)

			gotLo := activationOf(t, ms, lo.MemoryID)
			gotHi := activationOf(t, ms, hi.MemoryID)
			if !(gotHi > gotLo) {
				t.Errorf("reinforcement inverted or flattened the ordering: %v -> %v and %v -> %v",
					p.lower, gotLo, p.higher, gotHi)
			}
		})
	}
}

// ── Property 2: asymptotic, never reached ───────────────────────────────────

// TestReinforcementFromFloorFollowsStatedCurve pins the exact sequence the
// ruling states for a memory starting at the floor. These five numbers are the
// ones the caller-facing docs quote; if the curve moves they must move too, and
// this is where that is noticed.
func TestReinforcementFromFloorFollowsStatedCurve(t *testing.T) {
	// Hand-derived from a' = a + 0.2*(1 - a), starting at the 0.05 floor.
	want := []float64{0.24, 0.392, 0.5136, 0.61088, 0.688704}

	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.from_floor"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 0.05)

	for i, expected := range want {
		touchOnce(t, ms, rev.RevisionID)
		got := activationOf(t, ms, rev.MemoryID)
		if math.Abs(got-expected) > 1e-9 {
			t.Fatalf("touch %d: activation = %v, want %v", i+1, got, expected)
		}
	}
}

// TestReinforcementNeverReachesCeiling asserts the bound is an asymptote, not a
// clamp: a thousand reinforcements get arbitrarily close to 1.0 and never touch
// or cross it. A clamp would pass the "<= 1.0" half of this and fail the "< 1.0"
// half, which is the distinction the ruling turns on.
func TestReinforcementNeverReachesCeiling(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.asymptote"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 0.05)

	for i := 0; i < 1000; i++ {
		touchOnce(t, ms, rev.RevisionID)
		got := activationOf(t, ms, rev.MemoryID)
		if got >= 1.0 {
			t.Fatalf("touch %d reached or passed the ceiling: activation = %v", i+1, got)
		}
	}
	// And it does get close — a bound nothing approaches would also satisfy
	// the loop above.
	if got := activationOf(t, ms, rev.MemoryID); got < 0.999999 {
		t.Errorf("after 1000 reinforcements activation = %v, expected to be within 1e-6 of 1.0", got)
	}
}

// TestReinforcementAtCeilingIsAFixedPoint records a consequence the ruling did
// not consider and that is worth being explicit about: memory_state.activation
// DEFAULTS to 1.0, so a brand-new memory is created exactly at the ceiling, and
// 1.0 is a fixed point of the curve.
//
// A newly written memory therefore cannot be reinforced above where it started.
// That is a real behavior change — under the previous 2.0 ceiling it climbed to
// 1.1, 1.19, … — and it means "most-touched" can never outrank "just-written".
// Pinning it here so the next reader meets it as a decision rather than as a
// surprise.
func TestReinforcementAtCeilingIsAFixedPoint(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.fixed_point"))
	if err != nil {
		t.Fatal(err)
	}
	// A fresh row sits at the schema default. If that default ever changes,
	// this assertion is the thing that notices — the fixed-point claim below
	// only holds because the default and the ceiling are the same number.
	if got := activationOf(t, ms, rev.MemoryID); got != 1.0 {
		t.Fatalf("a freshly written memory starts at activation %v, want 1.0 "+
			"(memory_state.activation DEFAULT); the fixed-point property depends on this", got)
	}

	for i := 0; i < 10; i++ {
		touchOnce(t, ms, rev.RevisionID)
	}
	if got := activationOf(t, ms, rev.MemoryID); got != 1.0 {
		t.Errorf("after 10 reinforcements at the ceiling activation = %v, want it unmoved at 1.0", got)
	}
}

// ── Property 3: out-of-range rows self-correct ──────────────────────────────

// TestTouchPullsOutOfRangeRowsTowardCeiling makes the negative increment above
// the ceiling deliberate and tested rather than an accident of the formula.
//
// Decision: legacy rows above 1.0 are walked down by touch and by decay; they
// are NOT clamped on migration. Clamping would collapse 1.16, 1.10 and 1.05 to a
// single 1.0 — the same flattening the ruling rejected a hard cap for, applied
// all at once instead of continuously — and it would be an irreversible write to
// production data to correct a number that corrects itself. Ordering among such
// rows survives the walk down (see TestReinforcementPreservesOrdering), so
// nothing is lost by letting them converge.
//
// 1.1608 is the maximum a planning session measured on the live corpus at
// 0b482c4. Re-measured on this branch the live maximum is 1.0 and no row exceeds
// it, so the case is legacy rather than current — which is exactly why it needs
// a test rather than a migration.
func TestTouchPullsOutOfRangeRowsTowardCeiling(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.out_of_range"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 1.1608)

	// Hand-derived: 1.1608 + 0.2*(1 - 1.1608) = 1.1608 - 0.03216 = 1.12864.
	touchOnce(t, ms, rev.RevisionID)
	got := activationOf(t, ms, rev.MemoryID)
	if math.Abs(got-1.12864) > 1e-9 {
		t.Fatalf("one touch on an out-of-range row: activation = %v, want 1.12864", got)
	}
	if got >= 1.1608 {
		t.Errorf("touch did not reduce an out-of-range row: %v -> %v", 1.1608, got)
	}

	// It converges toward the ceiling from above and never crosses it — the
	// mirror image of the from-below case, and the reason no clamp is needed.
	//
	// "Strictly decreasing" is asserted only while the gap to the ceiling is
	// wider than float64 can resolve. Past that the row parks a few ULPs above
	// 1.0 and stops moving, which is convergence, not a stalled walk — asserting
	// strict decrease forever would be asserting that float64 has no epsilon.
	const resolvable = 1e-12
	prev := got
	converged := false
	for i := 0; i < 500; i++ {
		touchOnce(t, ms, rev.RevisionID)
		cur := activationOf(t, ms, rev.MemoryID)
		if cur <= 1.0 {
			t.Fatalf("touch %d crossed the ceiling from above: %v", i+2, cur)
		}
		if cur-1.0 <= resolvable {
			converged = true
			break
		}
		if cur >= prev {
			t.Fatalf("touch %d did not move an out-of-range row down: %v -> %v", i+2, prev, cur)
		}
		prev = cur
	}
	if !converged {
		t.Errorf("an out-of-range row did not converge to within %v of the ceiling in 500 touches; last = %v",
			resolvable, prev)
	}
}

// ── Property 4: equilibrium against decay ───────────────────────────────────

// TestReinforcementDecayEquilibrium is the "what does normal look like" number
// the caller guidance rests on. It drives the production reinforcement SQL and
// the production applyActivationDecay — nothing here reimplements either.
//
// Time is simulated the way decay_test.go already does it: last_accessed_at is
// back-dated and the real decay pass is run, once per hour of the gap, which is
// exactly what the hourly DecayJob does to a row it never re-stamps.
//
// The expected bands are stated as literals. They are wide because the quantity
// is a steady state approached over cycles, not a point — a tight equality here
// would be asserting the simulation's step count, not the mechanism.
func TestReinforcementDecayEquilibrium(t *testing.T) {
	cases := []struct {
		name           string
		gapHours       int
		wantLow        float64
		wantHigh       float64
		interpretation string
	}{
		// Hand-derived from a* = D*k/(1 - D*(1-k)), k=0.2, with D the cumulative
		// decay across the hourly passes in one gap. Bands are ±0.05 around it.
		{"touched_every_2h", 2, 0.92, 1.00, "a memory in the active working set of a long session"},
		{"touched_every_6h", 6, 0.77, 0.87, "a memory used a few times a day"},
		{"touched_every_12h", 12, 0.48, 0.58, "a memory used about twice a day"},
		{"touched_every_24h", 24, 0.14, 0.24, "a memory used once a day"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms, cleanup := newTestStore(t)
			defer cleanup()
			ctx := context.Background()

			rev, err := ms.WriteRevision(ctx, sampleInput("equilibrium."+tc.name))
			if err != nil {
				t.Fatal(err)
			}
			// Start at the floor so the steady state is approached from below;
			// starting at the 1.0 default would let a pass mean "has not fallen
			// yet" rather than "has settled".
			setActivation(t, ms, rev.MemoryID, 0.05)

			// 40 touch/decay cycles is well past settling for every gap here.
			for cycle := 0; cycle < 40; cycle++ {
				touchOnce(t, ms, rev.RevisionID)
				runDecayGap(t, ms, rev.MemoryID, tc.gapHours)
			}

			got := activationOf(t, ms, rev.MemoryID)
			// Logged, not only asserted: this is the number the caller-facing
			// docs quote as "what normal looks like", and `go test -v -run
			// TestReinforcementDecayEquilibrium` is how it gets re-derived
			// rather than carried.
			t.Logf("steady state, touched every %dh (%s): activation = %.4f",
				tc.gapHours, tc.interpretation, got)
			if got < tc.wantLow || got > tc.wantHigh {
				t.Errorf("steady state for %s (%s) = %v, want within [%v, %v]",
					tc.name, tc.interpretation, got, tc.wantLow, tc.wantHigh)
			}
			if got >= 1.0 {
				t.Errorf("steady state %v reached the ceiling; no touch rate should", got)
			}
		})
	}
}

// TestUntouchedMemoryFallsToFloor is the positive control for the equilibrium
// table above: the same simulation with no touches at all must land on the floor.
// Without it, a steady state near the floor would be indistinguishable from a
// simulation that was not reinforcing anything.
func TestUntouchedMemoryFallsToFloor(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("equilibrium.untouched"))
	if err != nil {
		t.Fatal(err)
	}
	// 72 hourly decay passes over a row nothing re-stamps.
	runDecayGap(t, ms, rev.MemoryID, 72)

	if got := activationOf(t, ms, rev.MemoryID); got != 0.05 {
		t.Errorf("an untouched memory after 72 hourly decay passes = %v, want the 0.05 floor", got)
	}
}

// runDecayGap simulates gapHours of the hourly DecayJob against one row.
//
// The job computes elapsed from last_accessed_at and never advances it, so the
// pass at hour i sees elapsed = i. Back-dating last_accessed_at to now-i and
// running the real pass reproduces that exactly. Decay applies to every row, so
// this is only single-row in the sense that these tests have one row.
func runDecayGap(t *testing.T, ms *memory.Store, memoryID string, gapHours int) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC()
	for i := 1; i <= gapHours; i++ {
		stamp := base.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339Nano)
		if _, err := ms.DB().ExecContext(ctx,
			`UPDATE memory_state SET last_accessed_at = ? WHERE memory_id = ?`,
			stamp, memoryID); err != nil {
			t.Fatalf("back-date last_accessed_at: %v", err)
		}
		if err := ms.ExportApplyActivationDecay(ctx); err != nil {
			t.Fatalf("applyActivationDecay: %v", err)
		}
	}
}

// TestDecayCompoundsAcrossPasses is a characterization test, not a change.
//
// applyActivationDecay multiplies the CURRENT stored activation by
// exp(-elapsed*ln2/halfLifeHours) where elapsed is measured from
// last_accessed_at — a baseline the pass never advances. Successive passes over
// an untouched row therefore compound, and the realised half-life is far shorter
// than the nominal 336 hours: the nominal figure is the half-life of a SINGLE
// pass, not of wall-clock time.
//
// This is pre-existing behavior and CW-20260825-0008 explicitly does not touch
// the decay math. It is pinned here because the equilibrium table above is only
// interpretable against it, and because it is the dominant reason activation is
// inert — larger than the missing reinforcement input this ticket adds.
func TestDecayCompoundsAcrossPasses(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("decay.compounding"))
	if err != nil {
		t.Fatal(err)
	}

	// One pass at elapsed = 24h. exp(-24*ln2/336) = 0.95169, from 1.0.
	stamp := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := ms.DB().ExecContext(ctx,
		`UPDATE memory_state SET last_accessed_at = ? WHERE memory_id = ?`,
		stamp, rev.MemoryID); err != nil {
		t.Fatal(err)
	}
	if err := ms.ExportApplyActivationDecay(ctx); err != nil {
		t.Fatal(err)
	}
	afterOne := activationOf(t, ms, rev.MemoryID)
	if math.Abs(afterOne-0.95169) > 1e-4 {
		t.Fatalf("one pass at elapsed=24h: activation = %v, want 0.95169", afterOne)
	}

	// A second pass at the SAME elapsed applies the same factor again, because
	// the baseline did not move. Nothing in the world changed between the two.
	if err := ms.ExportApplyActivationDecay(ctx); err != nil {
		t.Fatal(err)
	}
	afterTwo := activationOf(t, ms, rev.MemoryID)
	if math.Abs(afterTwo-0.90571) > 1e-4 {
		t.Fatalf("a second pass at the same elapsed: activation = %v, want 0.90571 "+
			"(0.95169 squared) — decay compounds per pass, not per hour", afterTwo)
	}
}

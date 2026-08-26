package memory_test

// The reinforcement curve, verified rather than assumed.
//
// The rule is `activation += k * (ceiling - activation)` with k = 0.1 and
// ceiling = 2.0 — the values the code has always had. Written out, one
// reinforcement is:
//
//	a' = a + 0.1*(2.0 - a) = 0.9a + 0.2
//
// Every expected value in this file is a literal, derived by hand from that
// identity and stated here independently of the constants in
// internal/memory/activation.go. That is deliberate: a test that recomputes its
// expectation from the same constants the production code reads asserts only
// that arithmetic is deterministic, and would pass for any pair of values. These
// break when either constant moves, which is what makes them a guard rather than
// a mirror — and is how a ceiling of 1.0 was caught before it merged.
//
// The four properties correspond one-to-one to the planner ruling on
// CW-20260825-0008 (comment 2310): monotonicity, asymptote, out-of-range
// self-correction, and the equilibrium against decay.

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// insertDefaultActivation is memory_state.activation's schema default, declared
// in internal/contextstore/store.go. Several properties below are only
// interesting relative to it — reinforcement has to have headroom above the
// value a memory is born at — so it is named once here and asserted against the
// real schema in TestFreshMemoryMovesUnderTouch.
const insertDefaultActivation = 1.0

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
// memories survives reinforcement, because d/da[a + k(C-a)] = 1-k > 0 for k < 1.
//
// It is checked across the whole range rather than at one pair, including pairs
// that straddle the ceiling, because the derivative argument does not care which
// side of the ceiling a value sits on and the test should not either.
func TestReinforcementPreservesOrdering(t *testing.T) {
	pairs := []struct{ lower, higher float64 }{
		{0.05, 0.051}, // the crowded floor: the smallest gap the corpus has
		{0.05, 1.0},   // floor against a freshly written memory
		{1.0, 1.1},    // a fresh memory against the same memory touched once
		{1.99, 1.999}, // just under the ceiling
		{2.0, 2.5},    // at the ceiling against a row above it
		{2.1, 2.5},    // two rows above the ceiling
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

// TestReinforcementFromFloorFollowsStatedCurve pins the exact sequence for a
// memory starting at the floor. Hand-derived from a' = 0.9a + 0.2:
//
//	0.9(0.05)     + 0.2 = 0.045      + 0.2 = 0.245
//	0.9(0.245)    + 0.2 = 0.2205     + 0.2 = 0.4205
//	0.9(0.4205)   + 0.2 = 0.37845    + 0.2 = 0.57845
//	0.9(0.57845)  + 0.2 = 0.520605   + 0.2 = 0.720605
//	0.9(0.720605) + 0.2 = 0.6485445  + 0.2 = 0.8485445
func TestReinforcementFromFloorFollowsStatedCurve(t *testing.T) {
	want := []float64{0.245, 0.4205, 0.57845, 0.720605, 0.8485445}

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
// clamp: reinforcement gets arbitrarily close to 2.0 and never touches or
// crosses it. A clamp at 2.0 would pass the "<= 2.0" half of this and fail the
// "< 2.0" half, which is the distinction the design turns on.
//
// Iteration count and epsilon are derived from the curve, not guessed. The gap
// to the ceiling shrinks by a factor of (1-k) = 0.9 per touch, so starting at
// the floor:
//
//	gap(n) = (2.0 - 0.05) * 0.9^n = 1.95 * 0.9^n
//
// gap(n) < 1e-6 needs 0.9^n < 5.128e-7, i.e. n > ln(5.128e-7)/ln(0.9) = 137.5,
// so 138 touches suffice and 300 is comfortably past it. 300 is also chosen to
// stay clear of the other end: gap(300) = 1.95 * 0.9^300 ~= 3.6e-14, about 80
// ULPs at 2.0 (one ULP there is 4.44e-16), so the value is still strictly
// representable below the ceiling and the "< 2.0" assertion is testing the
// curve rather than float64's resolution.
func TestReinforcementNeverReachesCeiling(t *testing.T) {
	const touches = 300
	const epsilon = 1e-6

	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.asymptote"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 0.05)

	for i := 0; i < touches; i++ {
		touchOnce(t, ms, rev.RevisionID)
		got := activationOf(t, ms, rev.MemoryID)
		if got >= 2.0 {
			t.Fatalf("touch %d reached or passed the ceiling: activation = %v", i+1, got)
		}
	}
	// And it does get close — a bound nothing approaches would also satisfy
	// the loop above.
	if got := activationOf(t, ms, rev.MemoryID); got < 2.0-epsilon {
		t.Errorf("after %d reinforcements activation = %v, expected to be within %v of 2.0",
			touches, got, epsilon)
	}
}

// TestFreshMemoryMovesUnderTouch is the property that matters most and the one
// whose absence was caught before merge.
//
// A memory is born at memory_state.activation's insert default of 1.0. If the
// ceiling were AT that default, 1.0 would be a fixed point of a + k(C-a) and
// touching a memory written this session would move activation by exactly zero
// — advancing access_count and last_accessed_at while the number the ranker
// reads stayed put. That is precisely the case tesseract_touch exists for: an
// agent recalls something written this session, reasons with it, and reports
// that it mattered.
//
// So: a fresh memory must move, and the ceiling must sit above the default.
// Hand-derived from a' = 0.9a + 0.2, starting at 1.0:
//
//	0.9(1.0)  + 0.2 = 0.9   + 0.2 = 1.1
//	0.9(1.1)  + 0.2 = 0.99  + 0.2 = 1.19
//	0.9(1.19) + 0.2 = 1.071 + 0.2 = 1.271
func TestFreshMemoryMovesUnderTouch(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.fresh_memory"))
	if err != nil {
		t.Fatal(err)
	}

	// Tied to the real schema. If the insert default in
	// internal/contextstore/store.go ever changes, this says so rather than
	// silently passing against a different premise.
	start := activationOf(t, ms, rev.MemoryID)
	if start != insertDefaultActivation {
		t.Fatalf("a freshly written memory starts at activation %v, want %v "+
			"(memory_state.activation DEFAULT, internal/contextstore/store.go); "+
			"the headroom this test checks is relative to that default", start, insertDefaultActivation)
	}

	for i, expected := range []float64{1.1, 1.19, 1.271} {
		touchOnce(t, ms, rev.RevisionID)
		got := activationOf(t, ms, rev.MemoryID)
		if math.Abs(got-expected) > 1e-9 {
			t.Fatalf("touch %d on a fresh memory: activation = %v, want %v", i+1, got, expected)
		}
		if got <= start {
			t.Fatalf("touch %d did not move a fresh memory: %v -> %v — "+
				"the ceiling must sit above the insert default or touch is a no-op "+
				"for exactly the memories it exists to reinforce", i+1, start, got)
		}
	}
}

// ── Property 3: out-of-range rows self-correct ──────────────────────────────

// TestTouchPullsOutOfRangeRowsTowardCeiling makes the negative increment above
// the ceiling deliberate and tested rather than an accident of the formula.
//
// Nothing in the corpus or in any write path produces a row above 2.0 — measured
// at d03f551, the live maximum was 1.0 and no row exceeded it. The seed here is
// therefore synthetic, and the case is a property of the curve rather than a
// migration problem: if such a row ever appears, touch and decay both walk it
// back rather than leaving it stranded above the range.
//
// Decision, unchanged from the earlier revision of this ticket: no migration.
// Clamping would collapse distinct out-of-range values to a single number — the
// flattening a hard cap was rejected for — and would be an irreversible write to
// correct something that corrects itself. Ordering among such rows survives the
// walk down; see TestReinforcementPreservesOrdering.
//
// Hand-derived from a' = 0.9a + 0.2, starting at the synthetic 2.5:
//
//	0.9(2.5)  + 0.2 = 2.25  + 0.2 = 2.45
//	0.9(2.45) + 0.2 = 2.205 + 0.2 = 2.405
func TestTouchPullsOutOfRangeRowsTowardCeiling(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("curve.out_of_range"))
	if err != nil {
		t.Fatal(err)
	}
	setActivation(t, ms, rev.MemoryID, 2.5)

	touchOnce(t, ms, rev.RevisionID)
	got := activationOf(t, ms, rev.MemoryID)
	if math.Abs(got-2.45) > 1e-9 {
		t.Fatalf("one touch on an out-of-range row: activation = %v, want 2.45", got)
	}
	if got >= 2.5 {
		t.Errorf("touch did not reduce an out-of-range row: %v -> %v", 2.5, got)
	}

	touchOnce(t, ms, rev.RevisionID)
	if got = activationOf(t, ms, rev.MemoryID); math.Abs(got-2.405) > 1e-9 {
		t.Fatalf("two touches on an out-of-range row: activation = %v, want 2.405", got)
	}

	// It converges toward the ceiling from above and never crosses it — the
	// mirror image of the from-below case, and the reason no clamp is needed.
	//
	// "Strictly decreasing" is asserted only while the gap to the ceiling is
	// wider than float64 can resolve. Past that the row parks a few ULPs above
	// 2.0 and stops moving, which is convergence, not a stalled walk — asserting
	// strict decrease forever would be asserting that float64 has no epsilon.
	const resolvable = 1e-12
	prev := got
	converged := false
	for i := 0; i < 500; i++ {
		touchOnce(t, ms, rev.RevisionID)
		cur := activationOf(t, ms, rev.MemoryID)
		if cur <= 2.0 {
			t.Fatalf("touch %d crossed the ceiling from above: %v", i+3, cur)
		}
		if cur-2.0 <= resolvable {
			converged = true
			break
		}
		if cur >= prev {
			t.Fatalf("touch %d did not move an out-of-range row down: %v -> %v", i+3, prev, cur)
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
// The closed form is the independent cross-check. One touch-and-decay cycle is
// a' = D*(a*(1-k) + k*C), so the steady state solves
//
//	a* = D*k*C / (1 - D*(1-k))    with k=0.1, C=2.0  =>  0.2D / (1 - 0.9D)
//
// where D = exp(-(ln2/336) * n(n+1)/2) is the cumulative decay across the n
// hourly passes in one gap, since the pass at hour i sees elapsed = i. Working,
// with ln2/336 = 0.00206265:
//
//	n=2:  sum=3    D=e^-0.0061880=0.993831  a*=0.1987662/0.1055520=1.8831
//	n=6:  sum=21   D=e^-0.0433157=0.957609  a*=0.1915218/0.1381518=1.3863
//	n=12: sum=78   D=e^-0.1608867=0.851393  a*=0.1702787/0.2337460=0.7285
//	n=24: sum=300  D=e^-0.6187950=0.538592  a*=0.1077183/0.5152676=0.2091
//
// The bands below are those figures +/-0.02, stated as literals.
//
// A steady state is approached, not arrived at, so the band has to cover the
// simulation's residual. One cycle contracts the remaining distance by D*(1-k),
// so after c cycles the residual is (a* - 0.05) * (D*(1-k))^c. At the 2h gap
// that factor is 0.8944, and 40 cycles leaves 2.1e-2 — enough to sit outside a
// +/-0.02 band and be mistaken for a mechanism difference. equilibriumCycles is
// set so the worst residual is ~1e-7:
//
//	gap=2h   rate=0.8944  residual(150) = 9.9e-08
//	gap=6h   rate=0.8618  residual(150) = 2.8e-10
//	gap=12h  rate=0.7662  residual(150) = 3.1e-18
//	gap=24h  rate=0.4847  residual(150) = 1.1e-48
//
// With settling that far below the band, a failure here is the mechanism and not
// the step count — which is what makes the closed form an independent check
// rather than something a wide band absorbs. Measured against production code at
// 150 cycles: 1.8831, 1.3863, 0.7284, 0.2099.
//
// The first three match the closed form to four places. The 24h case runs
// ~0.0009 HIGH, and that gap is real rather than noise: applyActivationDecay
// skips a pass whose change is under 0.001, and near 0.21 the passes at elapsed
// 1h and 2h change it by 0.00043 and 0.00087, so they are skipped. Less decay is
// applied than the closed form assumes, so the row settles slightly higher. The
// closed form does not model that threshold; the code has it, and the code is
// what is being measured.
const equilibriumCycles = 150

func TestReinforcementDecayEquilibrium(t *testing.T) {
	cases := []struct {
		name           string
		gapHours       int
		wantLow        float64
		wantHigh       float64
		interpretation string
	}{
		{"touched_every_2h", 2, 1.86, 1.90, "a memory in the active working set of a long session"},
		{"touched_every_6h", 6, 1.37, 1.41, "a memory used a few times a day"},
		{"touched_every_12h", 12, 0.71, 0.75, "a memory used about twice a day"},
		{"touched_every_24h", 24, 0.19, 0.23, "a memory used once a day"},
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

			for cycle := 0; cycle < equilibriumCycles; cycle++ {
				touchOnce(t, ms, rev.RevisionID)
				runDecayGap(t, ms, rev.MemoryID, tc.gapHours)
			}

			got := activationOf(t, ms, rev.MemoryID)
			// Logged, not only asserted: this is the number the caller-facing
			// docs describe as "what normal looks like", and `go test -v -run
			// TestReinforcementDecayEquilibrium` is how it gets re-derived
			// rather than carried.
			t.Logf("steady state, touched every %dh (%s): activation = %.4f",
				tc.gapHours, tc.interpretation, got)
			if got < tc.wantLow || got > tc.wantHigh {
				t.Errorf("steady state for %s (%s) = %v, want within [%v, %v]",
					tc.name, tc.interpretation, got, tc.wantLow, tc.wantHigh)
			}
			if got >= 2.0 {
				t.Errorf("steady state %v reached the ceiling; no touch rate should", got)
			}
			// The headroom above the insert default is what makes reinforcement
			// mean anything: a memory used through the day must be able to
			// outrank one merely written today. This is the ranking-level
			// counterpart of TestFreshMemoryMovesUnderTouch.
			if tc.gapHours <= 6 && got <= insertDefaultActivation {
				t.Errorf("steady state at a %dh touch interval = %v, which does not clear the "+
					"insert default of %v — a frequently used memory would rank no higher than a "+
					"brand-new one", tc.gapHours, got, insertDefaultActivation)
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
// last_accessed_at — a baseline the pass never advances (decay.go writes only
// activation). Successive passes over an untouched row therefore compound, and
// the realized half-life is far shorter than the nominal 336 hours: the nominal
// figure is the half-life of a SINGLE pass, not of wall-clock time.
//
// That is tracked as CW-20260826-0001 and is deliberately NOT fixed here. This
// test pins the behavior because the equilibrium table above is only
// interpretable against it, and because the reinforcement rate must not be tuned
// against activation levels this bug is setting.
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

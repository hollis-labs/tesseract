package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// stubResolver returns a canned outcome per locator and counts calls, so a
// test can assert both what was concluded and how many times the world was
// touched.
//
// BuildVerificationPlan resolves distinct targets on concurrent goroutines, so
// Resolve is called from several at once. byLocator is written once at
// construction and only read afterwards; calls is mutated on every call, so mu
// guards it. Reads go through callCount rather than touching the map directly,
// which keeps the counter safe by construction instead of by an argument about
// when the goroutines have finished.
type stubResolver struct {
	byLocator map[string]memory.PointerOutcome

	mu    sync.Mutex
	calls map[string]int
}

func newStubResolver(m map[string]memory.PointerOutcome) *stubResolver {
	return &stubResolver{byLocator: m, calls: map[string]int{}}
}

func (s *stubResolver) Resolve(_ context.Context, scheme, locator string) (memory.PointerOutcome, string) {
	s.mu.Lock()
	s.calls[locator]++
	s.mu.Unlock()
	if o, ok := s.byLocator[locator]; ok {
		return o, "stub_" + string(o)
	}
	return memory.OutcomeUnverifiable, "stub_unknown:" + scheme
}

// callCount reports how many times locator was resolved.
func (s *stubResolver) callCount(locator string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[locator]
}

func TestBuildVerificationPlan_ScopeSchemesAndSkips(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	fileRev := seedKnowledge(t, ks, "v.file", "file", "/tmp/v-file")
	seedKnowledge(t, ks, "v.https", "https", "https://example.test/v")
	seedKnowledge(t, ks, "v.nil", memory.SchemeNil, "no-source")
	seedKnowledge(t, ks, "v.conduit", "conduit", "memory://user/x/memory/decisions.y:01ABC")

	stub := newStubResolver(map[string]memory.PointerOutcome{"/tmp/v-file": memory.OutcomeResolved})
	plan, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Scope: memory.ScopeHeads, Schemes: []string{memory.SchemeFile}, Resolver: stub,
	})
	if err != nil {
		t.Fatalf("BuildVerificationPlan: %v", err)
	}

	if plan.Candidates != 4 {
		t.Errorf("candidates = %d, want 4 (every head carrying a pointer)", plan.Candidates)
	}
	if len(plan.Rows) != 1 || plan.Rows[0].RevisionID != fileRev.RevisionID {
		t.Fatalf("rows = %+v, want exactly the file pointer", plan.Rows)
	}
	if plan.Rows[0].Outcome != memory.OutcomeResolved {
		t.Errorf("outcome = %q, want %q", plan.Rows[0].Outcome, memory.OutcomeResolved)
	}

	// Everything not checked is reported, by reason. A run that quietly
	// narrowed its own scope must not look like a clean run.
	skipTotals := map[memory.VerificationSkipReason]int{}
	for _, s := range plan.Skipped {
		skipTotals[s.Kind] += s.Count
	}
	if skipTotals[memory.SkipNoExternalSource] != 1 {
		t.Errorf("no_external_source skips = %d, want 1", skipTotals[memory.SkipNoExternalSource])
	}
	if skipTotals[memory.SkipSchemeNotSelected] != 2 {
		t.Errorf("scheme_not_selected skips = %d, want 2 (https + conduit)", skipTotals[memory.SkipSchemeNotSelected])
	}
	if plan.NetworkEnabled {
		t.Error("NetworkEnabled is true for a file-only run")
	}
}

// TestBuildVerificationPlan_UnsupportedSchemeIsRecordedWhenAttempted proves the
// non-resolvable scheme is expressible rather than silent, once a run actually
// attempts it. The default resolver is used here, not the stub, because the
// point is the real dispatch.
func TestBuildVerificationPlan_UnsupportedSchemeIsRecordedWhenAttempted(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	seedKnowledge(t, ks, "u.conduit", "conduit", "memory://user/x/memory/decisions.y:01ABC")

	// A scheme with no resolver cannot be requested through the CLI's flag
	// validation, so drive BuildVerificationPlan directly to reach the
	// resolver's default branch.
	plan, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Schemes:  []string{"conduit"},
		Resolver: memory.NewPointerResolver(time.Second, false),
	})
	if err != nil {
		t.Fatalf("BuildVerificationPlan: %v", err)
	}
	if len(plan.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(plan.Rows))
	}
	if plan.Rows[0].Outcome != memory.OutcomeUnverifiable {
		t.Errorf("outcome = %q, want %q", plan.Rows[0].Outcome, memory.OutcomeUnverifiable)
	}
	if plan.Rows[0].Detail != "unsupported_scheme:conduit" {
		t.Errorf("detail = %q, want it to name the scheme", plan.Rows[0].Detail)
	}
}

// TestBuildVerificationPlan_DistinctTargetsResolvedOnce proves the dedup: two
// revisions citing one locator resolve it once and cannot disagree.
func TestBuildVerificationPlan_DistinctTargetsResolvedOnce(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	seedKnowledge(t, ks, "d.one", "file", "/tmp/shared-target")
	seedKnowledge(t, ks, "d.two", "file", "/tmp/shared-target")
	seedKnowledge(t, ks, "d.three", "file", "/tmp/other-target")

	stub := newStubResolver(map[string]memory.PointerOutcome{
		"/tmp/shared-target": memory.OutcomeUnresolvable,
		"/tmp/other-target":  memory.OutcomeResolved,
	})
	plan, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Schemes: []string{memory.SchemeFile}, Resolver: stub, Concurrency: 4,
	})
	if err != nil {
		t.Fatalf("BuildVerificationPlan: %v", err)
	}
	if len(plan.Rows) != 3 {
		t.Errorf("rows = %d, want 3 (one per revision)", len(plan.Rows))
	}
	if plan.DistinctTargets != 2 {
		t.Errorf("distinct targets = %d, want 2", plan.DistinctTargets)
	}
	if n := stub.callCount("/tmp/shared-target"); n != 1 {
		t.Errorf("shared target resolved %d times, want 1 — two revisions naming one file must not disagree", n)
	}
}

func TestBuildVerificationPlan_RecheckAfterSkipsRecentAndBoundsLogGrowth(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	now := time.Now().UTC()

	fresh := seedKnowledge(t, ks, "rc.fresh", "file", "/tmp/rc-fresh")
	stale := seedKnowledge(t, ks, "rc.stale", "file", "/tmp/rc-stale")
	recordObservation(t, db, fresh.RevisionID, "file", "/tmp/rc-fresh", memory.OutcomeResolved, "stat_ok", now.Add(-2*time.Hour))
	recordObservation(t, db, stale.RevisionID, "file", "/tmp/rc-stale", memory.OutcomeResolved, "stat_ok", now.Add(-200*time.Hour))

	stub := newStubResolver(map[string]memory.PointerOutcome{
		"/tmp/rc-fresh": memory.OutcomeResolved,
		"/tmp/rc-stale": memory.OutcomeResolved,
	})
	plan, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Schemes: []string{memory.SchemeFile}, RecheckAfter: 168 * time.Hour, Resolver: stub, Now: now,
	})
	if err != nil {
		t.Fatalf("BuildVerificationPlan: %v", err)
	}
	if len(plan.Rows) != 1 || plan.Rows[0].RevisionID != stale.RevisionID {
		t.Fatalf("rows = %+v, want only the stale one", plan.Rows)
	}
	// The check itself was skipped, which is what bounds growth: the row is
	// not written because the work was not done, not because it was deduped.
	if n := stub.callCount("/tmp/rc-fresh"); n != 0 {
		t.Errorf("recently-checked pointer was resolved %d time(s); recheck-after must skip the WORK, not just the row", n)
	}
}

func TestBuildVerificationPlan_ScopeAllIncludesHistory(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	seedKnowledge(t, ks, "h.entry", "file", "/tmp/h-v1")
	seedKnowledge(t, ks, "h.entry", "file", "/tmp/h-v2") // same key -> new head, v1 becomes history

	stub := newStubResolver(map[string]memory.PointerOutcome{
		"/tmp/h-v1": memory.OutcomeUnresolvable,
		"/tmp/h-v2": memory.OutcomeResolved,
	})
	heads, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Scope: memory.ScopeHeads, Schemes: []string{memory.SchemeFile}, Resolver: stub,
	})
	if err != nil {
		t.Fatalf("heads plan: %v", err)
	}
	if len(heads.Rows) != 1 {
		t.Errorf("heads scope produced %d row(s), want 1", len(heads.Rows))
	}

	all, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Scope: memory.ScopeAll, Schemes: []string{memory.SchemeFile}, Resolver: newStubResolver(stub.byLocator),
	})
	if err != nil {
		t.Fatalf("all plan: %v", err)
	}
	if len(all.Rows) != 2 {
		t.Errorf("all scope produced %d row(s), want 2", len(all.Rows))
	}
}

// --- digest -------------------------------------------------------------

func TestVerificationPlanDigest_StableAndSensitive(t *testing.T) {
	base := memory.VerificationPlan{Rows: []memory.PointerObservation{
		{RevisionID: "r1", Scheme: "file", Locator: "/a", Outcome: memory.OutcomeResolved, Detail: "stat_ok", CheckedAt: time.Unix(0, 0)},
		{RevisionID: "r2", Scheme: "file", Locator: "/b", Outcome: memory.OutcomeUnresolvable, Detail: "not_found", CheckedAt: time.Unix(0, 0)},
	}}

	// Row order must not matter.
	reordered := memory.VerificationPlan{Rows: []memory.PointerObservation{base.Rows[1], base.Rows[0]}}
	if base.Digest() != reordered.Digest() {
		t.Error("digest depends on row order")
	}

	// The run's own clock must not matter, or --expect-digest is useless.
	reclocked := memory.VerificationPlan{Rows: []memory.PointerObservation{
		{RevisionID: "r1", Scheme: "file", Locator: "/a", Outcome: memory.OutcomeResolved, Detail: "stat_ok", CheckedAt: time.Now()},
		{RevisionID: "r2", Scheme: "file", Locator: "/b", Outcome: memory.OutcomeUnresolvable, Detail: "not_found", CheckedAt: time.Now()},
	}}
	if base.Digest() != reclocked.Digest() {
		t.Error("digest covers checked_at; every run would then have a unique digest and the flag could never match")
	}

	// Outcome and detail must both move it: they are what would be written.
	changedOutcome := memory.VerificationPlan{Rows: []memory.PointerObservation{
		base.Rows[0],
		{RevisionID: "r2", Scheme: "file", Locator: "/b", Outcome: memory.OutcomeUnverifiable, Detail: "not_found"},
	}}
	if base.Digest() == changedOutcome.Digest() {
		t.Error("digest ignores outcome")
	}
	changedDetail := memory.VerificationPlan{Rows: []memory.PointerObservation{
		base.Rows[0],
		{RevisionID: "r2", Scheme: "file", Locator: "/b", Outcome: memory.OutcomeUnresolvable, Detail: "http_410"},
	}}
	if base.Digest() == changedDetail.Digest() {
		t.Error("digest ignores detail; a rate limit and an unknown scheme would approve as one another")
	}
}

// --- apply --------------------------------------------------------------

func TestApplyVerificationPlan_AppendsAndRefusesBadOutcome(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()
	rev := seedKnowledge(t, ks, "a.entry", "file", "/tmp/a-entry")

	good := memory.VerificationPlan{Rows: []memory.PointerObservation{{
		RevisionID: rev.RevisionID, Scheme: "file", Locator: "/tmp/a-entry",
		Outcome: memory.OutcomeResolved, Detail: "stat_ok", CheckedAt: time.Now().UTC(),
	}}}
	n, err := memory.ApplyVerificationPlan(ctx, db, good)
	if err != nil || n != 1 {
		t.Fatalf("apply: n=%d err=%v", n, err)
	}
	// Applying again appends rather than replacing — no upsert anywhere.
	if _, err = memory.ApplyVerificationPlan(ctx, db, good); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pointer_verifications`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("log holds %d row(s) after two applies, want 2", count)
	}

	bad := memory.VerificationPlan{Rows: []memory.PointerObservation{{
		RevisionID: rev.RevisionID, Scheme: "file", Locator: "/x", Outcome: "probably_fine", CheckedAt: time.Now().UTC(),
	}}}
	if _, err = memory.ApplyVerificationPlan(ctx, db, bad); err == nil {
		t.Error("apply accepted an outcome outside the vocabulary")
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pointer_verifications`).Scan(&count); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if count != 2 {
		t.Errorf("a refused apply wrote %d row(s); a refusal must leave the log untouched", count-2)
	}
}

// RFC3339Nano removes trailing zeroes, so textual comparison reverses the
// order when one fractional timestamp is a prefix of the other. Both cases
// below are real encodings emitted by time.Time.Format(time.RFC3339Nano).
func TestCountObservationsSinceComparesRFC3339NanoTimestampsChronologically(t *testing.T) {
	boundary := time.Date(2026, 9, 4, 13, 34, 55, 92_340_000, time.UTC)

	for _, tc := range []struct {
		name          string
		checkedAt     time.Time
		wantSinceRows int
	}{
		{
			name:          "later timestamp extends boundary precision",
			checkedAt:     time.Date(2026, 9, 4, 13, 34, 55, 92_342_000, time.UTC),
			wantSinceRows: 1,
		},
		{
			name:          "earlier timestamp is boundary prefix",
			checkedAt:     boundary,
			wantSinceRows: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, ks := newHealthFixture(t)
			db := ms.DB()
			rev := seedKnowledge(t, ks, "count.entry", "file", "/tmp/count")
			recordObservation(t, db, rev.RevisionID, "file", "/tmp/count", memory.OutcomeResolved, "stat_ok", tc.checkedAt)

			from := boundary
			if tc.wantSinceRows == 0 {
				from = boundary.Add(time.Nanosecond)
			}
			got, err := memory.CountObservationsSince(context.Background(), db, from)
			if err != nil {
				t.Fatalf("CountObservationsSince: %v", err)
			}
			if got != tc.wantSinceRows {
				t.Fatalf("CountObservationsSince(%s) = %d for checked_at %s, want %d",
					from.Format(time.RFC3339Nano), got, tc.checkedAt.Format(time.RFC3339Nano), tc.wantSinceRows)
			}
		})
	}
}

// TestVerification_EndToEndAgainstRealFilesystem drives the whole pipeline
// with the real resolver over a temp tree, so plan → apply → surface is proved
// without a stub anywhere in the path.
func TestVerification_EndToEndAgainstRealFilesystem(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	dir := t.TempDir()
	alive := filepath.Join(dir, "alive.md")
	if err := os.WriteFile(alive, []byte("here"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gone := filepath.Join(dir, "gone.md")

	aliveRev := seedKnowledge(t, ks, "e2e.alive", "file", alive)
	goneRev := seedKnowledge(t, ks, "e2e.gone", "file", gone)

	plan, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{Schemes: []string{memory.SchemeFile}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	counts := plan.OutcomeCounts()
	if counts[memory.OutcomeResolved] != 1 || counts[memory.OutcomeUnresolvable] != 1 {
		t.Fatalf("plan counts %+v, want one resolved and one unresolvable", counts)
	}

	if _, err = memory.ApplyVerificationPlan(ctx, db, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	dist, err := memory.PointerHealthDistribution(ctx, db, memory.ScopeHeads)
	if err != nil {
		t.Fatalf("distribution: %v", err)
	}
	if dist[string(memory.PointerHealthResolved)] != 1 || dist[string(memory.PointerHealthUnresolvable)] != 1 {
		t.Errorf("distribution %+v, want one of each", dist)
	}

	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{healthNamespace}, Ranking: memory.RankingChronological, Limit: 10,
		Filters: memory.RecallFilters{PointerHealth: []string{string(memory.PointerHealthUnresolvable)}},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) != 1 || results[0].Revision.RevisionID != goneRev.RevisionID {
		t.Fatalf("filtered recall returned %d row(s); want exactly the missing file's revision", len(results))
	}
	_ = aliveRev
}

// TestPointerHealthDistribution_CountsSumToScannedPopulation checks the
// aggregate reports on everything rather than quietly dropping a bucket.
func TestPointerHealthDistribution_CountsSumToScannedPopulation(t *testing.T) {
	ms, ks := newHealthFixture(t)
	db := ms.DB()
	ctx := context.Background()

	seedKnowledge(t, ks, "dist.a", "file", "/tmp/dist-a")
	seedKnowledge(t, ks, "dist.b", memory.SchemeNil, "none")
	seedKnowledge(t, ks, "dist.c", "conduit", "memory://x")

	dist, err := memory.PointerHealthDistribution(ctx, db, memory.ScopeHeads)
	if err != nil {
		t.Fatalf("distribution: %v", err)
	}
	total := 0
	for _, n := range dist {
		total += n
	}
	var heads int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memory_revisions r JOIN memory_state s ON s.current_revision = r.revision_id
WHERE r.domain = 'knowledge'`).Scan(&heads); err != nil {
		t.Fatalf("count heads: %v", err)
	}
	if total != heads {
		t.Errorf("distribution sums to %d over %d knowledge heads — a bucket is being dropped", total, heads)
	}
}

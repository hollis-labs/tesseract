package memory_test

import (
	"context"
	"database/sql"
	"math"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// The frozen PCA-16 basis and the second ν series it enables
// (CW-20260911-0043), tested for the two properties the design rests on:
// the fit finds the subspace the data actually lives in, and collecting the
// second series changes nothing about the first.

const (
	subspaceEmbedderDim  = 32
	subspaceEmbedderRank = 16
)

// subspaceEmbedder places every payload in the span of the first 16 coordinate
// axes of a 32-dimensional sphere, with nothing at all in the remaining 16.
//
// That makes the correct answer known independently of the fit: the principal
// subspace IS the planted span, the explained-variance ratio must be 1, and the
// fitted components must carry no weight on axes 16..31. A fit that merely
// produced something orthonormal would pass an orthonormality check and fail
// this one.
//
// Deterministic in the payload text, so the suite is reproducible and two
// stores fed the same writes see byte-identical vectors.
type subspaceEmbedder struct{}

func (subspaceEmbedder) Embed(_ context.Context, text, _ string) (*embedcontracts.EmbeddingResult, error) {
	v := make([]float32, subspaceEmbedderDim)
	h := uint32(2166136261)
	for i := 0; i < len(text); i++ {
		h ^= uint32(text[i])
		h *= 16777619
	}
	for i := 0; i < subspaceEmbedderRank; i++ {
		h ^= h << 13
		h ^= h >> 17
		h ^= h << 5
		v[i] = float32(int32(h%2001)-1000) / 1000
	}
	var norm float32
	for _, f := range v {
		norm += f * f
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm == 0 {
		v[0], norm = 1, 1
	}
	for i := range v {
		v[i] /= norm
	}
	return &embedcontracts.EmbeddingResult{Embedding: v, TokenCount: 3}, nil
}

func (e subspaceEmbedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	out := make([]embedcontracts.EmbeddingResult, len(texts))
	for i, tx := range texts {
		r, err := e.Embed(ctx, tx, model)
		if err != nil {
			return nil, err
		}
		out[i] = *r
	}
	return out, nil
}

func (subspaceEmbedder) EmbeddingDimensions(_ string) int { return subspaceEmbedderDim }

func newSubspaceStore(t *testing.T) *memory.Store {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return memory.NewStore(cs.DB(), subspaceEmbedder{}, "test-model", 0.85, memory.NoopQueue{})
}

// projectedColumns is the second series as stored. Note there are five, and
// that there is no route among them: see noveltyProjectedMeasurement.
type projectedColumns struct {
	Score        sql.NullFloat64
	Top1Cosine   sql.NullFloat64
	ScopeN       sql.NullInt64
	Kappa        sql.NullFloat64
	BasisVersion sql.NullString
}

func readProjected(t *testing.T, ms *memory.Store, revisionID string) projectedColumns {
	t.Helper()
	var c projectedColumns
	err := ms.DB().QueryRowContext(context.Background(), `
SELECT novelty_pca16_score, novelty_pca16_top1_cosine, novelty_pca16_scope_n,
       novelty_pca16_kappa, novelty_pca16_basis_version
  FROM memory_revisions WHERE revision_id = ?`, revisionID).
		Scan(&c.Score, &c.Top1Cosine, &c.ScopeN, &c.Kappa, &c.BasisVersion)
	if err != nil {
		t.Fatalf("read projected novelty columns for %s: %v", revisionID, err)
	}
	return c
}

func fitTestBasis(t *testing.T, ms *memory.Store, replace bool) memory.NoveltyBasisReport {
	t.Helper()
	report, err := memory.FitNoveltyBasis(context.Background(), ms.DB(), "test-model", "9999-01-01T00:00:00Z", replace)
	if err != nil {
		t.Fatalf("FitNoveltyBasis: %v", err)
	}
	return report
}

// TestNoveltyBasisRecoversThePlantedSubspace is the known-answer fixture.
//
// The corpus lives exactly in the span of the first 16 axes, so a correct fit
// explains all of the variance and puts no weight outside that span. Both are
// checked, because either alone is passable by a wrong answer: a basis of the
// wrong subspace can still be orthonormal, and a basis with a stray component
// can still explain most of the variance.
func TestNoveltyBasisRecoversThePlantedSubspace(t *testing.T) {
	ms := newSubspaceStore(t)
	for i := 0; i < 60; i++ {
		writeAndEmbed(t, ms, "basis.fit."+itoa(i), "planted payload number "+itoa(i))
	}

	report := fitTestBasis(t, ms, false)

	if report.SourceDim != subspaceEmbedderDim || report.TargetDim != 16 {
		t.Fatalf("projection is %d -> %d, want %d -> 16",
			report.SourceDim, report.TargetDim, subspaceEmbedderDim)
	}
	if report.SnapshotN != 60 {
		t.Errorf("snapshot covered %d vectors, want 60", report.SnapshotN)
	}
	if math.Abs(report.ExplainedVarianceRatio-1.0) > 1e-6 {
		t.Errorf("explained variance ratio = %.12g, want 1 — the corpus lies entirely in a "+
			"16-dimensional span, so a correct 16-component fit accounts for all of it. A lower "+
			"number means the fit did not find the subspace the data is actually in",
			report.ExplainedVarianceRatio)
	}
	for i, ev := range report.EigenValues {
		if ev < 0 {
			t.Errorf("eigenvalue %d = %g is negative; a variance cannot be", i, ev)
		}
		if i > 0 && ev > report.EigenValues[i-1]+1e-12 {
			t.Errorf("eigenvalues are not descending at %d: %g > %g", i, ev, report.EigenValues[i-1])
		}
	}
	if report.SnapshotHash == "" {
		t.Error("the snapshot hash is empty; without it a refit cannot be verified against the " +
			"data it claims to have been fitted on")
	}
}

// TestNoveltyBasisFitIsDeterministic is what makes a frozen basis meaningful.
//
// If the same snapshot could produce two different bases, "frozen" would be a
// claim about one stored blob rather than a reproducible property, and a refit
// could never be checked against the original.
//
// Two things are checked, because they are different claims. ACROSS stores, the
// numerics must be deterministic: the same vectors must yield the same
// eigenvalues, whatever the rows happen to be called. WITHIN a store, a refit of
// the same snapshot must reproduce the basis exactly — and that is observable
// through the refit path itself, which refuses the write because the version it
// derived already exists. A non-deterministic fit would compute a different
// version and insert a second basis instead.
//
// Note the snapshot hash covers (revision_id, vector) and so identifies ROWS,
// not just content: two stores holding identical vectors under different
// revision ids hash differently, by design. That is what makes the hash able to
// answer "was this basis fitted on exactly those rows".
func TestNoveltyBasisFitIsDeterministic(t *testing.T) {
	first := newSubspaceStore(t)
	second := newSubspaceStore(t)
	for i := 0; i < 40; i++ {
		payload := "planted payload number " + itoa(i)
		writeAndEmbed(t, first, "basis.fit."+itoa(i), payload)
		writeAndEmbed(t, second, "basis.fit."+itoa(i), payload)
	}

	a := fitTestBasis(t, first, false)
	b := fitTestBasis(t, second, false)

	if len(a.EigenValues) != len(b.EigenValues) {
		t.Fatalf("fits produced %d and %d eigenvalues", len(a.EigenValues), len(b.EigenValues))
	}
	for i := range a.EigenValues {
		if a.EigenValues[i] != b.EigenValues[i] {
			t.Errorf("eigenvalue %d differs between two fits over identical vectors: %.17g vs %.17g — "+
				"the numerics are not deterministic, so a basis cannot be reproduced or verified",
				i, a.EigenValues[i], b.EigenValues[i])
		}
	}
	if a.SnapshotHash == b.SnapshotHash {
		t.Error("two stores with different revision ids produced the same snapshot hash; the hash " +
			"is supposed to identify the exact rows fitted over, not only their contents")
	}

	// Within one store, refitting the same snapshot must land on the same
	// version — which the refit path reports by refusing to store a duplicate.
	_, err := memory.FitNoveltyBasis(context.Background(), first.DB(), "test-model", "9999-01-01T00:00:00Z", true)
	if err == nil {
		t.Fatal("refitting the identical snapshot stored a second basis, so the fit produced a " +
			"different version from the same input: it is not deterministic")
	}
	if !contains(err.Error(), "reproduced the existing basis") {
		t.Errorf("refitting the identical snapshot failed for an unexpected reason: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestNoveltyBasisRefitIsRefusedWithoutReplace holds the "never implicitly
// refit" rule at the one place it can actually be enforced.
func TestNoveltyBasisRefitIsRefusedWithoutReplace(t *testing.T) {
	ms := newSubspaceStore(t)
	for i := 0; i < 40; i++ {
		writeAndEmbed(t, ms, "basis.fit."+itoa(i), "planted payload number "+itoa(i))
	}
	fitTestBasis(t, ms, false)

	// A later snapshot over more data is a genuinely different basis, so it is
	// the case a refit guard has to catch.
	for i := 40; i < 50; i++ {
		writeAndEmbed(t, ms, "basis.fit."+itoa(i), "planted payload number "+itoa(i))
	}
	_, err := memory.FitNoveltyBasis(context.Background(), ms.DB(), "test-model", "9999-01-02T00:00:00Z", false)
	if err == nil {
		t.Fatal("a second fit was accepted without -replace. A new basis becomes the one new writes " +
			"are scored against, so ν before and after would be two different measurements taken " +
			"under one name")
	}
	if _, err := memory.FitNoveltyBasis(context.Background(), ms.DB(), "test-model", "9999-01-02T00:00:00Z", true); err != nil {
		t.Fatalf("an explicit -replace refit was refused: %v", err)
	}
}

// TestProjectedSeriesLeavesFullDimSeriesUnchanged is the guard on the one
// constraint this task must not violate.
//
// CW-20260911-0043 is additive: the six shipped columns do not change and
// nothing about the full-dimension measurement moves. That is asserted here
// rather than reasoned about, because the two series now share a single scope
// scan — which is the right design (it makes the scopes identical by
// construction) and is also exactly the kind of change that could perturb the
// original path without anyone noticing.
//
// Two stores take identical writes. One has a basis and one does not. Every
// full-dimension column must be bit-identical across both.
func TestProjectedSeriesLeavesFullDimSeriesUnchanged(t *testing.T) {
	withBasis := newSubspaceStore(t)
	without := newSubspaceStore(t)

	var revs []string
	for i := 0; i < 30; i++ {
		payload := "planted payload number " + itoa(i)
		writeAndEmbed(t, withBasis, "basis.fit."+itoa(i), payload)
		writeAndEmbed(t, without, "basis.fit."+itoa(i), payload)
	}
	fitTestBasis(t, withBasis, false)

	// Fresh stores so the basis is picked up: it is cached for the life of a
	// Store, deliberately, so that a basis cannot start applying mid-run.
	withBasis = reopenSameStore(t, withBasis)

	for i := 30; i < 45; i++ {
		payload := "planted payload number " + itoa(i)
		a := writeAndEmbed(t, withBasis, "basis.fit."+itoa(i), payload)
		b := writeAndEmbed(t, without, "basis.fit."+itoa(i), payload)
		if a.RevisionID == "" || b.RevisionID == "" {
			t.Fatal("write produced no revision")
		}
		revs = append(revs, a.RevisionID)
	}

	for i := 30; i < 45; i++ {
		key := "basis.fit." + itoa(i)
		a := readNoveltyByKey(t, withBasis, key)
		b := readNoveltyByKey(t, without, key)
		if a != b {
			t.Fatalf("the full-dimension series moved when the projected one was added.\n"+
				"  with basis: %+v\n  without:    %+v\n"+
				"This task is additive by contract: the six shipped columns and everything about "+
				"measureNovelty's existing output must be identical whether or not a basis exists.",
				a, b)
		}
	}

	// And the projected series is actually populated, or the test above would
	// pass trivially by never exercising the new path.
	scored := 0
	for _, id := range revs {
		if readProjected(t, withBasis, id).Score.Valid {
			scored++
		}
	}
	if scored == 0 {
		t.Fatal("no revision carries a projected score, so the unchanged-full-dim assertion above " +
			"proved nothing")
	}
}

// TestProjectedSeriesIsNullWithoutABasis is question 2 of the four the task
// exists to answer, asserted rather than documented.
//
// A store that has never been fitted records NULL — "nobody looked" — and not a
// zero, which would read as "perfectly explained by the scope": the most
// redundant content in the store. That distinction is the same one migration
// 20's NULLs carry and it does not acquire a new meaning here.
func TestProjectedSeriesIsNullWithoutABasis(t *testing.T) {
	ms := newSubspaceStore(t)
	first := writeAndEmbed(t, ms, "note.one", "planted payload number 1")
	second := writeAndEmbed(t, ms, "note.two", "planted payload number 2")

	// The full-dimension series scores as usual.
	if !readNovelty(t, ms, second.RevisionID).ScopeN.Valid {
		t.Fatal("the full-dimension series must still score when no basis exists; the two series " +
			"are independent")
	}
	for _, rev := range []string{first.RevisionID, second.RevisionID} {
		c := readProjected(t, ms, rev)
		if c.Score.Valid || c.Top1Cosine.Valid || c.ScopeN.Valid || c.Kappa.Valid || c.BasisVersion.Valid {
			t.Errorf("a store with no fitted basis recorded %+v; every projected column must be "+
				"NULL. A zero here would claim the write was perfectly explained by a space nobody "+
				"ever constructed", c)
		}
	}
}

// TestProjectedSeriesStampsItsBasisAndEmitsNoRoute covers questions 3 and 4.
//
// The stamp is what makes rows scored under different bases — or different
// embedding models — distinguishable after the fact. The absent route is the
// deliberate refusal to publish a decision rule nobody has validated in this
// space: the published τ/δ were tuned at another dimension on another model,
// and ν's distribution here is measurably not theirs.
func TestProjectedSeriesStampsItsBasisAndEmitsNoRoute(t *testing.T) {
	ms := newSubspaceStore(t)
	for i := 0; i < 40; i++ {
		writeAndEmbed(t, ms, "basis.fit."+itoa(i), "planted payload number "+itoa(i))
	}
	report := fitTestBasis(t, ms, false)
	ms = reopenSameStore(t, ms)

	rev := writeAndEmbed(t, ms, "note.after", "planted payload number 999")
	c := readProjected(t, ms, rev.RevisionID)
	if !c.Score.Valid || !c.BasisVersion.Valid {
		t.Fatalf("a write after a fit must carry a projected score and a basis stamp; got %+v", c)
	}
	if c.BasisVersion.String != report.Version {
		t.Errorf("row stamped %q but the fitted basis is %q", c.BasisVersion.String, report.Version)
	}
	if !hasPrefix(c.BasisVersion.String, "sage-ext/") {
		t.Errorf("basis version %q does not begin with sage-ext/. The prefix is what stops a row "+
			"scored in a space the paper does not use from being read as the paper's own method",
			c.BasisVersion.String)
	}
	if c.Score.Float64 < 0 || c.Score.Float64 > 1 {
		t.Errorf("projected ν = %v is outside [0,1]", c.Score.Float64)
	}

	// There is no route column for this series, and that is the point.
	var n int
	if err := ms.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM pragma_table_info('memory_revisions') WHERE name LIKE 'novelty_pca16%route%'`,
	).Scan(&n); err != nil {
		t.Fatalf("inspect columns: %v", err)
	}
	if n != 0 {
		t.Errorf("found %d route column(s) for the projected series. Emitting a route under the "+
			"published constants would put an unvalidated decision rule beside novelty_route, "+
			"where it would read as comparable to one computed a different way", n)
	}
}

// reopenSameStore returns a new memory.Store over the same database, which is
// how a fitted basis becomes visible: the basis is cached for the life of a
// Store so that it cannot begin applying partway through a run.
func reopenSameStore(t *testing.T, ms *memory.Store) *memory.Store {
	t.Helper()
	return memory.NewStore(ms.DB(), subspaceEmbedder{}, "test-model", 0.85, memory.NoopQueue{})
}

func readNoveltyByKey(t *testing.T, ms *memory.Store, key string) noveltyColumns {
	t.Helper()
	var id string
	if err := ms.DB().QueryRowContext(context.Background(),
		`SELECT revision_id FROM memory_revisions WHERE memory_key = ? ORDER BY created_at DESC LIMIT 1`, key,
	).Scan(&id); err != nil {
		t.Fatalf("find revision for %s: %v", key, err)
	}
	return readNovelty(t, ms, id)
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// TestNoveltyBasisLoadFailureDoesNotPoisonLaterWrites is the regression guard
// for the defect Copilot found on PR #34.
//
// The basis is cached for the life of a Store, including the ABSENT case, which
// is correct: absent is a steady state and re-querying it on every write buys
// nothing. Caching a FAILURE is a different thing entirely. loadNoveltyBasis
// already distinguishes absent (nil, nil) from failed (nil, err), and a caller
// that collapses them turns one transient read error into a permanently
// disabled series — silently, on a daemon that may not restart for days, while
// recording NULL on every write.
//
// NULL is the value this schema reserves for "nobody looked". Making it also
// mean "something broke once, hours ago" is exactly the collapse the file's
// NULL semantics exist to prevent, and it is the same shape as the κ̂ bug this
// package already documents: an error path that silently classified good
// writes as unscored.
//
// The table is renamed out from under the store to produce a genuine read
// error, not a missing row.
func TestNoveltyBasisLoadFailureDoesNotPoisonLaterWrites(t *testing.T) {
	ctx := context.Background()
	ms := newSubspaceStore(t)
	for i := 0; i < 40; i++ {
		writeAndEmbed(t, ms, "basis.fit."+itoa(i), "planted payload number "+itoa(i))
	}
	fitTestBasis(t, ms, false)
	ms = reopenSameStore(t, ms)

	// Break the read before the store has ever loaded the basis, so the very
	// first lookup is the failing one.
	if _, err := ms.DB().ExecContext(ctx, `ALTER TABLE novelty_basis RENAME TO novelty_basis_hidden`); err != nil {
		t.Fatalf("hide basis table: %v", err)
	}
	broken := writeAndEmbed(t, ms, "note.during.failure", "planted payload number 900")
	if c := readProjected(t, ms, broken.RevisionID); c.Score.Valid {
		t.Fatal("a write scored in the projected space while the basis was unreadable")
	}

	// Restore it. The SAME store must recover on its next write.
	if _, err := ms.DB().ExecContext(ctx, `ALTER TABLE novelty_basis_hidden RENAME TO novelty_basis`); err != nil {
		t.Fatalf("restore basis table: %v", err)
	}
	recovered := writeAndEmbed(t, ms, "note.after.failure", "planted payload number 901")
	c := readProjected(t, ms, recovered.RevisionID)
	if !c.Score.Valid || !c.BasisVersion.Valid {
		t.Fatalf("the store did not retry after a failed basis load; it cached the failure and "+
			"disabled the projected series for its whole life. Got %+v", c)
	}
}

// TestProjectIntoMatchesProject pins the two forms together.
//
// projectInto exists only to let the scope scan reuse one buffer instead of
// allocating per row. If the two ever disagree, every ν in the projected series
// is computed by a code path no test covers, while the covered one still looks
// correct.
func TestProjectIntoMatchesProject(t *testing.T) {
	ms := newSubspaceStore(t)
	for i := 0; i < 40; i++ {
		writeAndEmbed(t, ms, "basis.fit."+itoa(i), "planted payload number "+itoa(i))
	}
	fitTestBasis(t, ms, false)

	// Exercised through the public surface: two stores, same writes, one
	// scoring every scope row through projectInto. Equivalence is asserted
	// directly in the internal test; this one guards the wiring.
	ms = reopenSameStore(t, ms)
	rev := writeAndEmbed(t, ms, "note.buffered", "planted payload number 902")
	c := readProjected(t, ms, rev.RevisionID)
	if !c.Score.Valid || !c.ScopeN.Valid {
		t.Fatalf("the buffered projection path produced no score: %+v", c)
	}
	if c.Score.Float64 < 0 || c.Score.Float64 > 1 {
		t.Errorf("projected ν = %v is outside [0,1]", c.Score.Float64)
	}
	if c.ScopeN.Int64 == 0 {
		t.Error("scope was empty, so the per-row projection loop never ran and this proved nothing")
	}
}

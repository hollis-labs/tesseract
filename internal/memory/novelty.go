package memory

import (
	"context"
	"database/sql"
	"fmt"
	"math"
)

// Novelty: Tesseract's own derived measurement of how much new direction a
// revision adds to the scope it was written into.
//
// # Three bags, three owners — this is the third
//
// Tesseract already has two things a reader could confuse this with, and the
// distance between all three is why nothing in this file is ever spelled
// `state`:
//
//   - memory_state is a TABLE. It is Tesseract's MUTABLE per-ENTRY bookkeeping
//     — current_revision, activation, access_count, last_decayed_at — and it
//     moves on every read.
//   - consumer_state is a COLUMN (CW-20260909-0036). It is the CONSUMER's
//     immutable per-revision JSON bag, which Tesseract validates for shape and
//     never reads a value out of.
//   - novelty_* are these columns. They are TESSERACT's OWN derived values,
//     immutable with the revision that carries them, computed by this file and
//     read by nobody.
//
// The prefix is `novelty_` rather than `nu_` on purpose. The guard test below
// works by grepping this repository for the name, so a name that cannot be
// mistaken for a typo of "new" — and that appears nowhere else in the schema —
// is what makes the guard enforceable rather than decorative.
//
// # The discipline line
//
// Tesseract may COMPUTE novelty, STORE it, and REPORT it.
// Tesseract must never let it change what happens to a revision.
//
// Not dedup, not status, not activation, not decay, not retention, not recall
// ordering. The fixed-threshold dedup in dedup.go still governs writes and is
// untouched by this file. That is what "shadow mode" means here, and it is
// held by TestNoveltyIsConfinedToItsScorer and
// TestNoveltyScoreDoesNotChangeBehavior rather than by this paragraph.
//
// Ranking on novelty is a real and attractive idea — it is CW-20260910-0074 —
// and the follow-up record that raised it is explicit that it is OUR extension
// and not a published result. It does not land by accident here first.
//
// # What the score is
//
// SAGE (arXiv 2605.30711v2, Wang/Brahma/Henao): score a candidate against the
// whole scope with a von Mises-Fisher kernel density estimate rather than
// against its single nearest neighbor.
//
//	κ̂     ≈ R̄(d − R̄²)/(1 − R̄²)          R̄ = ‖mean of the scope's unit vectors‖
//	s_vMF  = (1/κ) · log( (1/N) Σ exp(κ·mᵢᵀc) )
//	ν      = (1 − s_vMF) / 2             larger means more novel
//
// # Two measured facts that shape this implementation
//
// Both were measured on the live corpus before this landed, by replaying every
// write in a namespace against the scope that existed before it. Neither is a
// reason not to collect ν; both are reasons the collected value must carry the
// statistics beside it.
//
// FIRST: at d=3072 this KDE IS top-1 cosine. κ̂ carries the embedding dimension
// in its numerator, so with the corpus's measured R̄ = 0.638 it lands at
// κ̂ ≈ 3300–4200. Since log-mean-exp is bounded by
//
//	max(cosᵢ) − log(N)/κ  ≤  s_vMF  ≤  max(cosᵢ)
//
// the score is pinned within log(N)/κ ≈ 0.0023 of the maximum. Replayed over
// 583 real writes, the largest observed |ν_KDE − ν_top1| was 9.05e-04 — 28×
// smaller than the paper's own uncertainty band δ = 0.025. The aggregation
// that is SAGE's entire argument against a top-1 gate is unobservable at this
// embedding dimension. It becomes observable in a low-dimensional space
// (κ̂ ≈ 15.6 at d'=16, median |Δν| = 0.12), which is a separate decision and is
// not taken here.
//
// So novelty_top1_cosine is stored beside novelty_score deliberately. It is
// both the honest disclosure of the above and the value the SHIPPED dedup gate
// thresholds at 0.85, which makes "would the new gate have agreed with the old
// one" answerable from one row.
//
// SECOND: the paper's density-adaptive threshold τ* = τ_min + τ₀·e^(−λρ) is
// pinned at τ_min here, so this file does not compute ρ. ρ = N/V where V is the
// product of d'=16 PCA coordinate ranges; unit vectors in 3072 dimensions
// spread their variance thinly, so the measured ranges are ~0.61, V collapses
// to 1e-7…3e-4, and ρ lands at 1.9e6–2.7e8. e^(−2ρ) underflows to exactly 0.0
// in float64 for any ρ above ~350. Fitting a 16-component PCA on every write to
// multiply τ₀ by a number that is exactly zero is cost for no signal. ρ also
// FELL as the corpus grew in every namespace measured, which is the opposite of
// the intuition the paper's decay is built on. The EMA smoothing (α = 0.9) goes
// with it: smoothing a constant returns the constant.
//
// Both facts mean the published hyperparameters do not transfer to this corpus.
// That is exactly why novelty_route is stamped with novelty_gate_version and
// why the statistics that would let a different (τ, δ) be evaluated offline —
// novelty_scope_n, novelty_kappa, novelty_top1_cosine — are stored beside it.
// Re-deciding the gate later must not require re-embedding the corpus.
//
// # Why the value is stored rather than recomputed on read
//
// The scope a write faced is a historical fact, and two of its three inputs are
// mutated in place afterwards: promotion rewrites memory_revisions.namespace
// (internal/memory/migrate.go), and EmbedRevision rewrites embedding_vector.
// A recomputed ν would therefore drift for reasons that have nothing to do with
// the write it describes.
//
// Row membership is reconstructible — no production path deletes from
// memory_revisions, so `created_at < T` recovers the ROWS as of T. But that is
// not the same set the scope selects, and the difference is not theoretical.
// The scope query also requires `embedding_vector IS NOT NULL`, and embedding
// is asynchronous: a revision created BEFORE the candidate but EMBEDDED AFTER
// it joins a reconstructed scope without ever having been in the original. So
// a replay can find a scope one or more rows larger than the `novelty_scope_n`
// that was stored, and compute a different ν from it.
//
// Measured on 2026-09-11 (CW-20260911-0043): an offline replay reproduced the
// stored ν exactly for 33 of 37 scored revisions. All four misses had a scope
// exactly one row larger than the stored count, every one of them written while
// an embedding backlog was draining. The stored value is the correct record of
// what the write faced; what is NOT guaranteed is that a later replay
// reproduces it. Anything that depends on exact replayability has to check
// novelty_scope_n against the scope it reconstructed, rather than assume.
//
// # When it is computed
//
// In EmbedRevision, immediately after the vector lands — not in WriteRevision.
// Embedding is asynchronous and post-commit (see the queue enqueue at the end
// of WriteRevision), so at INSERT time the revision has no vector to score.
// Scoring inline would mean a synchronous embedding API call on EVERY write,
// where today only an opt-in `dedup: "semantic"` write pays for one. Placing it
// in EmbedRevision also means all three production embed paths — the queue
// handler, the library facade, and the CLI backfill — get it without knowing
// about it.
//
// The consequence worth stating plainly: ν is an EMBED-time value, not an
// INSERT-time one, and the scope is defined by created_at ordering rather than
// by embed ordering, so it is deterministic regardless of when the worker ran.
const (
	// noveltyGateVersion stamps every routing decision with the parameterisation
	// that produced it. It is not decoration: the measurements above establish
	// that these published values do not transfer to a 3072-dimensional corpus,
	// so a row whose route cannot name its parameters is a row nobody can
	// re-interpret later.
	noveltyGateVersion = "sage-2605.30711v2/published/full-dim"

	// The published hyperparameters, held fixed across all eight backbones in
	// the paper and selected by grid search on a 20% LoCoMo subsample (§C).
	//
	// noveltyTau0 and noveltyLambda are retained as documentation of what the
	// gate WOULD use rather than as live inputs: the density term they feed is
	// identically zero here, as measured above. They are deliberately not
	// deleted — the next person to revisit this needs to see the whole rule,
	// including the half that does nothing.
	noveltyTau0   = 0.25
	noveltyTauMin = 0.025
	noveltyLambda = 2.0
	noveltyDelta  = 0.025
)

// The shadow routes, in TESSERACT's vocabulary rather than the paper's.
//
// SAGE's ADD / UPDATE / NOOP map onto write / supersede / skip, and that
// mapping is the reason this gate clears the append-only objection that
// CW-20260825-0018 raised against mem0's LLM controller: none of the three
// deletes anything, and none silently rewrites an authored revision.
//
// Every one of these is a value Tesseract RECORDS and does not ACT on.
const (
	noveltyRouteWrite     = "write"
	noveltyRouteSupersede = "supersede"
	noveltyRouteSkip      = "skip"
)

// noveltyMeasurement is one revision's scoring result, or the explicit absence
// of one.
//
// # "Not scored" and "identical to everything" are opposite claims
//
// A ν of 0 means the candidate is perfectly explained by the scope. A ν of NULL
// means nobody looked. Collapsing the second into the first would make the 84
// currently-unembedded revisions — and every revision written before this
// migration — read as the most redundant content in the store, which is the
// single most misleading value they could carry.
//
// So Scored gates the write, and an unscored revision keeps SQL NULL in every
// novelty column rather than a zero in any of them.
//
// There is a third case, and it is distinguishable from both: a scope that is
// EMPTY. The first write into a namespace has nothing to be novel against, and
// the paper handles it by emitting ADD without computing a score (§3.3, "When
// N = 0 … the controller directly emits ADD"). Such a row carries
// ScopeN = 0 with a route of "write" and a NULL score. The three states read
// off a row as:
//
//	novelty_scope_n IS NULL      → not scored (no vector, no embedder, pre-migration)
//	novelty_scope_n = 0          → scored; scope was empty; trivially novel
//	novelty_scope_n > 0          → scored against that many vectors
//
// novelty_kappa carries its own NULL, independent of the above: it is absent
// when the scope was a single vector, or N identical ones, because R̄ = 1 makes
// the concentration estimate undefined there. A NULL κ does NOT mean the score
// is missing or degraded — in exactly those scopes the log-mean-exp has a
// closed form that κ never enters. See measureNovelty.
type noveltyMeasurement struct {
	Scored     bool
	HasScore   bool // false for the empty-scope case, where ν is undefined
	HasKappa   bool // false when the scope is too small or too uniform to estimate κ̂
	Score      float64
	Top1Cosine float64
	ScopeN     int64
	Kappa      float64
	Route      string
}

// scoreNovelty measures revisionID against the scope it was written into and
// records the result on the revision.
//
// Best-effort by contract: it is called after a committed, already-embedded
// revision, and failing it must never undo that work or cost a second
// embedding API call on retry. Every failure path leaves the novelty columns
// NULL, which reads as "not scored" — the honest answer.
func (s *Store) scoreNovelty(ctx context.Context, revisionID string) error {
	var (
		domain, namespace, createdAt string
		model                        sql.NullString
		blob                         []byte
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT domain, namespace, created_at, embedding_model, embedding_vector
		   FROM memory_revisions WHERE revision_id = ?`, revisionID,
	).Scan(&domain, &namespace, &createdAt, &model, &blob)
	if err != nil {
		return fmt.Errorf("load revision for novelty: %w", err)
	}

	candidate := unitVector(blobToFloat32(blob))
	if candidate == nil {
		// No vector, or a zero vector that cannot be placed on the sphere.
		// Not scored, and said so by leaving the columns NULL.
		return nil
	}

	// The frozen PCA-16 basis, or nil when this store has never been fitted.
	// nil is not a failure: the projected columns stay NULL, which reads as
	// "not scored" exactly like every other novelty NULL. A basis is never
	// fitted implicitly here — see noveltybasis.go.
	basis := s.noveltyBasisFor(ctx, model.String)

	m, pm, err := s.measureNovelty(ctx, domain, namespace, createdAt, revisionID, candidate, basis)
	if err != nil {
		return err
	}
	if err := m.persist(ctx, s.db, revisionID); err != nil {
		return err
	}
	return pm.persist(ctx, s.db, revisionID)
}

// noveltyBasisFor returns the cached frozen basis for an embedding model, or
// nil when the store has none.
//
// Cached for the life of the Store, including the ABSENT case: without that, a
// store with no basis would issue a failed lookup on every single write,
// forever. The consequence, which is why the fit command says so in its output:
// a basis fitted while a daemon is running is not picked up until that daemon
// restarts. That is the right trade for a value defined to be frozen — a basis
// that could start applying mid-process would make ν incomparable across writes
// in the same run, which is the exact failure freezing exists to prevent.
//
// A load error is logged and treated as absent rather than propagated. Scoring
// is best-effort by contract (see EmbedRevision) and a store that cannot read
// its basis should record "not scored", not fail an already-paid-for embedding.
func (s *Store) noveltyBasisFor(ctx context.Context, embeddingModel string) *noveltyBasis {
	if embeddingModel == "" {
		return nil
	}
	s.noveltyBasisMu.Lock()
	defer s.noveltyBasisMu.Unlock()
	if s.noveltyBasisLoaded == embeddingModel {
		return s.noveltyBasisCache
	}
	b, err := loadNoveltyBasis(ctx, s.db, embeddingModel)
	if err != nil {
		// NOT cached. loadNoveltyBasis already distinguishes absent (nil, nil)
		// from failed (nil, err), and collapsing the two here would throw that
		// distinction away at the only place it matters: caching a failure
		// disables the projected series for the LIFE OF THE PROCESS, which on
		// the daemon is indefinite, silently, recording NULL on every write.
		//
		// That is the same shape as the bug this package already carries a
		// warning about — declining to score when κ̂ was undefined recorded
		// "not scored" for every namespace's second write — and it is worse
		// here, because NULL is exactly the value a reader would trust as
		// "nobody looked" rather than "something broke once". A transient read
		// error must cost one write, not all of them.
		s.log().WarnContext(ctx, "novelty basis load failed; this write records not-scored and the next will retry",
			"embedding_model", embeddingModel, "err", err)
		return nil
	}
	// Absent IS cached, and should be: it is the correct steady state for a
	// store nobody has fitted, the lookup is an indexed miss on a one-row
	// table, and repeating it on every write forever buys nothing. The cost is
	// that a basis fitted while a daemon runs needs a restart, which is stated
	// in the fit command's own output and is the behavior a frozen basis
	// wants anyway.
	s.noveltyBasisCache = b
	s.noveltyBasisLoaded = embeddingModel
	return b
}

// measureNovelty computes the measurement for candidate against every embedded
// revision that preceded it in the same domain and namespace.
//
// # Why the scope is "everything created before", and not "current revisions"
//
// The alternative — scoring against current revisions only, as findSemanticMatch
// does via recall's default revision scope — tracks what the shipped dedup gate
// sees, but it is not reproducible: deprecation moves as history accumulates,
// so the same row re-measured next year would face a different scope. The
// created_at bound is exact on an append-only store and makes the stored value
// auditable against the log.
//
// The consequence, stated rather than discovered later: a memory's tenth
// revision is scored against its own nine predecessors, which are near
// duplicates by construction, so revision chains score LOW. That is signal, not
// noise — "this looks like a supersede" is one of the three outcomes the gate
// exists to name — but any analysis that treats a low ν as "redundant capture"
// without splitting revision-of-existing from new-entry is reading it wrong.
//
// # Memory
//
// O(d + N), never O(N·d). The scope is streamed: the mean vector accumulates in
// place and only the scalar cosines are retained, because κ̂ has to be known
// before the exponentials can be taken and that needs two passes over the
// cosines but only one over the rows. At 200k scope vectors this is ~1.6 MB
// rather than the ~2.4 GB a materialized scope would cost. The CPU term is
// genuinely O(N·d) and is the real ceiling — measured at 1.67 ms per write
// against a 633-vector scope.
func (s *Store) measureNovelty(ctx context.Context, domain, namespace, createdAt, revisionID string, candidate []float64, basis *noveltyBasis) (noveltyMeasurement, noveltyProjectedMeasurement, error) {
	d := len(candidate)

	// The projected candidate, or nil when there is no basis, the basis does
	// not match this vector's model or width, or the candidate lands exactly on
	// the projected origin. Computed once, outside the scope loop.
	//
	// ONE scan serves both series deliberately. Scanning twice would be simpler
	// to read but would let a concurrent backfill embed an older revision
	// between the passes, so the two series would describe different scopes —
	// and comparing them on "the same writes" is the entire point of collecting
	// the second one. Sharing the scan makes the scopes identical by
	// construction rather than by assumption.
	var projCandidate []float64
	if basis != nil && basis.SourceDim == d {
		projCandidate = basis.project(candidate)
	}
	pd := 0
	if projCandidate != nil {
		pd = len(projCandidate)
	}

	// The keyset predicate is written out rather than as an SQLite row-value
	// comparison, matching ReadEventLog: timestamps are fixed-width
	// lexically-chronological TEXT, so the string comparison IS the
	// chronological one, and revision_id breaks ties inside one nanosecond
	// exactly as the log's own ordering does.
	rows, err := s.db.QueryContext(ctx, `
SELECT embedding_vector FROM memory_revisions
 WHERE domain = ? AND namespace = ? AND embedding_vector IS NOT NULL
   AND (created_at < ? OR (created_at = ? AND revision_id < ?))`,
		domain, namespace, createdAt, createdAt, revisionID)
	if err != nil {
		return noveltyMeasurement{}, noveltyProjectedMeasurement{}, fmt.Errorf("scan novelty scope: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sum := make([]float64, d)
	cosines := make([]float64, 0, 256)
	psum := make([]float64, pd)
	pcosines := make([]float64, 0, 256)
	pbuf := make([]float64, pd)
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return noveltyMeasurement{}, noveltyProjectedMeasurement{}, fmt.Errorf("scan novelty scope row: %w", err)
		}
		v := unitVector(blobToFloat32(blob))
		// A scope vector from a different embedding model has a different
		// dimension and is not comparable. Skipping it is right, and it is why
		// novelty_scope_n records what was actually scored against rather than
		// how many rows the namespace holds.
		if v == nil || len(v) != d {
			continue
		}
		var dot float64
		for i, f := range v {
			sum[i] += f
			dot += f * candidate[i]
		}
		cosines = append(cosines, dot)

		// The projected series accumulates from the SAME row, and separately: a
		// scope vector that projects onto the origin carries no direction in the
		// reduced space and is skipped there while still counting at full
		// dimension. That is why novelty_pca16_scope_n is its own column rather
		// than a copy of novelty_scope_n.
		if projCandidate == nil {
			continue
		}
		// projectInto rather than project: one buffer reused across the scan.
		// project allocates a fresh slice per call, and this call is per SCOPE
		// ROW — 200k allocations per write at the volumes the file comment
		// above reasons about. Peak retained memory is unaffected, so the
		// O(d + N) claim was never false; what this removes is allocation
		// churn in the one loop that runs per scope vector.
		if !basis.projectInto(pbuf, v) {
			continue
		}
		var pdot float64
		for i, f := range pbuf {
			psum[i] += f
			pdot += f * projCandidate[i]
		}
		pcosines = append(pcosines, pdot)
	}
	if err := rows.Err(); err != nil {
		return noveltyMeasurement{}, noveltyProjectedMeasurement{}, fmt.Errorf("novelty scope rows: %w", err)
	}

	pm := measureProjected(basis, projCandidate, psum, pcosines, pd)

	n := len(cosines)
	if n == 0 {
		// §3.3: with an empty scope the controller emits ADD without computing
		// a score. There is no density to be explained by, so ν is undefined
		// rather than maximal.
		return noveltyMeasurement{Scored: true, ScopeN: 0, Route: noveltyRouteWrite}, pm, nil
	}

	top1 := cosines[0]
	for _, c := range cosines[1:] {
		if c > top1 {
			top1 = c
		}
	}

	// κ̂ is only needed when the cosines DIFFER.
	//
	// log-mean-exp over a set of identical values is that value, for every κ:
	// (1/κ)·log((1/N)·Σ exp(κ·x)) = x. So a scope of ONE vector, or of N
	// identical ones, has an exact answer that κ never enters — and both are
	// exactly the scopes on which κ̂ is undefined, because a single unit vector
	// (or N copies of one) has mean resultant length R̄ = 1 and the estimator's
	// denominator 1 − R̄² vanishes.
	//
	// This was shipped wrong the first time and caught on live data within the
	// hour: declining to score when κ̂ was unavailable meant EVERY namespace's
	// SECOND write was recorded as "not scored", since a one-vector scope
	// always drives R̄ to 1. Three rows in the first half hour. The failure
	// landed hardest exactly where this gate is justified — a new event stream
	// starts empty, so its earliest entries are all small-N.
	//
	// Falling back to top1 is not an approximation here. It is the closed form
	// in the only cases that reach it. novelty_kappa is left NULL to say the
	// concentration estimate was undefined, and novelty_top1_cosine is stored
	// beside the score either way, so the two are always comparable.
	kappa, haveKappa := kappaHat(sum, n, d)

	sVMF := top1
	if haveKappa && n > 1 {
		// log-mean-exp with the maximum factored out. Mathematically identical
		// to (1/κ)·log((1/N)·Σ exp(κ·cosᵢ)) and the only form that survives κ
		// in the thousands: exp(3300 × 0.7) overflows float64 by hundreds of
		// orders of magnitude, while exp(3300 × (cosᵢ − max)) is bounded by 1.
		var acc float64
		for _, c := range cosines {
			acc += math.Exp(kappa * (c - top1))
		}
		sVMF = top1 + (math.Log(acc)-math.Log(float64(n)))/kappa
	}

	// The paper's Proposition (§E) bounds s_vMF to [−1, 1] for N ≥ 1; the clamp
	// is against float error at the edges, not against the mathematics.
	if sVMF > 1 {
		sVMF = 1
	} else if sVMF < -1 {
		sVMF = -1
	}
	score := (1 - sVMF) / 2

	m := noveltyMeasurement{
		Scored:     true,
		HasScore:   true,
		HasKappa:   haveKappa && n > 1,
		Score:      score,
		Top1Cosine: top1,
		ScopeN:     int64(n),
		Route:      shadowRoute(score),
	}
	if m.HasKappa {
		m.Kappa = kappa
	}
	return m, pm, nil
}

// shadowRoute maps a novelty score onto what the gate WOULD have decided.
//
// This is the one comparison in Tesseract that reads a novelty value, and it
// produces a string that is written to a column and never consulted again. The
// threshold is the constant τ_min rather than τ* = τ_min + τ₀·e^(−λρ) because
// the density term is identically zero on this corpus — measured, see the file
// comment — which also makes the EMA over consecutive writes a no-op.
//
// Nothing downstream may switch on the value this returns. The guard tests are
// TestNoveltyIsConfinedToItsScorer and TestNoveltyScoreDoesNotChangeBehavior.
func shadowRoute(score float64) string {
	tau := noveltyTauMin
	switch {
	case score >= tau+noveltyDelta:
		return noveltyRouteWrite
	case score >= tau:
		return noveltyRouteSupersede
	default:
		return noveltyRouteSkip
	}
}

// kappaHat estimates the vMF concentration from the scope's own geometry:
// κ̂ ≈ R̄(d − R̄²)/(1 − R̄²), where R̄ is the mean resultant length (Banerjee et
// al. 2005, as used by SAGE §3.3).
//
// sum is the unnormalized sum of the scope's unit vectors, so R̄ = ‖sum‖/n.
//
// Reports false rather than returning an infinity when the scope is degenerate
// — every vector pointing the same way drives R̄ to 1 and the denominator to 0.
// That is not a hypothetical: a fixed-vector test embedder produces exactly it,
// and so would a namespace holding one repeated payload.
func kappaHat(sum []float64, n, d int) (float64, bool) {
	var norm float64
	for _, f := range sum {
		norm += f * f
	}
	rbar := math.Sqrt(norm) / float64(n)
	if rbar < 0 {
		rbar = 0
	}
	denom := 1 - rbar*rbar
	if denom <= 1e-12 {
		return 0, false
	}
	k := rbar * (float64(d) - rbar*rbar) / denom
	if k <= 0 || math.IsInf(k, 0) || math.IsNaN(k) {
		return 0, false
	}
	return k, true
}

// unitVector converts a stored embedding to a float64 unit vector, or returns
// nil if there is nothing placeable on the sphere.
//
// The renormalization is defensive rather than corrective: text-embedding-3-large
// returns unit vectors and the live corpus measures ‖v‖ ∈ [0.999472, 1.000629].
// But the vMF kernel is only defined on Sᵈ⁻¹, and a scope carrying one
// unnormalized vector would otherwise produce a cosine outside [−1, 1] and a
// novelty score outside [0, 1] with no indication anything had gone wrong.
func unitVector(v []float32) []float64 {
	if len(v) == 0 {
		return nil
	}
	out := make([]float64, len(v))
	var norm float64
	for i, f := range v {
		out[i] = float64(f)
		norm += out[i] * out[i]
	}
	norm = math.Sqrt(norm)
	if norm == 0 || math.IsInf(norm, 0) || math.IsNaN(norm) {
		return nil
	}
	for i := range out {
		out[i] /= norm
	}
	return out
}

// persist writes the measurement onto the revision, or leaves every column NULL
// when nothing was scored.
//
// A single UPDATE of six columns, and deliberately NOT inside the transaction
// that wrote the revision — it runs long after that committed. It is the same
// shape as the embedding_vector write it follows, on the same already-durable
// row, and it is the only statement in Tesseract that writes these columns.
func (m noveltyMeasurement) persist(ctx context.Context, db *sql.DB, revisionID string) error {
	if !m.Scored {
		return nil
	}
	nullF := func(v float64, ok bool) sql.NullFloat64 {
		if !ok {
			return sql.NullFloat64{}
		}
		return sql.NullFloat64{Float64: v, Valid: true}
	}
	_, err := db.ExecContext(ctx, `
UPDATE memory_revisions
   SET novelty_score = ?, novelty_top1_cosine = ?, novelty_scope_n = ?,
       novelty_kappa = ?, novelty_route = ?, novelty_gate_version = ?
 WHERE revision_id = ?`,
		nullF(m.Score, m.HasScore),
		nullF(m.Top1Cosine, m.HasScore),
		m.ScopeN,
		nullF(m.Kappa, m.HasKappa),
		m.Route,
		noveltyGateVersion,
		revisionID,
	)
	if err != nil {
		return fmt.Errorf("persist novelty: %w", err)
	}
	return nil
}

// noveltyProjectedMeasurement is one revision's ν measured in the frozen
// PCA-16 space, or the explicit absence of one.
//
// # Why this carries no Route, deliberately
//
// The shipped full-dim series stamps novelty_route with what the gate WOULD
// have decided. This one does not, and the omission is the design rather than
// an oversight.
//
// τ_min = 0.025 and δ = 0.025 were grid-searched by the paper on a 20% LoCoMo
// subsample, at a different embedding dimension on a different model. ν's
// distribution in this space is measurably not the one they were tuned against
// — median |Δν| between the two spaces is 0.123, five times δ itself. A route
// emitted under those constants would be a decision rule nobody has validated,
// and it would sit in a column beside novelty_route where it would read as
// comparable to a number that was computed a different way. Storing the score
// and the statistics it derives from leaves every (τ, δ) choosable and
// evaluable offline; naming one now would be inventing the answer this series
// exists to make answerable.
//
// So there are five columns here against the full-dim series' six, and the
// missing one is the only one that expresses an opinion.
//
// # NULL means the same thing it means everywhere else
//
// No new NULL semantics are introduced. A store with no fitted basis, a
// revision whose embedding model the basis was not fitted for, and a candidate
// that projects onto the origin all record NULL in every column here — "nobody
// looked", exactly as documented on noveltyMeasurement. ScopeN = 0 remains
// "scored against an empty scope", distinguishable from both.
type noveltyProjectedMeasurement struct {
	Scored       bool
	HasScore     bool
	HasKappa     bool
	Score        float64
	Top1Cosine   float64
	ScopeN       int64
	Kappa        float64
	BasisVersion string
}

// measureProjected runs the same vMF estimator as the full-dimension path over
// the projected cosines.
//
// The arithmetic is deliberately identical — same κ̂ estimator, same
// max-factored log-mean-exp, same ν = (1 − s_vMF)/2, same closed-form fallback
// when κ̂ is undefined. That is what makes the two series comparable: any
// difference between them is a difference of SPACE, not of method. The only
// thing that changes is d, and d is the whole point — it is what drops κ̂ from
// the thousands to ~15.6 and lets the kernel actually aggregate.
func measureProjected(basis *noveltyBasis, candidate, sum []float64, cosines []float64, d int) noveltyProjectedMeasurement {
	if basis == nil || candidate == nil {
		return noveltyProjectedMeasurement{}
	}
	pm := noveltyProjectedMeasurement{Scored: true, BasisVersion: basis.Version}
	n := len(cosines)
	if n == 0 {
		// Empty scope: ν is undefined rather than maximal, matching §3.3 and
		// the full-dimension path.
		return pm
	}

	top1 := cosines[0]
	for _, c := range cosines[1:] {
		if c > top1 {
			top1 = c
		}
	}

	kappa, haveKappa := kappaHat(sum, n, d)
	sVMF := top1
	if haveKappa && n > 1 {
		var acc float64
		for _, c := range cosines {
			acc += math.Exp(kappa * (c - top1))
		}
		sVMF = top1 + (math.Log(acc)-math.Log(float64(n)))/kappa
	}
	if sVMF > 1 {
		sVMF = 1
	} else if sVMF < -1 {
		sVMF = -1
	}

	pm.HasScore = true
	pm.Score = (1 - sVMF) / 2
	pm.Top1Cosine = top1
	pm.ScopeN = int64(n)
	pm.HasKappa = haveKappa && n > 1
	if pm.HasKappa {
		pm.Kappa = kappa
	}
	return pm
}

// persist writes the projected measurement.
//
// The three states it can leave behind, spelled out because NULL is
// load-bearing in this file and a comment that misdescribes it is worse than
// no comment:
//
//   - NOT SCORED (!Scored) — no statement runs at all and every projected
//     column keeps its NULL. No basis, a model the basis was not fitted for,
//     or a candidate that projects onto the origin.
//   - SCORED, EMPTY SCOPE (Scored, !HasScore) — novelty_pca16_scope_n = 0 and
//     novelty_pca16_basis_version are written; score, top1 and kappa stay
//     NULL. This mirrors the full-dimension series exactly, where scope_n = 0
//     is the distinct "scored against nothing" case rather than a missing one.
//   - SCORED (Scored, HasScore) — everything except kappa, which keeps its own
//     independent NULL when the scope was too small or too uniform to estimate
//     κ̂.
//
// A separate UPDATE from the full-dimension one, on the same already-durable
// row. Separate rather than merged into a single statement so that the shipped
// series is written by exactly the statement that has always written it: a
// failure to store the projected columns cannot corrupt or roll back the six
// that were already correct.
func (m noveltyProjectedMeasurement) persist(ctx context.Context, db *sql.DB, revisionID string) error {
	if !m.Scored {
		return nil
	}
	nullF := func(v float64, ok bool) sql.NullFloat64 {
		if !ok {
			return sql.NullFloat64{}
		}
		return sql.NullFloat64{Float64: v, Valid: true}
	}
	_, err := db.ExecContext(ctx, `
UPDATE memory_revisions
   SET novelty_pca16_score = ?, novelty_pca16_top1_cosine = ?,
       novelty_pca16_scope_n = ?, novelty_pca16_kappa = ?,
       novelty_pca16_basis_version = ?
 WHERE revision_id = ?`,
		nullF(m.Score, m.HasScore),
		nullF(m.Top1Cosine, m.HasScore),
		m.ScopeN,
		nullF(m.Kappa, m.HasKappa),
		m.BasisVersion,
		revisionID,
	)
	if err != nil {
		return fmt.Errorf("persist projected novelty: %w", err)
	}
	return nil
}

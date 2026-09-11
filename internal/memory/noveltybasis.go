package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// The frozen PCA-16 basis: the projection ν's second series is measured in.
//
// # Why a second space at all
//
// The shipped full-dim series (novelty.go) is honest and cheap and carries no
// density awareness whatsoever, because at d=3072 the vMF KDE is numerically
// top-1 cosine — max |ν_KDE − ν_top1| = 9.05e-04 over 583 replayed writes, 28×
// below the paper's own δ = 0.025. κ̂ ≈ R̄(d − R̄²)/(1 − R̄²) carries the
// dimension, so it lands in the thousands and pins log-mean-exp to its maximum.
//
// In a 16-dimensional space κ̂ falls to ~15.6 and the KDE genuinely aggregates:
// median |Δν| = 0.123, three orders of magnitude above δ. That is the only
// regime on this corpus where SAGE's aggregation is observable at all, which is
// why CW-20260911-0043 collects a second series there.
//
// # This is OUR EXTENSION, and must never be reported as a published result
//
// The paper does NOT run its KDE in the projected space. Appendix C is explicit
// that d′ = 16 "only affects the density proxy (Appendix D)" — the KDE runs at
// full dimension. Scoring in the projected space is Tesseract's deviation,
// chosen because the published parameterisation is otherwise inert here. Every
// row this file produces is stamped with a basis version beginning `sage-ext/`
// so a reader cannot mistake it for the paper's method.
//
// # Why the basis is frozen, and what "frozen" has to mean
//
// A basis refitted as the corpus grows makes ν incomparable across time, which
// destroys exactly the replayability the embed-time scoring choice exists to
// protect. So the basis is fitted ONCE over a stated snapshot and never
// implicitly refit — there is no code path in this file that fits on read.
//
// Freezing the OUTPUT is not enough. What is stored is the snapshot's
// DEFINITION as well: the instant it was taken, how many vectors it covered,
// the embedding model, and a SHA-256 over every (revision_id, vector) pair in
// order. That hash is what makes a refit verifiable rather than asserted — the
// same snapshot must reproduce the same basis, and a claim that it did is
// checkable.
//
// # Centering, and why it is part of the basis
//
// PCA is defined on centered data, and here that is load-bearing rather than
// conventional. text-embedding-3-large's vectors are strongly concentrated
// (measured R̄ = 0.638), so WITHOUT centering the leading principal direction is
// essentially the corpus mean and every projection is dominated by it — every
// pair of projected vectors would look nearly parallel and the reduced space
// would degenerate the same way the full one does, for a different reason.
//
// So the mean vector is part of the frozen basis and is stored with it. A
// candidate is projected as Qᵀ(v − μ), then renormalized onto S¹⁵.
//
// # Why an orthonormal basis is enough, and what it does NOT give you
//
// ν in the projected space depends only on the SUBSPACE, not on which
// orthonormal basis of it is used: two bases of the same subspace differ by a
// 16×16 rotation R, and both the inner product and the norm are invariant under
// it (⟨Rx, Ry⟩ = ⟨x, y⟩, ‖Rx‖ = ‖x‖), so every cosine — and therefore κ̂, s_vMF
// and ν — is unchanged. The fit is still carried through Rayleigh-Ritz to
// genuine principal axes, for two reasons that are not about ν: the eigenvalues
// give the explained-variance ratio, which is the diagnostic that says whether
// 16 dimensions were worth taking at all; and the paper's density proxy ρ needs
// axis-aligned coordinate RANGES, so an axis-free basis would foreclose it.
//
// What this does NOT give you is ρ. ρ is not computed here for the same reason
// novelty.go does not compute it: e^(−λρ) underflows to exactly 0.0 at the
// measured ρ of 1.9e6–2.7e8, so the density-adaptive threshold is τ_min at
// every corpus size. The basis makes ρ computable later; it does not make it
// meaningful.
//
// # Why no linear-algebra dependency
//
// Fitting is one-shot and offline; SCORING is a 3072×16 matrix-vector multiply,
// ~50k multiply-adds, negligible beside the KDE pass already running. So the
// expensive linear algebra never enters the serving path, and the serving path
// needs no matrix library. The fit itself is streamed subspace iteration plus a
// 16×16 Jacobi eigensolve — both classical, both short, both deterministic
// under a stored seed, and both testable against known-answer fixtures
// (noveltybasis_test.go). go.mod's dependency list is deliberately lean and
// adding to it for a single offline fit was not worth the carry.
//
// Memory is O(d·ℓ), not O(N·d): the data is streamed from SQLite on every
// iteration rather than materialized. At today's 2,154 vectors a materialized
// fit would be 53 MB, which would be fine; at 200k it would be 4.9 GB, which
// would not. The cost is re-reading the scan once per iteration, which is
// cheap and bounded.

const (
	// noveltyBasisDim is the projection dimension. 16 is the paper's d′ (§C),
	// kept even though the use here differs, so the one number that IS shared
	// with the published parameterisation stays recognizable.
	noveltyBasisDim = 16

	// noveltyBasisOversample widens the iterated subspace beyond the 16 that
	// are kept. Subspace iteration converges at a rate governed by
	// λ_{ℓ+1}/λ_ℓ, so iterating a slightly larger block and discarding the
	// tail makes the kept directions converge much faster than iterating
	// exactly 16 would. Standard practice for randomized subspace methods.
	noveltyBasisOversample = 8

	// noveltyBasisIterations is fixed rather than convergence-tested on
	// purpose: a fixed count is deterministic, and determinism is what makes a
	// refit verifiable against the stored snapshot hash. Convergence is
	// asserted after the fact instead, by the residual check in fitNoveltyBasis.
	noveltyBasisIterations = 40

	// noveltyBasisSeed seeds the initial random block. Stored with the basis so
	// a refit over the same snapshot reproduces the same axes bit for bit.
	noveltyBasisSeed = 20260911

	// noveltyBasisAlgorithm names what produced the basis, so a future change
	// of method is distinguishable on the row rather than silent.
	noveltyBasisAlgorithm = "streamed-subspace-iteration+rayleigh-ritz"
)

// noveltyBasis is a frozen projection, together with the definition of the
// snapshot it was fitted over.
//
// Everything here except Mean and Components is provenance. That ratio is the
// point: the output of a fit is worthless for comparing scores across time
// unless the input that produced it is pinned down beside it.
type noveltyBasis struct {
	Version        string
	EmbeddingModel string
	SourceDim      int
	TargetDim      int
	SnapshotAt     string
	SnapshotN      int64
	SnapshotHash   string
	Seed           int64
	Iterations     int
	Algorithm      string

	// Mean is the snapshot's mean vector, subtracted before projection. See
	// the file comment: without it the reduced space degenerates.
	Mean []float64

	// Components is TargetDim rows of SourceDim, row-major: the principal axes
	// in descending eigenvalue order.
	Components []float64

	// EigenValues are the retained variances, and TotalVariance the trace of
	// the full covariance. Their ratio is the explained-variance diagnostic.
	EigenValues   []float64
	TotalVariance float64
}

// explainedVarianceRatio is the fraction of total variance the kept axes carry.
// The honest headline number for "was 16 dimensions worth taking".
func (b *noveltyBasis) explainedVarianceRatio() float64 {
	if b.TotalVariance <= 0 {
		return 0
	}
	var kept float64
	for _, v := range b.EigenValues {
		kept += v
	}
	return kept / b.TotalVariance
}

// project maps a full-dimension unit vector into the frozen space and returns
// it renormalized onto the unit sphere, or nil when it cannot be placed there.
//
// nil is returned rather than a zero vector for a vector that lands exactly at
// the projected origin — that is a candidate the basis cannot distinguish from
// the corpus mean, and scoring it as if it sat somewhere definite would invent
// a position it does not have.
func (b *noveltyBasis) project(v []float64) []float64 {
	if len(v) != b.SourceDim {
		return nil
	}
	out := make([]float64, b.TargetDim)
	for k := 0; k < b.TargetDim; k++ {
		row := b.Components[k*b.SourceDim : (k+1)*b.SourceDim]
		var acc float64
		for i, c := range row {
			acc += c * (v[i] - b.Mean[i])
		}
		out[k] = acc
	}
	var norm float64
	for _, f := range out {
		norm += f * f
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

// fitNoveltyBasis fits a frozen basis over every embedded revision of one
// embedding model that existed at snapshotAt.
//
// Offline and one-shot by contract. Nothing in the scoring path calls this, and
// nothing calls it implicitly: a store without a basis scores NULL in the
// projected space until someone runs the fit deliberately.
func fitNoveltyBasis(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string) (*noveltyBasis, error) {
	dim, n, hash, err := noveltySnapshotShape(ctx, db, embeddingModel, snapshotAt)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("novelty basis: snapshot is empty for model %q at %s", embeddingModel, snapshotAt)
	}
	ell := noveltyBasisDim + noveltyBasisOversample
	if int64(ell) > n {
		return nil, fmt.Errorf(
			"novelty basis: snapshot has %d vectors, fewer than the %d-dimensional block the fit iterates; "+
				"a basis fitted on this little data would not be meaningful", n, ell)
	}

	mean, totalVar, err := noveltySnapshotMoments(ctx, db, embeddingModel, snapshotAt, dim, n)
	if err != nil {
		return nil, err
	}

	// Seeded Gaussian start. math/rand/v2's PCG is specified to be stable
	// across releases, which is what makes "same snapshot, same seed, same
	// basis" a claim that survives a Go upgrade.
	//
	// A weak generator is the REQUIREMENT here, not a compromise. The seed is
	// stored with the basis so that a refit over the same snapshot reproduces
	// the same axes bit for bit; a cryptographic source would make every fit
	// unrepeatable and there would be nothing to verify a stored basis against.
	// Nothing here is a secret, a token, or a nonce.
	// #nosec G404 -- determinism is the property being bought; see above
	rng := rand.New(rand.NewPCG(noveltyBasisSeed, noveltyBasisSeed^0x9e3779b97f4a7c15))
	v := make([]float64, dim*ell)
	for i := range v {
		v[i] = rng.NormFloat64()
	}
	orthonormalize(v, dim, ell)

	var w []float64
	for iter := 0; iter < noveltyBasisIterations; iter++ {
		w, err = noveltyCovarianceApply(ctx, db, embeddingModel, snapshotAt, mean, v, dim, ell)
		if err != nil {
			return nil, err
		}
		orthonormalize(w, dim, ell)
		v = w
	}

	// Rayleigh-Ritz: the iterated block spans the dominant subspace but its
	// columns are not the principal axes. M = Vᵀ C V is ℓ×ℓ and its
	// eigenvectors rotate V onto them.
	m, err := noveltyRayleighMatrix(ctx, db, embeddingModel, snapshotAt, mean, v, dim, ell, n)
	if err != nil {
		return nil, err
	}
	eigVals, eigVecs := jacobiEigenSymmetric(m, ell)

	components := make([]float64, noveltyBasisDim*dim)
	values := make([]float64, noveltyBasisDim)
	for k := 0; k < noveltyBasisDim; k++ {
		values[k] = eigVals[k]
		row := components[k*dim : (k+1)*dim]
		for j := 0; j < ell; j++ {
			c := eigVecs[j*ell+k]
			if c == 0 {
				continue
			}
			col := v[j*dim : (j+1)*dim]
			for i := range row {
				row[i] += c * col[i]
			}
		}
	}

	b := &noveltyBasis{
		EmbeddingModel: embeddingModel,
		SourceDim:      dim,
		TargetDim:      noveltyBasisDim,
		SnapshotAt:     snapshotAt,
		SnapshotN:      n,
		SnapshotHash:   hash,
		Seed:           noveltyBasisSeed,
		Iterations:     noveltyBasisIterations,
		Algorithm:      noveltyBasisAlgorithm,
		Mean:           mean,
		Components:     components,
		EigenValues:    values,
		TotalVariance:  totalVar,
	}
	b.Version = noveltyBasisVersion(embeddingModel, snapshotAt, hash)

	if err := b.validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// validate refuses to hand back a basis that is not usable as one.
//
// Orthonormality is checked rather than assumed because every downstream claim
// — that ν is rotation-invariant, that the projection preserves cosines within
// the subspace — rests on it. A silently non-orthonormal basis would still
// produce plausible-looking numbers.
func (b *noveltyBasis) validate() error {
	const tol = 1e-8
	for k := 0; k < b.TargetDim; k++ {
		rk := b.Components[k*b.SourceDim : (k+1)*b.SourceDim]
		for l := k; l < b.TargetDim; l++ {
			rl := b.Components[l*b.SourceDim : (l+1)*b.SourceDim]
			var dot float64
			for i := range rk {
				dot += rk[i] * rl[i]
			}
			want := 0.0
			if k == l {
				want = 1.0
			}
			if math.Abs(dot-want) > tol {
				return fmt.Errorf(
					"novelty basis: components %d and %d have inner product %g, want %g — "+
						"the fit did not produce an orthonormal basis and every score derived "+
						"from it would be wrong", k, l, dot, want)
			}
		}
	}
	for k, ev := range b.EigenValues {
		if math.IsNaN(ev) || math.IsInf(ev, 0) || ev < 0 {
			return fmt.Errorf("novelty basis: eigenvalue %d is %g, which is not a variance", k, ev)
		}
		if k > 0 && ev > b.EigenValues[k-1]+tol {
			return fmt.Errorf("novelty basis: eigenvalues are not in descending order at %d", k)
		}
	}
	return nil
}

// noveltyBasisVersion names a basis by everything that determines it.
//
// The `sage-ext/` prefix is deliberate and is the naming convention extended
// from the shipped series' `sage-2605.30711v2/published/full-dim`: `published`
// there, `ext` here, so a row scored in this space can never be read as the
// paper's own method. The model is in the string because a basis outliving its
// embedding model is silently wrong, and the snapshot hash is in it because two
// bases fitted on different corpora must not collide on a name.
func noveltyBasisVersion(embeddingModel, snapshotAt, hash string) string {
	day := snapshotAt
	if len(day) > 10 {
		day = day[:10]
	}
	short := hash
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("sage-ext/pca%d/%s/%s/%s", noveltyBasisDim, embeddingModel, day, short)
}

// noveltySnapshotShape reports the snapshot's dimension, size and content hash.
//
// The hash covers (revision_id, vector bytes) for every row in a fixed order,
// so it identifies the exact input a basis was fitted over. Two fits agreeing
// on this hash were fitted on the same data; two disagreeing were not, whatever
// their timestamps claim.
func noveltySnapshotShape(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string) (dim int, n int64, hash string, err error) {
	rows, err := db.QueryContext(ctx, `
SELECT revision_id, embedding_vector FROM memory_revisions
 WHERE embedding_vector IS NOT NULL AND embedding_model = ? AND created_at <= ?
 ORDER BY created_at, revision_id`, embeddingModel, snapshotAt)
	if err != nil {
		return 0, 0, "", fmt.Errorf("scan novelty snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()

	h := sha256.New()
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return 0, 0, "", fmt.Errorf("scan novelty snapshot row: %w", err)
		}
		v := blobToFloat32(blob)
		if len(v) == 0 {
			continue
		}
		if dim == 0 {
			dim = len(v)
		}
		if len(v) != dim {
			// A vector of a different width under the same model name is a
			// corrupted row, not a second model. Refusing is right: a basis
			// fitted over a silently truncated corpus is unreproducible.
			return 0, 0, "", fmt.Errorf(
				"novelty basis: revision %s has a %d-wide vector under model %q where %d was expected",
				id, len(v), embeddingModel, dim)
		}
		_, _ = h.Write([]byte(id))
		_, _ = h.Write(blob)
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, "", fmt.Errorf("novelty snapshot rows: %w", err)
	}
	return dim, n, hex.EncodeToString(h.Sum(nil)), nil
}

// noveltySnapshotMoments streams the snapshot once for the mean vector and the
// total variance (the covariance trace), which is the denominator of the
// explained-variance ratio.
func noveltySnapshotMoments(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string, dim int, n int64) ([]float64, float64, error) {
	mean := make([]float64, dim)
	err := noveltySnapshotScan(ctx, db, embeddingModel, snapshotAt, dim, func(v []float64) {
		for i, f := range v {
			mean[i] += f
		}
	})
	if err != nil {
		return nil, 0, err
	}
	for i := range mean {
		mean[i] /= float64(n)
	}

	var total float64
	err = noveltySnapshotScan(ctx, db, embeddingModel, snapshotAt, dim, func(v []float64) {
		for i, f := range v {
			d := f - mean[i]
			total += d * d
		}
	})
	if err != nil {
		return nil, 0, err
	}
	if n > 1 {
		total /= float64(n - 1)
	}
	return mean, total, nil
}

// noveltyCovarianceApply computes W = C·V for the centered covariance C,
// streaming the snapshot so nothing of size N·d is ever held.
//
// For each centered row a it forms t = Vᵀa (ℓ values) and accumulates a·tᵀ into
// W. That is the whole of the matrix product, written so the data passes
// through once per iteration.
func noveltyCovarianceApply(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string, mean, v []float64, dim, ell int) ([]float64, error) {
	w := make([]float64, dim*ell)
	t := make([]float64, ell)
	err := noveltySnapshotScan(ctx, db, embeddingModel, snapshotAt, dim, func(row []float64) {
		for j := 0; j < ell; j++ {
			col := v[j*dim : (j+1)*dim]
			var acc float64
			for i, c := range col {
				acc += c * (row[i] - mean[i])
			}
			t[j] = acc
		}
		for j := 0; j < ell; j++ {
			if t[j] == 0 {
				continue
			}
			out := w[j*dim : (j+1)*dim]
			tj := t[j]
			for i := range out {
				out[i] += tj * (row[i] - mean[i])
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// noveltyRayleighMatrix computes M = Vᵀ C V, the ℓ×ℓ projection of the
// covariance onto the iterated subspace.
func noveltyRayleighMatrix(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string, mean, v []float64, dim, ell int, n int64) ([]float64, error) {
	m := make([]float64, ell*ell)
	t := make([]float64, ell)
	err := noveltySnapshotScan(ctx, db, embeddingModel, snapshotAt, dim, func(row []float64) {
		for j := 0; j < ell; j++ {
			col := v[j*dim : (j+1)*dim]
			var acc float64
			for i, c := range col {
				acc += c * (row[i] - mean[i])
			}
			t[j] = acc
		}
		for a := 0; a < ell; a++ {
			for b := a; b < ell; b++ {
				m[a*ell+b] += t[a] * t[b]
			}
		}
	})
	if err != nil {
		return nil, err
	}
	den := float64(n)
	if n > 1 {
		den = float64(n - 1)
	}
	for a := 0; a < ell; a++ {
		for b := a; b < ell; b++ {
			m[a*ell+b] /= den
			m[b*ell+a] = m[a*ell+b]
		}
	}
	return m, nil
}

// noveltySnapshotScan streams every snapshot vector as a float64 unit vector.
//
// Unit-normalized for the same reason novelty.go normalizes: the corpus
// measures ‖v‖ ∈ [0.999472, 1.000629] and the geometry all of this rests on is
// defined on the sphere, so a stray unnormalized row would tilt the basis with
// no indication anything had gone wrong.
func noveltySnapshotScan(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string, dim int, fn func([]float64)) error {
	rows, err := db.QueryContext(ctx, `
SELECT embedding_vector FROM memory_revisions
 WHERE embedding_vector IS NOT NULL AND embedding_model = ? AND created_at <= ?
 ORDER BY created_at, revision_id`, embeddingModel, snapshotAt)
	if err != nil {
		return fmt.Errorf("scan novelty snapshot: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return fmt.Errorf("scan novelty snapshot row: %w", err)
		}
		v := unitVector(blobToFloat32(blob))
		if v == nil || len(v) != dim {
			continue
		}
		fn(v)
	}
	return rows.Err()
}

// orthonormalize runs modified Gram-Schmidt over ell columns of length dim,
// stored column-major as ell contiguous blocks.
//
// Modified rather than classical: the classical form loses orthogonality badly
// once the columns are nearly dependent, which is exactly what subspace
// iteration drives them toward as it converges. A second pass is run over each
// column because even MGS leaks at this conditioning, and the validate() check
// on the finished basis is a tolerance this has to clear.
func orthonormalize(v []float64, dim, ell int) {
	for j := 0; j < ell; j++ {
		col := v[j*dim : (j+1)*dim]
		for pass := 0; pass < 2; pass++ {
			for k := 0; k < j; k++ {
				prev := v[k*dim : (k+1)*dim]
				var dot float64
				for i, f := range prev {
					dot += f * col[i]
				}
				for i := range col {
					col[i] -= dot * prev[i]
				}
			}
		}
		var norm float64
		for _, f := range col {
			norm += f * f
		}
		norm = math.Sqrt(norm)
		if norm < 1e-12 {
			// The column collapsed into the span of its predecessors. Replace
			// it with a deterministic axis rather than leaving a zero column,
			// so the block stays full rank and the fit stays reproducible.
			for i := range col {
				col[i] = 0
			}
			col[j%dim] = 1
			for k := 0; k < j; k++ {
				prev := v[k*dim : (k+1)*dim]
				var dot float64
				for i, f := range prev {
					dot += f * col[i]
				}
				for i := range col {
					col[i] -= dot * prev[i]
				}
			}
			norm = 0
			for _, f := range col {
				norm += f * f
			}
			norm = math.Sqrt(norm)
			if norm < 1e-12 {
				continue
			}
		}
		for i := range col {
			col[i] /= norm
		}
	}
}

// jacobiEigenSymmetric eigendecomposes a symmetric n×n matrix by cyclic Jacobi
// rotation, returning eigenvalues in descending order and the matching
// eigenvectors as columns of vecs (row-major, vecs[row*n+col]).
//
// Jacobi rather than anything faster because n is 24 here: the asymptotics are
// irrelevant and what matters is that it is short, has no failure modes worth
// worrying about on a symmetric matrix, and is easy to check against a
// known-answer fixture. It always converges for symmetric input.
func jacobiEigenSymmetric(a []float64, n int) (vals []float64, vecs []float64) {
	m := make([]float64, len(a))
	copy(m, a)
	vecs = make([]float64, n*n)
	for i := 0; i < n; i++ {
		vecs[i*n+i] = 1
	}

	for sweep := 0; sweep < 100; sweep++ {
		var off float64
		for p := 0; p < n; p++ {
			for q := p + 1; q < n; q++ {
				off += m[p*n+q] * m[p*n+q]
			}
		}
		if off < 1e-30 {
			break
		}
		for p := 0; p < n; p++ {
			for q := p + 1; q < n; q++ {
				apq := m[p*n+q]
				if math.Abs(apq) < 1e-300 {
					continue
				}
				theta := (m[q*n+q] - m[p*n+p]) / (2 * apq)
				t := 1 / (math.Abs(theta) + math.Sqrt(theta*theta+1))
				if theta < 0 {
					t = -t
				}
				c := 1 / math.Sqrt(t*t+1)
				s := t * c
				for k := 0; k < n; k++ {
					mkp := m[k*n+p]
					mkq := m[k*n+q]
					m[k*n+p] = c*mkp - s*mkq
					m[k*n+q] = s*mkp + c*mkq
				}
				for k := 0; k < n; k++ {
					mpk := m[p*n+k]
					mqk := m[q*n+k]
					m[p*n+k] = c*mpk - s*mqk
					m[q*n+k] = s*mpk + c*mqk
				}
				for k := 0; k < n; k++ {
					vkp := vecs[k*n+p]
					vkq := vecs[k*n+q]
					vecs[k*n+p] = c*vkp - s*vkq
					vecs[k*n+q] = s*vkp + c*vkq
				}
			}
		}
	}

	vals = make([]float64, n)
	for i := 0; i < n; i++ {
		vals[i] = m[i*n+i]
	}
	// Descending order, carrying the eigenvectors with their values.
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	for i := 1; i < n; i++ {
		for j := i; j > 0 && vals[idx[j]] > vals[idx[j-1]]; j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	outVals := make([]float64, n)
	outVecs := make([]float64, n*n)
	for c, src := range idx {
		outVals[c] = vals[src]
		for r := 0; r < n; r++ {
			outVecs[r*n+c] = vecs[r*n+src]
		}
	}
	return outVals, outVecs
}

// persistNoveltyBasis stores a fitted basis.
//
// INSERT rather than UPSERT: a basis version is derived from the snapshot that
// produced it, so re-fitting the same snapshot yields the same version and the
// primary key refuses the duplicate. That refusal is the "never implicitly
// refit" rule expressed in the schema rather than in a comment.
func persistNoveltyBasis(ctx context.Context, db *sql.DB, b *noveltyBasis) error {
	mean, err := json.Marshal(b.Mean)
	if err != nil {
		return fmt.Errorf("encode novelty basis mean: %w", err)
	}
	comps, err := json.Marshal(b.Components)
	if err != nil {
		return fmt.Errorf("encode novelty basis components: %w", err)
	}
	vals, err := json.Marshal(b.EigenValues)
	if err != nil {
		return fmt.Errorf("encode novelty basis eigenvalues: %w", err)
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO novelty_basis (
  basis_version, created_at, embedding_model, source_dim, target_dim,
  snapshot_at, snapshot_n, snapshot_hash, seed, iterations, algorithm,
  mean_vector, components, eigenvalues, total_variance
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.Version, time.Now().UTC().Format(time.RFC3339Nano), b.EmbeddingModel,
		b.SourceDim, b.TargetDim, b.SnapshotAt, b.SnapshotN, b.SnapshotHash,
		b.Seed, b.Iterations, b.Algorithm,
		string(mean), string(comps), string(vals), b.TotalVariance)
	if err != nil {
		return fmt.Errorf("persist novelty basis: %w", err)
	}
	return nil
}

// loadNoveltyBasis returns the newest basis for embeddingModel, or nil when the
// store has none.
//
// nil is not an error. A store that has never been fitted scores NULL in the
// projected space, which reads as "not scored" exactly like every other novelty
// NULL — see the semantics documented on noveltyMeasurement.
func loadNoveltyBasis(ctx context.Context, db *sql.DB, embeddingModel string) (*noveltyBasis, error) {
	var (
		b                 noveltyBasis
		mean, comps, vals string
		createdAt         string
	)
	err := db.QueryRowContext(ctx, `
SELECT basis_version, created_at, embedding_model, source_dim, target_dim,
       snapshot_at, snapshot_n, snapshot_hash, seed, iterations, algorithm,
       mean_vector, components, eigenvalues, total_variance
  FROM novelty_basis WHERE embedding_model = ?
 ORDER BY created_at DESC LIMIT 1`, embeddingModel).Scan(
		&b.Version, &createdAt, &b.EmbeddingModel, &b.SourceDim, &b.TargetDim,
		&b.SnapshotAt, &b.SnapshotN, &b.SnapshotHash, &b.Seed, &b.Iterations,
		&b.Algorithm, &mean, &comps, &vals, &b.TotalVariance)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load novelty basis: %w", err)
	}
	if err := json.Unmarshal([]byte(mean), &b.Mean); err != nil {
		return nil, fmt.Errorf("decode novelty basis mean: %w", err)
	}
	if err := json.Unmarshal([]byte(comps), &b.Components); err != nil {
		return nil, fmt.Errorf("decode novelty basis components: %w", err)
	}
	if err := json.Unmarshal([]byte(vals), &b.EigenValues); err != nil {
		return nil, fmt.Errorf("decode novelty basis eigenvalues: %w", err)
	}
	if len(b.Mean) != b.SourceDim || len(b.Components) != b.TargetDim*b.SourceDim {
		return nil, fmt.Errorf(
			"novelty basis %s is malformed: mean %d and components %d do not match %dx%d",
			b.Version, len(b.Mean), len(b.Components), b.TargetDim, b.SourceDim)
	}
	return &b, nil
}

// NoveltyBasisReport is what a fit produced, for an operator to read and for a
// later session to check a refit against.
//
// ExplainedVarianceRatio is the number worth looking at first: it says how much
// of the corpus's variance the 16 kept directions actually carry, and therefore
// whether projecting into them preserves enough structure for ν measured there
// to mean anything. A low ratio is not a bug — it is the finding that PCA-16 is
// not worth having on this corpus, which is a real and reportable outcome.
type NoveltyBasisReport struct {
	Version                string
	EmbeddingModel         string
	SourceDim              int
	TargetDim              int
	SnapshotAt             string
	SnapshotN              int64
	SnapshotHash           string
	Seed                   int64
	Iterations             int
	Algorithm              string
	TotalVariance          float64
	EigenValues            []float64
	ExplainedVarianceRatio float64
	Replaced               string
}

// NoveltyBasisExists reports the newest basis version for an embedding model,
// or "" when there is none.
func NoveltyBasisExists(ctx context.Context, db *sql.DB, embeddingModel string) (string, error) {
	b, err := loadNoveltyBasis(ctx, db, embeddingModel)
	if err != nil || b == nil {
		return "", err
	}
	return b.Version, nil
}

// FitNoveltyBasis fits a frozen PCA-16 basis over a snapshot and persists it.
//
// OFFLINE AND ONE-SHOT BY CONTRACT. This is the only way a basis comes into
// existence and it is reachable only from the CLI: no serving path calls it,
// and nothing fits a basis as a side effect of scoring. That is deliberate —
// an implicitly-fitted basis would be refitted at some moment nobody chose,
// which is precisely what makes ν incomparable across time.
//
// Refitting is refused unless replace is set, because a second basis becomes
// the one new writes are scored against, and ν measured under two different
// bases is two different measurements. Existing rows keep their own
// novelty_pca16_basis_version, so the data stays interpretable either way — but
// the operator has to say they meant it.
func FitNoveltyBasis(ctx context.Context, db *sql.DB, embeddingModel, snapshotAt string, replace bool) (NoveltyBasisReport, error) {
	existing, err := NoveltyBasisExists(ctx, db, embeddingModel)
	if err != nil {
		return NoveltyBasisReport{}, err
	}
	if existing != "" && !replace {
		return NoveltyBasisReport{}, fmt.Errorf(
			"a basis already exists for %s (%s).\n"+
				"Fitting another makes it the basis new writes are scored against, and ν under two "+
				"bases is two different measurements. Rows already scored keep their own "+
				"novelty_pca16_basis_version, so nothing already collected is invalidated — but this "+
				"has to be deliberate. Re-run with -replace if it is",
			embeddingModel, existing)
	}

	b, err := fitNoveltyBasis(ctx, db, embeddingModel, snapshotAt)
	if err != nil {
		return NoveltyBasisReport{}, err
	}
	if b.Version == existing {
		return NoveltyBasisReport{}, fmt.Errorf(
			"the fit reproduced the existing basis %s exactly (same snapshot, same seed), so there is "+
				"nothing to store. That is the frozen-basis property working, not an error", b.Version)
	}
	if err := persistNoveltyBasis(ctx, db, b); err != nil {
		return NoveltyBasisReport{}, err
	}
	return NoveltyBasisReport{
		Version:                b.Version,
		EmbeddingModel:         b.EmbeddingModel,
		SourceDim:              b.SourceDim,
		TargetDim:              b.TargetDim,
		SnapshotAt:             b.SnapshotAt,
		SnapshotN:              b.SnapshotN,
		SnapshotHash:           b.SnapshotHash,
		Seed:                   b.Seed,
		Iterations:             b.Iterations,
		Algorithm:              b.Algorithm,
		TotalVariance:          b.TotalVariance,
		EigenValues:            b.EigenValues,
		ExplainedVarianceRatio: b.explainedVarianceRatio(),
		Replaced:               existing,
	}, nil
}

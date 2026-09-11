package memory

import (
	"math"
	"math/rand/v2"
	"testing"
)

// The numeric kernels behind the frozen basis, tested against answers that are
// known independently of the code under test.
//
// These live in package memory rather than memory_test because the whole value
// of the tests is that they check the ARITHMETIC, not the behavior around it. A
// subspace-iteration bug that still produced a plausible-looking basis would be
// invisible from outside, and every ν measured in the projected space would be
// quietly wrong with nothing to show for it.

// TestJacobiEigenSymmetricKnownAnswer checks the eigensolver against a matrix
// whose spectrum can be worked out by hand.
//
// diag(a) + c·(J − I) — a constant matrix with a constant off-diagonal — has
// eigenvalue a + (n−1)c once, with the all-ones eigenvector, and a − c with
// multiplicity n−1. Here a = 2, c = 1, n = 4, so the spectrum is {5, 1, 1, 1}.
// Nothing in the implementation knows that.
func TestJacobiEigenSymmetricKnownAnswer(t *testing.T) {
	const n = 4
	m := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				m[i*n+j] = 2
			} else {
				m[i*n+j] = 1
			}
		}
	}

	vals, vecs := jacobiEigenSymmetric(m, n)
	want := []float64{5, 1, 1, 1}
	for i, w := range want {
		if math.Abs(vals[i]-w) > 1e-9 {
			t.Errorf("eigenvalue %d = %.12g, want %g — the Jacobi sweep is not converging to the "+
				"spectrum, so every basis it rotates is wrong", i, vals[i], w)
		}
	}

	// The dominant eigenvector is the all-ones direction, up to sign.
	first := make([]float64, n)
	for r := 0; r < n; r++ {
		first[r] = vecs[r*n+0]
	}
	sign := 1.0
	if first[0] < 0 {
		sign = -1
	}
	for r := 0; r < n; r++ {
		if math.Abs(sign*first[r]-0.5) > 1e-9 {
			t.Errorf("dominant eigenvector = %v, want ±(0.5,0.5,0.5,0.5)", first)
			break
		}
	}

	// Eigenvectors must be orthonormal, or the Rayleigh-Ritz rotation that
	// consumes them would destroy the orthonormality of the fitted basis.
	for a := 0; a < n; a++ {
		for b := a; b < n; b++ {
			var dot float64
			for r := 0; r < n; r++ {
				dot += vecs[r*n+a] * vecs[r*n+b]
			}
			want := 0.0
			if a == b {
				want = 1.0
			}
			if math.Abs(dot-want) > 1e-9 {
				t.Errorf("eigenvectors %d·%d = %g, want %g", a, b, dot, want)
			}
		}
	}
}

// TestOrthonormalizeHandlesADependentColumn checks the case subspace iteration
// actually drives the block toward: a column that has collapsed into the span
// of its predecessors.
//
// Left alone that column normalizes a rounding error into a unit vector
// pointing in an arbitrary direction, which is both non-deterministic and
// wrong. The replacement path has to produce something orthonormal instead.
func TestOrthonormalizeHandlesADependentColumn(t *testing.T) {
	const dim, ell = 6, 3
	v := make([]float64, dim*ell)
	// Column 0 and column 2 are the same direction; column 1 is independent.
	v[0*dim+0], v[1*dim+1] = 1, 1
	v[2*dim+0] = 1

	orthonormalize(v, dim, ell)

	for a := 0; a < ell; a++ {
		for b := a; b < ell; b++ {
			var dot float64
			for i := 0; i < dim; i++ {
				dot += v[a*dim+i] * v[b*dim+i]
			}
			want := 0.0
			if a == b {
				want = 1.0
			}
			if math.Abs(dot-want) > 1e-9 {
				t.Errorf("columns %d·%d = %g, want %g — a collapsed column was normalized from "+
					"noise instead of replaced, so the fit is not reproducible", a, b, dot, want)
			}
		}
	}
}

// TestProjectedCosinesAreRotationInvariant proves the claim the file comment
// makes and that the whole design leans on: ν in the projected space depends on
// the SUBSPACE, not on which orthonormal basis of it was chosen.
//
// If this were false, the fit would have to pin down the axes exactly for
// scores to be comparable, and every refit that produced the same subspace with
// different axes would silently change ν. It is true because a change of
// orthonormal basis is a rotation, and cosine is rotation-invariant — but
// "because the maths says so" is the kind of claim that is worth checking
// against the code that actually runs.
func TestProjectedCosinesAreRotationInvariant(t *testing.T) {
	const dim, target = 12, 4
	// Deterministic by design, exactly as the fit itself is.
	// #nosec G404 -- a fixed-seed generator is what makes this test repeatable
	rng := rand.New(rand.NewPCG(7, 11))

	comps := make([]float64, dim*target)
	for i := range comps {
		comps[i] = rng.NormFloat64()
	}
	orthonormalize(comps, dim, target)

	base := &noveltyBasis{SourceDim: dim, TargetDim: target, Mean: make([]float64, dim), Components: comps}

	// A second basis of the SAME subspace: rotate the components by a random
	// orthonormal target×target matrix.
	rot := make([]float64, target*target)
	for i := range rot {
		rot[i] = rng.NormFloat64()
	}
	orthonormalize(rot, target, target)
	rotated := make([]float64, dim*target)
	for k := 0; k < target; k++ {
		for j := 0; j < target; j++ {
			c := rot[k*target+j]
			for i := 0; i < dim; i++ {
				rotated[k*dim+i] += c * comps[j*dim+i]
			}
		}
	}
	other := &noveltyBasis{SourceDim: dim, TargetDim: target, Mean: make([]float64, dim), Components: rotated}

	cosine := func(b *noveltyBasis, x, y []float64) float64 {
		px, py := b.project(x), b.project(y)
		if px == nil || py == nil {
			t.Fatal("projection returned nil for a vector with a component in the subspace")
		}
		var d float64
		for i := range px {
			d += px[i] * py[i]
		}
		return d
	}

	for trial := 0; trial < 25; trial++ {
		x := make([]float64, dim)
		y := make([]float64, dim)
		for i := 0; i < dim; i++ {
			x[i] = rng.NormFloat64()
			y[i] = rng.NormFloat64()
		}
		a := cosine(base, x, y)
		b := cosine(other, x, y)
		if math.Abs(a-b) > 1e-9 {
			t.Fatalf("cosine under two bases of the same subspace: %.15g vs %.15g. ν is NOT "+
				"basis-independent, which means a refit that recovers the same subspace with "+
				"different axes would silently change every score", a, b)
		}
	}
}

// TestProjectIntoEqualsProject asserts the buffered and allocating projections
// agree exactly — not approximately.
//
// They are the same arithmetic written twice, so anything other than bit
// equality means one of them has drifted. The buffered form is the one the
// scope scan actually uses, so a divergence would silently change every
// projected ν while the allocating form the tests read stays correct.
func TestProjectIntoEqualsProject(t *testing.T) {
	const dim, target = 40, 8
	// #nosec G404 -- fixed seed keeps this test repeatable
	rng := rand.New(rand.NewPCG(31, 37))

	comps := make([]float64, dim*target)
	for i := range comps {
		comps[i] = rng.NormFloat64()
	}
	orthonormalize(comps, dim, target)
	mean := make([]float64, dim)
	for i := range mean {
		mean[i] = rng.NormFloat64() * 0.1
	}
	b := &noveltyBasis{SourceDim: dim, TargetDim: target, Mean: mean, Components: comps}

	buf := make([]float64, target)
	for trial := 0; trial < 50; trial++ {
		v := make([]float64, dim)
		for i := range v {
			v[i] = rng.NormFloat64()
		}
		want := b.project(v)
		ok := b.projectInto(buf, v)
		if (want == nil) != !ok {
			t.Fatalf("trial %d: project returned nil=%v but projectInto returned ok=%v",
				trial, want == nil, ok)
		}
		if want == nil {
			continue
		}
		for i := range want {
			if want[i] != buf[i] {
				t.Fatalf("trial %d component %d: project %.17g, projectInto %.17g — the buffered "+
					"path the scope scan uses has drifted from the one the tests read",
					trial, i, want[i], buf[i])
			}
		}
	}

	// A wrong-sized buffer must be refused, not written past.
	if b.projectInto(make([]float64, target-1), make([]float64, dim)) {
		t.Error("projectInto accepted an undersized destination")
	}
}

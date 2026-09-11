package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// revisionJSON renders a Revision exactly as the API surfaces would, so a test
// can assert what a caller can and cannot see on it.
func revisionJSON(t *testing.T, rev memory.Revision) string {
	t.Helper()
	b, err := json.Marshal(rev)
	if err != nil {
		t.Fatalf("marshal revision: %v", err)
	}
	return string(b)
}

// noveltyColumns is one revision's stored measurement, read back the only way
// anything reads it: directly in SQL. Every field is nullable because "not
// scored" is a state the schema represents and this suite has to assert.
type noveltyColumns struct {
	Score       sql.NullFloat64
	Top1Cosine  sql.NullFloat64
	ScopeN      sql.NullInt64
	Kappa       sql.NullFloat64
	Route       sql.NullString
	GateVersion sql.NullString
}

func readNovelty(t *testing.T, ms *memory.Store, revisionID string) noveltyColumns {
	t.Helper()
	var c noveltyColumns
	err := ms.DB().QueryRowContext(context.Background(), `
SELECT novelty_score, novelty_top1_cosine, novelty_scope_n,
       novelty_kappa, novelty_route, novelty_gate_version
  FROM memory_revisions WHERE revision_id = ?`, revisionID).
		Scan(&c.Score, &c.Top1Cosine, &c.ScopeN, &c.Kappa, &c.Route, &c.GateVersion)
	if err != nil {
		t.Fatalf("read novelty columns for %s: %v", revisionID, err)
	}
	return c
}

// axisEmbedder places each payload on a named axis of a 4-dimensional sphere
// and jitters it by a hash of its own text.
//
// Both halves are necessary. The axis is what makes novelty PREDICTABLE — two
// "alpha" payloads are near neighbors and a "gamma" is not — so a test can
// assert an ordering rather than a magic number. The jitter is what keeps the
// scope non-degenerate: identical vectors drive R̄ to 1, the concentration
// estimate becomes undefined, and the scorer correctly declines to score, which
// is exactly what the fixed-vector mockEmbedder produces and why it cannot be
// used here. Real corpora cluster; they do not collapse.
//
// The jitter is deterministic in the payload text, so the whole suite is
// reproducible.
type axisEmbedder struct{}

func (axisEmbedder) Embed(_ context.Context, text, _ string) (*embedcontracts.EmbeddingResult, error) {
	v := []float32{0, 0, 0, 0}
	switch {
	case containsMarker(text, "alpha"):
		v[0] = 1
	case containsMarker(text, "beta"):
		v[1] = 1
	case containsMarker(text, "gamma"):
		v[2] = 1
	default:
		v[3] = 1
	}
	// FNV-1a over the payload, spread across the four coordinates. Small
	// relative to the axis component, so cluster structure survives.
	h := uint32(2166136261)
	for i := 0; i < len(text); i++ {
		h ^= uint32(text[i])
		h *= 16777619
	}
	for i := range v {
		h ^= h << 13
		h ^= h >> 17
		h ^= h << 5
		v[i] += float32(int32(h%2001)-1000) / 1000 * 0.2
	}
	var norm float32
	for _, f := range v {
		norm += f * f
	}
	norm = float32(math.Sqrt(float64(norm)))
	for i := range v {
		v[i] /= norm
	}
	return &embedcontracts.EmbeddingResult{Embedding: v, TokenCount: 3}, nil
}

func (e axisEmbedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
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

func (axisEmbedder) EmbeddingDimensions(_ string) int { return 4 }

func containsMarker(text, marker string) bool {
	for i := 0; i+len(marker) <= len(text); i++ {
		if text[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}

func newAxisEmbedderStore(t *testing.T) *memory.Store {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return memory.NewStore(cs.DB(), axisEmbedder{}, "test-model", 0.85, memory.NoopQueue{})
}

func writeAndEmbed(t *testing.T, ms *memory.Store, key, summary string) memory.Revision {
	t.Helper()
	ctx := context.Background()
	in := sampleInput(key)
	in.Payload = memory.Payload{Summary: summary}
	rev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("WriteRevision(%s): %v", key, err)
	}
	if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
		t.Fatalf("EmbedRevision(%s): %v", key, err)
	}
	return rev
}

// TestNoveltyUnscoredIsNullNotZero is the answer to the question the task asked
// for: what does a write with no embedding score?
//
// 84 revisions in the live corpus carry no vector, and every revision written
// before migration 20 carries none of these columns. A novelty_score of 0 means
// "perfectly explained by the scope" — the most redundant thing in the store.
// NULL means nobody looked. Those are opposite claims about the same row, and
// the difference has to survive in the schema rather than in a comment.
func TestNoveltyUnscoredIsNullNotZero(t *testing.T) {
	ms, cleanup := newTestStore(t) // no embedder
	defer cleanup()

	rev, err := ms.WriteRevision(context.Background(), sampleInput("note.unembedded"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	c := readNovelty(t, ms, rev.RevisionID)
	if c.Score.Valid {
		t.Errorf("an unembedded revision carries novelty_score = %v; it must be NULL. A zero would "+
			"claim the revision is identical to everything in its scope, which is the opposite of "+
			"'nobody looked'", c.Score.Float64)
	}
	if c.ScopeN.Valid {
		t.Errorf("an unembedded revision carries novelty_scope_n = %v; it must be NULL, because "+
			"scope_n = 0 is the distinct and meaningful 'scored against an empty scope' case",
			c.ScopeN.Int64)
	}
	for name, v := range map[string]bool{
		"novelty_top1_cosine":  c.Top1Cosine.Valid,
		"novelty_kappa":        c.Kappa.Valid,
		"novelty_route":        c.Route.Valid,
		"novelty_gate_version": c.GateVersion.Valid,
	} {
		if v {
			t.Errorf("%s is set on an unembedded revision; not-scored must be NULL across every "+
				"novelty column, or a reader has to guess which ones to trust", name)
		}
	}
}

// TestNoveltyEmptyScopeIsDistinguishableFromUnscored pins the third state.
//
// SAGE §3.3: "When N = 0 (i.e., the memory scope is empty), the controller
// directly emits ADD without computing a score." The first write into a
// namespace has nothing to be novel against, so ν is undefined rather than
// maximal — but it HAS been scored, and a later analysis must be able to tell
// that apart from a revision nobody measured.
func TestNoveltyEmptyScopeIsDistinguishableFromUnscored(t *testing.T) {
	ms := newAxisEmbedderStore(t)
	first := writeAndEmbed(t, ms, "note.first", "alpha content")

	c := readNovelty(t, ms, first.RevisionID)
	if !c.ScopeN.Valid || c.ScopeN.Int64 != 0 {
		t.Fatalf("first write in a namespace: novelty_scope_n = %v (valid=%v), want 0 — that value "+
			"is what separates 'scored against nothing' from 'not scored'", c.ScopeN.Int64, c.ScopeN.Valid)
	}
	if c.Score.Valid {
		t.Errorf("novelty_score = %v on an empty scope; it must be NULL. With no stored direction "+
			"to be explained by, novelty is undefined, not maximal", c.Score.Float64)
	}
	if c.Route.String != "write" {
		t.Errorf("empty-scope route = %q, want %q — the paper emits ADD directly in this case",
			c.Route.String, "write")
	}
	if c.GateVersion.String == "" {
		t.Error("novelty_gate_version is empty on a scored revision; a route nobody can attribute " +
			"to a parameterisation cannot be re-interpreted later, which is the whole point of " +
			"recording it in shadow mode")
	}
}

// TestNoveltyScoresAgainstPriorScopeOnly asserts the three properties that make
// the stored value mean something: it is measured against what preceded the
// write, a near-duplicate scores lower than a fresh direction, and the
// statistics that let the gate be re-parameterised offline are present.
func TestNoveltyScoresAgainstPriorScopeOnly(t *testing.T) {
	ms := newAxisEmbedderStore(t)

	writeAndEmbed(t, ms, "note.a1", "alpha one")
	writeAndEmbed(t, ms, "note.a2", "alpha two")
	writeAndEmbed(t, ms, "note.a3", "alpha three")
	dup := writeAndEmbed(t, ms, "note.a4", "alpha four")
	fresh := writeAndEmbed(t, ms, "note.g1", "gamma one")

	dupN := readNovelty(t, ms, dup.RevisionID)
	freshN := readNovelty(t, ms, fresh.RevisionID)

	if !dupN.Score.Valid || !freshN.Score.Valid {
		t.Fatalf("both revisions must be scored; got dup valid=%v fresh valid=%v",
			dupN.Score.Valid, freshN.Score.Valid)
	}
	if dupN.Score.Float64 >= freshN.Score.Float64 {
		t.Errorf("a fourth alpha scored %v and a first gamma scored %v; the near-duplicate must be "+
			"LESS novel, or the measurement is not measuring what its name says",
			dupN.Score.Float64, freshN.Score.Float64)
	}

	// Scored against what preceded it, and only that. The fourth alpha saw
	// three; the gamma saw four.
	if dupN.ScopeN.Int64 != 3 {
		t.Errorf("novelty_scope_n = %d for the fourth write, want 3 — the scope is everything "+
			"created before the revision, which is what makes the value reproducible on an "+
			"append-only store", dupN.ScopeN.Int64)
	}
	if freshN.ScopeN.Int64 != 4 {
		t.Errorf("novelty_scope_n = %d for the fifth write, want 4", freshN.ScopeN.Int64)
	}

	// The sufficient statistics. These exist so a different (tau, delta) can be
	// evaluated against real writes later without re-embedding the corpus — the
	// published parameters were measured not to transfer to 3072 dimensions.
	if !dupN.Top1Cosine.Valid || !dupN.Kappa.Valid {
		t.Error("novelty_top1_cosine and novelty_kappa must be stored beside the score; without " +
			"them, re-deciding the gate later means replaying every write against a reconstructed " +
			"scope")
	}
	if dupN.Kappa.Float64 <= 0 {
		t.Errorf("novelty_kappa = %v, want positive; the vMF concentration is undefined at or "+
			"below zero", dupN.Kappa.Float64)
	}
	if dupN.Top1Cosine.Float64 < -1 || dupN.Top1Cosine.Float64 > 1 {
		t.Errorf("novelty_top1_cosine = %v is outside [-1, 1]; the vectors are not on the unit "+
			"sphere and every score derived from them is wrong", dupN.Top1Cosine.Float64)
	}
	if dupN.Score.Float64 < 0 || dupN.Score.Float64 > 1 {
		t.Errorf("novelty_score = %v is outside [0, 1]; the paper's Proposition (App. E) bounds "+
			"s_vMF to [-1, 1] for N >= 1, so nu = (1 - s)/2 cannot leave the unit interval",
			dupN.Score.Float64)
	}
}

// TestNoveltyScopeIsNamespaceAndDomainBounded asserts the scope matches what
// the shipped dedup gate looks at, so the shadow record and the live gate are
// answering questions about the same corpus.
//
// findSemanticMatch searches one namespace and names its domain explicitly —
// CW-20260909-0035 made that load-bearing, because recall's default corpus
// excludes the event log and a dedup check that searched it would report "no
// duplicate" every time. A novelty score measured over a different scope than
// the gate it is shadowing would be unusable evidence.
func TestNoveltyScopeIsNamespaceAndDomainBounded(t *testing.T) {
	ms := newAxisEmbedderStore(t)
	ctx := context.Background()

	writeAndEmbed(t, ms, "note.a1", "alpha one")
	writeAndEmbed(t, ms, "note.a2", "alpha two")

	// Same content, same domain, DIFFERENT namespace. It must see an empty
	// scope: the two alphas above are not in it.
	in := sampleInput("note.elsewhere")
	in.Namespace = "user/chrispian/memory/decisions"
	in.Payload = memory.Payload{Summary: "alpha three"}
	other, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if err := ms.EmbedRevision(ctx, other.RevisionID, "test-model"); err != nil {
		t.Fatalf("EmbedRevision: %v", err)
	}

	c := readNovelty(t, ms, other.RevisionID)
	if c.ScopeN.Int64 != 0 {
		t.Errorf("a write into an empty namespace saw a scope of %d; novelty is bounded by "+
			"namespace and domain, matching findSemanticMatch, so the shadow record and the gate "+
			"it shadows describe the same corpus", c.ScopeN.Int64)
	}
}

// TestNoveltyDegenerateScopeIsNotScored covers the scope every fixed-vector
// test embedder in this repository produces, and that a namespace holding one
// repeated payload would produce in production.
//
// When every stored vector points the same way, R̄ = 1 and the concentration
// estimator's denominator vanishes. Recording NULL is the honest outcome;
// recording a score derived from an infinity is not, and an unguarded
// implementation returns +Inf here rather than failing.
func TestNoveltyDegenerateScopeIsNotScored(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t) // fixed vector for every text
	defer cleanup()

	writeAndEmbedWith(t, ms, "note.d1", "one")
	second := writeAndEmbedWith(t, ms, "note.d2", "two")

	c := readNovelty(t, ms, second.RevisionID)
	if c.Score.Valid && (math.IsInf(c.Score.Float64, 0) || math.IsNaN(c.Score.Float64)) {
		t.Fatalf("novelty_score = %v on a degenerate scope; a non-finite score in the column is "+
			"worse than no score, because every downstream reader has to learn about it",
			c.Score.Float64)
	}
	if c.Score.Valid {
		t.Errorf("novelty_score = %v on a scope whose vectors are all identical; R̄ = 1 makes the "+
			"concentration estimate undefined and the revision must read as not scored",
			c.Score.Float64)
	}
}

func writeAndEmbedWith(t *testing.T, ms *memory.Store, key, summary string) memory.Revision {
	t.Helper()
	ctx := context.Background()
	in := sampleInput(key)
	in.Payload = memory.Payload{Summary: summary}
	rev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("WriteRevision(%s): %v", key, err)
	}
	if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
		t.Fatalf("EmbedRevision(%s): %v", key, err)
	}
	return rev
}

// TestNoveltyDoesNotChangeDedup is the behavioral assertion that matters most
// for this task's "nothing changes behavior" criterion, stated against the
// mechanism the gate would eventually replace.
//
// The shipped gate is findSemanticMatch: top-1 nearest neighbor against a
// fixed 0.85 threshold. Novelty is now computed on the same writes. The dedup
// verdict must be exactly what it was before novelty existed — decided by the
// cosine and the threshold, and by nothing the scorer wrote.
func TestNoveltyDoesNotChangeDedup(t *testing.T) {
	ms := newAxisEmbedderStore(t)
	ctx := context.Background()

	original := writeAndEmbed(t, ms, "note.a1", "alpha one")

	// A second alpha is a near-duplicate by construction: same axis, so its
	// cosine against the original clears 0.85 and the shipped gate fires.
	in := sampleInput("note.a1")
	in.Payload = memory.Payload{Summary: "alpha one again"}
	in.Dedup = "semantic"
	dupRev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("WriteRevision with semantic dedup: %v", err)
	}
	if dupRev.DedupMatch != original.RevisionID {
		t.Fatalf("semantic dedup matched %q, want %q — the fixture is not exercising the gate",
			dupRev.DedupMatch, original.RevisionID)
	}
	if dupRev.Supersedes != original.RevisionID {
		t.Errorf("a same-key semantic duplicate did not supersede its match (%q); novelty must "+
			"leave the shipped dedup path exactly as it found it", dupRev.Supersedes)
	}

	// A different axis must NOT match, on the same code path, with novelty
	// being computed for both.
	fresh := sampleInput("note.g1")
	fresh.Payload = memory.Payload{Summary: "gamma one"}
	fresh.Dedup = "semantic"
	freshRev, err := ms.WriteRevision(ctx, fresh)
	if err != nil {
		t.Fatalf("WriteRevision (fresh, semantic dedup): %v", err)
	}
	if freshRev.DedupMatch != "" {
		t.Errorf("a fresh direction matched %q at the 0.85 threshold; the fixed-threshold gate "+
			"still governs and novelty has no vote", freshRev.DedupMatch)
	}
}

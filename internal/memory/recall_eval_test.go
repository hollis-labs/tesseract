package memory_test

import (
	"context"
	"math"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// evalFixture pairs a query with the revision IDs that *should* rank
// highest for that query, in ideal order. Used by the recall eval
// harness to compute nDCG@10 and hit-rate@10 per ranking mode.
type evalFixture struct {
	name      string
	query     string
	namespace string
	// ideal is the ordered list of revision IDs that represent a perfect
	// ranking for this query. Metrics compare recall output against this.
	ideal []string
}

// nDCG computes normalized discounted cumulative gain at k using binary
// relevance (revision either matches the ideal set or doesn't). Returns
// 0 when the ideal set is empty. Range: [0, 1], higher is better.
func nDCG(got []memory.RecallResult, ideal []string, k int) float64 {
	if len(ideal) == 0 {
		return 0
	}
	idealSet := make(map[string]struct{}, len(ideal))
	for _, id := range ideal {
		idealSet[id] = struct{}{}
	}
	limit := k
	if limit > len(got) {
		limit = len(got)
	}
	var dcg float64
	for i := 0; i < limit; i++ {
		if _, ok := idealSet[got[i].Revision.RevisionID]; ok {
			dcg += 1.0 / math.Log2(float64(i+2))
		}
	}
	var idcg float64
	idealLimit := k
	if idealLimit > len(ideal) {
		idealLimit = len(ideal)
	}
	for i := 0; i < idealLimit; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// hitRate returns the fraction of ideal revisions that appear in the
// first k results. Range: [0, 1], higher is better.
func hitRate(got []memory.RecallResult, ideal []string, k int) float64 {
	if len(ideal) == 0 {
		return 0
	}
	idealSet := make(map[string]struct{}, len(ideal))
	for _, id := range ideal {
		idealSet[id] = struct{}{}
	}
	limit := k
	if limit > len(got) {
		limit = len(got)
	}
	var hits int
	for i := 0; i < limit; i++ {
		if _, ok := idealSet[got[i].Revision.RevisionID]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(ideal))
}

func average(vals map[string]float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var total float64
	for _, v := range vals {
		total += v
	}
	return total / float64(len(vals))
}

// seedEvalCorpus writes a deterministic set of revisions spanning
// keyword-heavy, semantic, and mixed topics. Every revision is embedded
// with the mock embedder so the cosine arm has a full candidate pool;
// topic differences show up in the BM25 arm.
func seedEvalCorpus(t *testing.T, ms *memory.Store) map[string]string {
	t.Helper()
	ctx := context.Background()
	ns := "user/eval/memory/notes"

	docs := []struct {
		key, summary, body string
	}{
		{"doc1", "FTS5 virtual tables power the BM25 arm of hybrid relevance",
			"external-content table with triggers keeping summaries and bodies in sync"},
		{"doc2", "user prefers terse output with no trailing summaries",
			"observed feedback across multiple sessions"},
		{"doc3", "reciprocal rank fusion combines dense and lexical signals",
			"smoothing constant k equal to sixty from the RRF paper"},
		{"doc4", "deterministic-first design with explicit DTO boundaries",
			"service invariants require append-only revisions"},
		{"doc5", "cosine similarity ranks embedded vectors against the query",
			"in-process vector scoring without an ANN index"},
		{"doc6", "hybrid recall flow integrates FTS5 and cosine via RRF fusion",
			"the new ranking mode multiplies RRF score by activation modifiers"},
	}

	ids := make(map[string]string, len(docs))
	for _, d := range docs {
		in := sampleInput(d.key)
		in.Namespace = ns
		in.Payload = memory.Payload{Summary: d.summary, Body: d.body}
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("seed %s: %v", d.key, err)
		}
		if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
			t.Fatalf("embed %s: %v", d.key, err)
		}
		ids[d.key] = rev.RevisionID
	}
	return ids
}

func evalFixtures(ids map[string]string, namespace string) []evalFixture {
	return []evalFixture{
		{
			name:      "exact-acronym",
			query:     "FTS5",
			namespace: namespace,
			ideal:     []string{ids["doc1"], ids["doc6"]},
		},
		{
			name:      "multi-token-semantic",
			query:     "rank fusion",
			namespace: namespace,
			ideal:     []string{ids["doc3"], ids["doc6"]},
		},
		{
			name:      "mixed-keyword-and-concept",
			query:     "cosine similarity",
			namespace: namespace,
			ideal:     []string{ids["doc5"], ids["doc6"]},
		},
	}
}

// TestRecall_HybridEval is the regression gate for hybrid relevance.
// It seeds a deterministic corpus, runs each ranking mode across three
// fixture query classes, and asserts:
//   - All nDCG@10 and hit-rate@10 values are finite and in [0, 1].
//   - Relevance mode's aggregate nDCG@10 does not regress below
//     similarity mode's aggregate.
//   - Relevance strictly outperforms similarity on at least one
//     fixture — proves the BM25 arm contributes ranking signal beyond
//     what cosine alone provides.
//
// When the mock embedder is swapped for a real semantic embedder, the
// similarity baseline will rise; the gate still holds because relevance
// fuses cosine with BM25.
func TestRecall_HybridEval(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	ids := seedEvalCorpus(t, ms)
	fixtures := evalFixtures(ids, "user/eval/memory/notes")

	type metrics struct {
		ndcg map[string]float64
		hit  map[string]float64
	}
	run := func(rank memory.Ranking) metrics {
		out := metrics{
			ndcg: map[string]float64{},
			hit:  map[string]float64{},
		}
		for _, f := range fixtures {
			results, err := ms.Recall(ctx, memory.RecallInput{
				Namespaces: []string{f.namespace},
				Ranking:    rank,
				Query:      f.query,
				Limit:      10,
			})
			if err != nil {
				t.Fatalf("recall %s/%s: %v", rank, f.name, err)
			}
			out.ndcg[f.name] = nDCG(results, f.ideal, 10)
			out.hit[f.name] = hitRate(results, f.ideal, 10)
		}
		return out
	}

	rel := run(memory.RankingRelevance)
	sim := run(memory.RankingSimilarity)

	// Sanity: all metrics finite and in [0, 1].
	for _, m := range []metrics{rel, sim} {
		for name, v := range m.ndcg {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
				t.Errorf("nDCG out of range for %s: %v", name, v)
			}
		}
		for name, v := range m.hit {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
				t.Errorf("hit-rate out of range for %s: %v", name, v)
			}
		}
	}

	// Regression gate: relevance's aggregate nDCG must not fall below
	// similarity's. Tiny epsilon handles tied floating-point cases.
	const epsilon = 1e-9
	relAvg := average(rel.ndcg)
	simAvg := average(sim.ndcg)
	if relAvg+epsilon < simAvg {
		t.Errorf("relevance aggregate nDCG regressed: relAvg=%.4f simAvg=%.4f\nrel=%v\nsim=%v",
			relAvg, simAvg, rel.ndcg, sim.ndcg)
	}

	// Strictly-better gate: relevance must outperform similarity on at
	// least one fixture — evidence the BM25 arm is contributing signal.
	var wins []string
	for name, rv := range rel.ndcg {
		if rv > sim.ndcg[name]+epsilon {
			wins = append(wins, name)
		}
	}
	if len(wins) == 0 {
		t.Errorf("relevance must strictly outperform similarity on ≥1 fixture; none did.\nrel=%v\nsim=%v",
			rel.ndcg, sim.ndcg)
	} else {
		t.Logf("relevance beats similarity on fixtures: %v", wins)
	}

	t.Logf("nDCG@10 — relevance=%.4f similarity=%.4f", relAvg, simAvg)
	t.Logf("per-fixture nDCG@10 rel=%v sim=%v", rel.ndcg, sim.ndcg)
	t.Logf("per-fixture hit@10 rel=%v sim=%v", rel.hit, sim.hit)
}

// TestRecall_HybridEval_Metrics sanity-checks the nDCG/hit-rate helpers
// against known inputs so the eval harness itself is trustworthy.
func TestRecall_HybridEval_Metrics(t *testing.T) {
	perfect := []memory.RecallResult{
		{Revision: memory.Revision{RevisionID: "a"}},
		{Revision: memory.Revision{RevisionID: "b"}},
		{Revision: memory.Revision{RevisionID: "c"}},
	}
	ideal := []string{"a", "b", "c"}
	if got := nDCG(perfect, ideal, 10); got != 1 {
		t.Errorf("perfect ranking nDCG@10 = %v, want 1", got)
	}
	if got := hitRate(perfect, ideal, 10); got != 1 {
		t.Errorf("perfect ranking hit-rate@10 = %v, want 1", got)
	}

	reversed := []memory.RecallResult{
		{Revision: memory.Revision{RevisionID: "c"}},
		{Revision: memory.Revision{RevisionID: "b"}},
		{Revision: memory.Revision{RevisionID: "a"}},
	}
	// Reversed still has all three in top 10, so hit-rate=1, nDCG≈1 too.
	if got := hitRate(reversed, ideal, 10); got != 1 {
		t.Errorf("reversed ranking hit-rate@10 = %v, want 1", got)
	}

	empty := []memory.RecallResult{}
	if got := nDCG(empty, ideal, 10); got != 0 {
		t.Errorf("empty ranking nDCG@10 = %v, want 0", got)
	}
	if got := hitRate(empty, ideal, 10); got != 0 {
		t.Errorf("empty ranking hit-rate@10 = %v, want 0", got)
	}

	// Order matters for nDCG when top-k < ideal — top hit gets full
	// weight; later hits decay. Check the decay is present.
	partial := []memory.RecallResult{
		{Revision: memory.Revision{RevisionID: "x"}}, // miss
		{Revision: memory.Revision{RevisionID: "a"}}, // hit at rank 2
	}
	got := nDCG(partial, ideal, 10)
	if got <= 0 || got >= 1 {
		t.Errorf("partial nDCG@10 should be in (0,1), got %v", got)
	}
}

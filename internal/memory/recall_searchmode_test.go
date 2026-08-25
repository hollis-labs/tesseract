package memory_test

// search_mode: hybrid | lexical | semantic (CW-20260825-0006).
//
// The fixture below is built to FAIL under each way this feature can be
// implemented wrongly, because a passing test on an inexpressive fixture is
// indistinguishable from a passing test on correct code:
//
//   - If lexical grouped the identifier's tokens as independent terms instead
//     of one adjacency-bound phrase, the decoys that scatter CW, 20260519 and
//     0032 across unrelated text would match, and the shorter ones would
//     outrank the target on bm25.
//   - If lexical applied hybrid's status/origin/confidence/recency/activation
//     modifiers, the deliberately low-confidence draft target would be pushed
//     below the canonical, high-confidence, freshly-reinforced decoys — which
//     is exactly what the hybrid assertion in the same test demonstrates.
//   - If search_mode were ignored, hybrid and lexical would return the same
//     ordering, and the two halves of the test would contradict each other.

import (
	"context"
	"errors"
	"strings"
	"testing"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const searchModeNS = "user/chrispian/memory/notes"

// ticketID is the acceptance criterion's exact-match case. It is also a MATCH
// metacharacter sequence: unicode61 splits it into three tokens, and the
// hyphens are gone by the time FTS5 sees anything.
const ticketID = "CW-20260519-0032"

// seedIdentifierCorpus writes one target carrying the ticket ID verbatim and
// four decoys that carry its three tokens scattered.
//
// The target is deliberately the WEAKEST row by every non-retrieval signal:
// draft status (0.6), observation origin (0.8), confidence 0.2, never read.
// The decoys are the strongest: canonical (1.0), feedback origin (1.3),
// confidence 1.0, and reinforced below so their activation and recency are
// high. Any ordering that consults those signals puts the target last.
func seedIdentifierCorpus(t *testing.T, ms *memory.Store) string {
	t.Helper()
	ctx := context.Background()

	target := memory.WriteInput{
		Namespace:  searchModeNS,
		MemoryKey:  "target.ticket",
		Author:     memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:searchmode",
		Origin:     memory.OriginObservation,
		Confidence: 0.2,
		Status:     memory.StatusDraft,
		Payload: memory.Payload{
			Summary: "Decision recorded under " + ticketID,
			Body:    "The lane that shipped it. " + strings.Repeat("padding words here. ", 40),
		},
	}
	rev, err := ms.WriteRevision(ctx, target)
	if err != nil {
		t.Fatalf("write target: %v", err)
	}

	// Decoys: each contains all three tokens, none contains them adjacent, and
	// each is short so bm25's length normalization favors it over the target.
	decoys := []struct{ key, summary, body string }{
		{"decoy.a", "CW status board", "20260519 was a busy day; ticket 0032 is unrelated"},
		{"decoy.b", "0032 retro", "CW planning for 20260519"},
		{"decoy.c", "20260519 notes", "CW 0032"},
		{"decoy.d", "CW", "0032 20260519"},
	}
	for _, d := range decoys {
		in := memory.WriteInput{
			Namespace:  searchModeNS,
			MemoryKey:  d.key,
			Author:     memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "manual:searchmode",
			Origin:     memory.OriginFeedback,
			Confidence: 1.0,
			Status:     memory.StatusCanonical,
			Payload:    memory.Payload{Summary: d.summary, Body: d.body},
		}
		if _, err := ms.WriteRevision(ctx, in); err != nil {
			t.Fatalf("write %s: %v", d.key, err)
		}
		// A deliberate read bumps activation, access_count and
		// last_accessed_at — the modifiers hybrid multiplies in.
		if _, err := ms.GetCurrentReinforced(ctx, searchModeNS, d.key); err != nil {
			t.Fatalf("reinforce %s: %v", d.key, err)
		}
	}
	return rev.RevisionID
}

func keysOf(results []memory.RecallResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Revision.MemoryKey
	}
	return out
}

// ACCEPTANCE: an exact ticket ID returns its memory as the top hit under
// search_mode=lexical — and hybrid's ordering for the same query is asserted
// alongside, because the claim being made is a COMPARISON.
func TestSearchModeLexical_ExactTicketIDIsTopHit(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	targetRev := seedIdentifierCorpus(t, ms)

	base := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		Query:      ticketID,
	}

	lexical := base
	lexical.SearchMode = memory.SearchModeLexical
	lex, err := ms.Recall(ctx, lexical)
	if err != nil {
		t.Fatalf("lexical recall: %v", err)
	}
	if len(lex) == 0 {
		t.Fatal("lexical recall returned nothing for an identifier that exists")
	}
	if lex[0].Revision.RevisionID != targetRev {
		t.Errorf("lexical: top hit is %q, want the revision carrying %s.\nordering: %v",
			lex[0].Revision.MemoryKey, ticketID, keysOf(lex))
	}
	// Adjacency binding is the mechanism, so assert it: the scattered decoys
	// must not be in the result set at all.
	if len(lex) != 1 {
		t.Errorf("lexical returned %d rows for an identifier present in exactly one revision: %v\n"+
			"a punctuated identifier must be matched as an adjacent phrase, not as independent terms",
			len(lex), keysOf(lex))
	}
	if lex[0].Score != nil {
		t.Errorf("lexical result carries score %v; bm25 is lower-is-better and Score is "+
			"documented as higher-is-better in every mode that populates it", *lex[0].Score)
	}

	hybrid := base // search_mode omitted — the default
	hyb, err := ms.Recall(ctx, hybrid)
	if err != nil {
		t.Fatalf("hybrid recall: %v", err)
	}
	hybRank := -1
	for i, r := range hyb {
		if r.Revision.RevisionID == targetRev {
			hybRank = i
			break
		}
	}
	t.Logf("query %q — lexical: %v | hybrid: %v (target at hybrid index %d of %d)",
		ticketID, keysOf(lex), keysOf(hyb), hybRank, len(hyb))

	// The ticket's premise. If this ever stops holding, the feature's
	// justification has changed and that is worth failing over rather than
	// quietly passing.
	if hybRank == 0 {
		t.Errorf("hybrid already ranks the exact match first, so this fixture does not "+
			"demonstrate the dilution search_mode=lexical exists to remove: %v", keysOf(hyb))
	}
	if hybRank < 0 {
		t.Errorf("hybrid did not return the target at all: %v", keysOf(hyb))
	}
}

// The tokens-vs-phrase distinction, isolated from ranking. Under hybrid the
// identifier's tokens are independent terms and the scattered decoys match;
// under lexical they must not.
func TestSearchModeLexical_BindsPunctuatedTokensAsAPhrase(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	seedIdentifierCorpus(t, ms)

	in := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		Query:      ticketID,
	}
	hyb, err := ms.Recall(ctx, in)
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	in.SearchMode = memory.SearchModeLexical
	lex, err := ms.Recall(ctx, in)
	if err != nil {
		t.Fatalf("lexical: %v", err)
	}
	if len(hyb) <= len(lex) {
		t.Fatalf("hybrid matched %d rows and lexical %d; the fixture cannot show that "+
			"phrase binding narrows the match set", len(hyb), len(lex))
	}
	for _, r := range lex {
		if !strings.Contains(r.Revision.Payload.Summary+r.Revision.Payload.Body, ticketID) {
			t.Errorf("lexical returned %q, which does not contain %s verbatim",
				r.Revision.MemoryKey, ticketID)
		}
	}
}

// Modifiers must not touch the lexical ordering. Written as an independent
// check because the acceptance test could pass on adjacency alone if the
// decoys were excluded before scoring ever ran.
func TestSearchModeLexical_IgnoresActivationModifiers(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	// Both rows carry the same term, so bm25 alone orders them by length:
	// weak (short) first. Every modifier points the other way.
	weak := memory.WriteInput{
		Namespace: searchModeNS, MemoryKey: "mod.weak",
		Author:  memory.Author{AgentID: "t", AgentVersion: "1"},
		Trigger: memory.TriggerExplicit, SessionID: "manual:mod",
		Origin: memory.OriginObservation, Confidence: 0.1, Status: memory.StatusDraft,
		Payload: memory.Payload{Summary: "xylophone", Body: ""},
	}
	strong := memory.WriteInput{
		Namespace: searchModeNS, MemoryKey: "mod.strong",
		Author:  memory.Author{AgentID: "t", AgentVersion: "1"},
		Trigger: memory.TriggerExplicit, SessionID: "manual:mod",
		Origin: memory.OriginFeedback, Confidence: 1.0, Status: memory.StatusCanonical,
		Payload: memory.Payload{Summary: "xylophone", Body: strings.Repeat("filler ", 300)},
	}
	if _, err := ms.WriteRevision(ctx, weak); err != nil {
		t.Fatalf("write weak: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, strong); err != nil {
		t.Fatalf("write strong: %v", err)
	}
	if _, err := ms.GetCurrentReinforced(ctx, searchModeNS, "mod.strong"); err != nil {
		t.Fatalf("reinforce: %v", err)
	}

	in := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		Query:      "xylophone",
	}
	hyb, err := ms.Recall(ctx, in)
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	in.SearchMode = memory.SearchModeLexical
	lex, err := ms.Recall(ctx, in)
	if err != nil {
		t.Fatalf("lexical: %v", err)
	}
	if len(hyb) != 2 || len(lex) != 2 {
		t.Fatalf("expected both rows in both modes, got hybrid %v lexical %v", keysOf(hyb), keysOf(lex))
	}
	if hyb[0].Revision.MemoryKey != "mod.strong" {
		t.Fatalf("hybrid put %q first; the fixture's modifiers are not strong enough to "+
			"distinguish modifier-weighted from raw bm25 ordering (hybrid: %v)",
			hyb[0].Revision.MemoryKey, keysOf(hyb))
	}
	if lex[0].Revision.MemoryKey != "mod.weak" {
		t.Errorf("lexical put %q first, matching hybrid's modifier-weighted order (%v); "+
			"lexical must pass the bm25 arm's ordering through untouched", lex[0].Revision.MemoryKey, keysOf(lex))
	}
}

// search_mode=semantic with no embedder must be an error, never a silent
// fallback to keyword matching under the semantic name.
func TestSearchModeSemantic_NoEmbedderIsAnErrorNotAFallback(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	seedIdentifierCorpus(t, ms)

	in := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeSemantic,
		Query:      ticketID,
	}
	got, err := ms.Recall(ctx, in)
	if err == nil {
		t.Fatalf("semantic with no embedder returned %d results instead of an error — "+
			"a caller asking for semantic received keyword results under that name: %v",
			len(got), keysOf(got))
	}
	if !errors.Is(err, memory.ErrEmbedderUnavailable) {
		t.Fatalf("expected ErrEmbedderUnavailable, got %v", err)
	}

	// Same store, same query: hybrid still answers from the BM25 arm. That
	// contrast is what makes the error above a deliberate refusal rather than
	// an incidental failure of the whole path.
	in.SearchMode = memory.SearchModeHybrid
	if _, err := ms.Recall(ctx, in); err != nil {
		t.Fatalf("hybrid must still fall back to BM25 with no embedder, got %v", err)
	}
}

// varyingEmbedder scores by token overlap so cosine ordering is meaningful,
// unlike the fixed-vector mockEmbedder which makes every similarity equal.
type varyingEmbedder struct{}

func (varyingEmbedder) Embed(_ context.Context, text, _ string) (*embedcontracts.EmbeddingResult, error) {
	// Three dimensions, one per marker word. Normalized so cosine is stable.
	var v [3]float32
	for i, marker := range []string{"alpha", "beta", "gamma"} {
		v[i] = float32(strings.Count(strings.ToLower(text), marker))
	}
	if v[0] == 0 && v[1] == 0 && v[2] == 0 {
		v[0] = 0.001
	}
	return &embedcontracts.EmbeddingResult{Embedding: v[:], TokenCount: 3}, nil
}

func (e varyingEmbedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
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

func (varyingEmbedder) EmbeddingDimensions(_ string) int { return 3 }

func newVaryingEmbedderStore(t *testing.T) (*memory.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), varyingEmbedder{}, "test-model", 0.85, memory.NoopQueue{})
	return ms, func() { _ = cs.Close() }
}

// semantic must return the cosine ordering with the cosine score attached, and
// must not be the same ordering lexical produces on the same corpus.
func TestSearchModeSemantic_ReturnsCosineOrderingWithScores(t *testing.T) {
	ms, cleanup := newVaryingEmbedderStore(t)
	defer cleanup()
	ctx := context.Background()

	// "alpha" is the query's meaning. The row that says "alpha" many times is
	// the semantic winner; the row that repeats the literal query string is
	// the lexical winner. The two orderings must therefore disagree.
	rows := []struct{ key, summary, body string }{
		{"sem.meaning", "alpha alpha alpha alpha", "concept note"},
		{"sem.literal", "quarterly report", "alpha beta beta beta gamma gamma"},
	}
	for _, r := range rows {
		in := memory.WriteInput{
			Namespace: searchModeNS, MemoryKey: r.key,
			Author:  memory.Author{AgentID: "t", AgentVersion: "1"},
			Trigger: memory.TriggerExplicit, SessionID: "manual:sem",
			Origin: memory.OriginUser, Confidence: 0.9, Status: memory.StatusCanonical,
			Payload: memory.Payload{Summary: r.summary, Body: r.body},
		}
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("write %s: %v", r.key, err)
		}
		if err := ms.EmbedRevision(ctx, rev.RevisionID, "test-model"); err != nil {
			t.Fatalf("embed %s: %v", r.key, err)
		}
	}

	in := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeSemantic,
		Query:      "alpha",
	}
	sem, err := ms.Recall(ctx, in)
	if err != nil {
		t.Fatalf("semantic: %v", err)
	}
	if len(sem) != 2 {
		t.Fatalf("expected both embedded rows, got %v", keysOf(sem))
	}
	if sem[0].Revision.MemoryKey != "sem.meaning" {
		t.Errorf("semantic put %q first; cosine should favor sem.meaning: %v",
			sem[0].Revision.MemoryKey, keysOf(sem))
	}
	// Score is a pointer precisely so a cosine of 0 or a negative one survives
	// omitempty; assert it is populated rather than assert a magnitude.
	for _, r := range sem {
		if r.Score == nil {
			t.Errorf("semantic result %q carries no score; cosine similarity is this "+
				"mode's ordering signal and must be reported", r.Revision.MemoryKey)
		}
	}

	in.SearchMode = memory.SearchModeLexical
	in.Query = "beta"
	lex, err := ms.Recall(ctx, in)
	if err != nil {
		t.Fatalf("lexical: %v", err)
	}
	if len(lex) != 1 || lex[0].Revision.MemoryKey != "sem.literal" {
		t.Errorf("lexical for 'beta' should return only sem.literal, got %v", keysOf(lex))
	}
}

// Unembedded revisions are unreachable under semantic and reachable under
// lexical. This is the property that makes the two modes complements rather
// than two spellings of one thing.
func TestSearchModeSemantic_SkipsUnembeddedThatLexicalFinds(t *testing.T) {
	ms, cleanup := newVaryingEmbedderStore(t)
	defer cleanup()
	ctx := context.Background()

	in := memory.WriteInput{
		Namespace: searchModeNS, MemoryKey: "fresh.unembedded",
		Author:  memory.Author{AgentID: "t", AgentVersion: "1"},
		Trigger: memory.TriggerExplicit, SessionID: "manual:sem",
		Origin: memory.OriginUser, Confidence: 0.9, Status: memory.StatusCanonical,
		Payload: memory.Payload{Summary: "alpha zebracrossing", Body: ""},
	}
	if _, err := ms.WriteRevision(ctx, in); err != nil {
		t.Fatalf("write: %v", err)
	}

	q := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		Query:      "zebracrossing",
		SearchMode: memory.SearchModeSemantic,
	}
	sem, err := ms.Recall(ctx, q)
	if err != nil {
		t.Fatalf("semantic: %v", err)
	}
	if len(sem) != 0 {
		t.Errorf("semantic returned an unembedded revision: %v", keysOf(sem))
	}

	q.SearchMode = memory.SearchModeLexical
	lex, err := ms.Recall(ctx, q)
	if err != nil {
		t.Fatalf("lexical: %v", err)
	}
	if len(lex) != 1 {
		t.Errorf("lexical should still reach the unembedded revision, got %v", keysOf(lex))
	}
}

func TestSearchMode_RejectsUnknownValue(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()

	_, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Query:      "anything",
		SearchMode: memory.SearchMode("keyword"),
	})
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for an unknown search_mode, got %v", err)
	}
	// The message must name the vocabulary, or a caller cannot correct a typo.
	for _, want := range memory.SearchModeVocabulary() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the accepted value %q", err, want)
		}
	}
}

// lexical and semantic name arms of the relevance pipeline. Under any other
// ranking there is nothing to select, so the combination is refused rather
// than silently ignored.
func TestSearchMode_RejectsNonRelevanceRanking(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	for _, ranking := range []memory.Ranking{
		memory.RankingActivation, memory.RankingChronological, memory.RankingSimilarity,
	} {
		for _, mode := range []memory.SearchMode{memory.SearchModeLexical, memory.SearchModeSemantic} {
			t.Run(string(ranking)+"/"+string(mode), func(t *testing.T) {
				_, err := ms.Recall(ctx, memory.RecallInput{
					Namespaces: []string{searchModeNS},
					Ranking:    ranking,
					Query:      "anything",
					SearchMode: mode,
				})
				if !errors.Is(err, memory.ErrInvalidInput) {
					t.Fatalf("expected ErrInvalidInput, got %v", err)
				}
			})
		}
		// hybrid is what an omitted argument resolves to, so it must remain
		// compatible with every ranking — otherwise every no-argument caller
		// under activation would start failing.
		t.Run(string(ranking)+"/hybrid-explicit", func(t *testing.T) {
			if _, err := ms.Recall(ctx, memory.RecallInput{
				Namespaces: []string{searchModeNS},
				Ranking:    ranking,
				Query:      "anything",
				SearchMode: memory.SearchModeHybrid,
			}); err != nil && !errors.Is(err, memory.ErrEmbedderUnavailable) {
				t.Fatalf("explicit hybrid must be accepted under ranking=%s, got %v", ranking, err)
			}
		})
	}
}

// A query that tokenizes to nothing cannot be answered lexically. Returning an
// empty page would be indistinguishable from "no such memory exists".
func TestSearchModeLexical_UntokenizableQueryIsAnError(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	seedIdentifierCorpus(t, ms)

	for _, q := range []string{"--*^:()", "日本語", "   "} {
		t.Run(q, func(t *testing.T) {
			_, err := ms.Recall(ctx, memory.RecallInput{
				Namespaces: []string{searchModeNS},
				Ranking:    memory.RankingRelevance,
				SearchMode: memory.SearchModeLexical,
				Query:      q,
			})
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput for %q, got %v", q, err)
			}
		})
	}
}

// SEAM: config default ↔ wire shape. There is deliberately no config key for
// search_mode, so the only default is the constant. A caller that passes
// nothing must get byte-identical results to one that passes hybrid, under
// every ranking the knob coexists with — otherwise adding this argument
// changed what existing callers receive.
func TestSearchMode_OmittedIsIdenticalToExplicitHybrid(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	seedIdentifierCorpus(t, ms)

	for _, ranking := range []memory.Ranking{
		"", memory.RankingRelevance, memory.RankingActivation, memory.RankingChronological,
	} {
		for _, query := range []string{"", ticketID, "CW"} {
			name := string(ranking) + "/" + query
			t.Run(name, func(t *testing.T) {
				base := memory.RecallInput{
					Namespaces: []string{searchModeNS},
					Ranking:    ranking,
					Query:      query,
				}
				omitted, errA := ms.Recall(ctx, base)
				explicit := base
				explicit.SearchMode = memory.SearchModeHybrid
				spelled, errB := ms.Recall(ctx, explicit)

				switch {
				case errA == nil && errB == nil:
				case errA != nil && errB != nil && errA.Error() == errB.Error():
					return
				default:
					t.Fatalf("omitted vs explicit hybrid disagreed on error: %v vs %v", errA, errB)
				}
				if got, want := keysOf(spelled), keysOf(omitted); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("explicit hybrid returned %v, omitted returned %v", got, want)
				}
			})
		}
	}

	// And the fingerprints must agree, or a cursor issued by one would be
	// rejected by the other.
	a := memory.RecallInput{Namespaces: []string{searchModeNS}, Query: ticketID}
	b := a
	b.SearchMode = memory.SearchModeHybrid
	if memory.RecallOrderingFingerprint(a) != memory.RecallOrderingFingerprint(b) {
		t.Error("omitted and explicit hybrid produce different ordering fingerprints; " +
			"a caller who spelled out the default on page 2 would be told the sort changed")
	}
}

// A cursor issued under one search_mode must not resume under another: the
// modes produce different candidate sets, so the offset would name a position
// in a sequence that no longer exists.
func TestSearchMode_CursorDoesNotCarryAcrossModes(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	seedIdentifierCorpus(t, ms)

	hybridIn := memory.RecallInput{
		Namespaces: []string{searchModeNS},
		Ranking:    memory.RankingRelevance,
		Query:      ticketID,
		SearchMode: memory.SearchModeHybrid,
	}
	page, err := ms.RecallPaged(ctx, hybridIn, memory.PageRequest{Limit: 1})
	if err != nil {
		t.Fatalf("hybrid page 1: %v", err)
	}
	cursor := page.Manifest.NextCursor
	if cursor == nil || *cursor == "" {
		t.Fatalf("hybrid page 1 issued no cursor; the fixture cannot test cursor carry-over "+
			"(returned %d of %d)", page.Manifest.ResultsReturned, page.Manifest.ResultsTotal)
	}

	lexicalIn := hybridIn
	lexicalIn.SearchMode = memory.SearchModeLexical
	if _, err := ms.RecallPaged(ctx, lexicalIn, memory.PageRequest{Limit: 1, Cursor: *cursor}); !errors.Is(err, memory.ErrInvalidCursor) {
		t.Fatalf("a hybrid cursor resumed under lexical must be ErrInvalidCursor, got %v", err)
	}
}

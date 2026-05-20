package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func relevanceInput(key, summary, body string) memory.WriteInput {
	in := sampleInput(key)
	in.Payload.Summary = summary
	in.Payload.Body = body
	return in
}

func TestRecall_Relevance_RequiresQuery(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()

	_, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingRelevance,
	})
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty query, got %v", err)
	}
}

func TestRecall_Relevance_BM25OnlyWithoutEmbedder(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, relevanceInput("rel.a",
		"Reciprocal rank fusion explained", "combining BM25 and dense retrieval")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, relevanceInput("rel.b",
		"unrelated memory about dinner", "pasta and red sauce")); err != nil {
		t.Fatalf("write b: %v", err)
	}

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingRelevance,
		Query:      "reciprocal",
	})
	if err != nil {
		t.Fatalf("Recall relevance (no embedder): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 BM25 match, got %d", len(got))
	}
	if got[0].Revision.MemoryKey != "rel.a" {
		t.Errorf("expected rel.a as BM25 hit, got %q", got[0].Revision.MemoryKey)
	}
}

func TestRecall_Relevance_FusesBothArms(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	// Rev A: keyword match AND embedded — wins both arms.
	revA, err := ms.WriteRevision(ctx, relevanceInput("fuse.a",
		"hybrid relevance sprint kickoff", "BM25 plus cosine"))
	if err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := ms.EmbedRevision(ctx, revA.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed a: %v", err)
	}

	// Rev B: no keyword match but embedded — appears in cosine only.
	revB, err := ms.WriteRevision(ctx, relevanceInput("fuse.b",
		"user prefers terse output", "no trailing summaries"))
	if err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := ms.EmbedRevision(ctx, revB.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed b: %v", err)
	}

	// Rev C: keyword match but NOT embedded — BM25 arm only.
	if _, err := ms.WriteRevision(ctx, relevanceInput("fuse.c",
		"hybrid recall implementation notes", "implementation details")); err != nil {
		t.Fatalf("write c: %v", err)
	}

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingRelevance,
		Query:      "hybrid",
	})
	if err != nil {
		t.Fatalf("Recall relevance: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected fused results, got empty")
	}
	if got[0].Revision.MemoryKey != "fuse.a" {
		t.Errorf("expected fuse.a (both arms) as top, got %q", got[0].Revision.MemoryKey)
	}
	// All three should be present; fuse.a outranks the others.
	keys := map[string]bool{}
	for _, r := range got {
		keys[r.Revision.MemoryKey] = true
	}
	for _, want := range []string{"fuse.a", "fuse.b", "fuse.c"} {
		if !keys[want] {
			t.Errorf("expected %s in fused results, absent", want)
		}
	}
}

func TestRecall_Relevance_AppliesStatusModifier(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	// Both revs have identical BM25-ranking text, different statuses.
	canonIn := relevanceInput("mod.canonical", "widget synchronizer", "shared text body")
	canonIn.Status = memory.StatusCanonical
	if _, err := ms.WriteRevision(ctx, canonIn); err != nil {
		t.Fatalf("write canonical: %v", err)
	}
	draftIn := relevanceInput("mod.draft", "widget synchronizer", "shared text body")
	draftIn.Status = memory.StatusDraft
	if _, err := ms.WriteRevision(ctx, draftIn); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	got, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingRelevance,
		Query:      "widget",
	})
	if err != nil {
		t.Fatalf("Recall relevance: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Revision.Status != memory.StatusCanonical {
		t.Errorf("canonical should outrank draft with identical BM25; got %s first",
			got[0].Revision.Status)
	}
}

// TestRecall_Relevance_DoesNotReinforceAccess locks in the corrected
// design: relevance recall is a search, not a deliberate read, so it
// must not bump access_count/activation. Reinforcement is reserved for
// the get paths (memory_get / memory_get_revision).
func TestRecall_Relevance_DoesNotReinforceAccess(t *testing.T) {
	ms, cleanup := newTestStoreNoEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, relevanceInput("reinforce.a",
		"reinforcement keyword exhibit", ""))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Capture access_count before.
	var beforeCount int
	if err := ms.DB().QueryRowContext(ctx,
		`SELECT access_count FROM memory_state WHERE memory_id = ?`, rev.MemoryID,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("read access_count before: %v", err)
	}

	if _, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingRelevance,
		Query:      "reinforcement",
	}); err != nil {
		t.Fatalf("Recall relevance: %v", err)
	}

	var afterCount int
	if err := ms.DB().QueryRowContext(ctx,
		`SELECT access_count FROM memory_state WHERE memory_id = ?`, rev.MemoryID,
	).Scan(&afterCount); err != nil {
		t.Fatalf("read access_count after: %v", err)
	}
	if afterCount != beforeCount {
		t.Errorf("relevance recall must not reinforce access; before=%d after=%d",
			beforeCount, afterCount)
	}
}

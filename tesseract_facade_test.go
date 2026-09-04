package tesseract_test

import (
	"context"
	"errors"
	"testing"

	tesseract "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/memory"
)

func openTestTesseract(t *testing.T) *tesseract.Tesseract {
	t.Helper()
	dir := t.TempDir()
	c, err := tesseract.Open(context.Background(), tesseract.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("tesseract.Open: %v", err)
	}
	return c
}

func TestTesseract_WriteAndRecall(t *testing.T) {
	ctx := context.Background()
	c := openTestTesseract(t)
	defer c.Close()

	rev, err := c.WriteMemory(ctx, memory.WriteInput{
		Namespace:  "user/test/memory/notes",
		MemoryKey:  "facade_test",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "facade test content", Body: "detailed body"},
	})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	if rev.RevisionID == "" {
		t.Fatal("expected non-empty revision ID")
	}

	results, err := c.RecallMemory(ctx, memory.RecallInput{
		Namespaces: []string{"user/test/memory/notes"},
		Ranking:    memory.RankingActivation,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("RecallMemory: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one recall result")
	}
	if results[0].Revision.RevisionID != rev.RevisionID {
		t.Errorf("expected revision %s, got %s", rev.RevisionID, results[0].Revision.RevisionID)
	}
}

func TestTesseract_GetCurrentAndHistory(t *testing.T) {
	ctx := context.Background()
	c := openTestTesseract(t)
	defer c.Close()

	_, err := c.WriteMemory(ctx, memory.WriteInput{
		Namespace:  "user/test/memory/notes",
		MemoryKey:  "history_test",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Tags:       []string{},
		Payload:    memory.Payload{Summary: "version 1", Body: "body v1"},
	})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}

	current, err := c.GetCurrentRevision(ctx, "user/test/memory/notes", "history_test")
	if err != nil {
		t.Fatalf("GetCurrentRevision: %v", err)
	}
	if current.Payload.Summary != "version 1" {
		t.Errorf("expected summary 'version 1', got %q", current.Payload.Summary)
	}

	history, err := c.GetRevisionHistory(ctx, "user/test/memory/notes", "history_test")
	if err != nil {
		t.Fatalf("GetRevisionHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
}

func TestTesseract_EmbedRevisionNoEmbedder(t *testing.T) {
	ctx := context.Background()
	c := openTestTesseract(t) // no embedder configured
	defer c.Close()

	err := c.EmbedRevision(ctx, "nonexistent")
	if !errors.Is(err, memory.ErrEmbedderUnavailable) {
		t.Fatalf("EmbedRevision error = %v, want memory.ErrEmbedderUnavailable", err)
	}
	if !errors.Is(err, tesseract.ErrEmbedderUnavailable) {
		t.Fatalf("EmbedRevision error = %v, want tesseract.ErrEmbedderUnavailable", err)
	}
	_, err = c.RecallMemory(ctx, memory.RecallInput{
		Namespaces: []string{"user/test/memory/notes"},
		Ranking:    memory.RankingSimilarity,
		Query:      "similarity contract",
	})
	if !errors.Is(err, memory.ErrEmbedderUnavailable) {
		t.Fatalf("similarity recall error = %v, want memory.ErrEmbedderUnavailable", err)
	}
}

func TestPublicMemoryRecallContract(t *testing.T) {
	ctx := context.Background()
	c := openTestTesseract(t)
	defer c.Close()

	in := memory.RecallInput{
		Namespaces: []string{"user/test/memory/notes"},
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeLexical,
		Query:      "facade contract",
	}
	page, err := c.MemoryStore().RecallPaged(ctx, in, memory.PageRequest{
		Limit:       1,
		PayloadMode: memory.PayloadModeSummary,
		Budget:      memory.Budget{Bytes: 4096},
	})
	if err != nil {
		t.Fatalf("RecallPaged through public facade: %v", err)
	}
	if page.Manifest.ResultsReturned != 0 {
		t.Fatalf("empty-store results_returned = %d, want 0", page.Manifest.ResultsReturned)
	}
	if got := memory.SearchModeVocabulary(); len(got) != 3 {
		t.Fatalf("SearchModeVocabulary() = %v, want three modes", got)
	}
	_ = []memory.Ranking{
		memory.RankingActivation,
		memory.RankingChronological,
		memory.RankingSimilarity,
		memory.RankingRelevance,
	}
	_ = []memory.SearchMode{
		memory.SearchModeHybrid,
		memory.SearchModeLexical,
		memory.SearchModeSemantic,
		memory.DefaultSearchMode,
	}
	_ = []memory.PayloadMode{
		memory.PayloadModeKeys,
		memory.PayloadModeSummary,
		memory.PayloadModeFull,
		memory.DefaultPayloadMode,
	}
	_ = []string{
		memory.TruncationBudgetBytes,
		memory.TruncationBudgetTokens,
		memory.TruncationLimit,
		memory.TruncationPayloadModeLimitCap,
	}
	_ = []int{
		memory.DefaultRecallLimit,
		memory.MaxRecallLimit,
		memory.MaxRecallLimitFull,
		memory.MaxHistoryLimit,
		memory.MaxTouchRevisions,
	}
	_ = memory.RecallPageResult{}
	_ = memory.PagedRecall{}
	_ = memory.PagedRevisions{}

	health := memory.PointerHealth{Status: memory.PointerHealthUnchecked}
	_ = memory.RecallResult{PointerHealth: &health}
	_ = memory.ProjectedResult{PointerHealth: &health}
	for _, status := range []memory.PointerHealthStatus{
		memory.PointerHealthResolved,
		memory.PointerHealthUnresolvable,
		memory.PointerHealthUnverifiable,
		memory.PointerHealthUnchecked,
		memory.PointerHealthNotApplicable,
	} {
		if !status.Valid() {
			t.Errorf("public pointer-health status %q is not valid", status)
		}
	}
	if got := memory.PointerHealthStatusVocabulary(); len(got) != 5 {
		t.Fatalf("PointerHealthStatusVocabulary() = %v, want five statuses", got)
	}
	touch, err := c.MemoryStore().TouchRevisions(ctx, nil)
	if err != nil {
		t.Fatalf("TouchRevisions through public facade: %v", err)
	}
	wantTouch := memory.TouchResult{NotFound: []string{}}
	if touch.Touched != wantTouch.Touched || len(touch.NotFound) != len(wantTouch.NotFound) {
		t.Fatalf("empty TouchRevisions result = %+v, want %+v", touch, wantTouch)
	}

	var reranker memory.Reranker = memory.RerankerFunc(
		func(_ context.Context, _ string, candidates []memory.Revision, _ int) ([]memory.Revision, error) {
			return candidates, nil
		},
	)
	c.MemoryStore().RegisterReranker("facade-contract", reranker)
	if memory.NewHTTPReranker(memory.HTTPRerankerConfig{}) == nil {
		t.Fatal("NewHTTPReranker returned nil")
	}

	_, err = c.RecallMemory(ctx, memory.RecallInput{
		Namespaces: []string{"user/test/memory/notes"},
		Ranking:    memory.RankingActivation,
		Reranker:   "missing",
	})
	if !errors.Is(err, memory.ErrRerankerUnavailable) {
		t.Fatalf("missing reranker error = %v, want memory.ErrRerankerUnavailable", err)
	}
}

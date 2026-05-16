package memory_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestWriteRevision_SemanticDedup_SameKey(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	ns := "user/chrispian/memory"

	// Write and embed a revision.
	rev1, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace: ns, MemoryKey: "dedup_test",
		Status:  memory.StatusDraft,
		Author:  memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger: memory.TriggerManual, SessionID: "s1",
		Origin: memory.OriginUser, Confidence: 0.9, Tags: []string{},
		Payload: memory.Payload{Summary: "original memory about Go testing"},
	})
	if err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	if err := ms.EmbedRevision(ctx, rev1.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed rev1: %v", err)
	}

	// Write similar revision with dedup enabled.
	rev2, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace: ns, MemoryKey: "dedup_test",
		Status:  memory.StatusDraft,
		Author:  memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger: memory.TriggerManual, SessionID: "s1",
		Origin: memory.OriginUser, Confidence: 0.9, Tags: []string{},
		Payload: memory.Payload{Summary: "updated memory about Go testing"},
		Dedup:   "semantic",
	})
	if err != nil {
		t.Fatalf("write rev2: %v", err)
	}

	// Same key → should auto-supersede.
	if rev2.Supersedes != rev1.RevisionID {
		t.Errorf("expected Supersedes=%s, got %s", rev1.RevisionID, rev2.Supersedes)
	}
	if rev2.DedupMatch != rev1.RevisionID {
		t.Errorf("expected DedupMatch=%s, got %s", rev1.RevisionID, rev2.DedupMatch)
	}
}

func TestWriteRevision_SemanticDedup_CrossKey(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()
	ns := "user/chrispian/memory"

	rev1, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace: ns, MemoryKey: "alpha",
		Status:  memory.StatusDraft,
		Author:  memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger: memory.TriggerManual, SessionID: "s1",
		Origin: memory.OriginUser, Confidence: 0.9, Tags: []string{},
		Payload: memory.Payload{Summary: "some content"},
	})
	if err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	if err := ms.EmbedRevision(ctx, rev1.RevisionID, "test-model"); err != nil {
		t.Fatalf("embed rev1: %v", err)
	}

	rev2, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace: ns, MemoryKey: "beta",
		Status:  memory.StatusDraft,
		Author:  memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger: memory.TriggerManual, SessionID: "s1",
		Origin: memory.OriginUser, Confidence: 0.9, Tags: []string{},
		Payload: memory.Payload{Summary: "similar content"},
		Dedup:   "semantic",
	})
	if err != nil {
		t.Fatalf("write rev2: %v", err)
	}

	// Cross-key: DedupMatch set but NOT Supersedes.
	if rev2.DedupMatch != rev1.RevisionID {
		t.Errorf("expected DedupMatch=%s, got %s", rev1.RevisionID, rev2.DedupMatch)
	}
	if rev2.Supersedes != "" {
		t.Errorf("expected empty Supersedes for cross-key dedup, got %s", rev2.Supersedes)
	}
}

func TestWriteRevision_SemanticDedup_NoMatch(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace: "user/chrispian/memory", MemoryKey: "unique",
		Status:  memory.StatusDraft,
		Author:  memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger: memory.TriggerManual, SessionID: "s1",
		Origin: memory.OriginUser, Confidence: 0.9, Tags: []string{},
		Payload: memory.Payload{Summary: "totally unique"},
		Dedup:   "semantic",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rev.DedupMatch != "" {
		t.Errorf("expected empty DedupMatch, got %s", rev.DedupMatch)
	}
}

func TestWriteRevision_NoDedup_Default(t *testing.T) {
	ms, cleanup := newTestStoreWithEmbedder(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace: "user/chrispian/memory", MemoryKey: "no_dedup",
		Status:  memory.StatusDraft,
		Author:  memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger: memory.TriggerManual, SessionID: "s1",
		Origin: memory.OriginUser, Confidence: 0.9, Tags: []string{},
		Payload: memory.Payload{Summary: "no dedup"},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rev.DedupMatch != "" {
		t.Errorf("expected empty DedupMatch, got %s", rev.DedupMatch)
	}
}

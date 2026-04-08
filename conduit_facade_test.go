package conduit_test

import (
	"context"
	"testing"

	conduit "github.com/hollis-labs/vanta-conduit"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func openTestConduit(t *testing.T) *conduit.Conduit {
	t.Helper()
	dir := t.TempDir()
	c, err := conduit.Open(context.Background(), conduit.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("conduit.Open: %v", err)
	}
	return c
}

func TestConduit_WriteAndRecall(t *testing.T) {
	ctx := context.Background()
	c := openTestConduit(t)
	defer c.Close()

	rev, err := c.WriteMemory(ctx, memory.WriteInput{
		Namespace:  "user/test/memory",
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
		Namespaces: []string{"user/test/memory"},
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

func TestConduit_GetCurrentAndHistory(t *testing.T) {
	ctx := context.Background()
	c := openTestConduit(t)
	defer c.Close()

	_, err := c.WriteMemory(ctx, memory.WriteInput{
		Namespace:  "user/test/memory",
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

	current, err := c.GetCurrentRevision(ctx, "user/test/memory", "history_test")
	if err != nil {
		t.Fatalf("GetCurrentRevision: %v", err)
	}
	if current.Payload.Summary != "version 1" {
		t.Errorf("expected summary 'version 1', got %q", current.Payload.Summary)
	}

	history, err := c.GetRevisionHistory(ctx, "user/test/memory", "history_test")
	if err != nil {
		t.Fatalf("GetRevisionHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
}

func TestConduit_EmbedRevisionNoEmbedder(t *testing.T) {
	ctx := context.Background()
	c := openTestConduit(t) // no embedder configured
	defer c.Close()

	err := c.EmbedRevision(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error with no embedder")
	}
}

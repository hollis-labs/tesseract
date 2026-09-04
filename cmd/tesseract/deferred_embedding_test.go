package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestMemorySubsystemWiresLiveDeferredEmbedding fails if production ever
// falls back to a queue that discards embed jobs. setupMemorySubsystem always
// builds a real go-queue adapter today; nothing checked it, so a regression to
// NoopQueue (or to a nil queue, which NewStore silently coalesces to
// NoopQueue) would have committed revisions that are never embedded with no
// visible symptom. CW-20260826-0018.
func TestMemorySubsystemWiresLiveDeferredEmbedding(t *testing.T) {
	layout := hermeticLayout(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mem, err := setupMemorySubsystem(context.Background(), store, nil, layout, config.Defaults())
	if err != nil {
		t.Fatalf("setup memory subsystem: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	status := mem.Store.DeferredEmbeddingStatus()
	if !status.Enabled {
		t.Fatalf("production memory subsystem reports deferred embedding disabled (queue=%s); "+
			"writes would commit unembedded", status.Queue)
	}
	if strings.Contains(status.Queue, "NoopQueue") {
		t.Fatalf("production memory subsystem wired %s", status.Queue)
	}

	// A write through the production store must actually reach the queue.
	rev, err := mem.Store.WriteRevision(context.Background(), memory.WriteInput{
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "wiring.deferred_embedding",
		Author:     memory.Author{AgentID: "wiring-test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:wiring",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Payload:    memory.Payload{Summary: "deferred embedding wiring probe"},
	})
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if rev.RevisionID == "" {
		t.Fatal("WriteRevision returned an empty revision id")
	}
	if got := mem.Store.DeferredEmbeddingStatus().EnqueueFailures; got != 0 {
		t.Fatalf("EnqueueFailures = %d after a production write, want 0", got)
	}
}

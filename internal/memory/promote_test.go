package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// sessionInput returns a WriteInput rooted in a session-scoped namespace.
func sessionInput(key string) memory.WriteInput {
	return memory.WriteInput{
		Namespace:  "user/chrispian/session/sess123/memory",
		MemoryKey:  key,
		Author:     memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "manual:01HXXXXX",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Payload: memory.Payload{
			Summary: "User prefers terse output",
			Body:    "**Why:** repeated feedback. **How to apply:** no trailing summaries.",
		},
	}
}

func TestPromote_SessionToUser(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Write a memory in a session-scoped namespace.
	srcRev, err := ms.WriteRevision(context.Background(), sessionInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	promoted, err := ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/session/sess123/memory",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/memory",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if promoted.Namespace != "user/chrispian/memory" {
		t.Fatalf("expected namespace=%s, got %s", "user/chrispian/memory", promoted.Namespace)
	}
	if promoted.Trigger != memory.TriggerPromotion {
		t.Fatalf("expected trigger=promotion, got %s", promoted.Trigger)
	}
	if promoted.Status != memory.StatusReviewed {
		t.Fatalf("expected status=reviewed, got %s", promoted.Status)
	}
	if promoted.Payload.Summary != srcRev.Payload.Summary {
		t.Fatalf("expected payload.summary=%q, got %q", srcRev.Payload.Summary, promoted.Payload.Summary)
	}
}

func TestPromote_SessionToProject(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	srcRev, err := ms.WriteRevision(context.Background(), sessionInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	promoted, err := ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/session/sess123/memory",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/project/conduit/memory",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if promoted.Namespace != "user/chrispian/project/conduit/memory" {
		t.Fatalf("expected project namespace, got %s", promoted.Namespace)
	}
	if promoted.Trigger != memory.TriggerPromotion {
		t.Fatalf("expected trigger=promotion, got %s", promoted.Trigger)
	}
}

func TestPromote_SupersedesExistingKey(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// First write a revision in the target namespace (user scope).
	targetRev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision target: %v", err)
	}

	// Now write a session-scoped revision with the same key.
	srcRev, err := ms.WriteRevision(context.Background(), sessionInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision session: %v", err)
	}

	promoted, err := ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/session/sess123/memory",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/memory",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// The promoted revision should supersede the previous one in the target.
	if promoted.Supersedes != targetRev.RevisionID {
		t.Fatalf("expected supersedes=%s, got %s", targetRev.RevisionID, promoted.Supersedes)
	}
}

func TestPromote_KeylessMemory(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Write a keyless memory in session namespace.
	srcRev, err := ms.WriteRevision(context.Background(), sessionInput(""))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	promoted, err := ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/session/sess123/memory",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/memory",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("Promote keyless: %v", err)
	}

	if promoted.MemoryKey != "" {
		t.Fatalf("expected empty memory_key, got %q", promoted.MemoryKey)
	}
	if promoted.Trigger != memory.TriggerPromotion {
		t.Fatalf("expected trigger=promotion, got %s", promoted.Trigger)
	}
}

func TestDeprecate_UpdatesCurrentRevision(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Write two revisions for the same key.
	rev1, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision rev1: %v", err)
	}
	rev2, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision rev2: %v", err)
	}

	// Deprecate the current revision (rev2).
	err = ms.Deprecate(context.Background(), rev2.RevisionID)
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}

	// GetCurrent should now return rev1 (the previous non-deprecated revision).
	current, err := ms.GetCurrent(context.Background(), "user/chrispian/memory", "prefs.output_style")
	if err != nil {
		t.Fatalf("GetCurrent after deprecate: %v", err)
	}
	if current.RevisionID != rev1.RevisionID {
		t.Fatalf("expected current=%s (rev1), got %s", rev1.RevisionID, current.RevisionID)
	}
}

func TestDeprecate_Idempotent(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	if err := ms.Deprecate(context.Background(), rev.RevisionID); err != nil {
		t.Fatalf("first Deprecate: %v", err)
	}
	if err := ms.Deprecate(context.Background(), rev.RevisionID); err != nil {
		t.Fatalf("second Deprecate (idempotent): %v", err)
	}
}

func TestPromote_RejectsNonSessionSource(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Write a memory in a user-scoped namespace (not session).
	srcRev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	_, err = ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/memory", // user scope, not session
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/project/conduit/memory",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err == nil {
		t.Fatal("expected error for non-session source, got nil")
	}
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

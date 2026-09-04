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
		Namespace:  "user/chrispian/session/sess123/memory/notes",
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
		SourceNamespace: "user/chrispian/session/sess123/memory/notes",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/memory/notes",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if promoted.Namespace != "user/chrispian/memory/notes" {
		t.Fatalf("expected namespace=%s, got %s", "user/chrispian/memory/notes", promoted.Namespace)
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
		SourceNamespace: "user/chrispian/session/sess123/memory/notes",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/project/tesseract/memory/notes",
		ActorAgentID:    "test-agent",
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	if promoted.Namespace != "user/chrispian/project/tesseract/memory/notes" {
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
		SourceNamespace: "user/chrispian/session/sess123/memory/notes",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/memory/notes",
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
		SourceNamespace: "user/chrispian/session/sess123/memory/notes",
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/memory/notes",
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
	ctx := context.Background()

	// Write three revisions for the same key.
	rev1, err := ms.WriteRevision(ctx, sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision rev1: %v", err)
	}
	rev2, err := ms.WriteRevision(ctx, sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision rev2: %v", err)
	}
	rev3, err := ms.WriteRevision(ctx, sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision rev3: %v", err)
	}

	// Use the fixed-width versions of the prefix-shaped timestamps that
	// RFC3339Nano used to invert under SQLite TEXT ordering.
	for revisionID, stamp := range map[string]string{
		rev1.RevisionID: "2026-09-04T13:34:55.092340000Z",
		rev2.RevisionID: "2026-09-04T13:34:55.092342000Z",
		rev3.RevisionID: "2026-09-04T13:34:55.092343000Z",
	} {
		if _, updateErr := ms.DB().ExecContext(ctx,
			`UPDATE memory_revisions SET created_at = ? WHERE revision_id = ?`,
			stamp, revisionID); updateErr != nil {
			t.Fatalf("set timestamp for %s: %v", revisionID, updateErr)
		}
	}

	// Deprecate the current revision (rev3).
	err = ms.Deprecate(ctx, rev3.RevisionID)
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}

	// GetCurrent should now return rev2 (the previous non-deprecated revision).
	current, err := ms.GetCurrent(ctx, "user/chrispian/memory/notes", "prefs.output_style")
	if err != nil {
		t.Fatalf("GetCurrent after deprecate: %v", err)
	}
	if current.RevisionID != rev2.RevisionID {
		t.Fatalf("expected current=%s (rev2), got %s", rev2.RevisionID, current.RevisionID)
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

// TestPromote_PreservesType verifies CW-20260519-0031: promoting a typed
// session memory lands in the same {type} under the target scope, and
// cross-type promotion is rejected as a scope-change-with-reclassification
// (a different operation).
func TestPromote_PreservesType(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	// Seed a session-scoped 'decisions' memory.
	in := sampleInput("decisions.session.one")
	in.Namespace = "user/chrispian/session/sess-decisions/memory/decisions"
	srcRev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("seed decisions: %v", err)
	}

	t.Run("matching type promotes", func(t *testing.T) {
		promoted, err := ms.Promote(context.Background(), memory.PromoteInput{
			SourceNamespace: "user/chrispian/session/sess-decisions/memory/decisions",
			SourceMemoryID:  srcRev.MemoryID,
			TargetNamespace: "user/chrispian/memory/decisions",
			ActorAgentID:    "test-agent",
		})
		if err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if promoted.Namespace != "user/chrispian/memory/decisions" {
			t.Errorf("namespace = %s, want user/chrispian/memory/decisions", promoted.Namespace)
		}
	})

	t.Run("cross-type promotion rejected", func(t *testing.T) {
		// Seed another session decisions memory to avoid reusing the just-deprecated source.
		in2 := sampleInput("decisions.session.two")
		in2.Namespace = "user/chrispian/session/sess-decisions/memory/decisions"
		srcRev2, err := ms.WriteRevision(context.Background(), in2)
		if err != nil {
			t.Fatalf("seed decisions 2: %v", err)
		}
		_, err = ms.Promote(context.Background(), memory.PromoteInput{
			SourceNamespace: "user/chrispian/session/sess-decisions/memory/decisions",
			SourceMemoryID:  srcRev2.MemoryID,
			TargetNamespace: "user/chrispian/memory/notes", // type mismatch
			ActorAgentID:    "test-agent",
		})
		if err == nil {
			t.Fatal("expected error for cross-type promotion, got nil")
		}
		if !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("expected ErrInvalidInput, got %v", err)
		}
	})
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
		SourceNamespace: "user/chrispian/memory/notes", // user scope, not session
		SourceMemoryID:  srcRev.MemoryID,
		TargetNamespace: "user/chrispian/project/tesseract/memory/notes",
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

package integration

// TestMemoryEndToEnd exercises the full memory subsystem through the Store:
// write → read → recall → promote → deprecate using a real SQLite database.

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/memory"
)

func newMemoryStore(t *testing.T) (*memory.Store, func()) {
	t.Helper()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, memory.NoopQueue{})
	return ms, func() { _ = cs.Close() }
}

func TestMemoryEndToEnd(t *testing.T) {
	ctx := context.Background()
	ms, cleanup := newMemoryStore(t)
	defer cleanup()

	const (
		userNS    = "user/chrispian/memory"
		sessionNS = "user/chrispian/session/manual:test01/memory"
		memKey    = "prefs.output_style"
		agentID   = "test-agent"
		sessionID = "manual:test01"
	)

	author := memory.Author{AgentID: agentID, AgentVersion: "1.0"}

	// ── Step 1: Write first revision ────────────────────────────────────────────
	rev1, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  userNS,
		MemoryKey:  memKey,
		Author:     author,
		Trigger:    memory.TriggerExplicit,
		SessionID:  sessionID,
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Payload: memory.Payload{
			Summary: "User prefers terse output",
			Body:    "No trailing summaries.",
		},
	})
	if err != nil {
		t.Fatalf("step1: WriteRevision: %v", err)
	}
	if rev1.RevisionID == "" {
		t.Fatal("step1: got empty RevisionID")
	}
	t.Logf("step1: wrote revision %s", rev1.RevisionID)

	// ── Step 2: GetCurrent returns the written revision ──────────────────────────
	cur, err := ms.GetCurrent(ctx, userNS, memKey)
	if err != nil {
		t.Fatalf("step2: GetCurrent: %v", err)
	}
	if cur.RevisionID != rev1.RevisionID {
		t.Errorf("step2: GetCurrent revision_id = %s, want %s", cur.RevisionID, rev1.RevisionID)
	}
	if cur.Payload.Summary != "User prefers terse output" {
		t.Errorf("step2: payload summary = %q, want %q", cur.Payload.Summary, "User prefers terse output")
	}
	t.Logf("step2: GetCurrent OK, revision=%s", cur.RevisionID)

	// ── Step 3: Recall returns the revision ─────────────────────────────────────
	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces:    []string{userNS},
		RevisionScope: memory.RevisionScopeCurrent,
		Ranking:       memory.RankingActivation,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("step3: Recall: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("step3: Recall returned no results")
	}
	found := false
	for _, r := range results {
		if r.Revision.RevisionID == rev1.RevisionID {
			found = true
		}
	}
	if !found {
		t.Errorf("step3: Recall did not return rev1 (%s)", rev1.RevisionID)
	}
	t.Logf("step3: Recall returned %d result(s), rev1 present", len(results))

	// ── Step 4: Write second revision (supersedes first) → first auto-deprecated ─
	rev2, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  userNS,
		MemoryKey:  memKey,
		Supersedes: rev1.RevisionID,
		Author:     author,
		Trigger:    memory.TriggerExplicit,
		SessionID:  sessionID,
		Origin:     memory.OriginUser,
		Confidence: 0.95,
		Status:     memory.StatusReviewed,
		Payload: memory.Payload{
			Summary: "User prefers terse output — updated",
			Body:    "No trailing summaries. Use bullet points.",
		},
	})
	if err != nil {
		t.Fatalf("step4: WriteRevision(supersedes): %v", err)
	}
	t.Logf("step4: wrote superseding revision %s", rev2.RevisionID)

	// Verify rev1 is now deprecated.
	deprecated, err := ms.GetRevisionByID(ctx, rev1.RevisionID)
	if err != nil {
		t.Fatalf("step4: GetRevisionByID(rev1): %v", err)
	}
	if deprecated.Status != memory.StatusDeprecated {
		t.Errorf("step4: rev1 status = %s, want deprecated", deprecated.Status)
	}
	t.Logf("step4: rev1 auto-deprecated OK")

	// ── Step 5: Recall returns only rev2 (non-deprecated) ───────────────────────
	results2, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces:    []string{userNS},
		RevisionScope: memory.RevisionScopeCurrent,
		Ranking:       memory.RankingActivation,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("step5: Recall: %v", err)
	}
	for _, r := range results2 {
		if r.Revision.RevisionID == rev1.RevisionID {
			t.Errorf("step5: Recall returned deprecated rev1 — should be excluded")
		}
	}
	found2 := false
	for _, r := range results2 {
		if r.Revision.RevisionID == rev2.RevisionID {
			found2 = true
		}
	}
	if !found2 {
		t.Errorf("step5: Recall did not return rev2 (%s)", rev2.RevisionID)
	}
	t.Logf("step5: Recall after supersedes returned %d result(s), rev2 present, rev1 absent", len(results2))

	// ── Step 6: GetHistory contains both revisions with correct statuses ─────────
	history, err := ms.GetHistory(ctx, userNS, memKey)
	if err != nil {
		t.Fatalf("step6: GetHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("step6: GetHistory len = %d, want 2", len(history))
	}
	// Newest first.
	if history[0].RevisionID != rev2.RevisionID {
		t.Errorf("step6: history[0] = %s, want %s", history[0].RevisionID, rev2.RevisionID)
	}
	if history[1].RevisionID != rev1.RevisionID {
		t.Errorf("step6: history[1] = %s, want %s", history[1].RevisionID, rev1.RevisionID)
	}
	if history[1].Status != memory.StatusDeprecated {
		t.Errorf("step6: history[1].Status = %s, want deprecated", history[1].Status)
	}
	t.Logf("step6: GetHistory OK — 2 revisions, statuses correct")

	// ── Step 7: Write session-scoped memory ──────────────────────────────────────
	sessionRev, err := ms.WriteRevision(ctx, memory.WriteInput{
		Namespace:  sessionNS,
		MemoryKey:  "session.insight",
		Author:     author,
		Trigger:    memory.TriggerExplicit,
		SessionID:  sessionID,
		Origin:     memory.OriginObservation,
		Confidence: 0.8,
		Status:     memory.StatusDraft,
		Payload: memory.Payload{
			Summary: "User mentioned deadline pressure during this session",
		},
	})
	if err != nil {
		t.Fatalf("step7: WriteRevision(session): %v", err)
	}
	t.Logf("step7: wrote session-scoped revision %s", sessionRev.RevisionID)

	// ── Step 8: Promote session → user scope ─────────────────────────────────────
	promoted, err := ms.Promote(ctx, memory.PromoteInput{
		SourceNamespace: sessionNS,
		SourceMemoryID:  sessionRev.MemoryID,
		TargetNamespace: userNS,
		ActorAgentID:    agentID,
		ActorVersion:    "1.0",
	})
	if err != nil {
		t.Fatalf("step8: Promote: %v", err)
	}
	if promoted.Namespace != userNS {
		t.Errorf("step8: promoted.Namespace = %s, want %s", promoted.Namespace, userNS)
	}
	if promoted.Status != memory.StatusReviewed {
		t.Errorf("step8: promoted.Status = %s, want reviewed", promoted.Status)
	}
	// Source revision should be deprecated.
	srcRev, err := ms.GetRevisionByID(ctx, sessionRev.RevisionID)
	if err != nil {
		t.Fatalf("step8: GetRevisionByID(sessionRev): %v", err)
	}
	if srcRev.Status != memory.StatusDeprecated {
		t.Errorf("step8: source revision status = %s, want deprecated", srcRev.Status)
	}
	t.Logf("step8: Promote OK — source deprecated, promoted revision %s in %s", promoted.RevisionID, promoted.Namespace)

	// ── Step 9: Deprecate promoted revision → GetCurrent returns ErrNotFound ─────
	err = ms.Deprecate(ctx, promoted.RevisionID)
	if err != nil {
		t.Fatalf("step9: Deprecate: %v", err)
	}
	_, err = ms.GetCurrent(ctx, userNS, "session.insight")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("step9: GetCurrent after deprecate: got %v, want ErrNotFound", err)
	}
	t.Logf("step9: Deprecate OK — GetCurrent returns ErrNotFound as expected")

	// ── Step 10: Deprecate is idempotent ─────────────────────────────────────────
	err = ms.Deprecate(ctx, promoted.RevisionID)
	if err != nil {
		t.Fatalf("step10: second Deprecate should be idempotent, got %v", err)
	}
	t.Logf("step10: idempotent Deprecate OK")
}

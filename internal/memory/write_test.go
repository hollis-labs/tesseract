package memory_test

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/memory"
)

func newTestStore(t *testing.T) (*memory.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, memory.NoopQueue{})
	cleanup := func() { _ = cs.Close() }
	return ms, cleanup
}

func sampleInput(key string) memory.WriteInput {
	return memory.WriteInput{
		Namespace:  "user/chrispian/memory",
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

func TestWriteRevision_KeyedCreatesLogicalMemory(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if rev.RevisionID == "" {
		t.Fatal("expected non-empty revision_id")
	}
	if rev.MemoryID == "" {
		t.Fatal("expected non-empty memory_id")
	}

	// Verify state row exists and points to this revision.
	state, err := ms.GetState(context.Background(), rev.MemoryID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.CurrentRevision != rev.RevisionID {
		t.Fatalf("expected current_revision=%s, got %s", rev.RevisionID, state.CurrentRevision)
	}
	if state.Activation != 1.0 {
		t.Fatalf("expected activation=1.0, got %f", state.Activation)
	}
}

func TestWriteRevision_KeyedSecondWriteAppendsRevision(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	key := "prefs.output_style"
	rev1, err := ms.WriteRevision(context.Background(), sampleInput(key))
	if err != nil {
		t.Fatalf("first WriteRevision: %v", err)
	}

	rev2, err := ms.WriteRevision(context.Background(), sampleInput(key))
	if err != nil {
		t.Fatalf("second WriteRevision: %v", err)
	}

	if rev1.MemoryID != rev2.MemoryID {
		t.Fatalf("expected same memory_id, got %s and %s", rev1.MemoryID, rev2.MemoryID)
	}
	if rev1.RevisionID == rev2.RevisionID {
		t.Fatal("expected distinct revision_ids")
	}

	state, err := ms.GetState(context.Background(), rev1.MemoryID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.CurrentRevision != rev2.RevisionID {
		t.Fatalf("expected current_revision=%s (latest), got %s", rev2.RevisionID, state.CurrentRevision)
	}
}

func TestWriteRevision_KeylessAlwaysCreatesNewMemory(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev1, err := ms.WriteRevision(context.Background(), sampleInput(""))
	if err != nil {
		t.Fatalf("first keyless WriteRevision: %v", err)
	}

	rev2, err := ms.WriteRevision(context.Background(), sampleInput(""))
	if err != nil {
		t.Fatalf("second keyless WriteRevision: %v", err)
	}

	if rev1.MemoryID == rev2.MemoryID {
		t.Fatalf("expected distinct memory_ids for keyless writes, both got %s", rev1.MemoryID)
	}
}

func TestWriteRevision_SupersedesAutoDeprecates(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev1, err := ms.WriteRevision(context.Background(), sampleInput("prefs.output_style"))
	if err != nil {
		t.Fatalf("first WriteRevision: %v", err)
	}

	in2 := sampleInput("prefs.output_style")
	in2.Supersedes = rev1.RevisionID
	rev2, err := ms.WriteRevision(context.Background(), in2)
	if err != nil {
		t.Fatalf("second WriteRevision with supersedes: %v", err)
	}
	_ = rev2

	// Read rev1 back and check its status is deprecated.
	gotRev1, err := ms.GetRevisionByID(context.Background(), rev1.RevisionID)
	if err != nil {
		t.Fatalf("GetRevision for rev1: %v", err)
	}
	if gotRev1.Status != memory.StatusDeprecated {
		t.Fatalf("expected rev1 status=%s, got %s", memory.StatusDeprecated, gotRev1.Status)
	}
}

func TestWriteRevision_ValidatesRequiredFields(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	tests := []struct {
		name  string
		tweak func(*memory.WriteInput)
	}{
		{"missing namespace", func(in *memory.WriteInput) { in.Namespace = "" }},
		{"invalid namespace", func(in *memory.WriteInput) { in.Namespace = "bad/namespace" }},
		{"invalid key", func(in *memory.WriteInput) { in.MemoryKey = "UPPER.case" }},
		{"missing session_id", func(in *memory.WriteInput) { in.SessionID = "" }},
		{"missing author", func(in *memory.WriteInput) { in.Author = memory.Author{} }},
		{"missing origin", func(in *memory.WriteInput) { in.Origin = "" }},
		{"invalid origin", func(in *memory.WriteInput) { in.Origin = "bogus" }},
		{"missing trigger", func(in *memory.WriteInput) { in.Trigger = "" }},
		{"invalid trigger", func(in *memory.WriteInput) { in.Trigger = "bogus" }},
		{"confidence too high", func(in *memory.WriteInput) { in.Confidence = 1.5 }},
		{"confidence negative", func(in *memory.WriteInput) { in.Confidence = -0.1 }},
		{"empty summary", func(in *memory.WriteInput) { in.Payload.Summary = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := sampleInput("prefs.test")
			tc.tweak(&in)
			_, err := ms.WriteRevision(context.Background(), in)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestOnlyDeprecationPathMutatesRevisionStatus(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	re := regexp.MustCompile(`UPDATE\s+memory_revisions\s+SET\s+status`)
	var matches []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name) //nolint:gosec // scanning own package source; path is from ReadDir
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		if re.Match(data) {
			matches = append(matches, name)
		}
	}

	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 file with UPDATE memory_revisions SET status, got %d: %v", len(matches), matches)
	}
	if matches[0] != "write.go" {
		t.Fatalf("expected the mutation to live in write.go, found it in %s", matches[0])
	}
}

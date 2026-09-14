package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func runMigrateCorpusCapture(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	code := runMigrateCorpus(context.Background(), "", args, stdoutW, stderrW)
	_ = stdoutW.Close()
	_ = stderrW.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	_, _ = stdoutBuf.ReadFrom(stdoutR)
	_, _ = stderrBuf.ReadFrom(stderrR)
	_ = stdoutR.Close()
	_ = stderrR.Close()

	return code, stdoutBuf.String(), stderrBuf.String()
}

func TestMigrateCorpusRequiresDB(t *testing.T) {
	code, stdout, stderr := runMigrateCorpusCapture(t, []string{})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing --db, got %d. stdout: %s, stderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--db is required") {
		t.Fatalf("expected error message to mention --db is required, got: %s", stderr)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout on error, got: %s", stdout)
	}
}

func TestMigrateCorpusDryRun(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	now := time.Now().UTC()
	_, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/session/s123/memory/notes",
		MemoryKey:   "item.1",
		Summary:     "test summary",
		Body:        "test body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "s123",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/cairn/adr",
		MemoryKey:   "cairn.adr",
		Summary:     "cairn adr",
		Body:        "body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "s123",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMigrateCorpusCapture(t, []string{"-db", dbPath})
	if code != 0 {
		t.Fatalf("expected exit code 0 for dry-run, got %d. stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "dry-run; pass --apply") {
		t.Errorf("expected dry run message in stdout, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Total namespaces in store:") {
		t.Errorf("expected total namespaces in stdout, got: %s", stdout)
	}
}

func TestMigrateCorpusApplyAndJSON(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	now := time.Now().UTC()
	_, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/session-close/tesseract",
		MemoryKey:   "close.key",
		Summary:     "closed",
		Body:        "body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "s123",
		Facets: memory.Facets{
			Kind:    "session_close",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMigrateCorpusCapture(t, []string{"-db", dbPath, "-apply", "-json"})
	if code != 0 {
		t.Fatalf("expected exit code 0 for apply, got %d. stderr: %s", code, stderr)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, stdout)
	}

	receipt, ok := out["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("expected receipt in JSON output: %+v", out)
	}
	if promoted, ok := receipt["promoted_records"].(float64); !ok || promoted < 1 {
		t.Errorf("expected at least 1 promoted record, got %v", receipt["promoted_records"])
	}
}

func TestMigrateCorpusCollision(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	wsStore := workspace.NewStore(cs.DB())

	now := time.Now().UTC()
	// Pre-occupy target workspace key
	_, err := wsStore.Create(ctx, workspace.CreateInput{
		Namespace: "project/tesseract/workspace/handoff",
		Key:       "handoff_key",
		Summary:   "pre-existing",
		Author:    memory.Author{AgentID: "agent"},
		SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create conflicting source item
	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/handoff/tesseract",
		MemoryKey:   "handoff_key",
		Summary:     "conflict",
		Body:        "body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "s1",
		Facets: memory.Facets{
			Kind:    "handoff",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runMigrateCorpusCapture(t, []string{"-db", dbPath, "-apply"})
	if code != 2 {
		t.Fatalf("expected exit code 2 on collision, got %d. stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "collision") {
		t.Errorf("expected collision in stderr, got: %s", stderr)
	}
}

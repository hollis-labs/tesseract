package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func runPromoteRecordCapture(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	code := runPromoteRecord(context.Background(), "", args, stdoutW, stderrW)
	_ = stdoutW.Close()
	_ = stderrW.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	_, _ = stdoutBuf.ReadFrom(stdoutR)
	_, _ = stderrBuf.ReadFrom(stderrR)
	_ = stdoutR.Close()
	_ = stderrR.Close()

	return code, stdoutBuf.String(), stderrBuf.String()
}

func setupTestDB(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data", "index", "context.db")
	cs, err := contextstore.Open(context.Background(), contextstore.Config{
		RootDir:    dir,
		DBPath:     dbPath,
		RecordsDir: filepath.Join(dir, "data", "records"),
	})
	if err != nil {
		t.Fatalf("setupTestDB: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, dbPath
}

func TestPromoteRecordRequiresDB(t *testing.T) {
	code, stdout, stderr := runPromoteRecordCapture(t, []string{"-source-item-id", "id1"})
	if code != 1 {
		t.Fatalf("expected code 1, got %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "error: --db is required") {
		t.Fatalf("expected '--db is required', got %s", stderr)
	}
}

func TestPromoteRecordDryRun(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()

	ws := workspace.NewStore(cs.DB())
	item, err := ws.Create(ctx, workspace.CreateInput{
		Namespace: "user/chrispian/workspace/notes",
		Key:       "dry.run.note",
		Summary:   "dry run summary",
		Body:      "dry run body",
		Author:    memory.Author{AgentID: "author"},
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-db", dbPath,
		"-source-domain", "workspace",
		"-source-item-id", item.ItemID,
		"-source-version-token", item.VersionToken,
		"-target-domain", "memory",
		"-target-namespace", "user/chrispian/memory/notes",
		"-target-key", "dry.run.mem",
		"-target-trigger", "promotion",
		"-target-derived-from", "project",
		"-target-status", "canonical",
	}

	code, stdout, stderr := runPromoteRecordCapture(t, args)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "dry-run; pass --apply to commit the promotion") {
		t.Fatalf("expected dry-run message, got: %s", stdout)
	}

	// Verify target was not created in memory
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	_, err = mem.GetCurrentInDomain(ctx, domains.Memory, "user/chrispian/memory/notes", "dry.run.mem")
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected target not found, got %v", err)
	}
}

func TestPromoteRecordApplyWorkspaceToKnowledge(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()

	ws := workspace.NewStore(cs.DB())
	item, err := ws.Create(ctx, workspace.CreateInput{
		Namespace: "user/chrispian/workspace/notes",
		Key:       "ws.note",
		Summary:   "workspace note summary",
		Body:      "workspace note body",
		Author:    memory.Author{AgentID: "author"},
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-db", dbPath,
		"-apply",
		"-source-domain", "workspace",
		"-source-item-id", item.ItemID,
		"-source-version-token", item.VersionToken,
		"-target-domain", "knowledge",
		"-target-namespace", "user/chrispian/knowledge/notes",
		"-target-key", "promoted.k",
		"-target-kind", "doc",
		"-target-source", "manual",
		"-target-pointer-scheme", "nil",
		"-target-pointer-locator", "none",
	}

	code, stdout, stderr := runPromoteRecordCapture(t, args)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Applied promotion:") {
		t.Fatalf("expected Applied promotion output, got: %s", stdout)
	}

	// Verify target knowledge item exists
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	rev, err := mem.GetCurrentInDomain(ctx, domains.Knowledge, "user/chrispian/knowledge/notes", "promoted.k")
	if err != nil {
		t.Fatalf("expected knowledge item: %v", err)
	}
	if rev.Payload.Summary != "workspace note summary" {
		t.Fatalf("expected summary %q, got %q", "workspace note summary", rev.Payload.Summary)
	}

	// Verify source workspace item remains
	wsRead, err := ws.GetCurrent(ctx, item.ItemID)
	if err != nil {
		t.Fatalf("expected source workspace item to remain: %v", err)
	}
	if wsRead.Summary != "workspace note summary" {
		t.Fatalf("source workspace summary mismatch: %q", wsRead.Summary)
	}
}

func TestPromoteRecordApplyKnowledgeToWorkspaceJSON(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()

	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	tx, err := cs.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	kRev, err := mem.WriteRevisionInTx(ctx, tx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/notes",
		MemoryKey:   "k.source",
		Summary:     "knowledge summary for ws",
		Body:        "knowledge body for ws",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromReference,
		Actor:       "user",
		Author:      memory.Author{AgentID: "author"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-db", dbPath,
		"-apply",
		"-json",
		"-source-domain", "knowledge",
		"-source-item-id", kRev.ItemID,
		"-source-version-token", kRev.RevisionID,
		"-target-domain", "workspace",
		"-target-namespace", "user/chrispian/workspace/notes",
		"-target-key", "promoted.ws",
		"-actor", "user",
	}

	code, stdout, stderr := runPromoteRecordCapture(t, args)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", code, stderr)
	}

	var receipt map[string]any
	if err = json.Unmarshal([]byte(stdout), &receipt); err != nil {
		t.Fatalf("failed to parse JSON receipt: %v\noutput: %s", err, stdout)
	}
	if receipt["status"] != "applied" {
		t.Fatalf("expected status applied, got %v", receipt["status"])
	}
	if receipt["source_domain"] != "knowledge" {
		t.Fatalf("expected source_domain knowledge, got %v", receipt["source_domain"])
	}
	if receipt["target_domain"] != "workspace" {
		t.Fatalf("expected target_domain workspace, got %v", receipt["target_domain"])
	}

	// Verify target workspace item exists
	ws := workspace.NewStore(cs.DB())
	targetItemID, _ := receipt["target_item_id"].(string)
	wsItem, err := ws.GetCurrent(ctx, targetItemID)
	if err != nil {
		t.Fatalf("target workspace item not found: %v", err)
	}
	if wsItem.Summary != "knowledge summary for ws" {
		t.Fatalf("workspace item summary mismatch: %q", wsItem.Summary)
	}

	// Verify source knowledge revision was deprecated
	kSource, err := mem.GetRevisionByID(ctx, kRev.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if kSource.Status != memory.StatusDeprecated {
		t.Fatalf("expected source knowledge revision deprecated, got %s", kSource.Status)
	}
}

func TestPromoteRecordKeyOccupied(t *testing.T) {
	cs, dbPath := setupTestDB(t)
	ctx := context.Background()

	ws := workspace.NewStore(cs.DB())
	_, err := ws.Create(ctx, workspace.CreateInput{
		Namespace: "user/chrispian/workspace/notes",
		Key:       "occupied.key",
		Summary:   "occupied item",
		Author:    memory.Author{AgentID: "author"},
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	source, err := ws.Create(ctx, workspace.CreateInput{
		Namespace: "user/chrispian/workspace/other",
		Key:       "source.key",
		Summary:   "source item",
		Author:    memory.Author{AgentID: "author"},
		SessionID: "sess-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-db", dbPath,
		"-apply",
		"-source-domain", "workspace",
		"-source-item-id", source.ItemID,
		"-source-version-token", source.VersionToken,
		"-target-domain", "workspace",
		"-target-namespace", "user/chrispian/workspace/notes",
		"-target-key", "occupied.key",
	}

	code, stdout, stderr := runPromoteRecordCapture(t, args)
	if code != 2 {
		t.Fatalf("expected exit code 2 (target key occupied), got %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "target key occupied") {
		t.Fatalf("expected 'target key occupied' in stderr, got: %s", stderr)
	}
}

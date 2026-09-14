package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestWorkspaceRetentionCommandDryRunThenExplicitApply(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "main.db")
	cs, err := contextstore.Open(ctx, contextstore.Config{RootDir: dir, DBPath: dbPath, RecordsDir: filepath.Join(dir, "records")})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	store := workspace.NewStore(cs.DB())
	item, err := store.Create(ctx, workspace.CreateInput{
		Namespace: "project/tesseract/workspace/cli-test", Key: "candidate", Summary: "candidate",
		Author: memory.Author{AgentID: "test"}, SessionID: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := cs.DB().Exec(`UPDATE workspace_items SET activation = ?, last_used_at = ?, last_decayed_at = ? WHERE item_id = ?`, workspace.ActivationFloor, old, old, item.ItemID); err != nil {
		t.Fatal(err)
	}

	run := func(cfg config.Config, args ...string) (int, []byte, []byte) {
		out, _ := os.CreateTemp(t.TempDir(), "out")
		errOut, _ := os.CreateTemp(t.TempDir(), "err")
		code := runWorkspaceRetention(ctx, dbPath, cfg, args, out, errOut)
		_, _ = out.Seek(0, 0)
		stdout, _ := os.ReadFile(out.Name())
		_, _ = errOut.Seek(0, 0)
		stderr, _ := os.ReadFile(errOut.Name())
		_ = out.Close()
		_ = errOut.Close()
		return code, stdout, stderr
	}

	cfg := config.Defaults()
	code, stdout, stderr := run(cfg, "--json")
	if code != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", code, stderr)
	}
	var report workspace.RetentionReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("decode dry-run: %v; %s", err, stdout)
	}
	if len(report.Candidates) != 1 || report.Candidates[0].PolicyReason != "operator_purge_disabled" {
		t.Fatalf("dry-run report = %#v", report)
	}
	if _, err := store.GetCurrent(ctx, item.ItemID); err != nil {
		t.Fatalf("dry-run removed item: %v", err)
	}

	code, _, stderr = run(cfg, "--apply", "--item-id", item.ItemID, "--json")
	if code == 0 {
		t.Fatalf("disabled apply succeeded; stderr=%s", stderr)
	}
	cfg.Workspace.Retention.PurgeEnabled = true
	code, stdout, stderr = run(cfg, "--apply", "--item-id", item.ItemID, "--json")
	if code != 0 {
		t.Fatalf("apply exit=%d stderr=%s", code, stderr)
	}
	var envelope struct {
		Apply workspace.RetentionApplyReport `json:"apply"`
	}
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		t.Fatalf("decode apply: %v; %s", err, stdout)
	}
	if envelope.Apply.Purged != 1 {
		t.Fatalf("apply = %#v", envelope.Apply)
	}
}

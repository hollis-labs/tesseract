package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func runWorkspaceRetentionForTest(t *testing.T, dbPath string, cfg config.Config, args ...string) (int, []byte, []byte) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	errOut, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	defer errOut.Close()
	code := runWorkspaceRetention(context.Background(), dbPath, cfg, args, out, errOut)
	stdout, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(errOut.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, stdout, stderr
}

func retentionCommandFixture(t *testing.T, count int) (*contextstore.Store, string, []string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "main.db")
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir, DBPath: dbPath, RecordsDir: filepath.Join(dir, "records")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	store := workspace.NewStore(cs.DB())
	itemIDs := make([]string, 0, count)
	for i := 0; i < count; i++ {
		item, err := store.Create(context.Background(), workspace.CreateInput{
			Namespace: "project/tesseract/workspace/cli-partial", Key: fmt.Sprintf("candidate-%d", i), Summary: "candidate",
			Author: memory.Author{AgentID: "test"}, SessionID: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
		itemIDs = append(itemIDs, item.ItemID)
	}
	sort.Strings(itemIDs)
	old := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := cs.DB().Exec(`UPDATE workspace_items SET activation = ?, last_used_at = ?, last_decayed_at = ?`, workspace.ActivationFloor, old, old); err != nil {
		t.Fatal(err)
	}
	return cs, dbPath, itemIDs
}

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

func TestWorkspaceRetentionCommandRejectsUnparsedArgumentsBeforeOpen(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workspace.Retention.PurgeEnabled = true
	for name, args := range map[string][]string{
		"positional_before_selector": {"--json", "--apply", "unexpected", "--item-id", "01IGNOREDSELECTOR"},
		"tokens_after_terminator":    {"--json", "--apply", "--", "--item-id", "01IGNOREDSELECTOR"},
	} {
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "must-not-open.db")
			code, stdout, stderr := runWorkspaceRetentionForTest(t, dbPath, cfg, args...)
			if code == 0 || len(stdout) != 0 || !strings.Contains(string(stderr), "unexpected argument") {
				t.Fatalf("invalid args: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
				t.Fatalf("invalid arguments opened database: stat error=%v", err)
			}
		})
	}
}

func TestWorkspaceRetentionCommandReportsPartialApply(t *testing.T) {
	for _, output := range []string{"json", "text"} {
		t.Run(output, func(t *testing.T) {
			cs, dbPath, itemIDs := retentionCommandFixture(t, 2)
			if _, err := cs.DB().Exec(fmt.Sprintf(`CREATE TRIGGER abort_second_cli_purge BEFORE DELETE ON workspace_items WHEN OLD.item_id = '%s' BEGIN SELECT RAISE(ABORT, 'injected CLI purge failure'); END`, itemIDs[1])); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Workspace.Retention.PurgeEnabled = true
			args := []string{"--apply", "--item-id", itemIDs[0], "--item-id", itemIDs[1]}
			if output == "json" {
				args = append(args, "--json")
			}
			code, stdout, stderr := runWorkspaceRetentionForTest(t, dbPath, cfg, args...)
			if code == 0 || !strings.Contains(string(stderr), "injected CLI purge failure") {
				t.Fatalf("partial failure: code=%d stderr=%q", code, stderr)
			}
			if output == "json" {
				var envelope struct {
					Apply workspace.RetentionApplyReport `json:"apply"`
				}
				if err := json.Unmarshal(stdout, &envelope); err != nil {
					t.Fatalf("decode partial result: %v; stdout=%q", err, stdout)
				}
				if envelope.Apply.Purged != 1 || len(envelope.Apply.Results) != 1 || envelope.Apply.Results[0].ItemID != itemIDs[0] {
					t.Fatalf("JSON partial result = %#v", envelope.Apply)
				}
			} else {
				text := string(stdout)
				if !strings.Contains(text, "Workspace retention apply — purged 1; skipped 0") || !strings.Contains(text, itemIDs[0]+"  purged") {
					t.Fatalf("text partial result = %q", text)
				}
			}
			var tombstones int
			if err := cs.DB().QueryRow(`SELECT COUNT(*) FROM workspace_tombstones`).Scan(&tombstones); err != nil {
				t.Fatal(err)
			}
			if tombstones != 1 {
				t.Fatalf("committed tombstones = %d, want 1", tombstones)
			}
		})
	}
}

func TestWorkspaceRetentionCommandPageApplyWithoutSelectorsRemainsBounded(t *testing.T) {
	cs, dbPath, _ := retentionCommandFixture(t, 2)
	cfg := config.Defaults()
	cfg.Workspace.Retention.PurgeEnabled = true
	code, stdout, stderr := runWorkspaceRetentionForTest(t, dbPath, cfg, "--apply", "--limit", "1", "--json")
	if code != 0 {
		t.Fatalf("bounded page apply: code=%d stderr=%q", code, stderr)
	}
	var envelope struct {
		Apply workspace.RetentionApplyReport `json:"apply"`
	}
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Apply.Purged != 1 {
		t.Fatalf("bounded page apply = %#v", envelope.Apply)
	}
	var live int
	if err := cs.DB().QueryRow(`SELECT COUNT(*) FROM workspace_items`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("bounded page apply left %d live items, want 1", live)
	}
}

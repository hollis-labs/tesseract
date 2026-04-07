package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/taskscli/config"
	"github.com/hollis-labs/vanta-conduit/internal/taskscli/index"
)

const testConfig = `version: 1
project:
  name: volon
`

func TestCreateStartDoneFlow(t *testing.T) {
	repo := setupRepo(t)
	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	runner := newRunner(cfg)
	runner.WithStdout(ioDiscard{})
	runner.WithStderr(ioDiscard{})
	fixed := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	runner.WithNow(func() time.Time { return fixed })

	if err := runner.Dispatch([]string{"create", "Test task"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	taskPath := filepath.Join(repo, ".agentrc", "tasks", "TASK-20260224-001.md")
	data, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !bytes.Contains(data, []byte("status: todo")) {
		t.Fatalf("expected todo status:\n%s", data)
	}

	if err := runner.Dispatch([]string{"start", "TASK-20260224-001"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := runner.Dispatch([]string{"done", "TASK-20260224-001"}); err != nil {
		t.Fatalf("done: %v", err)
	}

	data, _ = os.ReadFile(taskPath)
	if !bytes.Contains(data, []byte("status: done")) {
		t.Fatalf("expected done status:\n%s", data)
	}

	store, err := index.Open(context.Background(), cfg.DBPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer store.Close()
	rows, err := store.ListTasks(context.Background(), index.Filters{})
	if err != nil || len(rows) != 1 || rows[0].Status != "done" {
		t.Fatalf("unexpected rows: %+v err=%v", rows, err)
	}
}

func TestReindexAndList(t *testing.T) {
	repo := setupRepo(t)
	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	runner := newRunner(cfg)
	runner.WithStdout(ioDiscard{})
	runner.WithStderr(ioDiscard{})
	fixed := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	runner.WithNow(func() time.Time { return fixed })

	if err := runner.Dispatch([]string{"create", "First task"}); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := runner.Dispatch([]string{"create", "Second task"}); err != nil {
		t.Fatalf("create second: %v", err)
	}

	os.Remove(cfg.DBPath)
	if err := runner.Dispatch([]string{"reindex"}); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	var out bytes.Buffer
	runner.WithStdout(&out)
	if err := runner.Dispatch([]string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "TASK-20260224-001") || !strings.Contains(text, "TASK-20260224-002") {
		t.Fatalf("list output missing tasks:\n%s", text)
	}
}

func TestBacklogListAndShow(t *testing.T) {
	repo := setupRepo(t)
	backlogID := "BACKLOG-20260224-004"
	writeBacklogItem(t, repo, backlogID, `title: "Volon CLI backlog support"
status: captured
priority: B
tags: [cli]
context: dev
created_at: 2026-02-24
updated_at: 2026-02-24`)

	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	runner := newRunner(cfg)

	var out bytes.Buffer
	runner.WithStdout(&out)
	if err := runner.DispatchBacklog([]string{"list"}); err != nil {
		t.Fatalf("backlog list: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, backlogID) || !strings.Contains(text, "Volon CLI backlog support") {
		t.Fatalf("list output missing backlog item:\n%s", text)
	}

	out.Reset()
	if err := runner.DispatchBacklog([]string{"show", backlogID}); err != nil {
		t.Fatalf("backlog show: %v", err)
	}
	if !strings.Contains(out.String(), "Volon CLI backlog support") {
		t.Fatalf("show output missing contents:\n%s", out.String())
	}
}

func TestBacklogPromoteCreatesTask(t *testing.T) {
	repo := setupRepo(t)
	backlogID := "BACKLOG-20260224-004"
	writeBacklogItem(t, repo, backlogID, `title: "Volon CLI backlog support"
status: captured
priority: B
tags: [cli]
context: dev
created_at: 2026-02-24
updated_at: 2026-02-24`)

	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	runner := newRunner(cfg)
	runner.WithStdout(ioDiscard{})
	runner.WithStderr(ioDiscard{})
	fixed := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	runner.WithNow(func() time.Time { return fixed })

	if err := runner.DispatchBacklog([]string{"promote", backlogID, "--priority", "A"}); err != nil {
		t.Fatalf("backlog promote: %v", err)
	}

	taskPath := filepath.Join(repo, ".agentrc", "tasks", "TASK-20260224-001.md")
	data, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !bytes.Contains(data, []byte("promoted_from: BACKLOG-20260224-004")) {
		t.Fatalf("task missing promoted_from:\n%s", data)
	}

	backlogPath := filepath.Join(repo, ".agentrc", "backlog", backlogID+".md")
	updated, err := os.ReadFile(backlogPath)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	if !bytes.Contains(updated, []byte("status: promoted")) || !bytes.Contains(updated, []byte("promoted_to: TASK-20260224-001")) {
		t.Fatalf("backlog file not updated:\n%s", updated)
	}
}

func TestTaskCreateSupportsSprintAndListFilter(t *testing.T) {
	repo := setupRepo(t)
	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	runner := newRunner(cfg)
	runner.WithStdout(ioDiscard{})
	runner.WithStderr(ioDiscard{})
	fixed := time.Date(2026, 2, 24, 0, 0, 0, 0, time.UTC)
	runner.WithNow(func() time.Time { return fixed })

	if err := runner.Dispatch([]string{"create", "--sprint", "sprint-2026-02", "Sprint task"}); err != nil {
		t.Fatalf("create with sprint: %v", err)
	}

	taskPath := filepath.Join(repo, ".agentrc", "tasks", "TASK-20260224-001.md")
	data, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !bytes.Contains(data, []byte("sprint_id: sprint-2026-02")) {
		t.Fatalf("task missing sprint_id:\n%s", data)
	}

	var out bytes.Buffer
	runner.WithStdout(&out)
	if err := runner.Dispatch([]string{"list", "--sprint", "sprint-2026-02"}); err != nil {
		t.Fatalf("list with sprint filter: %v", err)
	}
	if !strings.Contains(out.String(), "TASK-20260224-001") {
		t.Fatalf("expected task in sprint filter:\n%s", out.String())
	}

	out.Reset()
	if err := runner.Dispatch([]string{"list", "--sprint", "sprint-2099-01"}); err != nil {
		t.Fatalf("list with missing sprint: %v", err)
	}
	if strings.Contains(out.String(), "TASK-20260224-001") {
		t.Fatalf("unexpected task in non-matching sprint:\n%s", out.String())
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agentrc.yaml"), []byte(testConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agentrc", "tasks"), 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agentrc", "backlog"), 0o755); err != nil {
		t.Fatalf("mkdir backlog: %v", err)
	}
	return dir
}

func writeBacklogItem(t *testing.T, repo, id, frontmatter string) {
	t.Helper()
	path := filepath.Join(repo, ".agentrc", "backlog", id+".md")
	body := "\n## Summary\n\nStub\n"
	data := fmt.Sprintf("---\nid: %s\n%s\n---%s", id, frontmatter, body)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write backlog item: %v", err)
	}
}

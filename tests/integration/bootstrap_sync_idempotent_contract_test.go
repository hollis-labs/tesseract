package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type bootstrapSyncIdempotentGolden struct {
	ExpectedTasksLine string `json:"expected_tasks_line"`
	ExpectedLogLine   string `json:"expected_log_line"`
}

func TestBootstrapSyncIdempotentContractAgainstGolden(t *testing.T) {
	golden := loadBootstrapSyncIdempotentGolden(t)
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-A.md"), "status: done\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-B.md"), "status: todo\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "logs", "run-iteration-010-2026-02-25.md"), "# latest log\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "bootstrap.md"), "updated_at: 2026-02-25T00:00:00Z\n- Tasks: 0 todo, 0 blocked, 0 done\n- Last run log: `<none>`\n")

	scriptRel := filepath.Join("..", "..", "scripts", "bootstrap-sync.sh")
	script, err := filepath.Abs(scriptRel)
	if err != nil {
		t.Fatalf("abs script path: %v", err)
	}

	apply1 := exec.Command("bash", script, "--apply")
	apply1.Dir = root
	if out, err := apply1.CombinedOutput(); err != nil {
		t.Fatalf("first bootstrap-sync apply failed: %v\noutput:\n%s", err, string(out))
	}
	firstTasks, firstLog := readBootstrapSummaryLines(t, filepath.Join(root, ".agentrc", "bootstrap.md"))
	if firstTasks != golden.ExpectedTasksLine {
		t.Fatalf("unexpected first tasks line: got=%q want=%q", firstTasks, golden.ExpectedTasksLine)
	}
	if firstLog != golden.ExpectedLogLine {
		t.Fatalf("unexpected first log line: got=%q want=%q", firstLog, golden.ExpectedLogLine)
	}

	apply2 := exec.Command("bash", script, "--apply")
	apply2.Dir = root
	if out, err := apply2.CombinedOutput(); err != nil {
		t.Fatalf("second bootstrap-sync apply failed: %v\noutput:\n%s", err, string(out))
	}
	secondTasks, secondLog := readBootstrapSummaryLines(t, filepath.Join(root, ".agentrc", "bootstrap.md"))
	if secondTasks != firstTasks {
		t.Fatalf("tasks line drifted between applies: first=%q second=%q", firstTasks, secondTasks)
	}
	if secondLog != firstLog {
		t.Fatalf("log line drifted between applies: first=%q second=%q", firstLog, secondLog)
	}
}

func loadBootstrapSyncIdempotentGolden(t *testing.T) bootstrapSyncIdempotentGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "bootstrap_sync_idempotent_contract_golden.json"))
	if err != nil {
		t.Fatalf("read bootstrap sync idempotent golden: %v", err)
	}
	var out bootstrapSyncIdempotentGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal bootstrap sync idempotent golden: %v", err)
	}
	return out
}

func readBootstrapSummaryLines(t *testing.T, path string) (string, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bootstrap summary lines: %v", err)
	}
	var tasksLine string
	var logLine string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "- Tasks: ") {
			tasksLine = line
		}
		if strings.HasPrefix(line, "- Last run log: ") {
			logLine = line
		}
	}
	if tasksLine == "" || logLine == "" {
		t.Fatalf("missing bootstrap summary lines in file:\n%s", string(data))
	}
	return tasksLine, logLine
}

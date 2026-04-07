package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type bootstrapSyncNoLogGolden struct {
	ReportMarkers    []string `json:"report_markers"`
	UpdatedTasksLine string   `json:"updated_tasks_line"`
	UpdatedLogLine   string   `json:"updated_log_line"`
}

func TestBootstrapSyncNoLogContractAgainstGolden(t *testing.T) {
	golden := loadBootstrapSyncNoLogGolden(t)
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-A.md"), "status: done\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-B.md"), "status: todo\n")
	if err := os.MkdirAll(filepath.Join(root, ".agentrc", "logs"), 0o755); err != nil {
		t.Fatalf("mkdir empty logs dir: %v", err)
	}
	mustWrite(t, filepath.Join(root, ".agentrc", "bootstrap.md"), "updated_at: 2026-02-25T00:00:00Z\n- Tasks: 0 todo, 0 blocked, 0 done\n- Last run log: `placeholder`\n")

	scriptRel := filepath.Join("..", "..", "scripts", "bootstrap-sync.sh")
	script, err := filepath.Abs(scriptRel)
	if err != nil {
		t.Fatalf("abs script path: %v", err)
	}

	report := exec.Command("bash", script)
	report.Dir = root
	reportOut, err := report.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap-sync report (no-log) failed: %v\noutput:\n%s", err, string(reportOut))
	}
	reportText := string(reportOut)
	for _, marker := range golden.ReportMarkers {
		if !strings.Contains(reportText, marker) {
			t.Fatalf("missing report marker %q in output:\n%s", marker, reportText)
		}
	}

	apply := exec.Command("bash", script, "--apply")
	apply.Dir = root
	applyOut, err := apply.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap-sync apply (no-log) failed: %v\noutput:\n%s", err, string(applyOut))
	}
	_ = applyOut

	bootstrapBytes, err := os.ReadFile(filepath.Join(root, ".agentrc", "bootstrap.md"))
	if err != nil {
		t.Fatalf("read bootstrap after apply: %v", err)
	}
	bootstrapText := string(bootstrapBytes)
	if !strings.Contains(bootstrapText, golden.UpdatedTasksLine) {
		t.Fatalf("missing updated tasks line in bootstrap:\n%s", bootstrapText)
	}
	if !strings.Contains(bootstrapText, golden.UpdatedLogLine) {
		t.Fatalf("missing updated log line in bootstrap:\n%s", bootstrapText)
	}
}

func loadBootstrapSyncNoLogGolden(t *testing.T) bootstrapSyncNoLogGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "bootstrap_sync_no_log_contract_golden.json"))
	if err != nil {
		t.Fatalf("read bootstrap sync no-log golden: %v", err)
	}
	var out bootstrapSyncNoLogGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal bootstrap sync no-log golden: %v", err)
	}
	return out
}

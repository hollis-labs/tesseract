package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type makeBootstrapSyncGolden struct {
	ReportMarkers    []string `json:"report_markers"`
	ApplyMarkers     []string `json:"apply_markers"`
	UpdatedTasksLine string   `json:"updated_tasks_line"`
	UpdatedLogLine   string   `json:"updated_log_line"`
}

func TestMakeBootstrapSyncContractAgainstGolden(t *testing.T) {
	golden := loadMakeBootstrapSyncGolden(t)
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-A.md"), "status: done\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-B.md"), "status: todo\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "logs", "run-iteration-001-2026-02-25.md"), "# log\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "bootstrap.md"), "updated_at: 2026-02-25T00:00:00Z\n- Tasks: 0 todo, 0 blocked, 0 done\n- Last run log: `none`\n")

	scriptSrc := filepath.Join("..", "..", "scripts", "bootstrap-sync.sh")
	scriptBytes, err := os.ReadFile(scriptSrc)
	if err != nil {
		t.Fatalf("read bootstrap-sync script: %v", err)
	}
	scriptPath := filepath.Join(root, "scripts", "bootstrap-sync.sh")
	mustWrite(t, scriptPath, string(scriptBytes))
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("chmod script: %v", err)
	}

	makefile, err := filepath.Abs(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("abs makefile path: %v", err)
	}

	report := exec.Command("make", "-f", makefile, "bootstrap-sync-report")
	report.Dir = root
	reportOut, err := report.CombinedOutput()
	if err != nil {
		t.Fatalf("make bootstrap-sync-report failed: %v\noutput:\n%s", err, string(reportOut))
	}
	assertOrderedMarkers(t, string(reportOut), golden.ReportMarkers)

	apply := exec.Command("make", "-f", makefile, "bootstrap-sync-apply")
	apply.Dir = root
	applyOut, err := apply.CombinedOutput()
	if err != nil {
		t.Fatalf("make bootstrap-sync-apply failed: %v\noutput:\n%s", err, string(applyOut))
	}
	assertOrderedMarkers(t, string(applyOut), golden.ApplyMarkers)

	bootstrapBytes, err := os.ReadFile(filepath.Join(root, ".agentrc", "bootstrap.md"))
	if err != nil {
		t.Fatalf("read bootstrap after make apply: %v", err)
	}
	bootstrapText := string(bootstrapBytes)
	if !strings.Contains(bootstrapText, golden.UpdatedTasksLine) {
		t.Fatalf("missing updated tasks line in bootstrap:\n%s", bootstrapText)
	}
	if !strings.Contains(bootstrapText, golden.UpdatedLogLine) {
		t.Fatalf("missing updated log line in bootstrap:\n%s", bootstrapText)
	}
}

func loadMakeBootstrapSyncGolden(t *testing.T) makeBootstrapSyncGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "make_bootstrap_sync_contract_golden.json"))
	if err != nil {
		t.Fatalf("read make bootstrap-sync golden: %v", err)
	}
	var out makeBootstrapSyncGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal make bootstrap-sync golden: %v", err)
	}
	return out
}

func assertOrderedMarkers(t *testing.T, content string, markers []string) {
	t.Helper()
	pos := 0
	for _, marker := range markers {
		next := strings.Index(content[pos:], marker)
		if next < 0 {
			t.Fatalf("missing marker %q in output:\n%s", marker, content)
		}
		pos += next + len(marker)
	}
}

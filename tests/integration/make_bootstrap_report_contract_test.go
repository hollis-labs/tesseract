package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type makeBootstrapReportGolden struct {
	Markers []string `json:"markers"`
}

func TestMakeBootstrapReportContractAgainstGolden(t *testing.T) {
	golden := loadMakeBootstrapReportGolden(t)
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-A.md"), "status: done\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "tasks", "TASK-B.md"), "status: todo\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "logs", "run-iteration-001-2026-02-25.md"), "# log\n")
	mustWrite(t, filepath.Join(root, ".agentrc", "bootstrap.md"), "updated_at: 2026-02-25T00:00:00Z\n- Tasks: 0 todo, 0 blocked, 0 done\n- Last run log: `<none>`\n")

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

	cmd := exec.Command("make", "-f", makefile, "bootstrap-report")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make bootstrap-report failed: %v\noutput:\n%s", err, string(out))
	}
	content := string(out)
	pos := 0
	for _, marker := range golden.Markers {
		next := strings.Index(content[pos:], marker)
		if next < 0 {
			t.Fatalf("missing marker %q in output:\n%s", marker, content)
		}
		pos += next + len(marker)
	}
}

func loadMakeBootstrapReportGolden(t *testing.T) makeBootstrapReportGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "make_bootstrap_report_contract_golden.json"))
	if err != nil {
		t.Fatalf("read make bootstrap-report golden: %v", err)
	}
	var out makeBootstrapReportGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal make bootstrap-report golden: %v", err)
	}
	return out
}

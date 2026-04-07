package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type runlogGolden struct {
	Markers []string `json:"markers"`
}

func TestRunlogHelperContractAgainstGolden(t *testing.T) {
	golden := loadRunlogGolden(t)
	outPath := filepath.Join(t.TempDir(), "runlog-077.md")
	script := filepath.Join("..", "..", "scripts", "volon-runlog-init.sh")
	cmd := exec.Command("bash", script,
		"--iteration", "77",
		"--date", "2026-02-25",
		"--profile", "orchestrator",
		"--out", outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("runlog helper failed: %v\noutput:\n%s", err, string(out))
	}
	contentBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	content := string(contentBytes)
	pos := 0
	for _, marker := range golden.Markers {
		next := strings.Index(content[pos:], marker)
		if next < 0 {
			t.Fatalf("missing marker %q in output:\n%s", marker, content)
		}
		pos += next + len(marker)
	}
}

func loadRunlogGolden(t *testing.T) runlogGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "runlog_helper_contract_golden.json"))
	if err != nil {
		t.Fatalf("read runlog golden: %v", err)
	}
	var out runlogGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal runlog golden: %v", err)
	}
	return out
}

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type makeContractRunGolden struct {
	Markers []string `json:"markers"`
}

func TestMakeContractCLIRunContractAgainstGolden(t *testing.T) {
	golden := loadMakeContractRunGolden(t)
	cmd := exec.Command("make", "contract-cli-run", "SUITE=api")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make contract-cli-run failed: %v\noutput:\n%s", err, string(out))
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

func loadMakeContractRunGolden(t *testing.T) makeContractRunGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "make_contract_cli_run_contract_golden.json"))
	if err != nil {
		t.Fatalf("read make contract-cli-run golden: %v", err)
	}
	var out makeContractRunGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal make contract-cli-run golden: %v", err)
	}
	return out
}

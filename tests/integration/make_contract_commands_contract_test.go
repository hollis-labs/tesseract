package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type makeContractCommandsGolden struct {
	OrderedSuites []string `json:"ordered_suites"`
}

func TestMakeContractCommandsContractAgainstGolden(t *testing.T) {
	golden := loadMakeContractCommandsGolden(t)
	cmd := exec.Command("make", "contract-commands")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make contract-commands failed: %v\noutput:\n%s", err, string(out))
	}
	lines := parseNonEmptyLines(string(out))
	if len(lines) == 0 {
		t.Fatalf("empty output from make contract-commands")
	}
	if strings.HasPrefix(lines[0], "scripts/contract-suite-commands.sh") {
		lines = lines[1:]
	}
	if len(lines) != len(golden.OrderedSuites) {
		t.Fatalf("line count mismatch: got=%d want=%d\n%s", len(lines), len(golden.OrderedSuites), string(out))
	}
	for i, suite := range golden.OrderedSuites {
		parts := strings.SplitN(lines[i], "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid line format at %d: %q", i, lines[i])
		}
		if parts[0] != suite {
			t.Fatalf("suite order mismatch at %d: got=%q want=%q", i, parts[0], suite)
		}
	}
}

func loadMakeContractCommandsGolden(t *testing.T) makeContractCommandsGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "make_contract_commands_contract_golden.json"))
	if err != nil {
		t.Fatalf("read make contract-commands golden: %v", err)
	}
	var out makeContractCommandsGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal make contract-commands golden: %v", err)
	}
	return out
}

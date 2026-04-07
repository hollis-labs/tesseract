package integration

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type contractSuiteCommandsGolden struct {
	OrderedSuites []string `json:"ordered_suites"`
}

func TestContractSuiteCommandsScriptAgainstGolden(t *testing.T) {
	golden := loadContractSuiteCommandsGolden(t)
	script := filepath.Join("scripts", "contract-suite-commands.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contract-suite-commands.sh failed: %v\noutput:\n%s", err, string(out))
	}
	lines := parseNonEmptyLines(string(out))
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
		if !strings.HasPrefix(parts[1], "go test ./tests/integration -run ") {
			t.Fatalf("unexpected command format at %d: %q", i, parts[1])
		}
	}
}

func parseNonEmptyLines(s string) []string {
	scanner := bufio.NewScanner(strings.NewReader(s))
	out := []string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func loadContractSuiteCommandsGolden(t *testing.T) contractSuiteCommandsGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_suite_commands_contract_golden.json"))
	if err != nil {
		t.Fatalf("read contract suite commands golden: %v", err)
	}
	var out contractSuiteCommandsGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract suite commands golden: %v", err)
	}
	return out
}

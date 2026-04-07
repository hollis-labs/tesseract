package integration

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractSuiteCommandsUniqueContract(t *testing.T) {
	script := filepath.Join("scripts", "contract-suite-commands.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contract-suite-commands.sh failed: %v\noutput:\n%s", err, string(out))
	}
	lines := parseNonEmptyLines(string(out))
	if len(lines) == 0 {
		t.Fatalf("contract-suite-commands produced no lines")
	}

	seen := map[string]int{}
	duplicates := []string{}
	for i, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("line %d is not suite<TAB>command: %q", i, line)
		}
		suite := strings.TrimSpace(parts[0])
		if suite == "" {
			t.Fatalf("line %d has empty suite name: %q", i, line)
		}
		seen[suite]++
		if seen[suite] == 2 {
			duplicates = append(duplicates, suite)
		}
	}
	if len(duplicates) > 0 {
		t.Fatalf("duplicate suite names found: %s", strings.Join(duplicates, ", "))
	}
	if len(seen) != len(lines) {
		t.Fatalf("suite uniqueness mismatch: unique=%d lines=%d", len(seen), len(lines))
	}
}

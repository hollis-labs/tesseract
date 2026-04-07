package integration

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractSuiteCommandsFormatContract(t *testing.T) {
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
	for i, line := range lines {
		if !strings.Contains(line, "\t") {
			t.Fatalf("line %d is not tab-delimited (suite<TAB>command): %q", i, line)
		}
		parts := strings.SplitN(line, "\t", 2)
		suite := strings.TrimSpace(parts[0])
		command := strings.TrimSpace(parts[1])
		if suite == "" {
			t.Fatalf("line %d has empty suite name: %q", i, line)
		}
		if command == "" {
			t.Fatalf("line %d has empty command: %q", i, line)
		}
	}
}

package integration

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractSuiteCommandsSuffixContract(t *testing.T) {
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
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("line %d is not suite<TAB>command: %q", i, line)
		}
		suite := strings.TrimSpace(parts[0])
		command := strings.TrimSpace(parts[1])
		if !strings.HasSuffix(command, "-count=1") {
			t.Fatalf("suite %q command missing -count=1 suffix: %q", suite, command)
		}
	}
}

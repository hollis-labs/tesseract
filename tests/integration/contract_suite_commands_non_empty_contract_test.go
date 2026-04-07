package integration

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractSuiteCommandsNonEmptyContract(t *testing.T) {
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

	const marker = "api\tgo test ./tests/integration -run APIContract -count=1"
	foundMarker := false
	for _, line := range lines {
		if strings.TrimSpace(line) == marker {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatalf("expected known suite marker %q in output", marker)
	}
}

package integration

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestContractSuiteCommandsDeterministicContract(t *testing.T) {
	run := func() string {
		t.Helper()
		script := filepath.Join("scripts", "contract-suite-commands.sh")
		cmd := exec.Command("bash", script)
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("contract-suite-commands.sh failed: %v\noutput:\n%s", err, string(out))
		}
		return string(out)
	}

	first := run()
	if first == "" {
		t.Fatalf("contract-suite-commands produced empty output")
	}
	second := run()
	third := run()
	if second != first {
		t.Fatalf("deterministic drift between run1 and run2\nrun1:\n%s\nrun2:\n%s", first, second)
	}
	if third != first {
		t.Fatalf("deterministic drift between run1 and run3\nrun1:\n%s\nrun3:\n%s", first, third)
	}
}

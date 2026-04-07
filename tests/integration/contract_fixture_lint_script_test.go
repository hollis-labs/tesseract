package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractFixtureLintScriptMissingFixture(t *testing.T) {
	root := t.TempDir()
	script := writeContractFixtureLintScript(t, root)
	mustWrite(t, filepath.Join(root, "fixtures", "present.json"), "{}\n")
	manifest := filepath.Join(root, "manifest.txt")
	mustWrite(t, manifest, "fixtures/present.json\nfixtures/missing.json\n")

	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CONTRACT_FIXTURE_LINT_MANIFEST="+manifest)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for missing fixture; output:\n%s", string(out))
	}
	if !strings.Contains(string(out), "missing fixture: fixtures/missing.json") {
		t.Fatalf("missing path not reported; output:\n%s", string(out))
	}
}

func TestContractFixtureLintScriptManifestHappyPath(t *testing.T) {
	root := t.TempDir()
	script := writeContractFixtureLintScript(t, root)
	mustWrite(t, filepath.Join(root, "fixtures", "a.json"), "{}\n")
	mustWrite(t, filepath.Join(root, "fixtures", "b.json"), "{}\n")
	manifest := filepath.Join(root, "manifest.txt")
	mustWrite(t, manifest, "fixtures/a.json\nfixtures/b.json\n")

	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CONTRACT_FIXTURE_LINT_MANIFEST="+manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success for present fixtures: %v\noutput:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "fixture lint ok (2 files)") {
		t.Fatalf("unexpected success output:\n%s", string(out))
	}
}

func writeContractFixtureLintScript(t *testing.T, root string) string {
	t.Helper()
	src := filepath.Join("..", "..", "scripts", "contract-fixture-lint.sh")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read contract-fixture-lint script: %v", err)
	}
	script := filepath.Join(root, "scripts", "contract-fixture-lint.sh")
	mustWrite(t, script, string(body))
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	return script
}

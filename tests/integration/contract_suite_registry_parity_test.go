package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextcli"
	"github.com/hollis-labs/cortex/internal/contextpolicy"
	"github.com/hollis-labs/cortex/internal/contextstore"
)

type suiteRegistryParityGolden struct {
	Items []suiteRegistryParityItem `json:"items"`
}

type suiteRegistryParityItem struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func TestContractSuiteRegistryParityAgainstGolden(t *testing.T) {
	golden := loadSuiteRegistryParityGolden(t)
	scriptItems := runSuiteCommandScript(t)
	cliItems := runCLISuiteList(t)

	if len(scriptItems) != len(golden.Items) {
		t.Fatalf("script item count mismatch: got=%d want=%d", len(scriptItems), len(golden.Items))
	}
	if len(cliItems) != len(golden.Items) {
		t.Fatalf("cli item count mismatch: got=%d want=%d", len(cliItems), len(golden.Items))
	}

	for i, expected := range golden.Items {
		if scriptItems[i] != expected {
			t.Fatalf("script mismatch at %d: got=%+v want=%+v", i, scriptItems[i], expected)
		}
		if cliItems[i] != expected {
			t.Fatalf("cli mismatch at %d: got=%+v want=%+v", i, cliItems[i], expected)
		}
	}
}

func loadSuiteRegistryParityGolden(t *testing.T) suiteRegistryParityGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_suite_registry_parity_golden.json"))
	if err != nil {
		t.Fatalf("read parity golden: %v", err)
	}
	var out suiteRegistryParityGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal parity golden: %v", err)
	}
	return out
}

func runSuiteCommandScript(t *testing.T) []suiteRegistryParityItem {
	t.Helper()
	script := filepath.Join("scripts", "contract-suite-commands.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contract-suite-commands.sh failed: %v\noutput:\n%s", err, string(out))
	}
	lines := parseNonEmptyLines(string(out))
	items := make([]suiteRegistryParityItem, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid script line format: %q", line)
		}
		items = append(items, suiteRegistryParityItem{Name: parts[0], Command: parts[1]})
	}
	return items
}

func runCLISuiteList(t *testing.T) []suiteRegistryParityItem {
	t.Helper()
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	payload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list", "--output", "json"})
	list := mustObjArray(t, payload["items"])
	items := make([]suiteRegistryParityItem, 0, len(list))
	for _, entry := range list {
		name, ok := entry["name"].(string)
		if !ok {
			t.Fatalf("suite name is not string: %T", entry["name"])
		}
		command, ok := entry["command"].(string)
		if !ok {
			t.Fatalf("suite command is not string: %T", entry["command"])
		}
		items = append(items, suiteRegistryParityItem{Name: name, Command: command})
	}
	return items
}

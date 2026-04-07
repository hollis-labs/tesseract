package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextcli"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

type cliContractGolden struct {
	ListTopKeys   []string `json:"list_top_keys"`
	RunTopKeys    []string `json:"run_top_keys"`
	OrderedSuites []string `json:"ordered_suites"`
}

func TestCLIContractCommandAgainstGolden(t *testing.T) {
	golden := loadCLIContractGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	listPayload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list", "--output", "json"})
	checkKeys(t, listPayload, golden.ListTopKeys)
	items := mustObjArray(t, listPayload["items"])
	if len(items) != len(golden.OrderedSuites) {
		t.Fatalf("suite count mismatch: got=%d want=%d", len(items), len(golden.OrderedSuites))
	}
	for i, name := range golden.OrderedSuites {
		if items[i]["name"] != name {
			t.Fatalf("suite order mismatch at %d: got=%v want=%s", i, items[i]["name"], name)
		}
	}

	runPayload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "api", "--output", "json"})
	checkKeys(t, runPayload, golden.RunTopKeys)
	if runPayload["executed"] != false {
		t.Fatalf("expected dry run executed=false, got %v", runPayload["executed"])
	}
	runItems := mustObjArray(t, runPayload["items"])
	if len(runItems) != 1 || runItems[0]["suite"] != "api" {
		t.Fatalf("unexpected run items: %v", runItems)
	}
}

func loadCLIContractGolden(t *testing.T) cliContractGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "cli_contract_command_golden.json"))
	if err != nil {
		t.Fatalf("read cli contract golden: %v", err)
	}
	var out cliContractGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal cli contract golden: %v", err)
	}
	return out
}

func runCLIJSON(t *testing.T, cli *contextcli.CLI, out, errOut *bytes.Buffer, args []string) map[string]any {
	t.Helper()
	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), args); code != 0 {
		t.Fatalf("cli run failed: %s", errOut.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal cli output: %v", err)
	}
	return payload
}

func mustObjArray(t *testing.T, value any) []map[string]any {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("value not array: %T", value)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("item not object: %T", v)
		}
		out = append(out, m)
	}
	return out
}

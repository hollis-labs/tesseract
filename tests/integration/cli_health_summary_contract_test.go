package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextcli"
	"github.com/hollis-labs/cortex/internal/contextpolicy"
	"github.com/hollis-labs/cortex/internal/contextstore"
)

type cliHealthSummaryGolden struct {
	JSONRequiredKeys  []string `json:"json_required_keys"`
	TableHeaderTokens []string `json:"table_header_tokens"`
	Statuses          []string `json:"statuses"`
}

func TestCLIHealthSummaryContractAgainstGolden(t *testing.T) {
	golden := loadCLIHealthSummaryGolden(t)
	root := t.TempDir()
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{
		Store:  store,
		Policy: contextpolicy.New(),
		Stdout: out,
		Stderr: errOut,
	}

	if code := cli.Run(context.Background(), []string{
		"context", "put",
		"--client-id", "editor",
		"--actor", "app:editor",
		"--namespace", "app/editor/session",
		"--key", "summary",
		"--json", `{"v":1}`,
	}); code != 0 {
		t.Fatalf("put failed: %s", errOut.String())
	}

	healthyJSON := runCLIHealthSummaryJSON(t, cli, out, errOut)
	checkKeys(t, healthyJSON, golden.JSONRequiredKeys)
	if healthyJSON["status"] != golden.Statuses[0] {
		t.Fatalf("expected first status %q, got %v", golden.Statuses[0], healthyJSON["status"])
	}

	if err := os.Remove(filepath.Join(root, "data", "records", "app", "editor", "session", "summary", "1.json")); err != nil {
		t.Fatalf("remove payload: %v", err)
	}
	degradedJSON := runCLIHealthSummaryJSON(t, cli, out, errOut)
	checkKeys(t, degradedJSON, golden.JSONRequiredKeys)
	if degradedJSON["status"] != golden.Statuses[1] {
		t.Fatalf("expected second status %q, got %v", golden.Statuses[1], degradedJSON["status"])
	}

	if err := os.RemoveAll(filepath.Join(root, "data", "records")); err != nil {
		t.Fatalf("remove records dir: %v", err)
	}
	table := runCLIHealthSummaryTable(t, cli, out, errOut)
	for _, token := range golden.TableHeaderTokens {
		if !strings.Contains(table, token) {
			t.Fatalf("missing table token %q in output: %s", token, table)
		}
	}
	if !strings.Contains(table, golden.Statuses[2]) {
		t.Fatalf("expected failing status in table output: %s", table)
	}
}

func runCLIHealthSummaryJSON(t *testing.T, cli *contextcli.CLI, out, errOut *bytes.Buffer) map[string]any {
	t.Helper()
	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{"context", "health", "--summary", "--output", "json"}); code != 0 {
		t.Fatalf("health summary json failed: %s", errOut.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal health summary json: %v", err)
	}
	return payload
}

func runCLIHealthSummaryTable(t *testing.T, cli *contextcli.CLI, out, errOut *bytes.Buffer) string {
	t.Helper()
	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{"context", "health", "--summary", "--output", "table"}); code != 0 {
		t.Fatalf("health summary table failed: %s", errOut.String())
	}
	return out.String()
}

func loadCLIHealthSummaryGolden(t *testing.T) cliHealthSummaryGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "cli_health_summary_contract_golden.json"))
	if err != nil {
		t.Fatalf("read cli health summary golden: %v", err)
	}
	var out cliHealthSummaryGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal cli health summary golden: %v", err)
	}
	return out
}

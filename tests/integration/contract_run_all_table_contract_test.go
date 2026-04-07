package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextcli"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

type contractRunAllTableGolden struct {
	HeaderTokens []string `json:"header_tokens"`
}

func TestContractRunAllTableContractAgainstGolden(t *testing.T) {
	golden := loadContractRunAllTableGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	jsonPayload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "all"})
	runItems := mustObjArray(t, jsonPayload["items"])

	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "all", "--output", "table"}); code != 0 {
		t.Fatalf("contract run all table failed: %s", errOut.String())
	}
	lines := parseNonEmptyLines(out.String())
	if len(lines) == 0 {
		t.Fatalf("empty table output")
	}
	for _, token := range golden.HeaderTokens {
		if !strings.Contains(lines[0], token) {
			t.Fatalf("missing header token %q in %q", token, lines[0])
		}
	}
	tableRows := 0
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			tableRows++
		}
	}
	if tableRows != len(runItems) {
		t.Fatalf("table/json row mismatch: table_rows=%d json_items=%d", tableRows, len(runItems))
	}
}

func loadContractRunAllTableGolden(t *testing.T) contractRunAllTableGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_all_table_golden.json"))
	if err != nil {
		t.Fatalf("read contract run all table golden: %v", err)
	}
	var out contractRunAllTableGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract run all table golden: %v", err)
	}
	return out
}

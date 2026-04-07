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

type contractListTableCountParityGolden struct {
	HeaderTokens []string `json:"header_tokens"`
}

func TestContractListTableCountParityContract(t *testing.T) {
	golden := loadContractListTableCountParityGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	jsonPayload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list"})
	items := mustObjArray(t, jsonPayload["items"])
	jsonCount := len(items)

	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "list", "--output", "table"}); code != 0 {
		t.Fatalf("contract list table failed: %s", errOut.String())
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
	if tableRows != jsonCount {
		t.Fatalf("table/json count mismatch: table_rows=%d json_count=%d", tableRows, jsonCount)
	}
}

func loadContractListTableCountParityGolden(t *testing.T) contractListTableCountParityGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_list_table_count_parity_golden.json"))
	if err != nil {
		t.Fatalf("read list table count parity golden: %v", err)
	}
	var out contractListTableCountParityGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal list table count parity golden: %v", err)
	}
	return out
}

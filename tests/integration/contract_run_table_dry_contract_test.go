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

type contractRunTableDryGolden struct {
	HeaderMarkers []string `json:"header_markers"`
	RowMarkers    []string `json:"row_markers"`
}

func TestContractRunTableDryContractAgainstGolden(t *testing.T) {
	golden := loadContractRunTableDryGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	if code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "api", "--output", "table"}); code != 0 {
		t.Fatalf("contract run dry table failed: %s", errOut.String())
	}
	body := out.String()
	for _, marker := range golden.HeaderMarkers {
		if !strings.Contains(body, marker) {
			t.Fatalf("missing header marker %q in output:\n%s", marker, body)
		}
	}
	for _, marker := range golden.RowMarkers {
		if !strings.Contains(body, marker) {
			t.Fatalf("missing row marker %q in output:\n%s", marker, body)
		}
	}
}

func loadContractRunTableDryGolden(t *testing.T) contractRunTableDryGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_table_dry_golden.json"))
	if err != nil {
		t.Fatalf("read contract run dry table golden: %v", err)
	}
	var out contractRunTableDryGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract run dry table golden: %v", err)
	}
	return out
}

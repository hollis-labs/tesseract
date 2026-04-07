package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextcli"
	"github.com/hollis-labs/cortex/internal/contextpolicy"
	"github.com/hollis-labs/cortex/internal/contextstore"
)

type contractRunDefaultOutputGolden struct {
	TopKeys  []string `json:"top_keys"`
	ItemKeys []string `json:"item_keys"`
}

func TestContractRunDefaultOutputContractAgainstGolden(t *testing.T) {
	golden := loadContractRunDefaultOutputGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	payload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "api"})
	checkKeys(t, payload, golden.TopKeys)
	if payload["executed"] != false {
		t.Fatalf("expected executed=false, got %v", payload["executed"])
	}
	items := mustObjArray(t, payload["items"])
	if len(items) != 1 {
		t.Fatalf("expected one suite item, got %d", len(items))
	}
	checkKeys(t, items[0], golden.ItemKeys)
}

func loadContractRunDefaultOutputGolden(t *testing.T) contractRunDefaultOutputGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_default_output_golden.json"))
	if err != nil {
		t.Fatalf("read contract run default-output golden: %v", err)
	}
	var out contractRunDefaultOutputGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract run default-output golden: %v", err)
	}
	return out
}

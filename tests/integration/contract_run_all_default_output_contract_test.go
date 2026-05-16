package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextcli"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

type contractRunAllDefaultOutputGolden struct {
	TopKeys  []string `json:"top_keys"`
	ItemKeys []string `json:"item_keys"`
}

func TestContractRunAllDefaultOutputContractAgainstGolden(t *testing.T) {
	golden := loadContractRunAllDefaultOutputGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	listPayload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list"})
	listItems := mustObjArray(t, listPayload["items"])

	runPayload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "all"})
	checkKeys(t, runPayload, golden.TopKeys)
	if runPayload["executed"] != false {
		t.Fatalf("expected executed=false, got %v", runPayload["executed"])
	}
	runItems := mustObjArray(t, runPayload["items"])
	if len(runItems) == 0 {
		t.Fatalf("expected non-empty run items for suite=all")
	}
	if len(runItems) != len(listItems) {
		t.Fatalf("suite count mismatch: run=%d list=%d", len(runItems), len(listItems))
	}
	if count, ok := runPayload["count"].(float64); !ok || int(count) != len(runItems) {
		t.Fatalf("count field mismatch: payload=%v items=%d", runPayload["count"], len(runItems))
	}
	checkKeys(t, runItems[0], golden.ItemKeys)
}

func loadContractRunAllDefaultOutputGolden(t *testing.T) contractRunAllDefaultOutputGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_all_default_output_golden.json"))
	if err != nil {
		t.Fatalf("read contract run all default-output golden: %v", err)
	}
	var out contractRunAllDefaultOutputGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract run all default-output golden: %v", err)
	}
	return out
}

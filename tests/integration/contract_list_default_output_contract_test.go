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

type contractListDefaultOutputGolden struct {
	TopKeys  []string `json:"top_keys"`
	ItemKeys []string `json:"item_keys"`
}

func TestContractListDefaultOutputContractAgainstGolden(t *testing.T) {
	golden := loadContractListDefaultOutputGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	payload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list"})
	checkKeys(t, payload, golden.TopKeys)
	items := mustObjArray(t, payload["items"])
	if len(items) == 0 {
		t.Fatalf("expected at least one suite item")
	}
	checkKeys(t, items[0], golden.ItemKeys)
}

func loadContractListDefaultOutputGolden(t *testing.T) contractListDefaultOutputGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_list_default_output_golden.json"))
	if err != nil {
		t.Fatalf("read contract list default-output golden: %v", err)
	}
	var out contractListDefaultOutputGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract list default-output golden: %v", err)
	}
	return out
}

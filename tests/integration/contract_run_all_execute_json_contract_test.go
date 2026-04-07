package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextcli"
	"github.com/hollis-labs/cortex/internal/contextpolicy"
	"github.com/hollis-labs/cortex/internal/contextstore"
)

type contractRunAllExecuteJSONGolden struct {
	TopKeys      []string `json:"top_keys"`
	ItemKeys     []string `json:"item_keys"`
	SuccessSuite string   `json:"success_suite"`
	FailureSuite string   `json:"failure_suite"`
}

func TestContractRunAllExecuteJSONContractAgainstGolden(t *testing.T) {
	golden := loadContractRunAllExecuteJSONGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
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
		ExecCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			cmd := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
			if strings.Contains(cmd, "APIErrorContract") {
				return []byte("simulated failure"), errors.New("simulated failure")
			}
			return []byte("simulated success"), nil
		},
	}

	payload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "all", "--execute"})
	checkKeys(t, payload, golden.TopKeys)
	if payload["executed"] != true {
		t.Fatalf("expected executed=true, got %v", payload["executed"])
	}
	items := mustObjArray(t, payload["items"])
	if len(items) == 0 {
		t.Fatalf("expected non-empty items")
	}
	checkKeys(t, items[0], golden.ItemKeys)

	hasSuccess := false
	hasFailure := false
	for _, item := range items {
		suite, _ := item["suite"].(string)
		okValue, _ := item["ok"].(bool)
		if suite == golden.SuccessSuite && okValue {
			hasSuccess = true
		}
		if suite == golden.FailureSuite && !okValue {
			hasFailure = true
		}
	}
	if !hasSuccess {
		t.Fatalf("missing success suite marker for %q", golden.SuccessSuite)
	}
	if !hasFailure {
		t.Fatalf("missing failure suite marker for %q", golden.FailureSuite)
	}
}

func loadContractRunAllExecuteJSONGolden(t *testing.T) contractRunAllExecuteJSONGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_all_execute_json_golden.json"))
	if err != nil {
		t.Fatalf("read contract run all execute-json golden: %v", err)
	}
	var out contractRunAllExecuteJSONGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract run all execute-json golden: %v", err)
	}
	return out
}

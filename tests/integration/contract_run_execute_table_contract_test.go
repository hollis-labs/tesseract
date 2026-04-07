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

type contractRunExecuteTableGolden struct {
	HeaderMarkers     []string `json:"header_markers"`
	SuccessRowMarkers []string `json:"success_row_markers"`
	FailureRowMarkers []string `json:"failure_row_markers"`
}

func TestContractRunExecuteTableContractAgainstGolden(t *testing.T) {
	golden := loadContractRunExecuteTableGolden(t)
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

	successOutput := runContractTable(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "api", "--execute", "--output", "table"})
	for _, marker := range golden.HeaderMarkers {
		if !strings.Contains(successOutput, marker) {
			t.Fatalf("missing table header marker %q in success output:\n%s", marker, successOutput)
		}
	}
	for _, marker := range golden.SuccessRowMarkers {
		if !strings.Contains(successOutput, marker) {
			t.Fatalf("missing success row marker %q in output:\n%s", marker, successOutput)
		}
	}

	failureOutput := runContractTable(t, cli, out, errOut, []string{"context", "contract", "run", "--suite", "api-errors", "--execute", "--output", "table"})
	for _, marker := range golden.HeaderMarkers {
		if !strings.Contains(failureOutput, marker) {
			t.Fatalf("missing table header marker %q in failure output:\n%s", marker, failureOutput)
		}
	}
	for _, marker := range golden.FailureRowMarkers {
		if !strings.Contains(failureOutput, marker) {
			t.Fatalf("missing failure row marker %q in output:\n%s", marker, failureOutput)
		}
	}
}

func loadContractRunExecuteTableGolden(t *testing.T) contractRunExecuteTableGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_execute_table_golden.json"))
	if err != nil {
		t.Fatalf("read contract run execute-table golden: %v", err)
	}
	var out contractRunExecuteTableGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal contract run execute-table golden: %v", err)
	}
	return out
}

func runContractTable(t *testing.T, cli *contextcli.CLI, out, errOut *bytes.Buffer, args []string) string {
	t.Helper()
	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), args); code != 0 {
		t.Fatalf("cli run failed: %s", errOut.String())
	}
	return out.String()
}

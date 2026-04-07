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

type contractRunExecuteInvalidOutputErrorGolden struct {
	StderrMarkers []string `json:"stderr_markers"`
}

func TestContractRunExecuteInvalidOutputErrorContractAgainstGolden(t *testing.T) {
	golden := loadContractRunExecuteInvalidOutputErrorGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "api", "--execute", "--output", "yaml"})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for invalid output mode with --execute")
	}
	stderr := errOut.String()
	for _, marker := range golden.StderrMarkers {
		if !strings.Contains(stderr, marker) {
			t.Fatalf("missing stderr marker %q in output:\n%s", marker, stderr)
		}
	}
}

func loadContractRunExecuteInvalidOutputErrorGolden(t *testing.T) contractRunExecuteInvalidOutputErrorGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_execute_invalid_output_error_golden.json"))
	if err != nil {
		t.Fatalf("read execute invalid output error golden: %v", err)
	}
	var out contractRunExecuteInvalidOutputErrorGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal execute invalid output error golden: %v", err)
	}
	return out
}

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextcli"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

type contractRunUnknownSuiteErrorGolden struct {
	StderrMarkers []string `json:"stderr_markers"`
}

func TestContractRunUnknownSuiteErrorContractAgainstGolden(t *testing.T) {
	golden := loadContractRunUnknownSuiteErrorGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "unknown-suite", "--output", "json"})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for unknown suite")
	}
	stderr := errOut.String()
	for _, marker := range golden.StderrMarkers {
		if !strings.Contains(stderr, marker) {
			t.Fatalf("missing stderr marker %q in output:\n%s", marker, stderr)
		}
	}
}

func loadContractRunUnknownSuiteErrorGolden(t *testing.T) contractRunUnknownSuiteErrorGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_run_unknown_suite_error_golden.json"))
	if err != nil {
		t.Fatalf("read unknown suite error golden: %v", err)
	}
	var out contractRunUnknownSuiteErrorGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal unknown suite error golden: %v", err)
	}
	return out
}

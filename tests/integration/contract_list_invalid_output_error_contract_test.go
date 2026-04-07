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

type contractListInvalidOutputErrorGolden struct {
	StderrMarkers []string `json:"stderr_markers"`
}

func TestContractListInvalidOutputErrorContractAgainstGolden(t *testing.T) {
	golden := loadContractListInvalidOutputErrorGolden(t)
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	code := cli.Run(context.Background(), []string{"context", "contract", "list", "--output", "yaml"})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for invalid output mode")
	}
	stderr := errOut.String()
	for _, marker := range golden.StderrMarkers {
		if !strings.Contains(stderr, marker) {
			t.Fatalf("missing stderr marker %q in output:\n%s", marker, stderr)
		}
	}
}

func loadContractListInvalidOutputErrorGolden(t *testing.T) contractListInvalidOutputErrorGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixtures", "contract_list_invalid_output_error_golden.json"))
	if err != nil {
		t.Fatalf("read list invalid output error golden: %v", err)
	}
	var out contractListInvalidOutputErrorGolden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal list invalid output error golden: %v", err)
	}
	return out
}

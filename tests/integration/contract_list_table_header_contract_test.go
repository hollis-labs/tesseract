package integration

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextcli"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestContractListTableHeaderContract(t *testing.T) {
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	if code := cli.Run(context.Background(), []string{"context", "contract", "list", "--output", "table"}); code != 0 {
		t.Fatalf("contract list table failed: %s", errOut.String())
	}
	lines := parseNonEmptyLines(out.String())
	if len(lines) == 0 {
		t.Fatalf("empty table output")
	}
	header := lines[0]
	suiteIdx := strings.Index(header, "SUITE")
	commandIdx := strings.Index(header, "COMMAND")
	if suiteIdx < 0 || commandIdx < 0 {
		t.Fatalf("table header missing tokens: %q", header)
	}
	if suiteIdx >= commandIdx {
		t.Fatalf("table header token order drift: %q", header)
	}
}

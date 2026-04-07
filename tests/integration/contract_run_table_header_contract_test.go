package integration

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextcli"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

func TestContractRunTableHeaderContract(t *testing.T) {
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "api", "--output", "table"}); code != 0 {
		t.Fatalf("contract run dry table failed: %s", errOut.String())
	}
	dryLines := parseNonEmptyLines(out.String())
	assertHeaderTokensInOrder(t, firstLine(dryLines), "SUITE", "COMMAND")

	out.Reset()
	errOut.Reset()
	if code := cli.Run(context.Background(), []string{"context", "contract", "run", "--suite", "api", "--execute", "--output", "table"}); code != 0 {
		t.Fatalf("contract run execute table failed: %s", errOut.String())
	}
	execLines := parseNonEmptyLines(out.String())
	assertHeaderTokensInOrder(t, firstLine(execLines), "SUITE", "OK", "COMMAND")
}

func firstLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func assertHeaderTokensInOrder(t *testing.T, header string, tokens ...string) {
	t.Helper()
	if strings.TrimSpace(header) == "" {
		t.Fatalf("empty header")
	}
	last := -1
	for _, token := range tokens {
		idx := strings.Index(header, token)
		if idx < 0 {
			t.Fatalf("missing header token %q in %q", token, header)
		}
		if idx < last {
			t.Fatalf("header token order drift for %q in %q", token, header)
		}
		last = idx
	}
}

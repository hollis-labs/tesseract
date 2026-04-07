package integration

import (
	"bytes"
	"context"
	"testing"

	"github.com/hollis-labs/cortex/internal/contextcli"
	"github.com/hollis-labs/cortex/internal/contextpolicy"
	"github.com/hollis-labs/cortex/internal/contextstore"
)

func TestContractListDefaultOutputDeterministicContract(t *testing.T) {
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	run := func() []string {
		t.Helper()
		payload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list"})
		items := mustObjArray(t, payload["items"])
		names := make([]string, 0, len(items))
		for _, item := range items {
			name, ok := item["name"].(string)
			if !ok || name == "" {
				t.Fatalf("invalid suite name entry: %v", item["name"])
			}
			names = append(names, name)
		}
		return names
	}

	first := run()
	if len(first) == 0 {
		t.Fatalf("contract list returned no suites")
	}
	second := run()
	third := run()
	if len(second) != len(first) || len(third) != len(first) {
		t.Fatalf("suite count drift: first=%d second=%d third=%d", len(first), len(second), len(third))
	}
	for i := range first {
		if second[i] != first[i] {
			t.Fatalf("ordering drift at index %d between run1/run2: %q vs %q", i, first[i], second[i])
		}
		if third[i] != first[i] {
			t.Fatalf("ordering drift at index %d between run1/run3: %q vs %q", i, first[i], third[i])
		}
	}
}

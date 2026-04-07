package integration

import (
	"bytes"
	"context"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextcli"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

func TestContractListCountParityContract(t *testing.T) {
	store, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cli := &contextcli.CLI{Store: store, Policy: contextpolicy.New(), Stdout: out, Stderr: errOut}

	payload := runCLIJSON(t, cli, out, errOut, []string{"context", "contract", "list"})
	items := mustObjArray(t, payload["items"])
	count, ok := payload["count"].(float64)
	if !ok {
		t.Fatalf("count field is not numeric: %T", payload["count"])
	}
	if int(count) != len(items) {
		t.Fatalf("count parity mismatch: count=%d items=%d", int(count), len(items))
	}
}

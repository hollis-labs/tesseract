package parity

// Helpers shared across the parity suite, deliberately NOT behind the `drift`
// build tag.
//
// repoRoot lived in toolname_drift_test.go until CW-20260911-0050 put that file
// behind the tag. skill_examples_test.go keeps blocking and uses it, so leaving
// it there would have made the blocking half of this package stop compiling on
// an ordinary `go test ./...` — a build-tag split fails loudly in exactly this
// way, which is the one good thing about it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// repoRoot resolves the module root from this package's working directory and
// verifies it, so a moved test file fails loudly instead of scanning nothing.
func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %q has no go.mod (%v) — this test's relative path is stale", root, err)
	}
	return root
}

// registeredToolNames introspects the live tool surface the same way
// TestMCPRegistrationMatchesCatalog does.
func registeredToolNames(t *testing.T) map[string]struct{} {
	t.Helper()
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("toolname-drift-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	names := map[string]struct{}{}
	for name := range srv.ListTools() {
		names[name] = struct{}{}
	}
	if len(names) == 0 {
		t.Fatal("registered zero tools — the adapter is not wired, so any clean result here is meaningless")
	}
	return names
}

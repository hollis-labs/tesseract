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

	gomcpserver "github.com/hollis-labs/libs/plugin-mcp/go-mcp/server"
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
	srv := gomcpserver.NewServer("toolname-drift-test", "0.0.0")
	adapter.RegisterAllTools(srv)

	names := map[string]struct{}{}
	for _, def := range srv.ToolDefinitions() {
		names[def.Name] = struct{}{}
	}
	if len(names) == 0 {
		t.Fatal("registered zero tools — the adapter is not wired, so any clean result here is meaningless")
	}
	return names
}

// toolDefByName looks up one registered tool's definition by name. Its only
// callers are in touch_loop_docs_test.go, which is `//go:build drift`-gated
// and excluded from the default build (see AGENTS.md), so the default lint
// run sees it as unused even though it is not.
func toolDefByName(srv *gomcpserver.Server, name string) (gomcpserver.ToolDefinition, bool) { //nolint:unused // used only by drift-tagged tests
	for _, def := range srv.ToolDefinitions() {
		if def.Name == name {
			return def, true
		}
	}
	return gomcpserver.ToolDefinition{}, false
}

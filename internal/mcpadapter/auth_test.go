package mcpadapter

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/authscope"
)

// TestEveryRegisteredToolHasAScope is the MCP half of deny-by-default: a tool
// registered without a tesseract.<verb> would be refused under enforce.
func TestEveryRegisteredToolHasAScope(t *testing.T) {
	srv := registeredSurface(t)
	registered := map[string]bool{}
	for _, def := range srv.ToolDefinitions() {
		registered[def.Name] = true
		scope, ok := toolScopes[def.Name]
		if !ok {
			t.Errorf("tool %q has no entry in toolScopes (denied as route_not_mapped under enforce)", def.Name)
			continue
		}
		valid := false
		for _, c := range authscope.Canonical() {
			valid = valid || c == scope
		}
		if !valid {
			t.Errorf("tool %q maps to unknown scope %q", def.Name, scope)
		}
	}
	for name := range toolScopes {
		if !registered[name] {
			t.Errorf("toolScopes names %q, which is not a registered tool", name)
		}
	}
}

func TestToolScopeTableDeniesUnknownTools(t *testing.T) {
	tbl := ToolScopeTable()
	if scope, known := tbl.ForTool("tesseract_recall"); !known || scope != authscope.Read {
		t.Fatalf("tesseract_recall = (%q, %v)", scope, known)
	}
	if _, known := tbl.ForTool("brand_new_tool"); known {
		t.Fatal("an unmapped tool must not resolve to a scope")
	}
}

package mcpadapter

import (
	"testing"

	gomcpserver "github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestMemoryKnowledgeUnifiedToolsAnnotated enforces that every memory / knowledge /
// cross-domain / meta MCP tool declares its annotation hints deliberately.
//
// Under go-mcp, Tool.ReadOnlyHint/DestructiveHint/IdempotentHint/OpenWorldHint are
// required plain bool fields — a registration cannot omit them, so the presence
// check mark3labs needed (nil means "never set") no longer applies; the compiler
// enforces it now. What remains worth asserting is the SEMANTIC content for the
// tools where reinforcement makes the "obvious" value wrong.
//
// This is a drift guard: keep the enforced list below in sync with
// docs/superpowers/specs/2026-04-19-mcp-surface-v2.md §5.4.
func TestMemoryKnowledgeUnifiedToolsAnnotated(t *testing.T) {
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = ks

	srv := gomcpserver.NewServer("test", "0.0.0")
	a.RegisterAllTools(srv)

	// Authoritative list of v2-rewritten tools. Keep in sync with spec §5.4.
	enforced := []string{
		// memory domain
		"memory_write",
		"memory_promote",
		// knowledge domain
		"knowledge_write",
		// cross-domain
		"tesseract_get",
		"tesseract_history",
		"tesseract_recall",
		"tesseract_get_revision",
		"tesseract_deprecate",
		"tesseract_touch",
		// meta
		"tesseract_skills",
	}

	registered := map[string]gomcpserver.ToolDefinition{}
	for _, def := range srv.ToolDefinitions() {
		registered[def.Name] = def
	}
	if len(registered) == 0 {
		t.Fatal("srv.ToolDefinitions() returned nothing; RegisterAllTools wired no tools")
	}

	for _, name := range enforced {
		if _, ok := registered[name]; !ok {
			t.Errorf("tool %q not registered; RegisterAllTools or handler wiring broken", name)
		}
	}

	// These look like reads at the protocol level, but both can reinforce
	// activation/access_count. The annotation must describe that observable
	// mutation so a client does not treat repeated calls as side-effect free.
	for _, name := range []string{"tesseract_get", "tesseract_get_revision"} {
		def, ok := registered[name]
		if !ok {
			continue // the registration assertion above already reports this
		}
		if def.Annotations.ReadOnlyHint {
			t.Errorf("tool %q ReadOnlyHint = true, want false: the tool reinforces activation", name)
		}
		if def.Annotations.IdempotentHint {
			t.Errorf("tool %q IdempotentHint = true, want false: each call can reinforce again", name)
		}
	}
}

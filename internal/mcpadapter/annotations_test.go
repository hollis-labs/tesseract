package mcpadapter

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/server"
)

// TestMemoryKnowledgeUnifiedToolsAnnotated enforces that every memory / knowledge /
// unified / meta MCP tool has non-nil ReadOnlyHint and IdempotentHint annotations.
// Context-domain tools are NOT enforced here — they carry only a pointer-footer in v2
// per spec §5.5, and a future spec will bring them to the same standard.
//
// This is a drift guard: new tools added to these domains must set both annotations,
// and existing tools must not have them stripped. Keep the enforced list below in
// sync with docs/superpowers/specs/2026-04-19-mcp-surface-v2.md §5.4.
func TestMemoryKnowledgeUnifiedToolsAnnotated(t *testing.T) {
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)

	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = ks

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	// Authoritative list of v2-rewritten tools. Keep in sync with spec §5.4.
	enforced := []string{
		// memory domain
		"memory_write",
		"memory_get",
		"memory_history",
		"memory_recall",
		"memory_get_revision",
		"memory_promote",
		"memory_deprecate",
		// knowledge domain
		"knowledge_write",
		"knowledge_get",
		"knowledge_history",
		// unified / meta
		"conduit_lookup",
		"vanta_skills",
	}

	registered := srv.ListTools()
	if registered == nil {
		t.Fatal("srv.ListTools() returned nil; RegisterAllTools wired no tools")
	}

	for _, name := range enforced {
		st, ok := registered[name]
		if !ok {
			t.Errorf("tool %q not registered; RegisterAllTools or handler wiring broken", name)
			continue
		}
		if st.Tool.Annotations.ReadOnlyHint == nil {
			t.Errorf("tool %q missing ReadOnlyHint annotation (v2 template requires it)", name)
		}
		if st.Tool.Annotations.IdempotentHint == nil {
			t.Errorf("tool %q missing IdempotentHint annotation (v2 template requires it)", name)
		}
	}
}

package mcpadapter

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/server"
)

// TestMemoryKnowledgeUnifiedToolsAnnotated enforces that every memory / knowledge /
// cross-domain / meta MCP tool has non-nil ReadOnlyHint and IdempotentHint annotations.
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

	// These look like reads at the protocol level, but both can reinforce
	// activation/access_count. The annotation must describe that observable
	// mutation so a client does not treat repeated calls as side-effect free.
	for _, name := range []string{"tesseract_get", "tesseract_get_revision"} {
		st := registered[name]
		if st.Tool.Annotations.ReadOnlyHint == nil || st.Tool.Annotations.IdempotentHint == nil {
			continue // the required-annotation assertions above already report this
		}
		if *st.Tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q ReadOnlyHint = true, want false: the tool reinforces activation", name)
		}
		if *st.Tool.Annotations.IdempotentHint {
			t.Errorf("tool %q IdempotentHint = true, want false: each call can reinforce again", name)
		}
	}
}

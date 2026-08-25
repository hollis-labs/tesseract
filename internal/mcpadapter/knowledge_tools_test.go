package mcpadapter

import (
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/server"
)

// TestKnowledgeWriteToolDescribesClosedKindVocabulary guards the tool
// description against advertising a kind set the write path does not accept.
//
// This surface matters more than the skills: an agent reads the tool
// description before it reads a skill, so a stale list here is the first thing
// it sees and the rejection is the last. The description is rendered from
// memory.KnowledgeKindList() rather than restated, and this test asserts the
// rendering actually reaches the registered tool.
func TestKnowledgeWriteToolDescribesClosedKindVocabulary(t *testing.T) {
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})

	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = knowledge.New(ms)

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	st, ok := srv.ListTools()["knowledge_write"]
	if !ok {
		t.Fatal("knowledge_write not registered")
	}

	schema, err := st.Tool.InputSchema.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	desc := string(schema)

	for _, kind := range memory.KnowledgeKindVocabulary() {
		if !strings.Contains(desc, kind) {
			t.Errorf("knowledge_write `kind` description omits canonical kind %q", kind)
		}
	}

	// Values the write path rejects must not be presented as examples.
	for _, stale := range []string{"mcp-server", "issue/bug", "session-close"} {
		if strings.Contains(desc, stale) {
			t.Errorf("knowledge_write description still offers %q, which the write path rejects", stale)
		}
	}

	// And the description must say the set is closed, not merely list values —
	// an "e.g." list reads as open and is what this replaced.
	if !strings.Contains(strings.ToLower(desc), "closed vocabulary") {
		t.Error("knowledge_write `kind` description does not state that the vocabulary is closed")
	}
}

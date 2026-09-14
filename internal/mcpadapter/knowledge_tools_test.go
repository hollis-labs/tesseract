package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
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

func TestKnowledgeWrite_UserScopeRejectionTeachesCorrectScope(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write")
	a.KnowledgeStore = knowledge.New(a.MemoryStore)

	for _, tc := range []struct {
		name  string
		actor any
	}{
		{"omitted actor", nil},
		{"explicit agent actor", "agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{
				"namespace":       "user/chrispian/knowledge/framework",
				"key":             "framework.go-providers",
				"kind":            "package",
				"source":          "filesystem",
				"pointer_scheme":  "file",
				"pointer_locator": "/pkg/go-providers",
				"summary":         "go-providers multi-provider adapter",
				"author_agent_id": "indexer",
				"session_id":      "indexer:01HX",
			}
			if tc.actor != nil {
				args["actor"] = tc.actor
			}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = args
			res, err := a.handleKnowledgeWrite(context.Background(), req)
			if err != nil {
				t.Fatalf("handleKnowledgeWrite: %v", err)
			}
			body := parseResult(t, res)
			if body["code"] != string(codeNamespaceNotPermitted) {
				t.Fatalf("code = %v, want %s; body=%v", body["code"], codeNamespaceNotPermitted, body)
			}
			msg, _ := body["message"].(string)
			for _, needle := range []string{
				"writes to protected namespace",
				"require actor=user",
				"project/{slug}",
				"system",
				"tesseract_skills namespaces",
			} {
				if !strings.Contains(msg, needle) {
					t.Errorf("error message missing %q:\n%s", needle, msg)
				}
			}
		})
	}
}

package mcpadapter

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/mark3labs/mcp-go/mcp"
)

func validMemoryToolArgs() map[string]any {
	return map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "facet.mcp",
		"author_agent_id": "test",
		"trigger":         "explicit",
		"session_id":      "s1",
		"origin":          "user",
		"confidence":      0.9,
		"payload_summary": "valid memory",
	}
}

func validKnowledgeToolArgs() map[string]any {
	return map[string]any{
		"namespace":       "user/chrispian/knowledge/docs",
		"key":             "facet-mcp",
		"kind":            "doc",
		"source":          "filesystem",
		"pointer_scheme":  "file",
		"pointer_locator": "/tmp/doc",
		"summary":         "valid knowledge",
		"author_agent_id": "test",
		"session_id":      "s1",
	}
}

func TestMCPWriteToolsEnforceDomainFacetContract(t *testing.T) {
	tests := []struct {
		name      string
		knowledge bool
		mutate    func(map[string]any)
		wantCode  string
	}{
		{name: "valid memory"},
		{name: "valid knowledge", knowledge: true},
		{
			name:      "knowledge rejects missing kind",
			knowledge: true,
			mutate:    func(args map[string]any) { args["kind"] = "" },
			wantCode:  "validation_error",
		},
		{
			name:      "knowledge rejects unknown kind",
			knowledge: true,
			mutate:    func(args map[string]any) { args["kind"] = "mcp-server" },
			wantCode:  "validation_error",
		},
		{
			name:      "knowledge rejects missing source",
			knowledge: true,
			mutate:    func(args map[string]any) { args["source"] = "" },
			wantCode:  "validation_error",
		},
		{
			name:      "knowledge rejects missing pointer scheme",
			knowledge: true,
			mutate:    func(args map[string]any) { args["pointer_scheme"] = "" },
			wantCode:  "validation_error",
		},
		{
			name:      "knowledge rejects missing pointer locator",
			knowledge: true,
			mutate:    func(args map[string]any) { args["pointer_locator"] = "" },
			wantCode:  "validation_error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newMemoryAdapter(t, "memory:write")
			a.KnowledgeStore = knowledge.New(a.MemoryStore)
			args := validMemoryToolArgs()
			handler := a.handleMemoryWrite
			if tc.knowledge {
				args = validKnowledgeToolArgs()
				handler = a.handleKnowledgeWrite
			}
			if tc.mutate != nil {
				tc.mutate(args)
			}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = args
			res, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("handler transport error: %v", err)
			}
			body := parseResult(t, res)
			if tc.wantCode != "" {
				if body["code"] != tc.wantCode {
					t.Fatalf("code = %v, want %s; body=%v", body["code"], tc.wantCode, body)
				}
				var count int
				if err := a.MemoryStore.DB().QueryRow(`SELECT COUNT(*) FROM memory_revisions`).Scan(&count); err != nil {
					t.Fatalf("count revisions: %v", err)
				}
				if count != 0 {
					t.Fatalf("rejected call persisted %d revisions", count)
				}
				return
			}
			if body["revision_id"] == nil || body["revision_id"] == "" {
				t.Fatalf("valid control returned no revision: %v", body)
			}
		})
	}
}

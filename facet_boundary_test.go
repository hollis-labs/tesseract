package tesseract_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tesseract/memory"
)

func publicMemoryInput() memory.WriteInput {
	return memory.WriteInput{
		Domain:     memory.DomainMemory,
		Namespace:  "user/test/memory/notes",
		MemoryKey:  "facet.boundary",
		Status:     memory.StatusDraft,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerManual,
		SessionID:  "s1",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Payload:    memory.Payload{Summary: "public facade facet contract"},
	}
}

func publicKnowledgeFacets() memory.Facets {
	return memory.Facets{
		Kind:    "doc",
		Source:  "filesystem",
		Pointer: &memory.Pointer{Scheme: "file", Locator: "/tmp/doc"},
	}
}

func TestPublicFacadeWriteEnforcesDomainFacetContract(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*memory.WriteInput)
		wantErr bool
	}{
		{name: "valid memory"},
		{
			name: "memory with knowledge facets",
			mutate: func(in *memory.WriteInput) {
				in.Facets = publicKnowledgeFacets()
			},
			wantErr: true,
		},
		{
			name: "valid knowledge through raw public input",
			mutate: func(in *memory.WriteInput) {
				in.Domain = memory.DomainKnowledge
				in.Namespace = "user/test/knowledge/docs"
				in.Facets = publicKnowledgeFacets()
			},
		},
		{
			name: "knowledge with unknown kind",
			mutate: func(in *memory.WriteInput) {
				in.Domain = memory.DomainKnowledge
				in.Namespace = "user/test/knowledge/docs"
				in.Facets = publicKnowledgeFacets()
				in.Facets.Kind = "mcp-server"
			},
			wantErr: true,
		},
		{
			name: "knowledge without source",
			mutate: func(in *memory.WriteInput) {
				in.Domain = memory.DomainKnowledge
				in.Namespace = "user/test/knowledge/docs"
				in.Facets = publicKnowledgeFacets()
				in.Facets.Source = ""
			},
			wantErr: true,
		},
		{
			name: "knowledge without pointer locator",
			mutate: func(in *memory.WriteInput) {
				in.Domain = memory.DomainKnowledge
				in.Namespace = "user/test/knowledge/docs"
				in.Facets = publicKnowledgeFacets()
				in.Facets.Pointer.Locator = ""
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := openTestTesseract(t)
			defer client.Close()
			in := publicMemoryInput()
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			rev, err := client.WriteMemory(context.Background(), in)
			if tc.wantErr {
				if !errors.Is(err, memory.ErrInvalidInput) {
					t.Fatalf("error = %v, want memory.ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("WriteMemory: %v", err)
			}
			if rev.RevisionID == "" {
				t.Fatal("valid control returned no revision")
			}
		})
	}
}

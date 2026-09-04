package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// TestWriteRevision_DefaultDomainIsMemory verifies backward-compatibility:
// a WriteInput with an empty Domain field defaults to domains.Memory.
func TestWriteRevision_DefaultDomainIsMemory(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.default"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if rev.Domain != domains.Memory {
		t.Errorf("rev.Domain = %q, want %q", rev.Domain, domains.Memory)
	}

	// Read-back path preserves domain.
	got, err := ms.GetRevisionByID(context.Background(), rev.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if got.Domain != domains.Memory {
		t.Errorf("read-back Domain = %q, want %q", got.Domain, domains.Memory)
	}

	// State carries the same domain.
	st, err := ms.GetState(context.Background(), rev.MemoryID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.Domain != domains.Memory {
		t.Errorf("state Domain = %q, want %q", st.Domain, domains.Memory)
	}
}

// TestWriteRevision_KnowledgeDomain writes under the knowledge domain and
// confirms namespace policy validation accepts knowledge-shaped namespaces.
func TestWriteRevision_KnowledgeDomain(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("framework.go-providers")
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/knowledge/framework"
	in.Facets = validKnowledgeFacets()

	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("WriteRevision (knowledge): %v", err)
	}
	if rev.Domain != domains.Knowledge {
		t.Errorf("rev.Domain = %q, want %q", rev.Domain, domains.Knowledge)
	}
}

// TestWriteRevision_KnowledgeDomainRejectsMemoryNamespace ensures the
// KnowledgeDomain policy rejects namespaces without a `knowledge` segment.
func TestWriteRevision_KnowledgeDomainRejectsMemoryNamespace(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	in := sampleInput("framework.bad")
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/memory/notes"
	in.Facets = validKnowledgeFacets()

	_, err := ms.WriteRevision(context.Background(), in)
	if err == nil {
		t.Fatal("expected knowledge policy to reject memory namespace")
	}
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput wrap", err)
	}
	if !strings.Contains(err.Error(), "knowledge") {
		t.Errorf("err message missing 'knowledge' hint: %v", err)
	}
}

func validKnowledgeFacets() memory.Facets {
	return memory.Facets{
		Kind:    "package",
		Source:  "filesystem",
		Pointer: &memory.Pointer{Scheme: "file", Locator: "/abs/path/to/pkg"},
	}
}

// TestWriteRevision_KnowledgeFactsPersist writes a knowledge revision with
// all facet fields populated and confirms they round-trip through storage.
func TestWriteRevision_KnowledgeFactsPersist(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	resolvedAt := time.Now().UTC().Truncate(time.Second)
	in := sampleInput("framework.go-providers")
	in.Domain = domains.Knowledge
	in.Namespace = "user/chrispian/knowledge/framework"
	in.Facets = memory.Facets{
		Kind:   "package",
		Source: "filesystem",
		Pointer: &memory.Pointer{
			Scheme:     "file",
			Locator:    "/abs/path/to/pkg",
			ResolvedAt: &resolvedAt,
		},
	}

	rev, err := ms.WriteRevision(context.Background(), in)
	if err != nil {
		t.Fatalf("WriteRevision (knowledge): %v", err)
	}

	got, err := ms.GetRevisionByID(context.Background(), rev.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if got.Facets.Kind != "package" {
		t.Errorf("facet.Kind = %q, want %q", got.Facets.Kind, "package")
	}
	if got.Facets.Source != "filesystem" {
		t.Errorf("facet.Source = %q, want %q", got.Facets.Source, "filesystem")
	}
	if got.Facets.Pointer == nil {
		t.Fatal("facet.Pointer is nil; want populated")
	}
	if got.Facets.Pointer.Scheme != "file" {
		t.Errorf("pointer.Scheme = %q, want %q", got.Facets.Pointer.Scheme, "file")
	}
	if got.Facets.Pointer.Locator != "/abs/path/to/pkg" {
		t.Errorf("pointer.Locator = %q, want %q", got.Facets.Pointer.Locator, "/abs/path/to/pkg")
	}
	if got.Facets.Pointer.ResolvedAt == nil || !got.Facets.Pointer.ResolvedAt.Equal(resolvedAt) {
		t.Errorf("pointer.ResolvedAt = %v, want %v", got.Facets.Pointer.ResolvedAt, resolvedAt)
	}
}

// TestWriteRevision_MemoryDomainLeavesFactsZero verifies memory-domain
// writes leave facet columns NULL/zero.
func TestWriteRevision_MemoryDomainLeavesFactsZero(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()

	rev, err := ms.WriteRevision(context.Background(), sampleInput("prefs.no_facets"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	got, err := ms.GetRevisionByID(context.Background(), rev.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if !got.Facets.IsZero() {
		t.Errorf("memory-domain facets not zero: %+v", got.Facets)
	}
}

func TestWriteRevision_EnforcesDomainFacetContract(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*memory.WriteInput)
		wantErr string
	}{
		{
			name: "memory accepts zero facets",
		},
		{
			name: "memory rejects kind",
			mutate: func(in *memory.WriteInput) {
				in.Facets.Kind = "note"
			},
			wantErr: "memory revisions must not carry knowledge facets",
		},
		{
			name: "memory rejects source",
			mutate: func(in *memory.WriteInput) {
				in.Facets.Source = "manual"
			},
			wantErr: "memory revisions must not carry knowledge facets",
		},
		{
			name: "memory rejects even an empty pointer object",
			mutate: func(in *memory.WriteInput) {
				in.Facets.Pointer = &memory.Pointer{}
			},
			wantErr: "memory revisions must not carry knowledge facets",
		},
		{
			name: "knowledge accepts complete canonical facets",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
			},
		},
		{
			name: "knowledge rejects missing kind",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
				in.Facets.Kind = ""
			},
			wantErr: "facet.kind is required",
		},
		{
			name: "knowledge rejects kind outside the closed vocabulary",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
				in.Facets.Kind = "mcp-server"
			},
			wantErr: "not a canonical knowledge kind",
		},
		{
			name: "knowledge rejects missing source",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
				in.Facets.Source = ""
			},
			wantErr: "facet.source is required",
		},
		{
			name: "knowledge rejects nil pointer",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
				in.Facets.Pointer = nil
			},
			wantErr: "facet.pointer.scheme and facet.pointer.locator are required",
		},
		{
			name: "knowledge rejects missing pointer scheme",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
				in.Facets.Pointer.Scheme = ""
			},
			wantErr: "facet.pointer.scheme and facet.pointer.locator are required",
		},
		{
			name: "knowledge rejects missing pointer locator",
			mutate: func(in *memory.WriteInput) {
				in.Domain = domains.Knowledge
				in.Namespace = "user/chrispian/knowledge/framework"
				in.Facets = validKnowledgeFacets()
				in.Facets.Pointer.Locator = ""
			},
			wantErr: "facet.pointer.scheme and facet.pointer.locator are required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ms, cleanup := newTestStore(t)
			defer cleanup()
			in := sampleInput("facet.contract")
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			rev, err := ms.WriteRevision(context.Background(), in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("WriteRevision: %v", err)
				}
				if rev.RevisionID == "" {
					t.Fatal("valid control returned no revision")
				}
				return
			}
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tc.wantErr)
			}
			var count int
			if countErr := ms.DB().QueryRow(`SELECT COUNT(*) FROM memory_revisions`).Scan(&count); countErr != nil {
				t.Fatalf("count revisions: %v", countErr)
			}
			if count != 0 {
				t.Fatalf("rejected input persisted %d revision rows", count)
			}
		})
	}
}

func TestWriteRevision_AcceptsEveryCanonicalKnowledgeKind(t *testing.T) {
	kinds := memory.KnowledgeKindVocabulary()
	if len(kinds) == 0 {
		t.Fatal("canonical knowledge kind vocabulary is empty")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			ms, cleanup := newTestStore(t)
			defer cleanup()
			in := sampleInput("kind." + kind)
			in.Domain = domains.Knowledge
			in.Namespace = "user/chrispian/knowledge/framework"
			in.Facets = validKnowledgeFacets()
			in.Facets.Kind = kind
			if _, err := ms.WriteRevision(context.Background(), in); err != nil {
				t.Fatalf("canonical kind rejected at persistence boundary: %v", err)
			}
		})
	}
}

// NOTE: A direct domain-mismatch test through WriteRevision is impractical
// because each domain's ValidateNamespace rejects the other's namespace shape
// — so the same (namespace, memory_key) tuple cannot be reached under two
// domains via the public API. The defensive check in resolveOrCreateMemory
// is still present as belt-and-suspenders for future domain configurations
// whose namespaces could overlap.

package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/vanta-conduit/internal/domains"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
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
	in.Namespace = "user/chrispian/memory"

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

// NOTE: A direct domain-mismatch test through WriteRevision is impractical
// because each domain's ValidateNamespace rejects the other's namespace shape
// — so the same (namespace, memory_key) tuple cannot be reached under two
// domains via the public API. The defensive check in resolveOrCreateMemory
// is still present as belt-and-suspenders for future domain configurations
// whose namespaces could overlap.

package knowledge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
	"github.com/hollis-labs/vanta-conduit/domains"
	"github.com/hollis-labs/vanta-conduit/internal/knowledge"
	"github.com/hollis-labs/vanta-conduit/internal/memory"
)

func newTestStore(t *testing.T) *knowledge.Store {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return knowledge.New(mem)
}

func validInput() knowledge.WriteInput {
	return knowledge.WriteInput{
		Namespace: "user/chrispian/knowledge/framework",
		Key:       "framework.go-providers",
		Kind:      "package",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/abs/path"},
		Summary:   "go-providers: multi-provider AI adapter",
		Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		SessionID: "indexer:01HX",
	}
}

func TestWrite_Success(t *testing.T) {
	s := newTestStore(t)
	rev, err := s.Write(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rev.Domain != domains.Knowledge {
		t.Errorf("Domain = %q, want %q", rev.Domain, domains.Knowledge)
	}
	if rev.Facets.Kind != "package" {
		t.Errorf("Facets.Kind = %q, want package", rev.Facets.Kind)
	}
	if rev.Facets.Pointer == nil || rev.Facets.Pointer.ResolvedAt == nil {
		t.Error("ResolvedAt should be auto-populated when nil")
	}
}

func TestWrite_MissingKindRejected(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Kind = ""
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput wrap", err)
	}
}

func TestWrite_MissingPointerRejected(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Pointer = memory.Pointer{}
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for missing pointer")
	}
}

func TestWrite_MemoryNamespaceRejected(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Namespace = "user/chrispian/memory"
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected knowledge-policy rejection for memory namespace")
	}
}

func TestGetCurrent_Success(t *testing.T) {
	s := newTestStore(t)
	written, err := s.Write(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.GetCurrent(context.Background(), validInput().Namespace, validInput().Key)
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if got.RevisionID != written.RevisionID {
		t.Errorf("RevisionID = %q, want %q", got.RevisionID, written.RevisionID)
	}
	if got.Domain != domains.Knowledge {
		t.Errorf("Domain = %q, want knowledge", got.Domain)
	}
}

func TestGetCurrent_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetCurrent(context.Background(), "user/chrispian/knowledge/missing", "no-such-key")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound wrap", err)
	}
}

func TestGetHistory_MultipleRevisions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.Write(ctx, validInput())
	if err != nil {
		t.Fatalf("Write first: %v", err)
	}
	in2 := validInput()
	in2.Supersedes = first.RevisionID
	in2.Summary = "go-providers: updated summary"
	second, err := s.Write(ctx, in2)
	if err != nil {
		t.Fatalf("Write second: %v", err)
	}
	revs, err := s.GetHistory(ctx, validInput().Namespace, validInput().Key)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("len(revs) = %d, want 2", len(revs))
	}
	if revs[0].RevisionID != second.RevisionID {
		t.Errorf("newest-first broken: revs[0] = %q, want %q", revs[0].RevisionID, second.RevisionID)
	}
	for _, rev := range revs {
		if rev.Domain != domains.Knowledge {
			t.Errorf("revision %q has Domain %q, want knowledge", rev.RevisionID, rev.Domain)
		}
	}
}

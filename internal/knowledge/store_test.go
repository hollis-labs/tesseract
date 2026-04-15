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

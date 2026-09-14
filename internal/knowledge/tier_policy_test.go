package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newTestKnowledgeWithDB(t *testing.T) (*knowledge.Store, *contextstore.Store) {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	mem.SetNamespaceRegistrar(cs)
	return knowledge.New(mem), cs
}

func validKnowledgeInput(ns, key string) knowledge.WriteInput {
	return knowledge.WriteInput{
		Namespace: ns,
		Key:       key,
		Kind:      "package",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/path/to/file"},
		Summary:   "Summary description",
		Author:    memory.Author{AgentID: "test-agent"},
		SessionID: "sess-1",
	}
}

func setKnowledgeNamespacePolicy(t *testing.T, cs *contextstore.Store, ns, ownerType, ownerID string, policy map[string]any) {
	t.Helper()
	b, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = cs.DB().ExecContext(context.Background(), `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(namespace) DO UPDATE SET owner_type=excluded.owner_type, owner_id=excluded.owner_id, policy_json=excluded.policy_json, updated_at=excluded.updated_at`,
		ns, ownerType, ownerID, string(b), now)
	if err != nil {
		t.Fatalf("set namespace policy %s: %v", ns, err)
	}
}

func TestKnowledge_TierPolicy_AllowedOps(t *testing.T) {
	ks, cs := newTestKnowledgeWithDB(t)
	ctx := context.Background()
	const ns = "project/tesseract/knowledge/docs"

	setKnowledgeNamespacePolicy(t, cs, ns, "project", "tesseract", map[string]any{
		"tier":        "read-only-knowledge",
		"allowed_ops": []string{"promote.request"},
	})

	in := validKnowledgeInput(ns, "tesseract.overview")
	_, err := ks.Write(ctx, in)
	if err == nil {
		t.Fatal("expected write error on namespace where write op is not allowed")
	}

	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) || pv.Field != "allowed_ops" {
		t.Fatalf("expected allowed_ops PolicyViolation, got: %v", err)
	}
}

func TestKnowledge_TierPolicy_MaxBytesPerKey(t *testing.T) {
	ks, cs := newTestKnowledgeWithDB(t)
	ctx := context.Background()
	const ns = "project/tesseract/knowledge/docs"

	setKnowledgeNamespacePolicy(t, cs, ns, "project", "tesseract", map[string]any{
		"tier":              "docs",
		"max_bytes_per_key": 60,
	})

	in := validKnowledgeInput(ns, "tesseract.small")
	in.Summary = "Short summary"
	if _, err := ks.Write(ctx, in); err != nil {
		t.Fatalf("expected write within size limit to succeed: %v", err)
	}

	inBad := validKnowledgeInput(ns, "tesseract.big")
	inBad.Body = strings.Repeat("A", 100)
	_, err := ks.Write(ctx, inBad)
	if err == nil {
		t.Fatal("expected max_bytes_per_key violation")
	}
	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) || pv.Field != "max_bytes_per_key" {
		t.Fatalf("expected max_bytes_per_key PolicyViolation, got: %v", err)
	}
}

func TestKnowledge_TierPolicy_ScopeHeadInheritance(t *testing.T) {
	ks, cs := newTestKnowledgeWithDB(t)
	ctx := context.Background()

	// Seed scope head policy on project/tesseract.
	setKnowledgeNamespacePolicy(t, cs, "project/tesseract", "project", "tesseract", map[string]any{
		"tier":              "project-tier",
		"max_bytes_per_key": 40,
	})

	// Knowledge write to project/tesseract/knowledge/sub/docs with large summary.
	in := validKnowledgeInput("project/tesseract/knowledge/sub/docs", "tesseract.doc")
	in.Summary = strings.Repeat("x", 50)
	_, err := ks.Write(ctx, in)
	if err == nil {
		t.Fatal("expected max_bytes_per_key violation inherited from project/tesseract")
	}
	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) || pv.Field != "max_bytes_per_key" {
		t.Fatalf("expected max_bytes_per_key PolicyViolation, got: %v", err)
	}
}

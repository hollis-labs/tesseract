package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newTestMemoryStore(t *testing.T) (*memory.Store, *contextstore.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: dir})
	if err != nil {
		t.Fatalf("contextstore.Open: %v", err)
	}
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ms.SetNamespaceRegistrar(cs)
	cleanup := func() { _ = cs.Close() }
	return ms, cs, cleanup
}

func baseInput(ns, key string) memory.WriteInput {
	return memory.WriteInput{
		Domain:      domains.Memory,
		Actor:       "user",
		Namespace:   ns,
		MemoryKey:   key,
		Author:      memory.Author{AgentID: "test-agent"},
		Trigger:     memory.TriggerExplicit,
		SessionID:   "sess-1",
		DerivedFrom: memory.DerivedFromObservation,
		Confidence:  0.9,
		Summary:     "Test summary",
		Body:        "Test body",
	}
}

func setNamespacePolicy(t *testing.T, cs *contextstore.Store, ns, ownerType, ownerID string, policy map[string]any) {
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

func TestTierPolicy_AllowedOps(t *testing.T) {
	ms, cs, cleanup := newTestMemoryStore(t)
	defer cleanup()

	ctx := context.Background()
	const ns = "project/torque/memory/notes"

	// Policy that does NOT allow "write".
	setNamespacePolicy(t, cs, ns, "project", "torque", map[string]any{
		"tier":        "locked",
		"allowed_ops": []string{"promote.request"},
	})

	in := baseInput(ns, "note.1")
	_, err := ms.WriteRevision(ctx, in)
	if err == nil {
		t.Fatal("expected write failure on namespace with disallowed write op")
	}

	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) {
		t.Fatalf("expected *contextpolicy.PolicyViolation, got %T: %v", err, err)
	}
	if pv.Field != "allowed_ops" {
		t.Errorf("expected Field=allowed_ops, got %q", pv.Field)
	}
}

func TestTierPolicy_MaxBytesPerKey(t *testing.T) {
	ms, cs, cleanup := newTestMemoryStore(t)
	defer cleanup()

	ctx := context.Background()
	const ns = "project/torque/memory/notes"

	// Max 50 bytes total payload (Summary + Body + Data).
	setNamespacePolicy(t, cs, ns, "project", "torque", map[string]any{
		"tier":              "small",
		"max_bytes_per_key": 50,
	})

	// Input with summary (12 bytes) + body (9 bytes) = 21 bytes <= 50 -> OK.
	in := baseInput(ns, "note.ok")
	in.Summary = "Short sum"
	in.Body = "Short bod"
	if _, err := ms.WriteRevision(ctx, in); err != nil {
		t.Fatalf("expected write under max_bytes_per_key to succeed: %v", err)
	}

	// Input with large body exceeding 50 bytes.
	inBad := baseInput(ns, "note.big")
	inBad.Body = strings.Repeat("a", 60)
	_, err := ms.WriteRevision(ctx, inBad)
	if err == nil {
		t.Fatal("expected max_bytes_per_key violation")
	}
	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) {
		t.Fatalf("expected PolicyViolation, got: %v", err)
	}
	if pv.Field != "max_bytes_per_key" {
		t.Errorf("expected Field=max_bytes_per_key, got %q", pv.Field)
	}
}

func TestTierPolicy_RequiredSchemaKeys(t *testing.T) {
	ms, cs, cleanup := newTestMemoryStore(t)
	defer cleanup()

	ctx := context.Background()
	const ns = "project/torque/memory/notes"

	setNamespacePolicy(t, cs, ns, "project", "torque", map[string]any{
		"tier":                 "structured",
		"required_schema_keys": []string{"target", "action"},
	})

	// Missing data altogether -> violation.
	inEmpty := baseInput(ns, "schema.1")
	_, err := ms.WriteRevision(ctx, inEmpty)
	if err == nil {
		t.Fatal("expected required_schema_keys violation on empty Data")
	}
	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) || pv.Field != "required_schema_keys" {
		t.Fatalf("expected required_schema_keys PolicyViolation, got: %v", err)
	}

	// Partial data missing "action" -> violation.
	inPartial := baseInput(ns, "schema.2")
	inPartial.Data = json.RawMessage(`{"target":"build"}`)
	_, err = ms.WriteRevision(ctx, inPartial)
	if err == nil {
		t.Fatal("expected required_schema_keys violation on partial Data")
	}

	// Valid data containing both keys -> OK.
	inGood := baseInput(ns, "schema.3")
	inGood.Data = json.RawMessage(`{"target":"build","action":"deploy"}`)
	if _, err := ms.WriteRevision(ctx, inGood); err != nil {
		t.Fatalf("expected valid schema keys write to succeed: %v", err)
	}
}

func TestTierPolicy_UndeclaredPassesThrough(t *testing.T) {
	ms, _, cleanup := newTestMemoryStore(t)
	defer cleanup()

	ctx := context.Background()
	const ns = "user/chrispian/memory/notes"

	in := baseInput(ns, "plain.note")
	in.Body = strings.Repeat("x", 5000)
	rev, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatalf("expected undeclared namespace to pass without restrictions: %v", err)
	}
	if rev.RevisionID == "" {
		t.Fatal("expected valid revision")
	}
}

func TestTierPolicy_InheritsFromScopeHead(t *testing.T) {
	ms, cs, cleanup := newTestMemoryStore(t)
	defer cleanup()

	ctx := context.Background()

	// Set policy on scope head project/nanite.
	setNamespacePolicy(t, cs, "project/nanite", "project", "nanite", map[string]any{
		"tier":              "nanite-tier",
		"max_bytes_per_key": 30,
	})

	// Write to child namespace project/nanite/memory/notes with payload > 30 bytes.
	in := baseInput("project/nanite/memory/notes", "child.note")
	in.Summary = strings.Repeat("z", 40)
	_, err := ms.WriteRevision(ctx, in)
	if err == nil {
		t.Fatal("expected max_bytes_per_key violation via scope head inheritance")
	}
	var pv *contextpolicy.PolicyViolation
	if !errors.As(err, &pv) || pv.Field != "max_bytes_per_key" {
		t.Fatalf("expected max_bytes_per_key violation, got %v", err)
	}
}

func TestMemoryStore_NamespaceOwner(t *testing.T) {
	ms, cs, cleanup := newTestMemoryStore(t)
	defer cleanup()

	ctx := context.Background()

	// Seed Cerberus projects.
	if _, err := cs.SeedCerberusProjects(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Exact scope head.
	oType, oID, err := ms.NamespaceOwner(ctx, "project/torque")
	if err != nil {
		t.Fatalf("NamespaceOwner(project/torque): %v", err)
	}
	if oType != "project" || oID != "torque" {
		t.Errorf("got (%q, %q), want (project, torque)", oType, oID)
	}

	// Child namespace inherited from scope head.
	oType, oID, err = ms.NamespaceOwner(ctx, "project/torque/memory/notes")
	if err != nil {
		t.Fatalf("NamespaceOwner(child): %v", err)
	}
	if oType != "project" || oID != "torque" {
		t.Errorf("got (%q, %q), want (project, torque)", oType, oID)
	}

	// Fallback to path derivation for unregistered namespace.
	oType, oID, err = ms.NamespaceOwner(ctx, "user/chrispian/memory/notes")
	if err != nil {
		t.Fatalf("NamespaceOwner(user): %v", err)
	}
	if oType != "user" || oID != "chrispian" {
		t.Errorf("got (%q, %q), want (user, chrispian)", oType, oID)
	}
}

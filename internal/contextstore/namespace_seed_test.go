package contextstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestIsCerberusProject(t *testing.T) {
	if len(SeededCerberusProjects) != 18 {
		t.Fatalf("expected 18 seeded Cerberus projects, got %d", len(SeededCerberusProjects))
	}

	for _, slug := range SeededCerberusProjects {
		if !IsCerberusProject(slug) {
			t.Errorf("IsCerberusProject(%q) = false, want true", slug)
		}
	}

	for _, invalid := range []string{"", "unknown", "other", "chrispian", "agent-1"} {
		if IsCerberusProject(invalid) {
			t.Errorf("IsCerberusProject(%q) = true, want false", invalid)
		}
	}
}

func TestSeedCerberusProjects_IdempotentAndPreservesCustom(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// 1. Pre-insert custom policy for one project to ensure INSERT OR IGNORE preserves it.
	const preExisting = "project/torque"
	customPolicy := map[string]any{"source": "custom", "tier": "special"}
	customJSON, _ := json.Marshal(customPolicy)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err = s.db.ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, preExisting, "custom_owner", "custom_id", string(customJSON), now); err != nil {
		t.Fatalf("insert pre-existing: %v", err)
	}

	// 2. Run SeedCerberusProjects. It should insert 17 rows (18 minus the pre-existing one).
	var inserted int
	inserted, err = s.SeedCerberusProjects(ctx)
	if err != nil {
		t.Fatalf("SeedCerberusProjects: %v", err)
	}
	if inserted != 17 {
		t.Fatalf("expected 17 newly seeded projects, got %d", inserted)
	}

	// Verify the pre-existing row was NOT overwritten.
	entry, err := s.GetNamespacePolicy(ctx, preExisting)
	if err != nil {
		t.Fatalf("GetNamespacePolicy(%q): %v", preExisting, err)
	}
	if entry.OwnerType != "custom_owner" || entry.OwnerID != "custom_id" {
		t.Errorf("pre-existing owner overwritten: got (%q, %q)", entry.OwnerType, entry.OwnerID)
	}
	if entry.Policy["source"] != "custom" {
		t.Errorf("pre-existing policy overwritten: %+v", entry.Policy)
	}

	// Verify all other 17 projects were seeded.
	for _, slug := range SeededCerberusProjects {
		if slug == "torque" {
			continue
		}
		ns := "project/" + slug
		p, pErr := s.GetNamespacePolicy(ctx, ns)
		if pErr != nil {
			t.Errorf("GetNamespacePolicy(%q): %v", ns, pErr)
			continue
		}
		if p.OwnerType != "project" || p.OwnerID != slug {
			t.Errorf("%s: got owner (%q, %q), want (project, %s)", ns, p.OwnerType, p.OwnerID, slug)
		}
		if p.Policy["source"] != "seed" || p.Policy["scope"] != "project" {
			t.Errorf("%s: unexpected policy: %+v", ns, p.Policy)
		}
	}

	// 3. Second run should be a complete no-op (0 inserted).
	secondInserted, err := s.SeedCerberusProjects(ctx)
	if err != nil {
		t.Fatalf("second SeedCerberusProjects: %v", err)
	}
	if secondInserted != 0 {
		t.Fatalf("expected 0 on second seed, got %d", secondInserted)
	}
}

func TestGetNamespaceOwner_ConsultsRegistryAndFallsBack(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Seed projects.
	if _, err = s.SeedCerberusProjects(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Exact scope head registered:
	oType, oID, err := s.GetNamespaceOwner(ctx, "project/tesseract")
	if err != nil {
		t.Fatalf("GetNamespaceOwner(project/tesseract): %v", err)
	}
	if oType != "project" || oID != "tesseract" {
		t.Errorf("got (%q, %q), want (project, tesseract)", oType, oID)
	}

	// Child namespace inherits registered owner from scope head:
	oType, oID, err = s.GetNamespaceOwner(ctx, "project/tesseract/memory/notes")
	if err != nil {
		t.Fatalf("GetNamespaceOwner(project/tesseract/memory/notes): %v", err)
	}
	if oType != "project" || oID != "tesseract" {
		t.Errorf("got (%q, %q), want (project, tesseract)", oType, oID)
	}

	// Overridden owner in registry for a specific child namespace:
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err = s.db.ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "project/tesseract/delegated", "user", "chrispian", `{"source":"declared"}`, now); err != nil {
		t.Fatalf("insert override: %v", err)
	}

	oType, oID, err = s.GetNamespaceOwner(ctx, "project/tesseract/delegated")
	if err != nil {
		t.Fatalf("GetNamespaceOwner: %v", err)
	}
	if oType != "user" || oID != "chrispian" {
		t.Errorf("registry override failed: got (%q, %q), want (user, chrispian)", oType, oID)
	}

	// Undeclared path falls back to DeriveNamespaceOwner:
	oType, oID, err = s.GetNamespaceOwner(ctx, "org/unregistered-team/notes")
	if err != nil {
		t.Fatalf("GetNamespaceOwner(unregistered): %v", err)
	}
	if oType != "org" || oID != "unregistered-team" {
		t.Errorf("got (%q, %q), want (org, unregistered-team)", oType, oID)
	}
}

func TestResolveNamespacePolicy_ExactAndScopeHeadFallback(t *testing.T) {
	s, err := Open(context.Background(), Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	// Declare policy on head project/hadron.
	if _, err = s.db.ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "project/hadron", "project", "hadron", `{"tier":"engine","max_bytes_per_key":500}`, now); err != nil {
		t.Fatalf("insert hadron: %v", err)
	}

	// Insert inferred child.
	if _, err = s.db.ExecContext(ctx, `
INSERT INTO namespace_policies (namespace, owner_type, owner_id, policy_json, updated_at)
VALUES (?, ?, ?, ?, ?)`, "project/hadron/memory/notes", "project", "hadron", `{"source":"inferred"}`, now); err != nil {
		t.Fatalf("insert child: %v", err)
	}

	entry, ok, err := s.ResolveNamespacePolicy(ctx, "project/hadron/memory/notes")
	if err != nil {
		t.Fatalf("ResolveNamespacePolicy: %v", err)
	}
	if !ok {
		t.Fatalf("expected policy to resolve")
	}
	// Inferred child should fall back to scope head's declared policy.
	if entry.Namespace != "project/hadron" {
		t.Errorf("expected resolution to scope head project/hadron, got %s", entry.Namespace)
	}
	if entry.Policy["tier"] != "engine" {
		t.Errorf("expected tier=engine from head, got %+v", entry.Policy)
	}
}

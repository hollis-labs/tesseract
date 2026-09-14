package corpusmigration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/corpusmigration"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/memorylinks"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func setupTestDB(t *testing.T) (*contextstore.Store, *memory.Store, *sql.DB) {
	t.Helper()

	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open contextstore: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	memStore := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return cs, memStore, cs.DB()
}

func TestClassifyNamespaceComprehensive(t *testing.T) {
	cases := []struct {
		namespace    string
		domain       domains.Domain
		wantCat      corpusmigration.RuleCategory
		wantAction   corpusmigration.Action
		wantTargetNS string
	}{
		// 1. Session rename
		{
			namespace:    "user/chrispian/session/session-abc-123/memory/notes",
			domain:       domains.Memory,
			wantCat:      corpusmigration.CategorySessionRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "session/session-abc-123/memory/notes",
		},
		// 2. Project memory rename
		{
			namespace:    "user/chrispian/project/proj-x/memory/decisions",
			domain:       domains.Memory,
			wantCat:      corpusmigration.CategoryProjectMemoryRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/proj-x/memory/decisions",
		},
		// 3. Project knowledge rename
		{
			namespace:    "user/chrispian/knowledge/cairn/adr",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryProjectKnowledgeRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/cairn/knowledge/adr",
		},
		{
			namespace:    "user/chrispian/knowledge/nanite/architecture",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryProjectKnowledgeRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/nanite/knowledge/architecture",
		},
		// 3b. Project knowledge rename additions (Part A)
		{
			namespace:    "user/chrispian/knowledge/tesseract/adr",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryProjectKnowledgeRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/tesseract/knowledge/adr",
		},
		{
			namespace:    "user/chrispian/knowledge/tesseract",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryProjectKnowledgeRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/tesseract/knowledge",
		},
		{
			namespace:    "user/chrispian/knowledge/design-kit/catalogs",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryProjectKnowledgeRename,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/design-kit/knowledge/catalogs",
		},
		// 4. System verify
		{
			namespace:    "system/knowledge/guidelines",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategorySystemVerify,
			wantAction:   corpusmigration.ActionVerify,
			wantTargetNS: "system/knowledge/guidelines",
		},
		// 5. Reclassify session-close
		{
			namespace:    "user/chrispian/knowledge/session-close/tesseract",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategorySessionCloseReclassify,
			wantAction:   corpusmigration.ActionReclassify,
			wantTargetNS: "project/tesseract/event/reasoning",
		},
		// 6. Reclassify handoff
		{
			namespace:    "user/chrispian/knowledge/handoff/cerberus",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryHandoffReclassify,
			wantAction:   corpusmigration.ActionReclassify,
			wantTargetNS: "project/cerberus/workspace/handoff",
		},
		// 7. Reclassify boot-prompt
		{
			namespace:    "user/chrispian/knowledge/boot-prompt/hadron",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryBootPromptReclassify,
			wantAction:   corpusmigration.ActionReclassify,
			wantTargetNS: "project/hadron/workspace/boot-prompt",
		},
		// 8. Ambiguous how-we-work
		{
			namespace:    "user/chrispian/knowledge/runbooks",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryAmbiguousHowWeWork,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "system/knowledge/runbooks",
		},
		{
			namespace:    "user/chrispian/knowledge/playbook/orchestration",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryAmbiguousHowWeWork,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "system/knowledge/playbook/orchestration",
		},
		// 9. Ambiguous packages
		{
			namespace:    "user/chrispian/knowledge/laravel/filament",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryAmbiguousPackages,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "system/knowledge/packages/laravel/filament",
		},
		{
			namespace:    "user/chrispian/knowledge/tools/mcp",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryAmbiguousPackages,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "system/knowledge/packages/tools/mcp",
		},
		// 10. Ambiguous portfolio
		{
			namespace:    "user/chrispian/knowledge/portfolio/investigations",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryAmbiguousPortfolio,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "project/hollis-labs-portfolio/knowledge/investigations",
		},
		// 11. Ambiguous low-confidence
		{
			namespace:    "user/chrispian/knowledge/ideas",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryAmbiguousLowConfidence,
			wantAction:   corpusmigration.ActionRename,
			wantTargetNS: "system/knowledge/ideas",
		},
		// 12. Deferred app
		{
			namespace:    "app/my-app/knowledge/docs",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryDeferredApp,
			wantAction:   corpusmigration.ActionDeferred,
			wantTargetNS: "app/my-app/knowledge/docs",
		},
		// 13. Deferred Tether (broadened)
		{
			namespace:    "user/chrispian/knowledge/tether/sync",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryDeferredTether,
			wantAction:   corpusmigration.ActionDeferred,
			wantTargetNS: "user/chrispian/knowledge/tether/sync",
		},
		{
			namespace:    "user/chrispian/knowledge/investigation/tether",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryDeferredTether,
			wantAction:   corpusmigration.ActionDeferred,
			wantTargetNS: "user/chrispian/knowledge/investigation/tether",
		},
		{
			namespace:    "user/chrispian/knowledge/projects/tether/playbooks",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryDeferredTether,
			wantAction:   corpusmigration.ActionDeferred,
			wantTargetNS: "user/chrispian/knowledge/projects/tether/playbooks",
		},
		// 14. Event reasoning routing
		{
			namespace:    "user/chrispian/event/reasoning",
			domain:       domains.Event,
			wantCat:      corpusmigration.CategoryEventReasoningRouting,
			wantAction:   corpusmigration.ActionClassify,
			wantTargetNS: "",
		},
		// 15. Unmapped default
		{
			namespace:    "other/unrecognized/custom",
			domain:       domains.Knowledge,
			wantCat:      corpusmigration.CategoryUnmapped,
			wantAction:   corpusmigration.ActionSkip,
			wantTargetNS: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.namespace, func(t *testing.T) {
			dec := corpusmigration.ClassifyNamespace(tc.namespace, tc.domain)
			if dec.Category != tc.wantCat {
				t.Errorf("category mismatch: got %v, want %v", dec.Category, tc.wantCat)
			}
			if dec.Action != tc.wantAction {
				t.Errorf("action mismatch: got %v, want %v", dec.Action, tc.wantAction)
			}
			if tc.wantTargetNS != "" && dec.NewNamespace != tc.wantTargetNS {
				t.Errorf("target namespace mismatch: got %q, want %q", dec.NewNamespace, tc.wantTargetNS)
			}
		})
	}
}

func TestMigrationRunnerFixturesEndToEnd(t *testing.T) {
	ctx := context.Background()
	_, mem, db := setupTestDB(t)

	now := time.Now().UTC()

	// 1. Session memory row
	sRev, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/session/sess-1/memory/notes",
		MemoryKey:   "scratch.1",
		Summary:     "session scratch",
		Body:        "working notes",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Project knowledge row
	kRev, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/cairn/adr",
		MemoryKey:   "adr.001",
		Summary:     "cairn adr 1",
		Body:        "adr text referring to [[scratch.1]]",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Multi-revision record for lineage check
	linRev1, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/nanite/architecture",
		MemoryKey:   "nanite.arch",
		Summary:     "nanite arch v1",
		Body:        "nanite arch v1 body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	linRev2, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/nanite/architecture",
		MemoryKey:   "nanite.arch",
		Supersedes:  linRev1.RevisionID,
		Summary:     "nanite arch v2",
		Body:        "nanite arch v2 body with [[adr.001]]",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-2",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Session close row (to reclassify to event)
	scRev, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/session-close/tesseract",
		MemoryKey:   "close_2026_09_14",
		Summary:     "session close summary",
		Body:        "closed cleanly",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "session_close",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 5. Handoff row (to reclassify to workspace)
	hoRev, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/handoff/tesseract",
		MemoryKey:   "handoff_2026_09_14",
		Summary:     "handoff summary",
		Body:        "handoff body with [[adr.001]]",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "handoff",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 6. Boot prompt row (to reclassify to workspace)
	bpRev, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/boot-prompt/tesseract",
		MemoryKey:   "boot_2026_09_14",
		Summary:     "boot prompt summary",
		Body:        "boot prompt body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "boot_prompt",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 7. Ambiguous how-we-work row
	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/runbooks",
		MemoryKey:   "deploy_guide",
		Summary:     "deployment runbook",
		Body:        "runbook text",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 8. Deferred app namespace
	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "app/agent-os/knowledge/docs",
		MemoryKey:   "overview",
		Summary:     "app overview",
		Body:        "app doc body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "app:agent-os",
		ClientID:    "agent-os",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "doc",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 9. Unmapped namespace (user/chrispian/memory/notes has no project or session scope)
	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/notes",
		MemoryKey:   "unmapped_key",
		Summary:     "unmapped summary",
		Body:        "unmapped body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Write references into memory_links using memorylinks.WriteReferences
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := memorylinks.TxResolver{Tx: tx}
	_ = memorylinks.WriteReferences(ctx, tx, res, memorylinks.RevisionRef{
		RevisionID: kRev.RevisionID,
		MemoryID:   kRev.ItemID,
		Namespace:  kRev.Namespace,
		CreatedAt:  now.Format(time.RFC3339),
	}, kRev.Payload.Body)

	_ = memorylinks.WriteReferences(ctx, tx, res, memorylinks.RevisionRef{
		RevisionID: linRev2.RevisionID,
		MemoryID:   linRev2.ItemID,
		Namespace:  linRev2.Namespace,
		CreatedAt:  now.Format(time.RFC3339),
	}, linRev2.Payload.Body)

	_ = memorylinks.WriteReferences(ctx, tx, res, memorylinks.RevisionRef{
		RevisionID: hoRev.RevisionID,
		MemoryID:   hoRev.ItemID,
		Namespace:  hoRev.Namespace,
		CreatedAt:  now.Format(time.RFC3339),
	}, hoRev.Payload.Body)

	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Capture wikilink graph before
	graphBefore, err := corpusmigration.EnumerateWikilinkGraph(ctx, db)
	if err != nil {
		t.Fatalf("enumerate wikilink graph before: %v", err)
	}
	if graphBefore.ResolvedLinks == 0 {
		t.Fatalf("expected resolved links before migration, got 0")
	}

	// Capture lineage before
	lineageBefore, err := corpusmigration.EnumerateLineage(ctx, db, 10)
	if err != nil {
		t.Fatalf("enumerate lineage before: %v", err)
	}
	if lineageBefore.TotalLineageEdges == 0 {
		t.Fatalf("expected lineage edges before migration, got 0")
	}

	// Build migration plan
	plan, err := corpusmigration.BuildMigrationPlan(ctx, db)
	if err != nil {
		t.Fatalf("build migration plan: %v", err)
	}

	if len(plan.Collisions) > 0 {
		t.Fatalf("unexpected collisions in plan: %v", plan.Collisions)
	}

	// Verify unmapped namespace is in SkippedNamespaces
	foundUnmapped := false
	for _, skipped := range plan.SkippedNamespaces {
		if skipped == "user/chrispian/memory/notes" {
			foundUnmapped = true
			break
		}
	}
	if !foundUnmapped {
		t.Errorf("expected custom/unmapped/namespace to be in SkippedNamespaces")
	}

	// Apply migration
	receipt, err := corpusmigration.ApplyMigration(ctx, db, plan)
	if err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	if receipt.RenamedNamespaces == 0 {
		t.Errorf("expected renamed namespaces, got 0")
	}
	if receipt.PromotedRecords < 3 {
		t.Errorf("expected at least 3 promoted records, got %d", receipt.PromotedRecords)
	}

	// Verify wikilink graph after
	graphAfter, err := corpusmigration.EnumerateWikilinkGraph(ctx, db)
	if err != nil {
		t.Fatalf("enumerate wikilink graph after: %v", err)
	}

	diff := corpusmigration.DiffWikilinkGraphs(graphBefore, graphAfter)
	if len(diff.BrokenLinks) > 0 {
		t.Fatalf("FATAL DEFECT: %d wikilinks became unresolved after migration: %+v", len(diff.BrokenLinks), diff.BrokenLinks)
	}

	// Verify lineage after
	lineageAfter, err := corpusmigration.EnumerateLineage(ctx, db, 10)
	if err != nil {
		t.Fatalf("enumerate lineage after: %v", err)
	}
	if lineageAfter.TotalLineageEdges != lineageBefore.TotalLineageEdges {
		t.Fatalf("lineage edges count mismatch: before %d, after %d", lineageBefore.TotalLineageEdges, lineageAfter.TotalLineageEdges)
	}

	// Verify session memory was renamed to session/...
	var sessNS string
	err = db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, sRev.ItemID).Scan(&sessNS)
	if err != nil {
		t.Fatal(err)
	}
	if sessNS != "session/sess-1/memory/notes" {
		t.Errorf("expected renamed session namespace session/sess-1/memory/notes, got %s", sessNS)
	}

	// Verify cairn adr was renamed to project/cairn/knowledge/adr
	var cairnNS string
	err = db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, kRev.ItemID).Scan(&cairnNS)
	if err != nil {
		t.Fatal(err)
	}
	if cairnNS != "project/cairn/knowledge/adr" {
		t.Errorf("expected project/cairn/knowledge/adr, got %s", cairnNS)
	}

	// Verify session close was promoted to event domain
	var eventCount int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions WHERE domain = 'event' AND namespace = 'project/tesseract/event/reasoning'`).Scan(&eventCount)
	if err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 session-close event in project/tesseract/event/reasoning, got %d", eventCount)
	}

	// Verify handoff was promoted to workspace domain
	var wsCount int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_items WHERE namespace = 'project/tesseract/workspace/handoff' AND key_name = 'handoff_2026_09_14'`).Scan(&wsCount)
	if err != nil {
		t.Fatal(err)
	}
	if wsCount != 1 {
		t.Errorf("expected 1 handoff in project/tesseract/workspace/handoff, got %d", wsCount)
	}

	// Verify boot prompt was promoted to workspace domain
	var bpCount int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_items WHERE namespace = 'project/tesseract/workspace/boot-prompt' AND key_name = 'boot_2026_09_14'`).Scan(&bpCount)
	if err != nil {
		t.Fatal(err)
	}
	if bpCount != 1 {
		t.Errorf("expected 1 boot-prompt in project/tesseract/workspace/boot-prompt, got %d", bpCount)
	}

	// Verify source records in knowledge are now deprecated
	var scStatus, hoStatus, bpStatus string
	_ = db.QueryRowContext(ctx, `SELECT status FROM memory_revisions WHERE revision_id = ?`, scRev.RevisionID).Scan(&scStatus)
	_ = db.QueryRowContext(ctx, `SELECT status FROM memory_revisions WHERE revision_id = ?`, hoRev.RevisionID).Scan(&hoStatus)
	_ = db.QueryRowContext(ctx, `SELECT status FROM memory_revisions WHERE revision_id = ?`, bpRev.RevisionID).Scan(&bpStatus)

	if scStatus != "deprecated" {
		t.Errorf("expected session-close source revision to be deprecated, got %s", scStatus)
	}
	if hoStatus != "deprecated" {
		t.Errorf("expected handoff source revision to be deprecated, got %s", hoStatus)
	}
	if bpStatus != "deprecated" {
		t.Errorf("expected boot-prompt source revision to be deprecated, got %s", bpStatus)
	}
}

func TestMigrationCollisionRejection(t *testing.T) {
	ctx := context.Background()
	_, mem, db := setupTestDB(t)

	now := time.Now().UTC()
	// Create item in destination workspace
	wsStore := workspace.NewStore(db)
	_, err := wsStore.Create(ctx, workspace.CreateInput{
		Namespace: "project/tesseract/workspace/handoff",
		Key:       "handoff_key",
		Summary:   "existing summary",
		Author:    memory.Author{AgentID: "agent"},
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create source item that maps to the same target namespace + key
	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Knowledge,
		Namespace:   "user/chrispian/knowledge/handoff/tesseract",
		MemoryKey:   "handoff_key",
		Summary:     "conflict test",
		Body:        "body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Facets: memory.Facets{
			Kind:    "handoff",
			Source:  "manual",
			Pointer: &memory.Pointer{Scheme: "nil", Locator: "none", ResolvedAt: &now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := corpusmigration.BuildMigrationPlan(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Collisions) == 0 {
		t.Fatalf("expected collision detected for occupied workspace key")
	}

	_, err = corpusmigration.ApplyMigration(ctx, db, plan)
	if err == nil {
		t.Fatalf("expected ApplyMigration to refuse with error when collisions exist")
	}
}

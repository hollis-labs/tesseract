package corpusmigration_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/corpusmigration"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestBacklogApplyDispositions(t *testing.T) {
	cases := []struct {
		name         string
		rec          corpusmigration.BacklogRecordInput
		wantOutcome  corpusmigration.BacklogApplyOutcome
		wantProject  string
		wantTargetNS string
		wantSkipRsn  bool
	}{
		// 1. Applied via Tier 1/2/3
		{
			name: "applied-via-tier1/2/3 explicit tag canonical nanite",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01APPLY_T1",
				Namespace: "user/chrispian/memory/decisions",
				Domain:    "memory",
				MemoryKey: "nanite_worker_dispatch",
				Summary:   "Worker dispatch strategy",
				Tags:      []string{"decision", "project:nanite"},
			},
			wantOutcome:  corpusmigration.OutcomeAppliedTier123,
			wantProject:  "nanite",
			wantTargetNS: "project/nanite/memory/decisions",
		},
		{
			name: "applied-via-tier1/2/3 bare tag canonical cairn",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01APPLY_T2",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "cairn_install_flow",
				Summary:   "Install flow details",
				Tags:      []string{"cairn", "install"},
			},
			wantOutcome:  corpusmigration.OutcomeAppliedTier123,
			wantProject:  "cairn",
			wantTargetNS: "project/cairn/memory/notes",
		},
		{
			name: "applied-via-tier1/2/3 key prefix canonical cerberus",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01APPLY_T3",
				Namespace: "user/chrispian/memory/followups",
				Domain:    "memory",
				MemoryKey: "cerberus_reconnect_probe",
				Summary:   "Probe reconnect",
				Tags:      []string{"followup"},
			},
			wantOutcome:  corpusmigration.OutcomeAppliedTier123,
			wantProject:  "cerberus",
			wantTargetNS: "project/cerberus/memory/followups",
		},
		// 2. Applied via Tier 4 System Default
		{
			name: "applied-via-tier4-system-default clean safety rule",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01APPLY_T4",
				Namespace: "user/chrispian/memory/feedback",
				Domain:    "memory",
				MemoryKey: "destructive_git_ops_require_approval",
				Summary:   "Always require approval before force push or delete",
				Tags:      []string{"feedback", "safety", "git"},
			},
			wantOutcome:  corpusmigration.OutcomeAppliedTier4System,
			wantProject:  "",
			wantTargetNS: "system/memory/feedback",
		},
		{
			name: "applied-via-tier4-system-default event reasoning",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01APPLY_T4_EVT",
				Namespace: "user/chrispian/event/reasoning",
				Domain:    "event",
				MemoryKey: "",
				Summary:   "General architectural reflection",
				Tags:      []string{"reflection"},
			},
			wantOutcome:  corpusmigration.OutcomeAppliedTier4System,
			wantProject:  "",
			wantTargetNS: "system/event/reasoning",
		},
		// 3. Skipped Conflict
		{
			name: "skipped-conflict multiple project tags",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01SKIP_CONF",
				Namespace: "user/chrispian/memory/limitations",
				Domain:    "memory",
				MemoryKey: "shared_service_boundary",
				Summary:   "Affects torque and cerberus",
				Tags:      []string{"project:torque", "project:cerberus"},
			},
			wantOutcome:  corpusmigration.OutcomeSkippedConflict,
			wantTargetNS: "user/chrispian/memory/limitations",
			wantSkipRsn:  true,
		},
		// 4. Skipped Non-Canonical
		{
			name: "skipped-non-canonical clockwork-manifold",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01SKIP_NONCANON_CW",
				Namespace: "user/chrispian/memory/decisions",
				Domain:    "memory",
				MemoryKey: "modelcatalog_phase2_default_on",
				Summary:   "Modelcatalog phase 2",
				Tags:      []string{"decision", "project:clockwork_manifold"},
			},
			wantOutcome:  corpusmigration.OutcomeSkippedNonCanonical,
			wantProject:  "clockwork-manifold",
			wantTargetNS: "user/chrispian/memory/decisions",
			wantSkipRsn:  true,
		},
		{
			name: "skipped-non-canonical go-providers",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01SKIP_NONCANON_GP",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "provider_abstraction_seam",
				Summary:   "Provider abstraction",
				Tags:      []string{"notes", "project:go-providers"},
			},
			wantOutcome:  corpusmigration.OutcomeSkippedNonCanonical,
			wantProject:  "go-providers",
			wantTargetNS: "user/chrispian/memory/notes",
			wantSkipRsn:  true,
		},
		// 5. Skipped Agridd
		{
			name: "skipped-agridd via memory_key prefix in Tier 4",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01SKIP_AGRIDD_KEY",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "agridd.bootprofile_phase_a_audit_2026_05_20",
				Summary:   "Phase A audit details for agridd",
				Tags:      []string{"audit"},
			},
			wantOutcome:  corpusmigration.OutcomeSkippedAgridd,
			wantTargetNS: "user/chrispian/memory/notes",
			wantSkipRsn:  true,
		},
		{
			name: "skipped-agridd via tags in Tier 4",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01SKIP_AGRIDD_TAG",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "task_writer_pattern_keeper_fu",
				Summary:   "Pattern for keeper",
				Tags:      []string{"agridd-keeper", "pattern"},
			},
			wantOutcome:  corpusmigration.OutcomeSkippedAgridd,
			wantTargetNS: "user/chrispian/memory/notes",
			wantSkipRsn:  true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			classRes := corpusmigration.ClassifyRecord(tc.rec)
			disp := corpusmigration.DetermineDisposition(tc.rec, classRes)

			if disp.Outcome != tc.wantOutcome {
				t.Fatalf("outcome mismatch: got %q, want %q", disp.Outcome, tc.wantOutcome)
			}
			if tc.wantProject != "" && disp.MatchedProject != tc.wantProject {
				t.Errorf("matched project mismatch: got %q, want %q", disp.MatchedProject, tc.wantProject)
			}
			if disp.TargetNamespace != tc.wantTargetNS {
				t.Errorf("target namespace mismatch: got %q, want %q", disp.TargetNamespace, tc.wantTargetNS)
			}
			if tc.wantSkipRsn && disp.SkipReason == "" {
				t.Error("expected non-empty skip reason")
			}
		})
	}
}

func TestBacklogApplyEndToEnd(t *testing.T) {
	ctx := context.Background()
	_, mem, db := setupTestDB(t)

	// 1. Applied Tier 1 (canonical nanite)
	r1, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/decisions",
		MemoryKey:   "nanite_worker_strategy",
		Summary:     "Worker strategy",
		Body:        "Nanite worker body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Tags:        []string{"decision", "project:nanite"},
	})
	if err != nil {
		t.Fatalf("write r1: %v", err)
	}

	// 2. Applied Tier 4 (system default)
	r2, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/feedback",
		MemoryKey:   "safety_guardrails_destructive",
		Summary:     "Safety guardrail",
		Body:        "Safety body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Tags:        []string{"safety", "feedback"},
	})
	if err != nil {
		t.Fatalf("write r2: %v", err)
	}

	// 3. Skipped Conflict
	r3, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/limitations",
		MemoryKey:   "conflict_record",
		Summary:     "Conflict summary",
		Body:        "Conflict body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Tags:        []string{"project:torque", "project:cerberus"},
	})
	if err != nil {
		t.Fatalf("write r3: %v", err)
	}

	// 4. Skipped Non-Canonical (clockwork-manifold)
	r4, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/decisions",
		MemoryKey:   "clockwork_legacy_decision",
		Summary:     "Clockwork legacy",
		Body:        "Clockwork body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Tags:        []string{"project:clockwork_manifold"},
	})
	if err != nil {
		t.Fatalf("write r4: %v", err)
	}

	// 5. Skipped Agridd
	r5, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/notes",
		MemoryKey:   "agridd_pm_notes",
		Summary:     "Agridd notes",
		Body:        "Agridd body",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Tags:        []string{"agridd-keeper"},
	})
	if err != nil {
		t.Fatalf("write r5: %v", err)
	}

	// Build plan
	plan, err := corpusmigration.BuildBacklogApplyPlan(ctx, db)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}

	if plan.TotalRecords != 5 {
		t.Fatalf("expected 5 total records, got %d", plan.TotalRecords)
	}
	if plan.TotalApplied != 2 {
		t.Fatalf("expected 2 applied records, got %d", plan.TotalApplied)
	}
	if plan.TotalSkipped != 3 {
		t.Fatalf("expected 3 skipped records, got %d", plan.TotalSkipped)
	}
	if plan.AppliedTier123Count != 1 {
		t.Errorf("expected 1 applied tier 1/2/3, got %d", plan.AppliedTier123Count)
	}
	if plan.AppliedTier4SystemCount != 1 {
		t.Errorf("expected 1 applied tier 4 system, got %d", plan.AppliedTier4SystemCount)
	}
	if plan.SkippedConflictCount != 1 {
		t.Errorf("expected 1 skipped conflict, got %d", plan.SkippedConflictCount)
	}
	if plan.SkippedNonCanonicalCount != 1 {
		t.Errorf("expected 1 skipped non-canonical, got %d", plan.SkippedNonCanonicalCount)
	}
	if plan.SkippedAgriddCount != 1 {
		t.Errorf("expected 1 skipped agridd, got %d", plan.SkippedAgriddCount)
	}

	// Apply plan
	receipt, err := corpusmigration.ApplyBacklogPlan(ctx, db, plan)
	if err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	if receipt.TotalApplied != 2 {
		t.Errorf("receipt applied count mismatch: got %d, want 2", receipt.TotalApplied)
	}
	if receipt.TotalSkipped != 3 {
		t.Errorf("receipt skipped count mismatch: got %d, want 3", receipt.TotalSkipped)
	}
	if receipt.UpdatedStateRows != 2 {
		t.Errorf("receipt updated state rows mismatch: got %d, want 2", receipt.UpdatedStateRows)
	}

	// Verify database state:
	// Record 1 moved to project/nanite/memory/decisions
	var ns1 string
	if err := db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, r1.MemoryID).Scan(&ns1); err != nil {
		t.Fatalf("query r1 namespace: %v", err)
	}
	if ns1 != "project/nanite/memory/decisions" {
		t.Errorf("r1 namespace mismatch: got %q, want 'project/nanite/memory/decisions'", ns1)
	}

	// Record 2 moved to system/memory/feedback
	var ns2 string
	if err := db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, r2.MemoryID).Scan(&ns2); err != nil {
		t.Fatalf("query r2 namespace: %v", err)
	}
	if ns2 != "system/memory/feedback" {
		t.Errorf("r2 namespace mismatch: got %q, want 'system/memory/feedback'", ns2)
	}

	// Record 3 untouched in user/chrispian/memory/limitations
	var ns3 string
	if err := db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, r3.MemoryID).Scan(&ns3); err != nil {
		t.Fatalf("query r3 namespace: %v", err)
	}
	if ns3 != "user/chrispian/memory/limitations" {
		t.Errorf("r3 namespace mismatch: got %q, want 'user/chrispian/memory/limitations'", ns3)
	}

	// Record 4 untouched in user/chrispian/memory/decisions
	var ns4 string
	if err := db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, r4.MemoryID).Scan(&ns4); err != nil {
		t.Fatalf("query r4 namespace: %v", err)
	}
	if ns4 != "user/chrispian/memory/decisions" {
		t.Errorf("r4 namespace mismatch: got %q, want 'user/chrispian/memory/decisions'", ns4)
	}

	// Record 5 untouched in user/chrispian/memory/notes
	var ns5 string
	if err := db.QueryRowContext(ctx, `SELECT namespace FROM memory_state WHERE memory_id = ?`, r5.MemoryID).Scan(&ns5); err != nil {
		t.Fatalf("query r5 namespace: %v", err)
	}
	if ns5 != "user/chrispian/memory/notes" {
		t.Errorf("r5 namespace mismatch: got %q, want 'user/chrispian/memory/notes'", ns5)
	}
}

func TestBacklogApplyCollisionRefusal(t *testing.T) {
	ctx := context.Background()
	_, mem, db := setupTestDB(t)

	// 1. Write an existing record directly in target namespace
	_, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "project/nanite/memory/decisions",
		MemoryKey:   "colliding_key",
		Summary:     "Pre-existing record in project namespace",
		Body:        "Existing",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-0",
	})
	if err != nil {
		t.Fatalf("write existing: %v", err)
	}

	// 2. Write a backlog record with same key targeting project/nanite/memory/decisions
	_, err = mem.WriteRevision(ctx, memory.WriteInput{
		Domain:      domains.Memory,
		Namespace:   "user/chrispian/memory/decisions",
		MemoryKey:   "colliding_key",
		Summary:     "Backlog record with same key",
		Body:        "Backlog",
		Status:      memory.StatusCanonical,
		Trigger:     memory.TriggerManual,
		DerivedFrom: memory.DerivedFromProject,
		Actor:       "user",
		Author:      memory.Author{AgentID: "agent"},
		SessionID:   "sess-1",
		Tags:        []string{"project:nanite"},
	})
	if err != nil {
		t.Fatalf("write backlog: %v", err)
	}

	plan, err := corpusmigration.BuildBacklogApplyPlan(ctx, db)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}

	if len(plan.Collisions) == 0 {
		t.Fatal("expected collision to be detected, got 0")
	}

	// Applying should refuse
	_, err = corpusmigration.ApplyBacklogPlan(ctx, db, plan)
	if err == nil {
		t.Fatal("expected apply to fail on collision, got nil error")
	}
}

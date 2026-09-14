package corpusmigration_test

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/corpusmigration"
)

func TestClassifierPrecedenceAndTiers(t *testing.T) {
	cases := []struct {
		name        string
		rec         corpusmigration.BacklogRecordInput
		wantTier    corpusmigration.SignalTier
		wantProject string
		wantTarget  string
		wantConfLen int
	}{
		// 1. Tier 1: Explicit project tag
		{
			name: "tier1 explicit project tag",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST1",
				Namespace: "user/chrispian/memory/decisions",
				Domain:    "memory",
				MemoryKey: "auth_token_format",
				Summary:   "Decided to use ULID tokens",
				Tags:      []string{"decision", "project:nanite", "auth"},
			},
			wantTier:    corpusmigration.Tier1ExplicitProjectTag,
			wantProject: "nanite",
			wantTarget:  "project/nanite/memory/decisions",
		},
		// 1b. Tier 1: Spelling normalization (underscore to hyphen)
		{
			name: "tier1 slug spelling normalization underscore to hyphen",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST1B",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "stack_explorer_state",
				Summary:   "Notes on stack explorer",
				Tags:      []string{"notes", "project:stack_explorer"},
			},
			wantTier:    corpusmigration.Tier1ExplicitProjectTag,
			wantProject: "stack-explorer",
			wantTarget:  "project/stack-explorer/memory/notes",
		},
		// 2. Tier 2: Bare project tag matching known project
		{
			name: "tier2 bare project tag with non-project tags (Director calibration sample 1)",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST2",
				Namespace: "user/chrispian/memory/limitations",
				Domain:    "memory",
				MemoryKey: "agent_mux_concurrency",
				Summary:   "Concurrency limitation in agent mux",
				Tags:      []string{"nanite", "agent-mux", "cw-20260420-0047", "limitations"},
			},
			wantTier:    corpusmigration.Tier2BareProjectTag,
			wantProject: "nanite",
			wantTarget:  "project/nanite/memory/limitations",
		},
		// 3. Tier 3: memory_key prefix (dot join)
		{
			name: "tier3 key prefix dot join without tags (Director calibration sample 3)",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST3A",
				Namespace: "user/chrispian/memory/decisions",
				Domain:    "memory",
				MemoryKey: "torque.cw_20260519_0085.option_4_terminal_reconcile_build",
				Summary:   "Option 4 terminal reconcile build",
				Tags:      []string{"refiled-cw-20260911-0005", "from-records-table"},
			},
			wantTier:    corpusmigration.Tier3KeyPrefix,
			wantProject: "torque",
			wantTarget:  "project/torque/memory/decisions",
		},
		// 3b. Tier 3: memory_key prefix (underscore join)
		{
			name: "tier3 key prefix underscore join",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST3B",
				Namespace: "user/chrispian/memory/followups",
				Domain:    "memory",
				MemoryKey: "cerberus_healthcheck_reconnection",
				Summary:   "Reconnect health checks",
				Tags:      []string{"followup", "healthcheck"},
			},
			wantTier:    corpusmigration.Tier3KeyPrefix,
			wantProject: "cerberus",
			wantTarget:  "project/cerberus/memory/followups",
		},
		// 4. Tier 4: System default (Director calibration sample 4: safety rule)
		{
			name: "tier4 system default cross-cutting safety rule",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST4A",
				Namespace: "user/chrispian/memory/feedback",
				Domain:    "memory",
				MemoryKey: "destructive_git_ops_require_approval",
				Summary:   "Destructive git operations require approval",
				Tags:      []string{"feedback", "safety", "git", "destructive-ops", "dispatch", "subagent", "orchestrator", "load-bearing"},
			},
			wantTier:    corpusmigration.Tier4SystemDefault,
			wantProject: "",
			wantTarget:  "system/memory/feedback",
		},
		// 4b. Tier 4: System default (Director calibration sample 5: portfolio layout decision)
		{
			name: "tier4 system default portfolio layout decision (no over-matching bare hollis-labs)",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST4B",
				Namespace: "user/chrispian/memory/decisions",
				Domain:    "memory",
				MemoryKey: "portfolio_seam_hollis_labs_apps_vs_personal_projects",
				Summary:   "~/dev/hollis-labs/apps/ holds Hollis Labs products only; personal projects live in ~/dev/projects/",
				Tags:      []string{"decision", "portfolio", "layout", "seam", "hollis-labs", "projects", "captured_during_session"},
			},
			wantTier:    corpusmigration.Tier4SystemDefault,
			wantProject: "",
			wantTarget:  "system/memory/decisions",
		},
		// 4c. Tier 4: System default (Director calibration sample 6: codex app-server runtime)
		{
			name: "tier4 system default codex runtime integration",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST4C",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "codex.app_server_runtime_integration",
				Summary:   "Codex app-server... Any project driving codex as a long-lived JSON-RPC session",
				Tags:      []string{},
			},
			wantTier:    corpusmigration.Tier4SystemDefault,
			wantProject: "",
			wantTarget:  "system/memory/notes",
		},
		// 5. Conflict: Multiple explicit project tags
		{
			name: "conflict multiple project tags",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST5A",
				Namespace: "user/chrispian/memory/limitations",
				Domain:    "memory",
				MemoryKey: "settled_decision_cross_project",
				Summary:   "Cross-project limitation affecting torque and cerberus",
				Tags:      []string{"limitation", "project:torque", "project:cerberus"},
			},
			wantTier:    corpusmigration.TierConflict,
			wantConfLen: 2,
		},
		// 5b. Conflict: Tier 1 vs Tier 2 conflict
		{
			name: "conflict tier1 vs tier2 different projects",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST5B",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "sync_observation",
				Summary:   "Observation on sync",
				Tags:      []string{"project:nanite", "hadron"},
			},
			wantTier:    corpusmigration.TierConflict,
			wantConfLen: 2,
		},
		// 6. Agreement across all tiers
		{
			name: "agreement across all 3 tiers resolves to tier1",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST6",
				Namespace: "user/chrispian/memory/notes",
				Domain:    "memory",
				MemoryKey: "nanite_worker_status",
				Summary:   "Nanite worker status details",
				Tags:      []string{"project:nanite", "nanite"},
			},
			wantTier:    corpusmigration.Tier1ExplicitProjectTag,
			wantProject: "nanite",
			wantTarget:  "project/nanite/memory/notes",
		},
		// 7. Event reasoning routing
		{
			name: "event reasoning routing per-record",
			rec: corpusmigration.BacklogRecordInput{
				MemoryID:  "01TEST7",
				Namespace: "user/chrispian/event/reasoning",
				Domain:    "event",
				MemoryKey: "",
				Summary:   "Tesseract schema 20 deployed: novelty scoring live",
				Tags:      []string{"friction", "project:tesseract", "task:CW-20260825-0018"},
			},
			wantTier:    corpusmigration.Tier1ExplicitProjectTag,
			wantProject: "tesseract",
			wantTarget:  "project/tesseract/event/reasoning",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			res := corpusmigration.ClassifyRecord(tc.rec)
			if res.SignalTier != tc.wantTier {
				t.Fatalf("tier mismatch: got %v, want %v", res.SignalTier, tc.wantTier)
			}
			if tc.wantTier == corpusmigration.TierConflict {
				if len(res.CandidateProjects) != tc.wantConfLen {
					t.Fatalf("candidate count mismatch: got %d (%v), want %d", len(res.CandidateProjects), res.CandidateProjects, tc.wantConfLen)
				}
				if res.ConflictReason == "" {
					t.Error("expected non-empty conflict reason")
				}
				return
			}
			if res.MatchedProject != tc.wantProject {
				t.Errorf("project mismatch: got %q, want %q", res.MatchedProject, tc.wantProject)
			}
			if res.TargetNamespace != tc.wantTarget {
				t.Errorf("target namespace mismatch: got %q, want %q", res.TargetNamespace, tc.wantTarget)
			}
		})
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/hollis-labs/tesseract/internal/corpusmigration"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"

	_ "modernc.org/sqlite"
)

type BacklogApplyJSONOutput struct {
	Plan    *corpusmigration.BacklogApplyPlan    `json:"plan"`
	Receipt *corpusmigration.BacklogApplyReceipt `json:"receipt"`
}

func runClassifyBacklog(ctx context.Context, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("classify-backlog", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dbPath := fs.String("db", "", "path to the SQLite store to inspect (required)")
	apply := fs.Bool("apply", false, "apply classified renames to the database")
	jsonOut := fs.Bool("json", false, "emit the full report as JSON")
	verbose := fs.Bool("verbose", false, "emit detailed per-record classification output")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "error: --db is required (no default store access permitted)")
		return 1
	}

	db, err := sql.Open("sqlite", sqlitedsn.DSN(*dbPath, "journal_mode(WAL)"))
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	if pingErr := db.PingContext(ctx); pingErr != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", pingErr)
		return 1
	}

	if *apply {
		// Snapshot wikilink graph before apply
		beforeGraph, bgErr := corpusmigration.EnumerateWikilinkGraph(ctx, db)
		if bgErr != nil {
			fmt.Fprintf(stderr, "error: snapshot wikilink graph before apply: %v\n", bgErr)
			return 1
		}

		plan, planErr := corpusmigration.BuildBacklogApplyPlan(ctx, db)
		if planErr != nil {
			fmt.Fprintf(stderr, "error: build backlog apply plan: %v\n", planErr)
			return 1
		}

		if len(plan.Collisions) > 0 {
			fmt.Fprintf(stderr, "error: refusing to apply: %d collision(s) detected\n", len(plan.Collisions))
			for i, c := range plan.Collisions {
				fmt.Fprintf(stderr, "  [%d] target: %s, key: %s, items: %v, reason: %s\n", i+1, c.TargetNamespace, c.Key, c.ItemIDs, c.Reason)
			}
			return 1
		}

		receipt, applyErr := corpusmigration.ApplyBacklogPlan(ctx, db, plan)
		if applyErr != nil {
			fmt.Fprintf(stderr, "error: apply backlog plan: %v\n", applyErr)
			return 1
		}

		// Snapshot wikilink graph after apply
		afterGraph, agErr := corpusmigration.EnumerateWikilinkGraph(ctx, db)
		if agErr == nil {
			receipt.WikilinkDiff = corpusmigration.DiffWikilinkGraphs(beforeGraph, afterGraph)
		}

		// Snapshot lineage
		lineage, linErr := corpusmigration.EnumerateLineage(ctx, db, 10)
		if linErr == nil {
			receipt.LineageReport = lineage
		}

		if *jsonOut {
			out := BacklogApplyJSONOutput{
				Plan:    plan,
				Receipt: receipt,
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
			return 0
		}

		printApplySummary(stdout, plan, receipt, *verbose)
		return 0
	}

	// Read-only report mode
	report, err := corpusmigration.ClassifyBacklog(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: classify backlog: %v\n", err)
		return 1
	}

	plan, _ := corpusmigration.BuildBacklogApplyPlan(ctx, db)

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return 0
	}

	printClassificationSummary(stdout, report, plan, *verbose)
	return 0
}

func printClassificationSummary(w io.Writer, report *corpusmigration.ClassificationReport, plan *corpusmigration.BacklogApplyPlan, verbose bool) {
	fmt.Fprintf(w, "Backlog Memory & Event Classification Report\n")
	fmt.Fprintf(w, "=============================================\n")
	fmt.Fprintf(w, "Total historical records classified: %d (READ ONLY: zero writes performed)\n\n", report.TotalRecords)

	// 1. Per-tier counts
	fmt.Fprintf(w, "Signal Tier Breakdown:\n")
	tierOrder := []corpusmigration.SignalTier{
		corpusmigration.Tier1ExplicitProjectTag,
		corpusmigration.Tier2BareProjectTag,
		corpusmigration.Tier3KeyPrefix,
		corpusmigration.Tier4SystemDefault,
		corpusmigration.TierConflict,
	}

	tierLabels := map[corpusmigration.SignalTier]string{
		corpusmigration.Tier1ExplicitProjectTag: "Tier 1: Explicit project tag (project:{slug})",
		corpusmigration.Tier2BareProjectTag:     "Tier 2: Bare portfolio project tag ({slug})",
		corpusmigration.Tier3KeyPrefix:          "Tier 3: Key prefix ({slug}_ or {slug}.)",
		corpusmigration.Tier4SystemDefault:      "Tier 4: No match -> System default candidate",
		corpusmigration.TierConflict:            "Conflict: Multiple project signals matched",
	}

	for _, tier := range tierOrder {
		cnt := report.PerTierCounts[tier]
		pct := 0.0
		if report.TotalRecords > 0 {
			pct = float64(cnt) / float64(report.TotalRecords) * 100.0
		}
		fmt.Fprintf(w, "  %-48s : %4d records (%5.1f%%)\n", tierLabels[tier], cnt, pct)
	}

	// 2. Planned disposition breakdown if plan is available
	if plan != nil {
		fmt.Fprintf(w, "\nPlanned Apply Disposition:\n")
		fmt.Fprintf(w, "  Applies (Total: %d):\n", plan.TotalApplied)
		fmt.Fprintf(w, "    %-46s : %4d records\n", "Applied via Tier 1/2/3 (Canonical Projects)", plan.AppliedTier123Count)
		fmt.Fprintf(w, "    %-46s : %4d records\n", "Applied via Tier 4 (System Default)", plan.AppliedTier4SystemCount)
		fmt.Fprintf(w, "  Skips / Deferred (Total: %d):\n", plan.TotalSkipped)
		fmt.Fprintf(w, "    %-46s : %4d records\n", "Skipped: Conflicts (>1 candidate project)", plan.SkippedConflictCount)
		fmt.Fprintf(w, "    %-46s : %4d records\n", "Skipped: Non-canonical projects", plan.SkippedNonCanonicalCount)
		fmt.Fprintf(w, "    %-46s : %4d records\n", "Skipped: Agridd exclusion pattern", plan.SkippedAgriddCount)
	}

	// 3. Per-project counts
	fmt.Fprintf(w, "\nPer-Project Target Breakdown:\n")
	var projects []string
	for p := range report.PerProjectCounts {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	for _, p := range projects {
		cnt := report.PerProjectCounts[p]
		fmt.Fprintf(w, "  %-32s : %4d records\n", p, cnt)
	}

	// 4. Conflicts list
	if len(report.Conflicts) > 0 {
		fmt.Fprintf(w, "\nConflicts Requiring Review (%d):\n", len(report.Conflicts))
		fmt.Fprintf(w, "--------------------------------\n")
		for i, c := range report.Conflicts {
			fmt.Fprintf(w, "[%2d] Key: %s\n", i+1, c.MemoryKey)
			fmt.Fprintf(w, "     Namespace: %s\n", c.Namespace)
			fmt.Fprintf(w, "     Candidates: %s\n", strings.Join(c.CandidateProjects, ", "))
			fmt.Fprintf(w, "     Reason: %s\n", c.ConflictReason)
			fmt.Fprintf(w, "     Tags: %s\n", strings.Join(c.Tags, ", "))
			fmt.Fprintf(w, "     Summary: %s\n\n", c.SummarySnippet)
		}
	} else {
		fmt.Fprintf(w, "\nConflicts Requiring Review: 0\n")
	}

	// 5. System-defaulted list
	if len(report.SystemDefaulted) > 0 {
		fmt.Fprintf(w, "\nSystem-Defaulted Candidates (%d):\n", len(report.SystemDefaulted))
		fmt.Fprintf(w, "---------------------------------\n")
		for i, s := range report.SystemDefaulted {
			fmt.Fprintf(w, "[%3d] Key: %s\n", i+1, s.MemoryKey)
			fmt.Fprintf(w, "      Namespace: %s -> %s\n", s.Namespace, s.TargetNamespace)
			if len(s.Tags) > 0 {
				fmt.Fprintf(w, "      Tags: %s\n", strings.Join(s.Tags, ", "))
			}
			fmt.Fprintf(w, "      Summary: %s\n\n", s.SummarySnippet)
		}
	} else {
		fmt.Fprintf(w, "\nSystem-Defaulted Candidates: 0\n")
	}

	if verbose && len(report.Classified) > 0 {
		fmt.Fprintf(w, "\nAll Classified Records (%d):\n", len(report.Classified))
		fmt.Fprintf(w, "-----------------------------\n")
		for i, rec := range report.Classified {
			fmt.Fprintf(w, "[%4d] %s -> %s (Tier: %s, Project: %s)\n", i+1, rec.Namespace, rec.TargetNamespace, rec.SignalTier, rec.MatchedProject)
		}
	}
}

func printApplySummary(w io.Writer, plan *corpusmigration.BacklogApplyPlan, receipt *corpusmigration.BacklogApplyReceipt, verbose bool) {
	fmt.Fprintf(w, "Backlog Memory & Event Classification Apply Receipt\n")
	fmt.Fprintf(w, "====================================================\n")
	fmt.Fprintf(w, "Total historical records processed: %d\n", plan.TotalRecords)
	fmt.Fprintf(w, "Total records successfully applied: %d\n", receipt.TotalApplied)
	fmt.Fprintf(w, "Total records skipped / deferred : %d\n\n", receipt.TotalSkipped)

	fmt.Fprintf(w, "Applied Records Breakdown:\n")
	fmt.Fprintf(w, "  Applied via Tier 1/2/3 (Canonical Projects) : %4d\n", plan.AppliedTier123Count)
	fmt.Fprintf(w, "  Applied via Tier 4 (System Default)         : %4d\n\n", plan.AppliedTier4SystemCount)

	fmt.Fprintf(w, "Skipped Records Breakdown:\n")
	fmt.Fprintf(w, "  Skipped: Conflicts (>1 candidate project)   : %4d\n", plan.SkippedConflictCount)
	fmt.Fprintf(w, "  Skipped: Non-canonical projects             : %4d\n", plan.SkippedNonCanonicalCount)
	fmt.Fprintf(w, "  Skipped: Agridd exclusion pattern           : %4d\n\n", plan.SkippedAgriddCount)

	fmt.Fprintf(w, "Database Table Rows Affected:\n")
	fmt.Fprintf(w, "  memory_state rows updated      : %4d\n", receipt.UpdatedStateRows)
	fmt.Fprintf(w, "  memory_revisions rows updated  : %4d\n", receipt.UpdatedRevisionRows)
	fmt.Fprintf(w, "  records rows updated           : %4d\n", receipt.UpdatedRecordRows)
	fmt.Fprintf(w, "  heads rows updated             : %4d\n", receipt.UpdatedHeadRows)
	fmt.Fprintf(w, "  namespace_policies registered  : %4d\n\n", receipt.RegisteredNamespaces)

	if receipt.WikilinkDiff != nil {
		fmt.Fprintf(w, "Wikilink Graph Diff:\n")
		fmt.Fprintf(w, "  Total links                    : %d\n", receipt.WikilinkDiff.AfterTotal)
		fmt.Fprintf(w, "  Resolved links before          : %d\n", receipt.WikilinkDiff.BeforeResolved)
		fmt.Fprintf(w, "  Resolved links after           : %d\n", receipt.WikilinkDiff.AfterResolved)
		fmt.Fprintf(w, "  Broken links (fatal defect)    : %d\n", len(receipt.WikilinkDiff.BrokenLinks))
		fmt.Fprintf(w, "  Newly resolved links           : %d\n", len(receipt.WikilinkDiff.NewlyResolved))
		fmt.Fprintf(w, "  Changed target memory IDs      : %d\n\n", len(receipt.WikilinkDiff.ChangedTargets))
	}

	if receipt.LineageReport != nil {
		fmt.Fprintf(w, "Lineage Report:\n")
		fmt.Fprintf(w, "  Total lineage edges (supersedes): %d\n\n", receipt.LineageReport.TotalLineageEdges)
	}

	fmt.Fprintf(w, "Applied Counts by Project:\n")
	var projects []string
	for p := range plan.AppliedCountsByProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	for _, p := range projects {
		fmt.Fprintf(w, "  %-32s : %4d records\n", p, plan.AppliedCountsByProject[p])
	}

	if verbose {
		fmt.Fprintf(w, "\nSkipped Records Detail (%d):\n", len(plan.SkippedRecords))
		fmt.Fprintf(w, "----------------------------\n")
		for i, s := range plan.SkippedRecords {
			fmt.Fprintf(w, "[%3d] %s | %s | %s\n", i+1, s.MemoryID, s.MemoryKey, s.OldNamespace)
			fmt.Fprintf(w, "      Outcome: %s | Reason: %s\n", s.Outcome, s.SkipReason)
			if len(s.Tags) > 0 {
				fmt.Fprintf(w, "      Tags: %s\n", strings.Join(s.Tags, ", "))
			}
			fmt.Fprintf(w, "      Summary: %s\n\n", s.SummarySnippet)
		}
	}
}

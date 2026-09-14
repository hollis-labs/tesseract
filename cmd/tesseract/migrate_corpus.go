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

	"github.com/hollis-labs/tesseract/internal/corpusmigration"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"

	_ "modernc.org/sqlite"
)

type corpusMigrationOutput struct {
	Plan    *corpusmigration.MigrationPlan    `json:"plan,omitempty"`
	Receipt *corpusmigration.MigrationReceipt `json:"receipt,omitempty"`
	Diff    *corpusmigration.WikilinkDiff     `json:"wikilink_diff,omitempty"`
	Lineage *corpusmigration.LineageReport    `json:"lineage,omitempty"`
}

func runMigrateCorpus(ctx context.Context, defaultDB string, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("migrate-corpus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dbPath := fs.String("db", defaultDB, "path to the SQLite store to migrate (required)")
	apply := fs.Bool("apply", false, "commit the plan instead of planning it")
	jsonOut := fs.Bool("json", false, "emit the full plan and diff as JSON")
	verbose := fs.Bool("verbose", false, "emit detailed per-namespace migration progress")

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

	graphBefore, err := corpusmigration.EnumerateWikilinkGraph(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: enumerate wikilink graph: %v\n", err)
		return 1
	}

	lineageBefore, err := corpusmigration.EnumerateLineage(ctx, db, 10)
	if err != nil {
		fmt.Fprintf(stderr, "error: enumerate lineage: %v\n", err)
		return 1
	}

	plan, err := corpusmigration.BuildMigrationPlan(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: build migration plan: %v\n", err)
		return 1
	}

	if !*apply {
		if *jsonOut {
			out := corpusMigrationOutput{
				Plan:    plan,
				Lineage: lineageBefore,
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
		} else {
			printPlanSummary(stdout, plan, graphBefore, lineageBefore, *verbose)
			fmt.Fprintln(stdout, "\n(dry-run; pass --apply to commit the migration)")
		}

		if len(plan.Collisions) > 0 {
			fmt.Fprintf(stderr, "\nerror: %d collision(s) detected in plan\n", len(plan.Collisions))
			return 2
		}
		return 0
	}

	if len(plan.Collisions) > 0 {
		fmt.Fprintf(stderr, "error: refusing to apply — %d collision(s) detected\n", len(plan.Collisions))
		return 2
	}

	receipt, err := corpusmigration.ApplyMigration(ctx, db, plan)
	if err != nil {
		fmt.Fprintf(stderr, "error: apply migration: %v\n", err)
		return 1
	}

	graphAfter, err := corpusmigration.EnumerateWikilinkGraph(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: enumerate wikilink graph after: %v\n", err)
		return 1
	}

	diff := corpusmigration.DiffWikilinkGraphs(graphBefore, graphAfter)

	lineageAfter, err := corpusmigration.EnumerateLineage(ctx, db, 10)
	if err != nil {
		fmt.Fprintf(stderr, "error: enumerate lineage after: %v\n", err)
		return 1
	}

	if *jsonOut {
		out := corpusMigrationOutput{
			Plan:    plan,
			Receipt: receipt,
			Diff:    diff,
			Lineage: lineageAfter,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		printExecutionSummary(stdout, plan, receipt, diff, lineageBefore, lineageAfter, *verbose)
	}

	if len(diff.BrokenLinks) > 0 {
		fmt.Fprintf(stderr, "\nFATAL DEFECT: %d wikilinks became unresolved after migration!\n", len(diff.BrokenLinks))
		return 1
	}

	return 0
}

func printPlanSummary(w io.Writer, plan *corpusmigration.MigrationPlan, graph *corpusmigration.WikilinkGraph, lineage *corpusmigration.LineageReport, verbose bool) {
	fmt.Fprintf(w, "N6 Corpus Migration Plan\n")
	fmt.Fprintf(w, "========================\n")
	fmt.Fprintf(w, "Total namespaces in store: %d\n", plan.TotalNamespaces)
	fmt.Fprintf(w, "Total rows to update/promote: %d\n", plan.TotalRowsAffected)
	fmt.Fprintf(w, "Total reclassification records: %d\n\n", len(plan.Reclassifications))

	fmt.Fprintf(w, "Category Breakdown:\n")
	var categories []string
	for cat := range plan.CategoryCounts {
		categories = append(categories, string(cat))
	}
	sort.Strings(categories)

	for _, c := range categories {
		cat := corpusmigration.RuleCategory(c)
		fmt.Fprintf(w, "  %-32s : %4d namespaces, %5d rows\n", cat, plan.CategoryCounts[cat], plan.CategoryRows[cat])
	}

	if len(plan.SkippedNamespaces) > 0 {
		fmt.Fprintf(w, "\nSkipped / Deferred Namespaces (%d):\n", len(plan.SkippedNamespaces))
		for _, sk := range plan.SkippedNamespaces {
			fmt.Fprintf(w, "  - %s\n", sk)
		}
	}

	if len(plan.Collisions) > 0 {
		fmt.Fprintf(w, "\nCollisions Detected (%d):\n", len(plan.Collisions))
		for _, col := range plan.Collisions {
			fmt.Fprintf(w, "  - Target: %s, Key: %s, Source: %s\n", col.TargetNamespace, col.Key, col.Source)
		}
	}

	fmt.Fprintf(w, "\nGraph & Lineage State:\n")
	fmt.Fprintf(w, "  Wikilinks: %d total (%d resolved)\n", graph.TotalLinks, graph.ResolvedLinks)
	fmt.Fprintf(w, "  Lineage edges: %d supersedes relations\n", lineage.TotalLineageEdges)

	if verbose {
		fmt.Fprintf(w, "\nDetailed Namespace Mapping (%d):\n", len(plan.Namespaces))
		for _, ns := range plan.Namespaces {
			switch ns.Action {
			case corpusmigration.ActionRename:
				fmt.Fprintf(w, "  [rename] %s -> %s (%d rows)\n", ns.OldNamespace, ns.NewNamespace, ns.TotalRows)
			case corpusmigration.ActionReclassify:
				fmt.Fprintf(w, "  [reclassify] %s -> %s (%s, %d rows)\n", ns.OldNamespace, ns.NewNamespace, ns.Category, ns.TotalRows)
			case corpusmigration.ActionVerify:
				fmt.Fprintf(w, "  [verify] %s (%d rows)\n", ns.OldNamespace, ns.TotalRows)
			case corpusmigration.ActionDeferred:
				fmt.Fprintf(w, "  [deferred] %s\n", ns.OldNamespace)
			case corpusmigration.ActionSkip:
				fmt.Fprintf(w, "  [skip] %s\n", ns.OldNamespace)
			}
		}
	}
}

func printExecutionSummary(w io.Writer, plan *corpusmigration.MigrationPlan, receipt *corpusmigration.MigrationReceipt, diff *corpusmigration.WikilinkDiff, lineageBefore, lineageAfter *corpusmigration.LineageReport, verbose bool) {
	fmt.Fprintf(w, "N6 Corpus Migration Applied\n")
	fmt.Fprintf(w, "===========================\n")
	fmt.Fprintf(w, "Namespaces renamed: %d\n", receipt.RenamedNamespaces)
	fmt.Fprintf(w, "Rows updated in memory_state: %d\n", receipt.UpdatedStateRows)
	fmt.Fprintf(w, "Rows updated in memory_revisions: %d\n", receipt.UpdatedRevisionRows)
	fmt.Fprintf(w, "Rows updated in records (context): %d\n", receipt.UpdatedRecordRows)
	fmt.Fprintf(w, "Rows updated in heads (context): %d\n", receipt.UpdatedHeadRows)
	fmt.Fprintf(w, "Rows updated in workspace_items: %d\n", receipt.UpdatedWorkspaceRows)
	fmt.Fprintf(w, "Records promoted (cross-domain): %d\n", receipt.PromotedRecords)

	fmt.Fprintf(w, "\nWikilink Graph Diff:\n")
	fmt.Fprintf(w, "  Before: %d links (%d resolved)\n", diff.BeforeTotal, diff.BeforeResolved)
	fmt.Fprintf(w, "  After:  %d links (%d resolved)\n", diff.AfterTotal, diff.AfterResolved)
	fmt.Fprintf(w, "  Broken links: %d\n", len(diff.BrokenLinks))
	fmt.Fprintf(w, "  Newly resolved: %d\n", len(diff.NewlyResolved))

	fmt.Fprintf(w, "\nLineage Integrity:\n")
	fmt.Fprintf(w, "  Lineage edges before: %d\n", lineageBefore.TotalLineageEdges)
	fmt.Fprintf(w, "  Lineage edges after:  %d\n", lineageAfter.TotalLineageEdges)
	if lineageBefore.TotalLineageEdges == lineageAfter.TotalLineageEdges {
		fmt.Fprintf(w, "  Status: 100%% lineage edges preserved intact\n")
	} else {
		fmt.Fprintf(w, "  Status: WARNING edge count changed (%d -> %d)\n", lineageBefore.TotalLineageEdges, lineageAfter.TotalLineageEdges)
	}

	if verbose {
		fmt.Fprintf(w, "\nExecution Plan Verification:\n")
		fmt.Fprintf(w, "  Planned namespaces: %d\n", len(plan.Namespaces))
		fmt.Fprintf(w, "  Planned reclassifications: %d\n", len(plan.Reclassifications))
	}

	if len(receipt.Errors) > 0 {
		fmt.Fprintf(w, "\nErrors encountered (%d):\n", len(receipt.Errors))
		for _, e := range receipt.Errors {
			fmt.Fprintf(w, "  - %s\n", e)
		}
	}
}

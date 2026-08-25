package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"

	_ "modernc.org/sqlite"
)

// runMigrateKnowledgeKinds normalizes off-vocabulary knowledge `facet_kind`
// values in place. Dry-run by default; --apply commits. Mirrors
// runMigrateNamespaces, but the plan is per-revision and the apply touches a
// single column on a single table.
func runMigrateKnowledgeKinds(ctx context.Context, defaultDB string, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("migrate-knowledge-kinds", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", defaultDB, "path to the SQLite store to migrate (defaults to resolved layout DB)")
	apply := fs.Bool("apply", false, "actually write the changes; otherwise dry-run")
	jsonOut := fs.Bool("json", false, "emit the full plan as JSON instead of the human-readable summary")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "error: --db is required (and no layout DB was resolved)")
		return 1
	}

	// Open the DB directly (not via contextstore.Open) so a run against an
	// arbitrary file copy doesn't materialize workspace layout side-effects.
	db, err := sql.Open("sqlite", sqlitedsn.DSN(*dbPath, "journal_mode(WAL)"))
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", err)
		return 1
	}

	plan, err := memory.BuildKindMigrationPlan(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: build plan: %v\n", err)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(plan)
	} else {
		printKindMigrationSummary(stdout, plan)
	}

	if !*apply {
		if !*jsonOut {
			fmt.Fprintln(stdout, "\n(dry-run; pass --apply to commit the plan)")
		}
		return 0
	}

	if len(plan.Unmapped) > 0 {
		fmt.Fprintf(stderr, "error: refusing to apply — plan has %d unmapped off-vocabulary kind(s); resolve before re-running\n", len(plan.Unmapped))
		return 2
	}

	revN, err := memory.ApplyKindMigration(ctx, db, plan)
	if err != nil {
		fmt.Fprintf(stderr, "error: apply: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nApplied: %d memory_revisions row(s) updated.\n", revN)
	return 0
}

func printKindMigrationSummary(w io.Writer, plan memory.KindMigrationPlan) {
	fmt.Fprintf(w, "Knowledge kind migration plan — %d row(s) (%d head, %d historical)\n",
		len(plan.Rows), plan.HeadRows, len(plan.Rows)-plan.HeadRows)
	fmt.Fprintf(w, "Target vocabulary (%d): %s\n", len(plan.Vocabulary), strings.Join(plan.Vocabulary, ", "))
	fmt.Fprintf(w, "Source filter: %s\n", plan.SourceFilter)

	// Counts per old -> new mapping.
	counts := map[string]int{}
	for _, r := range plan.Rows {
		counts[r.OldKind+" -> "+r.NewKind]++
	}
	fmt.Fprintln(w, "\nMapping distribution:")
	if len(counts) == 0 {
		fmt.Fprintln(w, "  (nothing to migrate — no off-vocabulary kinds found)")
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-32s (%d)\n", k, counts[k])
	}

	if len(plan.Rows) > 0 {
		fmt.Fprintln(w, "\nPer-row mapping:")
		for _, r := range plan.Rows {
			head := "historical"
			if r.IsHead {
				head = "head"
			}
			fmt.Fprintf(w, "  %s  %s -> %s  [%s]\n    %s :: %s  (memory_id=%s, %s)\n",
				r.RevisionID, r.OldKind, r.NewKind, r.Reason,
				r.Namespace, r.MemoryKey, r.MemoryID, head)
		}
	}

	if len(plan.Unmapped) > 0 {
		fmt.Fprintf(w, "\nUNMAPPED (%d) — off-vocabulary kinds with no mapping; apply refused:\n", len(plan.Unmapped))
		for _, u := range plan.Unmapped {
			fmt.Fprintf(w, "  %s (%d) -> %v\n", u.Kind, u.Count, u.RevisionIDs)
		}
	}
}

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

	_ "modernc.org/sqlite"
)

func runMigrateNamespaces(ctx context.Context, defaultDB string, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("migrate-namespaces", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", defaultDB, "path to the SQLite store to migrate (defaults to resolved layout DB)")
	apply := fs.Bool("apply", false, "actually write the changes; otherwise dry-run")
	jsonOut := fs.Bool("json", false, "emit the full plan as JSON instead of the human-readable summary")
	threshold := fs.Int("project-threshold", 2, "min occurrences for a segment to be lifted as project: tag")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "error: --db is required (and no layout DB was resolved)")
		return 1
	}

	// Open the DB directly (not via contextstore.Open) so a migration run
	// against an arbitrary file copy doesn't try to materialize the workspace
	// layout side-effects.
	db, err := sql.Open("sqlite", *dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", err)
		return 1
	}

	plan, err := memory.BuildMigrationPlan(ctx, db, *threshold)
	if err != nil {
		fmt.Fprintf(stderr, "error: build plan: %v\n", err)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(plan)
	} else {
		printMigrationSummary(stdout, plan)
	}

	if !*apply {
		if !*jsonOut {
			fmt.Fprintln(stdout, "\n(dry-run; pass --apply to commit the plan)")
		}
		return 0
	}

	if len(plan.Collisions) > 0 {
		fmt.Fprintf(stderr, "error: refusing to apply — plan has %d collision(s); resolve before re-running\n", len(plan.Collisions))
		return 2
	}

	stateN, revN, err := memory.ApplyMigration(ctx, db, plan)
	if err != nil {
		fmt.Fprintf(stderr, "error: apply: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nApplied: %d memory_state rows, %d memory_revisions rows updated.\n", stateN, revN)
	return 0
}

func printMigrationSummary(w io.Writer, plan memory.MigrationPlan) {
	fmt.Fprintf(w, "Migration plan — %d row(s)\n", len(plan.Rows))
	fmt.Fprintf(w, "Source filter: %s\n", plan.SourceFilter)
	if len(plan.ProjectSet) > 0 {
		fmt.Fprintf(w, "Detected projects (>= threshold): %s\n", strings.Join(plan.ProjectSet, ", "))
	} else {
		fmt.Fprintln(w, "Detected projects: (none above threshold)")
	}

	// Counts per new namespace.
	counts := map[string]int{}
	for _, r := range plan.Rows {
		counts[r.NewNamespace]++
	}
	fmt.Fprintln(w, "\nNew namespace distribution:")
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %s  (%d)\n", k, counts[k])
	}

	fmt.Fprintln(w, "\nPer-row mapping (sampled — first 40 of plan):")
	for i, r := range plan.Rows {
		if i >= 40 {
			fmt.Fprintf(w, "  ... %d more rows (use --json for full plan)\n", len(plan.Rows)-i)
			break
		}
		fmt.Fprintf(w, "  %s\n    old: ns=%-50s key=%s tags=%v\n    new: ns=%-50s key=%s tags=%v  [%s]\n",
			r.MemoryID,
			r.OldNamespace, r.OldKey, r.OldTags,
			r.NewNamespace, r.NewKey, r.NewTags, r.Reason)
	}

	if len(plan.Collisions) > 0 {
		fmt.Fprintf(w, "\nCOLLISIONS (%d) — would violate UNIQUE(namespace, memory_key); apply refused:\n", len(plan.Collisions))
		for _, c := range plan.Collisions {
			fmt.Fprintf(w, "  %s :: %s -> %v\n", c.Namespace, c.Key, c.MemoryIDs)
		}
	}
}

// sortStrings is a tiny helper to keep import surface small.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

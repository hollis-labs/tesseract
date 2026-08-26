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

// Exit codes. 2 and 3 are both "refused to apply, nothing was written"; they
// are distinct so a caller can tell a corpus problem (an unknown kind needing
// a mapping) from an approval problem (the corpus moved since it was reviewed).
const (
	exitKindsRefusedUnmapped    = 2
	exitKindsRefusedExpectation = 3
)

// runMigrateKnowledgeKinds normalizes off-vocabulary knowledge `facet_kind`
// values in place. Dry-run by default; --apply commits.
//
// --apply rebuilds the plan in-process, so nothing structurally ties the plan a
// reviewer inspected to the plan that commits. --expect-rows / --expect-digest
// close that gap: the apply refuses unless the freshly built plan still matches
// what was approved. Use them for any apply against a live store — the default
// --db is the live store, and the corpus can gain a row between review and run.
func runMigrateKnowledgeKinds(ctx context.Context, defaultDB string, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("migrate-knowledge-kinds", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", defaultDB, "path to the SQLite store to migrate (defaults to resolved layout DB)")
	apply := fs.Bool("apply", false, "actually write the changes; otherwise dry-run")
	jsonOut := fs.Bool("json", false, "emit the full plan as JSON instead of the human-readable summary")
	expectRows := fs.Int("expect-rows", -1, "refuse to apply unless the freshly built plan has exactly this many rows")
	expectDigest := fs.String("expect-digest", "", "refuse to apply unless the freshly built plan digests to this value")
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
	//
	// A dry-run opens read-only, so "a dry-run cannot write" is a property of
	// the connection rather than of the code path. This deliberately diverges
	// from the namespace migration, which keeps one writable handle for both
	// modes: this tool's default --db is the live store, and there is no reason
	// to hold a writable handle open during a read-only operation. mode=ro also
	// rules out the WAL pragma, which would itself be a write.
	db, err := sql.Open("sqlite", kindsDSN(*dbPath, *apply))
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	if pingErr := db.PingContext(ctx); pingErr != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", pingErr)
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
		_ = enc.Encode(planEnvelope{KindMigrationPlan: plan, Digest: plan.Digest()})
	} else {
		printKindMigrationSummary(stdout, plan)
	}

	if !*apply {
		if !*jsonOut {
			fmt.Fprintf(stdout, "\n(dry-run; nothing written — the database was opened read-only)\n")
			fmt.Fprintf(stdout, "To apply exactly this plan:\n  tesseract migrate-knowledge-kinds --db %s --apply --expect-rows %d --expect-digest %s\n",
				*dbPath, len(plan.Rows), plan.Digest())
		}
		return 0
	}

	// Approval checks first: they describe the plan as a whole, so they should
	// refuse before any per-row consideration.
	if *expectRows >= 0 && len(plan.Rows) != *expectRows {
		fmt.Fprintf(stderr, "error: refusing to apply — plan has %d row(s), --expect-rows said %d; the corpus changed since it was reviewed, re-run the dry-run\n",
			len(plan.Rows), *expectRows)
		return exitKindsRefusedExpectation
	}
	if *expectDigest != "" && !strings.EqualFold(plan.Digest(), *expectDigest) {
		fmt.Fprintf(stderr, "error: refusing to apply — plan digest is %s, --expect-digest said %s; the corpus changed since it was reviewed, re-run the dry-run\n",
			plan.Digest(), *expectDigest)
		return exitKindsRefusedExpectation
	}

	if len(plan.Unmapped) > 0 {
		fmt.Fprintf(stderr, "error: refusing to apply — plan has %d unmapped off-vocabulary kind(s); resolve before re-running\n", len(plan.Unmapped))
		return exitKindsRefusedUnmapped
	}

	revN, err := memory.ApplyKindMigration(ctx, db, plan)
	if err != nil {
		fmt.Fprintf(stderr, "error: apply: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nApplied: %d memory_revisions row(s) updated.\n", revN)

	// Assert the post-condition rather than inferring it from the row count.
	// Measured by aggregate query, a different code path from the planner.
	remaining, err := memory.CountNonConformantKinds(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: post-apply conformance check: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Post-apply check: %d knowledge revision(s) still carry an off-vocabulary facet_kind.\n", remaining)
	if remaining != 0 {
		fmt.Fprintln(stderr, "error: migration applied but the store is not conformant; investigate before enforcing the vocabulary")
		return 1
	}
	return 0
}

// kindsDSN picks the connection string for the run. A dry-run gets a
// read-only handle so it cannot write even if the code path were wrong; only
// --apply gets a writable one. mode=ro also precludes the WAL pragma, which
// is itself a write and would fail on a read-only connection.
func kindsDSN(dbPath string, apply bool) string {
	if apply {
		return sqlitedsn.DSN(dbPath, "journal_mode(WAL)")
	}
	return sqlitedsn.DSN(dbPath) + "&mode=ro"
}

// planEnvelope adds the digest to the JSON form without embedding a derived
// value in the plan struct itself.
type planEnvelope struct {
	memory.KindMigrationPlan
	Digest string `json:"digest"`
}

func printKindMigrationSummary(w io.Writer, plan memory.KindMigrationPlan) {
	fmt.Fprintf(w, "Knowledge kind migration plan — %d row(s) (%d head, %d historical)\n",
		len(plan.Rows), plan.HeadRows, len(plan.Rows)-plan.HeadRows)
	fmt.Fprintf(w, "Plan digest: %s\n", plan.Digest())
	fmt.Fprintf(w, "Target vocabulary (%d): %s\n", len(plan.Vocabulary), strings.Join(plan.Vocabulary, ", "))
	fmt.Fprintf(w, "Source filter: %s (vocabulary applied in Go)\n", plan.SourceFilter)

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

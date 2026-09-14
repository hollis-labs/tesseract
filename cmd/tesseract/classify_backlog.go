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

func runClassifyBacklog(ctx context.Context, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("classify-backlog", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dbPath := fs.String("db", "", "path to the SQLite store to inspect (required)")
	jsonOut := fs.Bool("json", false, "emit the full classification report as JSON")
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

	report, err := corpusmigration.ClassifyBacklog(ctx, db)
	if err != nil {
		fmt.Fprintf(stderr, "error: classify backlog: %v\n", err)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return 0
	}

	printClassificationSummary(stdout, report, *verbose)
	return 0
}

func printClassificationSummary(w io.Writer, report *corpusmigration.ClassificationReport, verbose bool) {
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

	// 2. Per-project counts
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

	// 3. Conflicts list
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

	// 4. System-defaulted list
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

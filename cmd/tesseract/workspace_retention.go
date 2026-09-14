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
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"
	"github.com/hollis-labs/tesseract/internal/workspace"

	_ "modernc.org/sqlite"
)

type repeatedString []string

func (v *repeatedString) String() string { return strings.Join(*v, ",") }
func (v *repeatedString) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("item-id cannot be empty")
	}
	*v = append(*v, value)
	return nil
}

func runWorkspaceRetention(ctx context.Context, defaultDB string, cfg config.Config, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("workspace-retention", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", defaultDB, "path to the SQLite store (defaults to resolved layout DB)")
	apply := fs.Bool("apply", false, "purge eligible items after transactional recheck; otherwise report only")
	jsonOut := fs.Bool("json", false, "emit JSON")
	cursor := fs.String("cursor", "", "last scanned item_id from next_cursor")
	limit := fs.Int("limit", 100, "maximum workspace items to scan (1-1000)")
	var itemIDs repeatedString
	fs.Var(&itemIDs, "item-id", "specific reported item_id to recheck and purge; repeatable and valid only with -apply")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument(s): %s\n", strings.Join(fs.Args(), " "))
		return 1
	}
	if *dbPath == "" {
		fmt.Fprintln(stderr, "error: --db is required")
		return 1
	}
	if len(itemIDs) > 0 && !*apply {
		fmt.Fprintln(stderr, "error: --item-id requires --apply")
		return 1
	}
	parsed, err := config.ParseWorkspaceRetention(cfg.Workspace.Retention)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if *apply && !parsed.PurgeEnabled {
		fmt.Fprintln(stderr, "error: workspace purge is disabled by operator configuration")
		return 1
	}
	db, err := sql.Open("sqlite", workspaceRetentionDSN(*dbPath, *apply))
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	if pingErr := db.PingContext(ctx); pingErr != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", pingErr)
		return 1
	}
	store := workspace.NewStore(db)
	settings := workspace.RetentionSettings{PurgeEnabled: parsed.PurgeEnabled, MinimumIdle: parsed.MinimumIdle}
	report, err := store.ReportRetention(ctx, workspace.RetentionReportInput{Settings: settings, Cursor: *cursor, Limit: *limit})
	if err != nil {
		fmt.Fprintf(stderr, "error: retention report: %v\n", err)
		return 1
	}
	if !*jsonOut {
		printWorkspaceRetentionReport(stdout, report)
	}
	if !*apply {
		if *jsonOut {
			writeIndentedJSON(stdout, report)
		}
		return 0
	}
	selected := []string(itemIDs)
	if len(selected) == 0 {
		for _, candidate := range report.Candidates {
			if candidate.Eligible {
				selected = append(selected, candidate.ItemID)
			}
		}
	}
	if len(selected) == 0 {
		if *jsonOut {
			writeIndentedJSON(stdout, map[string]any{"report": report, "apply": workspace.RetentionApplyReport{EvaluatedAt: report.EvaluatedAt, Results: []workspace.RetentionApplyResult{}}})
		} else {
			fmt.Fprintln(stdout, "No eligible items selected for apply.")
		}
		return 0
	}
	applied, err := store.ApplyRetention(ctx, workspace.RetentionApplyInput{Settings: settings, ItemIDs: selected})
	if *jsonOut {
		writeIndentedJSON(stdout, map[string]any{"report": report, "apply": applied})
	} else {
		printWorkspaceRetentionApply(stdout, applied)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: retention apply: %v\n", err)
		return 1
	}
	return 0
}

// workspaceRetentionDSN makes report-only runs read-only at SQLite's boundary.
// Apply gets WAL plus the ordinary busy timeout for concurrent-use retries.
func workspaceRetentionDSN(dbPath string, apply bool) string {
	if apply {
		return sqlitedsn.DSN(dbPath, "journal_mode(WAL)")
	}
	return sqlitedsn.DSN(dbPath) + "&mode=ro"
}

func writeIndentedJSON(w io.Writer, value any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}

func printWorkspaceRetentionReport(w io.Writer, report workspace.RetentionReport) {
	fmt.Fprintf(w, "Workspace retention report — evaluated %s; scanned %d; truncated=%t\n", report.EvaluatedAt.Format(time.RFC3339), report.Scanned, report.Truncated)
	for _, c := range report.Candidates {
		fmt.Fprintf(w, "  %s  ns=%s  last_used=%s  activation=%.6f effective=%.6f  eligible=%t  reason=%s\n",
			c.ItemID, c.Namespace, c.LastUsedAt.Format(time.RFC3339), c.StoredActivation, c.EffectiveActivation, c.Eligible, c.PolicyReason)
	}
	if report.NextCursor != "" {
		fmt.Fprintf(w, "next_cursor: %s\n", report.NextCursor)
	}
	fmt.Fprintln(w, "(dry-run; no content was read or removed)")
}

func printWorkspaceRetentionApply(w io.Writer, report workspace.RetentionApplyReport) {
	fmt.Fprintf(w, "Workspace retention apply — purged %d; skipped %d\n", report.Purged, report.Skipped)
	for _, result := range report.Results {
		fmt.Fprintf(w, "  %s  %s", result.ItemID, result.Status)
		if result.Reason != "" {
			fmt.Fprintf(w, "  reason=%s", result.Reason)
		}
		fmt.Fprintln(w)
	}
}

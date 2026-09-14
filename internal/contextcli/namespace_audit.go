package contextcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

var inactiveCutoff = time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)

// NamespaceAuditReport summarizes the state of the namespace registry.
// All fields are computed in a report-only pass with zero mutations.
type NamespaceAuditReport struct {
	TotalNamespaces  int `json:"total_namespaces"`
	Declared         int `json:"declared"`
	Seeded           int `json:"seeded"`
	Inferred         int `json:"inferred"`
	InferredInactive int `json:"inferred_inactive"`
	UndeclaredPolicy int `json:"undeclared_policy"`
	DeclaredPolicy   int `json:"declared_policy"`
	LegacyNested     int `json:"legacy_nested"`
	Suspicious       int `json:"suspicious"`
}

// ComputeNamespaceAudit aggregates metrics over the entire namespace registry
// using paged queries through ListNamespacePolicyPage. It performs zero writes.
func (c *CLI) ComputeNamespaceAudit(ctx context.Context) (NamespaceAuditReport, error) {
	var report NamespaceAuditReport
	const pageSize = 250
	cursor := ""

	for {
		page, err := c.Store.ListNamespacePolicyPage(ctx, contextstore.NamespaceQuery{
			Limit:  pageSize,
			Cursor: cursor,
		})
		if err != nil {
			return NamespaceAuditReport{}, err
		}

		for _, entry := range page.Items {
			report.TotalNamespaces++

			src, _ := entry.Policy["source"].(string)
			switch src {
			case "seed":
				report.Seeded++
			case "inferred", "inferred-backfill":
				report.Inferred++
				if entry.UpdatedAt != "" {
					if t, err := parseAuditTime(entry.UpdatedAt); err == nil && t.Before(inactiveCutoff) {
						report.InferredInactive++
					}
				}
			default:
				report.Declared++
			}

			if isDeclaredPolicy(entry.Policy) {
				report.DeclaredPolicy++
			} else {
				report.UndeclaredPolicy++
			}

			if isLegacyNested(entry.Namespace) {
				report.LegacyNested++
			}

			if isSuspicious(entry.Namespace) {
				report.Suspicious++
			}
		}

		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	return report, nil
}

func (c *CLI) runNamespaceAudit(args []string) int {
	fs := flag.NewFlagSet("namespace audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "", "output format: json|table")
	output := fs.String("output", "", "output format: json|table (alias for -format)")
	if code, done := c.parseFlags(fs, args); done {
		return code
	}

	fmtMode := "json"
	if *format != "" {
		fmtMode = strings.ToLower(strings.TrimSpace(*format))
	} else if *output != "" {
		fmtMode = strings.ToLower(strings.TrimSpace(*output))
	}

	if fmtMode != "json" && fmtMode != "table" {
		return c.fail("format must be json|table")
	}

	report, err := c.ComputeNamespaceAudit(context.Background())
	if err != nil {
		return c.fail(err.Error())
	}

	if fmtMode == "json" {
		return c.writeJSON(report)
	}

	w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "METRIC\tCOUNT")
	_, _ = fmt.Fprintf(w, "total_namespaces\t%d\n", report.TotalNamespaces)
	_, _ = fmt.Fprintf(w, "declared\t%d\n", report.Declared)
	_, _ = fmt.Fprintf(w, "seeded\t%d\n", report.Seeded)
	_, _ = fmt.Fprintf(w, "inferred\t%d\n", report.Inferred)
	_, _ = fmt.Fprintf(w, "inferred_inactive\t%d\n", report.InferredInactive)
	_, _ = fmt.Fprintf(w, "undeclared_policy\t%d\n", report.UndeclaredPolicy)
	_, _ = fmt.Fprintf(w, "declared_policy\t%d\n", report.DeclaredPolicy)
	_, _ = fmt.Fprintf(w, "legacy_nested\t%d\n", report.LegacyNested)
	_, _ = fmt.Fprintf(w, "suspicious\t%d\n", report.Suspicious)
	_ = w.Flush()
	return 0
}

func parseAuditTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}

func isDeclaredPolicy(policy map[string]any) bool {
	if policy == nil {
		return false
	}
	src, _ := policy["source"].(string)
	if src == "inferred" || src == "inferred-backfill" {
		return false
	}
	return len(policy) > 0
}

func isLegacyNested(ns string) bool {
	parts := strings.Split(strings.TrimSpace(ns), "/")
	return len(parts) >= 4 && parts[0] == "user" && (parts[2] == "project" || parts[2] == "session")
}

func isSuspicious(ns string) bool {
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return true
	}
	if ns == "system" {
		return false
	}
	parts := strings.Split(ns, "/")
	if len(parts) < 2 {
		return true
	}
	for _, p := range parts {
		if p == "" {
			return true
		}
	}
	switch parts[0] {
	case "user", "project", "app", "org", "session":
		return false
	case "system":
		return false
	default:
		return true
	}
}

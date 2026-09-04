package memory

import (
	"context"
	"strings"
	"testing"
)

func TestCurrentDeprecatedPlanUsesSupersedesIndex(t *testing.T) {
	store, cleanup := newBM25TestStore(t)
	defer cleanup()

	in := RecallInput{Filters: RecallFilters{Statuses: []Status{StatusDeprecated}}}
	query := `EXPLAIN QUERY PLAN
SELECT r.revision_id
FROM memory_revisions r
` + currentRevisionJoin(in) + `
WHERE r.status = ?`
	rows, err := store.DB().QueryContext(context.Background(), query, string(StatusDeprecated))
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "idx_memory_revisions_supersedes") {
		t.Fatalf("terminal anti-lookup does not use supersedes index:\n%s", joined)
	}
}

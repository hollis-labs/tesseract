package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestFetchBM25Candidates_MatchesMemoryKey is the regression test for
// CW-20260903-0062. Before memory_key joined memory_revisions_fts, an exact
// key used as a lexical query matched every record that CITED the key and
// never the record that owned it — and returned an empty page when nobody
// cited it, which reads as "no such record".
//
// The fixture reproduces the reported shape rather than a reduction of it:
// one record whose key IS the query, one whose body merely mentions it the way
// this corpus cites keys, in [[wikilink]] form.
func TestFetchBM25Candidates_MatchesMemoryKey(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	const key = "migrate_mcp_keys_to_1password"

	if _, err := ms.WriteRevision(ctx, bm25SampleInput(key,
		"Move plaintext API keys in MCP catalog configs to a 1Password-backed mechanism.",
		"The interim state leaves a literal token in the catalog file.")); err != nil {
		t.Fatalf("write owner: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, bm25SampleInput("cerberus_secret_kind",
		"Pair the secret kind with a resolver.",
		"Depends on [[migrate_mcp_keys_to_1password]] landing first.")); err != nil {
		t.Fatalf("write citer: %v", err)
	}

	in := RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Query:      key,
		Ranking:    RankingRelevance,
		SearchMode: SearchModeLexical,
		Filters:    RecallFilters{Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical}},
	}
	revs, err := ms.fetchBM25Candidates(ctx, in, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(revs) == 0 {
		t.Fatalf("exact-key lexical search returned nothing; the key's own record is not indexed")
	}

	var keys []string
	for _, r := range revs {
		keys = append(keys, r.MemoryKey)
	}
	if revs[0].MemoryKey != key {
		t.Errorf("exact-key search ranked %q first, want the key's own record %q; got order %v",
			revs[0].MemoryKey, key, keys)
	}
	// The citing record must still be found — the fix adds a field to the
	// index, it does not narrow what lexical matches.
	if len(revs) != 2 {
		t.Errorf("expected both the owner and the citing record, got %v", keys)
	}
}

// TestBM25MemoryKeyOutranksBodyMention pins the ranking DECISION, not just the
// presence of the field. Without the memory_key column weight both records
// match and the ordering between them is left to prose length, which is what
// put citations above referents.
func TestBM25MemoryKeyOutranksBodyMention(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	const key = "cairn_open_spec_gaps"

	// The citing record is deliberately the stronger prose match: it repeats
	// the key's tokens several times in a short body, which is the shape that
	// beats a bare key hit at weight 1.0.
	mention := strings.Repeat("cairn open spec gaps are tracked. ", 6)
	if _, err := ms.WriteRevision(ctx, bm25SampleInput("local_agent_pilot_next_steps",
		"Next steps for the local agent pilot.", mention)); err != nil {
		t.Fatalf("write citer: %v", err)
	}
	if _, err := ms.WriteRevision(ctx, bm25SampleInput(key,
		"Gaps still open in the spec.",
		"Enumerated during the review pass.")); err != nil {
		t.Fatalf("write owner: %v", err)
	}

	revs, err := ms.fetchBM25Candidates(ctx, RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Query:      key,
		Ranking:    RankingRelevance,
		SearchMode: SearchModeLexical,
		Filters:    RecallFilters{Statuses: []Status{StatusDraft, StatusReviewed, StatusCanonical}},
	}, 10)
	if err != nil {
		t.Fatalf("fetchBM25Candidates: %v", err)
	}
	if len(revs) < 2 {
		t.Fatalf("expected both records to match, got %d", len(revs))
	}
	if revs[0].MemoryKey != key {
		t.Errorf("a record that merely mentions the key outranked the key's owner: got %q first, want %q",
			revs[0].MemoryKey, key)
	}
}

// TestBM25ColumnWeightsMatchIndexOrder binds the Go weight vector to the SQL
// column declaration. bm25() weights are POSITIONAL, so a future migration
// that inserts a column ahead of memory_key would silently boost that column
// instead — with no error, no test failure anywhere else, and a ranking that
// is simply wrong. This is the only thing standing between those two facts.
func TestBM25ColumnWeightsMatchIndexOrder(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	var ddl string
	if err := ms.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='memory_revisions_fts'`,
	).Scan(&ddl); err != nil {
		t.Fatalf("read memory_revisions_fts DDL: %v", err)
	}

	// Column order as SQLite actually stores it, read back off the table
	// rather than off the DDL text.
	rows, err := ms.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('memory_revisions_fts')`)
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(got) != len(bm25IndexColumns) {
		t.Fatalf("index has %d columns %v, bm25IndexColumns has %d %v",
			len(got), got, len(bm25IndexColumns), bm25IndexColumns)
	}
	for i := range got {
		if got[i] != bm25IndexColumns[i] {
			t.Fatalf("column %d is %q in the index but %q in bm25IndexColumns; "+
				"bm25() weights are positional, so the boost is now on the wrong column\nDDL: %s",
				i, got[i], bm25IndexColumns[i], ddl)
		}
	}

	// And the expression names exactly one non-default weight, on memory_key.
	want := fmt.Sprintf("bm25(memory_revisions_fts, %g, 1, 1, 1)", bm25MemoryKeyWeight)
	if bm25RankExpr != want {
		t.Errorf("bm25RankExpr = %q, want %q", bm25RankExpr, want)
	}

	// The expression has to survive SQLite's parser, not just string equality.
	if _, err := ms.db.ExecContext(ctx,
		`SELECT `+bm25RankExpr+` FROM memory_revisions_fts WHERE memory_revisions_fts MATCH 'x' LIMIT 1`,
	); err != nil {
		t.Fatalf("SQLite rejected %s: %v", bm25RankExpr, err)
	}
}

// TestFTSIndexFollowsMemoryKeyRename covers the half of "memory_key is
// indexed" that is not about the column list. ApplyMigration rewrites
// namespace/memory_key/tags in place, so without an AFTER UPDATE trigger the
// index would keep answering for the OLD key — a stale entry, which is worse
// than the missing one this ticket started from, because it answers.
func TestFTSIndexFollowsMemoryKeyRename(t *testing.T) {
	ms, cleanup := newBM25TestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, bm25SampleInput("old_key_name",
		"A record about nothing in particular.", "Body text."))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := ms.db.ExecContext(ctx,
		`UPDATE memory_revisions SET memory_key = ? WHERE revision_id = ?`,
		"new_key_name", rev.RevisionID,
	); err != nil {
		t.Fatalf("rename: %v", err)
	}

	search := func(q string) int {
		revs, err := ms.fetchBM25Candidates(ctx, RecallInput{
			Namespaces: []string{"user/chrispian/memory/notes"},
			Query:      q,
			Ranking:    RankingRelevance,
			SearchMode: SearchModeLexical,
		}, 10)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		return len(revs)
	}

	if n := search("new_key_name"); n != 1 {
		t.Errorf("after rename, searching the NEW key found %d records, want 1", n)
	}
	if n := search("old_key_name"); n != 0 {
		t.Errorf("after rename, searching the OLD key still found %d records, want 0 (stale index entry)", n)
	}
}

package contextstore

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// The two halves of operator gate G2, bound together.
//
// [[tesseract_registry_index_ddl_constrained]] answered "constrain": a type may
// DECLARE hot fields in configuration, and the registry loader must never emit
// DDL. Indexes materialize through the ordered migration list in this package,
// where a human reviews the statement before it runs. Seer — which invented the
// hot_fields mechanism — declared hot fields in its registry and still
// materialized them by hand-written migration; that precedent is the whole
// argument.
//
// typeregistry's own no_ddl_test.go proves the first half: the package imports
// no database and spells no schema statement. This file proves the second, and
// it is the half nobody thinks to write — a constrained registry with a
// migration that forgot two of the four declared fields still passes every test
// on the registry side, and the declaration quietly becomes a performance claim
// about queries nobody indexed.
//
// The seam between them is deliberately manual. There is no code path from a
// declaration to a statement here, and there must not be: two config files
// producing two schemas from one binary is exactly what the gate refused.

// TestDeclaredHotFieldsAreMaterializedByMigration asserts every hot field the
// SHIPPED registry declares has a matching index in a migrated store.
//
// Shipped defaults, not an operator's types.yaml. An operator who declares a
// fifth hot field gets no index until a migration ships, and that is not a bug
// to fix here — it is G2's accepted trade-off stated as behavior: "adding a
// type is free; indexing one still costs a release."
func TestDeclaredHotFieldsAreMaterializedByMigration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	indexed := indexedStateFields(t, store)

	reg := typeregistry.NewRegistry()
	declared := map[string][]string{}
	for _, vocab := range reg.VocabularyIDs() {
		for _, ty := range reg.Types(vocab) {
			for _, f := range ty.HotFields {
				declared[f] = append(declared[f], vocab+"/"+ty.TypeID)
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("no type declares a hot field; this guard is not testing anything. " +
			"If the last structured type was removed, remove its indexes from migration 19 too.")
	}

	for field, byType := range declared {
		if _, ok := indexed[field]; !ok {
			t.Errorf("hot field %q is declared by %s and has no index in a migrated store.\n"+
				"A declaration is what tells an operator which fields carry an index, so an "+
				"unmaterialized one is a performance claim about a query nobody indexed.\n"+
				"Add the statement to migration %d in store.go — by hand, reviewed. The registry "+
				"must never emit it ([[tesseract_registry_index_ddl_constrained]], gate G2).",
				field, strings.Join(byType, ", "), schemaVersion)
		}
	}

	// And the reverse: an index over a field nothing declares is an index
	// nobody is told about, which costs write throughput to serve a query no
	// caller knows is fast.
	for field := range indexed {
		if _, ok := declared[field]; !ok {
			t.Errorf("consumer_state field %q is indexed by a migration and declared by no type.\n"+
				"Either declare it as a hot field on the type that needs it, or drop the index — "+
				"an undeclared index is write cost paid for a promise nobody made.", field)
		}
	}
}

// TestHotFieldIndexesArePartialOnTheStructuredRows pins the predicate the
// indexes are built on.
//
// Partial on `consumer_state IS NOT NULL` keeps each index proportional to the
// structured objects rather than to the whole corpus: every revision written
// before migration 19 carries NULL and none of them can ever match a state
// filter. It is also load-bearing on the READ side —
// memory.buildStateFilterClauses emits the same predicate ahead of every
// json_extract comparison, and without a predicate SQLite can prove implies the
// index's own, it declines the partial index and scans.
func TestHotFieldIndexesArePartialOnTheStructuredRows(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	rows, err := store.DB().QueryContext(ctx,
		`SELECT name, sql FROM sqlite_master
		 WHERE type = 'index' AND name LIKE 'idx_memory_revisions_state_%'`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := 0
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found++
		lower := strings.ToLower(ddl)
		if !strings.Contains(lower, "consumer_state is not null") {
			t.Errorf("index %s is not partial on `consumer_state IS NOT NULL`:\n  %s\n"+
				"A full index over memory_revisions pays for every prose revision in the corpus to "+
				"serve a filter none of them can match.", name, ddl)
		}
		if !strings.Contains(lower, "json_extract") {
			t.Errorf("index %s does not index a json_extract expression:\n  %s", name, ddl)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if found == 0 {
		t.Fatal("no consumer_state indexes exist in a migrated store")
	}
}

// TestStateFilterQueryUsesTheHotFieldIndex is the end-to-end claim: a filter on
// a declared hot field is served by an index rather than by a scan.
//
// Without it, "declared hot fields are indexed and filterable" is two facts
// that have never been checked against each other. The query text here mirrors
// what memory.buildStateFilterClauses emits — including the IS NOT NULL
// conjunct, which is the part that makes the partial index usable and would
// otherwise look like removable noise to whoever tidies it next.
func TestStateFilterQueryUsesTheHotFieldIndex(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	var plan strings.Builder
	rows, err := store.DB().QueryContext(ctx,
		`EXPLAIN QUERY PLAN
		 SELECT revision_id FROM memory_revisions r
		 WHERE r.consumer_state IS NOT NULL
		   AND json_extract(r.consumer_state, '$.section') IN (?)`, "now")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if !strings.Contains(plan.String(), "idx_memory_revisions_state_section") {
		t.Errorf("a filter on the declared hot field `section` does not use its index.\nPlan:\n%s\n"+
			"Either the index predicate and the query predicate have drifted apart, or the query "+
			"lost the `consumer_state IS NOT NULL` conjunct — SQLite will not use a partial index "+
			"without a predicate it can prove implies the index's own.", plan.String())
	}
}

// indexedStateFields returns the consumer_state field name each hot-field index
// covers, read back out of the schema the migration actually produced rather
// than out of the list it was written from.
func indexedStateFields(t *testing.T, store *Store) map[string]struct{} {
	t.Helper()
	rows, err := store.DB().QueryContext(context.Background(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND sql LIKE '%consumer_state%'`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]struct{}{}
	for rows.Next() {
		var ddl string
		if err := rows.Scan(&ddl); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if f := fieldFromJSONPath(ddl); f != "" {
			out[f] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	t.Logf("indexed consumer_state fields: %v", names)
	return out
}

// fieldFromJSONPath pulls `x` out of the first `'$.x'` in a statement.
func fieldFromJSONPath(ddl string) string {
	const marker = "'$."
	i := strings.Index(ddl, marker)
	if i < 0 {
		return ""
	}
	rest := ddl[i+len(marker):]
	j := strings.Index(rest, "'")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

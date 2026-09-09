package contextstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// seedNamespaces registers entries as (namespace, owner_type, owner_id,
// updated_at) tuples. updated_at is written directly so ordering tests can
// control ties without sleeping.
func seedNamespaces(t *testing.T, s *Store, rows [][4]string) {
	t.Helper()
	ctx := context.Background()
	for _, row := range rows {
		if err := s.UpsertNamespacePolicy(ctx, NamespacePolicyEntry{
			Namespace: row[0],
			OwnerType: row[1],
			OwnerID:   row[2],
		}); err != nil {
			t.Fatalf("upsert %s: %v", row[0], err)
		}
		if row[3] != "" {
			if _, err := s.db.ExecContext(ctx,
				`UPDATE namespace_policies SET updated_at = ? WHERE namespace = ?`, row[3], row[0]); err != nil {
				t.Fatalf("set updated_at for %s: %v", row[0], err)
			}
		}
	}
}

func namespacesOf(page NamespacePage) []string {
	out := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		out = append(out, item.Namespace)
	}
	return out
}

func TestNamespaceQueryMatchModes(t *testing.T) {
	s := newTestStore(t)
	seedNamespaces(t, s, [][4]string{
		{"user/alice/memory/notes", "user", "alice", ""},
		{"user/alice/knowledge/docs", "user", "alice", ""},
		{"user/bob/memory/notes", "user", "bob", ""},
		{"app/indexer/state", "app", "indexer", ""},
	})

	cases := []struct {
		name string
		q    NamespaceQuery
		want []string
	}{
		{
			name: "prefix is the default mode",
			q:    NamespaceQuery{Match: "user/alice/"},
			want: []string{"user/alice/knowledge/docs", "user/alice/memory/notes"},
		},
		{
			name: "contains matches mid-string",
			q:    NamespaceQuery{Match: "memory", MatchMode: NamespaceMatchContains},
			want: []string{"user/alice/memory/notes", "user/bob/memory/notes"},
		},
		{
			name: "glob honors wildcards",
			q:    NamespaceQuery{Match: "user/*/memory/*", MatchMode: NamespaceMatchGlob},
			want: []string{"user/alice/memory/notes", "user/bob/memory/notes"},
		},
		{
			name: "prefix does not read a glob",
			q:    NamespaceQuery{Match: "user/*", MatchMode: NamespaceMatchPrefix},
			want: nil,
		},
		{
			name: "owner_type filter",
			q:    NamespaceQuery{OwnerType: "app"},
			want: []string{"app/indexer/state"},
		},
		{
			name: "owner_id filter",
			q:    NamespaceQuery{OwnerID: "bob"},
			want: []string{"user/bob/memory/notes"},
		},
		{
			name: "filters combine",
			q:    NamespaceQuery{Match: "user/", OwnerID: "alice", MatchMode: NamespaceMatchPrefix},
			want: []string{"user/alice/knowledge/docs", "user/alice/memory/notes"},
		},
		{
			name: "empty match matches everything",
			q:    NamespaceQuery{MatchMode: NamespaceMatchGlob},
			want: []string{"app/indexer/state", "user/alice/knowledge/docs", "user/alice/memory/notes", "user/bob/memory/notes"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.ListNamespacePolicyPage(context.Background(), tc.q)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			got := namespacesOf(page)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// A namespace is a caller-supplied literal, and _ and % are ordinary
// characters in one. Reaching SQL as LIKE wildcards would quietly widen the
// filter — "a_b" would also match "axb".
func TestNamespaceQueryEscapesLikeMetacharacters(t *testing.T) {
	s := newTestStore(t)
	seedNamespaces(t, s, [][4]string{
		{"user/a_b/memory", "user", "a", ""},
		{"user/axb/memory", "user", "a", ""},
		{"user/100%/memory", "user", "a", ""},
		{"user/100x/memory", "user", "a", ""},
	})

	for _, tc := range []struct {
		match string
		mode  NamespaceMatchMode
		want  []string
	}{
		{match: "user/a_b", mode: NamespaceMatchPrefix, want: []string{"user/a_b/memory"}},
		{match: "user/100%", mode: NamespaceMatchPrefix, want: []string{"user/100%/memory"}},
		{match: "a_b", mode: NamespaceMatchContains, want: []string{"user/a_b/memory"}},
	} {
		page, err := s.ListNamespacePolicyPage(context.Background(),
			NamespaceQuery{Match: tc.match, MatchMode: tc.mode})
		if err != nil {
			t.Fatalf("list %q: %v", tc.match, err)
		}
		got := namespacesOf(page)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("match %q mode %q: got %v, want %v", tc.match, tc.mode, got, tc.want)
		}
	}
}

func TestNamespaceQuerySorting(t *testing.T) {
	s := newTestStore(t)
	seedNamespaces(t, s, [][4]string{
		{"ns/c", "user", "zoe", "2026-01-03T00:00:00Z"},
		{"ns/a", "app", "indexer", "2026-01-01T00:00:00Z"},
		{"ns/b", "user", "adam", "2026-01-02T00:00:00Z"},
	})

	for _, tc := range []struct {
		name string
		q    NamespaceQuery
		want []string
	}{
		{"namespace asc is the default", NamespaceQuery{}, []string{"ns/a", "ns/b", "ns/c"}},
		{"namespace desc", NamespaceQuery{Desc: true}, []string{"ns/c", "ns/b", "ns/a"}},
		{"owner asc", NamespaceQuery{Sort: NamespaceSortOwner}, []string{"ns/a", "ns/b", "ns/c"}},
		{"owner desc", NamespaceQuery{Sort: NamespaceSortOwner, Desc: true}, []string{"ns/c", "ns/b", "ns/a"}},
		{"updated_at asc", NamespaceQuery{Sort: NamespaceSortUpdatedAt}, []string{"ns/a", "ns/b", "ns/c"}},
		{"updated_at desc", NamespaceQuery{Sort: NamespaceSortUpdatedAt, Desc: true}, []string{"ns/c", "ns/b", "ns/a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.ListNamespacePolicyPage(context.Background(), tc.q)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			got := namespacesOf(page)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The bug this whole change exists to remove: a caller that pages must be able
// to reach every row. 125 rows over a limit of 10 is the shape of the real
// case (1125 namespaces against a cap of 1000) with the numbers made small.
func TestNamespaceQueryCursorWalksCompleteSet(t *testing.T) {
	s := newTestStore(t)
	const total = 125
	rows := make([][4]string, 0, total)
	for i := 0; i < total; i++ {
		// Ties on updated_at across every row force the namespace tiebreak to
		// carry the ordering; without it a keyset cursor loses or repeats rows.
		rows = append(rows, [4]string{fmt.Sprintf("ns/%04d", i), "user", "chrispian", "2026-01-01T00:00:00Z"})
	}
	seedNamespaces(t, s, rows)

	for _, sort := range []NamespaceSortField{NamespaceSortNamespace, NamespaceSortOwner, NamespaceSortUpdatedAt} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("sort=%s desc=%t", sort, desc), func(t *testing.T) {
				seen := make([]string, 0, total)
				dupes := map[string]bool{}
				cursor := ""
				for pages := 0; ; pages++ {
					if pages > total {
						t.Fatalf("paging did not terminate after %d pages", pages)
					}
					page, err := s.ListNamespacePolicyPage(context.Background(), NamespaceQuery{
						Sort: sort, Desc: desc, Limit: 10, Cursor: cursor,
					})
					if err != nil {
						t.Fatalf("page %d: %v", pages, err)
					}
					if page.Total != total {
						t.Fatalf("page %d: Total = %d, want %d (Total is the whole match, not the page)", pages, page.Total, total)
					}
					for _, item := range page.Items {
						if dupes[item.Namespace] {
							t.Fatalf("namespace %s returned twice", item.Namespace)
						}
						dupes[item.Namespace] = true
						seen = append(seen, item.Namespace)
					}
					if page.NextCursor == "" {
						break
					}
					cursor = page.NextCursor
				}
				if len(seen) != total {
					t.Fatalf("paged to %d namespaces, want %d", len(seen), total)
				}
			})
		}
	}
}

// A cursor encodes the ordering it was issued under. Resuming it into a
// different one would return a page with holes in it, so it errors instead.
func TestNamespaceQueryCursorIsBoundToOrdering(t *testing.T) {
	s := newTestStore(t)
	seedNamespaces(t, s, [][4]string{
		{"ns/a", "user", "a", "2026-01-01T00:00:00Z"},
		{"ns/b", "user", "b", "2026-01-02T00:00:00Z"},
		{"ns/c", "user", "c", "2026-01-03T00:00:00Z"},
	})

	first, err := s.ListNamespacePolicyPage(context.Background(), NamespaceQuery{Limit: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("expected a next cursor")
	}

	for _, tc := range []struct {
		name string
		q    NamespaceQuery
	}{
		{"different sort", NamespaceQuery{Sort: NamespaceSortUpdatedAt, Limit: 1, Cursor: first.NextCursor}},
		{"different direction", NamespaceQuery{Desc: true, Limit: 1, Cursor: first.NextCursor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.ListNamespacePolicyPage(context.Background(), tc.q); err == nil {
				t.Fatal("expected an error resuming a cursor under a different ordering")
			}
		})
	}

	if _, err := s.ListNamespacePolicyPage(context.Background(), NamespaceQuery{Limit: 1, Cursor: "not-a-cursor"}); err == nil {
		t.Fatal("expected an error for a malformed cursor")
	}
}

// Total counts the filtered set, not the paged one — a filter must narrow it
// and a cursor must not.
func TestNamespaceQueryTotalIgnoresPagingButHonorsFilters(t *testing.T) {
	s := newTestStore(t)
	seedNamespaces(t, s, [][4]string{
		{"user/a/one", "user", "a", ""},
		{"user/a/two", "user", "a", ""},
		{"user/b/one", "user", "b", ""},
		{"app/x/one", "app", "x", ""},
	})

	page, err := s.ListNamespacePolicyPage(context.Background(), NamespaceQuery{Match: "user/", Limit: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("Total = %d, want 3 (the filtered set, not the page and not the table)", page.Total)
	}
	if len(page.Items) != 1 {
		t.Fatalf("page size = %d, want 1", len(page.Items))
	}
	if page.NextCursor == "" {
		t.Fatal("expected a next cursor with 3 matches and a limit of 1")
	}

	second, err := s.ListNamespacePolicyPage(context.Background(),
		NamespaceQuery{Match: "user/", Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if second.Total != 3 {
		t.Fatalf("Total on page 2 = %d, want 3", second.Total)
	}
}

// Every caller-argument failure must be distinguishable from a database
// failure, because the transports answer 400/validation_error for one and
// 500/internal_error for the other. A bare errors.New here would read as a
// store fault and surface as a 500.
func TestNamespaceQueryRejectsUnknownKnobs(t *testing.T) {
	s := newTestStore(t)
	// More than one row, so the first page actually issues a cursor to reuse.
	seedNamespaces(t, s, [][4]string{
		{"ns/a", "user", "a", ""},
		{"ns/b", "user", "b", ""},
	})

	first, err := s.ListNamespacePolicyPage(context.Background(), NamespaceQuery{Limit: 1})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("expected a cursor from the first page")
	}

	for _, tc := range []struct {
		name string
		q    NamespaceQuery
	}{
		{"unknown sort", NamespaceQuery{Sort: "owner_id"}},
		{"unknown match mode", NamespaceQuery{Match: "ns/", MatchMode: "regex"}},
		// A bad mode is reported even with no pattern to bind to, so a typo
		// surfaces on the first call rather than the first non-empty one.
		{"unknown match mode, empty match", NamespaceQuery{MatchMode: "regex"}},
		{"malformed cursor", NamespaceQuery{Limit: 1, Cursor: "not-a-cursor"}},
		{"cursor under a different ordering", NamespaceQuery{Sort: NamespaceSortOwner, Limit: 1, Cursor: first.NextCursor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.ListNamespacePolicyPage(context.Background(), tc.q)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, ErrNamespaceQuery) {
				t.Fatalf("error %v does not wrap ErrNamespaceQuery, so a transport would report it as a store fault", err)
			}
		})
	}
}

// The unfiltered helper is the same code path with no options, and the three
// internal callers that reload policy still depend on it returning everything.
func TestListNamespacePoliciesReturnsEverythingUnpaged(t *testing.T) {
	s := newTestStore(t)
	rows := make([][4]string, 0, 50)
	for i := 0; i < 50; i++ {
		rows = append(rows, [4]string{fmt.Sprintf("ns/%03d", i), "user", "c", ""})
	}
	seedNamespaces(t, s, rows)

	all, err := s.ListNamespacePolicies(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 50 {
		t.Fatalf("got %d namespaces, want 50", len(all))
	}
	if all[0].Namespace != "ns/000" || all[49].Namespace != "ns/049" {
		t.Fatalf("expected namespace-ascending order, got %s..%s", all[0].Namespace, all[49].Namespace)
	}
}

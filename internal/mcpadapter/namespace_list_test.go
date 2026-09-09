package mcpadapter

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func seedNamespacesForList(t *testing.T, s *contextstore.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
			Namespace: fmt.Sprintf("user/chrispian/ns%03d", i),
			OwnerType: "user",
			OwnerID:   "chrispian",
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

// budget.Apply infers `total` and `truncated` from the slice it is handed.
// Now that the store returns one page rather than the whole match, letting it
// infer them would report every page as the complete set — the exact silent
// completeness bug this change removes, reintroduced one layer up.
func TestNamespacesList_TotalAndTruncatedDescribeTheWholeMatch(t *testing.T) {
	s := newTestStore(t)
	seedNamespacesForList(t, s, 30)
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{
		"kind":  "namespaces",
		"limit": float64(10),
	}))

	if got := body["count"]; got != float64(10) {
		t.Errorf("count = %v, want 10 (items in this page)", got)
	}
	if got := body["total"]; got != float64(30) {
		t.Errorf("total = %v, want 30 (namespaces matching in all)", got)
	}
	if got := body["truncated"]; got != true {
		t.Errorf("truncated = %v, want true", got)
	}
	if body["next_cursor"] == nil || body["next_cursor"] == "" {
		t.Errorf("next_cursor is %v; a truncated page must say how to get the rest", body["next_cursor"])
	}
	if body["hint"] == nil || body["hint"] == "" {
		t.Error("a truncated page should carry a hint naming the cursor")
	}
}

// The last page reports itself as the last page, so a caller has a termination
// condition that does not depend on counting.
func TestNamespacesList_CursorPagesToCompletion(t *testing.T) {
	s := newTestStore(t)
	const total = 30
	seedNamespacesForList(t, s, total)
	a := New(s, "")

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
		args := map[string]any{"kind": "namespaces", "limit": float64(10)}
		if cursor != "" {
			args["cursor"] = cursor
		}
		body := wantNoError(t, mustCall(t, a.handleRegistryList, args))

		if got := body["total"]; got != float64(total) {
			t.Fatalf("page %d: total = %v, want %d", pages, got, total)
		}
		items, ok := body["items"].([]any)
		if !ok {
			t.Fatalf("page %d: items is %T, want a list", pages, body["items"])
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("page %d: item is %T", pages, raw)
			}
			ns, _ := item["namespace"].(string)
			if seen[ns] {
				t.Fatalf("namespace %s returned on more than one page", ns)
			}
			seen[ns] = true
		}

		next, _ := body["next_cursor"].(string)
		truncated, _ := body["truncated"].(bool)
		if truncated != (next != "") {
			t.Fatalf("page %d: truncated=%t but next_cursor=%q — the two must agree", pages, truncated, next)
		}
		if next == "" {
			break
		}
		cursor = next
	}

	if len(seen) != total {
		t.Fatalf("paged to %d namespaces, want %d", len(seen), total)
	}
}

func TestNamespacesList_FiltersAndSorts(t *testing.T) {
	s := newTestStore(t)
	for _, row := range [][3]string{
		{"user/alice/memory/notes", "user", "alice"},
		{"user/bob/memory/notes", "user", "bob"},
		{"app/indexer/state", "app", "indexer"},
	} {
		if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
			Namespace: row[0], OwnerType: row[1], OwnerID: row[2],
		}); err != nil {
			t.Fatalf("seed %s: %v", row[0], err)
		}
	}
	a := New(s, "")

	namespacesIn := func(body map[string]any) []string {
		items, _ := body["items"].([]any)
		out := make([]string, 0, len(items))
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			ns, _ := item["namespace"].(string)
			out = append(out, ns)
		}
		return out
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"prefix", map[string]any{"kind": "namespaces", "prefix": "user/"},
			[]string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"contains", map[string]any{"kind": "namespaces", "match": "memory", "match_mode": "contains"},
			[]string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"glob", map[string]any{"kind": "namespaces", "match": "user/*/memory/*", "match_mode": "glob"},
			[]string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"owner_type", map[string]any{"kind": "namespaces", "owner_type": "app"},
			[]string{"app/indexer/state"}},
		{"owner_id", map[string]any{"kind": "namespaces", "owner_id": "bob"},
			[]string{"user/bob/memory/notes"}},
		{"desc", map[string]any{"kind": "namespaces", "dir": "desc"},
			[]string{"user/bob/memory/notes", "user/alice/memory/notes", "app/indexer/state"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := wantNoError(t, mustCall(t, a.handleRegistryList, tc.args))
			got := namespacesIn(body)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}

	// A knob outside the stated set is a validation_error, not a silent
	// fallback to the default.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"unknown match_mode", map[string]any{"kind": "namespaces", "match": "user/", "match_mode": "regex"}},
		{"unknown sort", map[string]any{"kind": "namespaces", "sort": "owner_id"}},
		{"unknown dir", map[string]any{"kind": "namespaces", "dir": "sideways"}},
		{"malformed cursor", map[string]any{"kind": "namespaces", "cursor": "not-a-cursor"}},
		{"prefix and match together", map[string]any{"kind": "namespaces", "prefix": "user/", "match": "alice"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantErrorCode(t, mustCall(t, a.handleRegistryList, tc.args), "validation_error")
		})
	}
}

// Every list-shaping knob must be refused by the arms that do not return a
// list. Accepting one and dropping it is the failure the merged tool exists to
// remove, and a knob added to the schema is easy to forget here.
func TestNamespacesList_ListKnobsRefusedOnNonListArms(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
		Namespace: "app/known/ns", OwnerType: "app", OwnerID: "test",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := New(s, "")

	sample := map[string]any{
		"prefix": "app/", "match": "app", "match_mode": "contains",
		"owner_type": "app", "owner_id": "test", "sort": "namespace",
		"dir": "desc", "limit": float64(5), "cursor": "abc",
	}

	for _, knob := range namespaceListKnobs {
		value, ok := sample[knob]
		if !ok {
			t.Fatalf("no sample value for knob %q — add one so this guard actually exercises it", knob)
		}
		for _, arm := range []struct {
			name string
			args map[string]any
		}{
			{"kind=types", map[string]any{"kind": "types", knob: value}},
			{"kind=views", map[string]any{"kind": "views", knob: value}},
			{"with name", map[string]any{"kind": "namespaces", "name": "app/known/ns", knob: value}},
		} {
			t.Run(knob+" under "+arm.name, func(t *testing.T) {
				body := mustCall(t, a.handleRegistryList, arm.args)
				wantErrorCode(t, body, "validation_error")
				wantMessageNames(t, body, knob)
			})
		}
	}

	// Positive controls: the same arms minus the knob succeed, so the
	// rejections above are the knob and not the arm.
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "types"}))
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "views"}))
	wantNoError(t, mustCall(t, a.handleRegistryList, map[string]any{"kind": "namespaces", "name": "app/known/ns"}))
}

// namespaceListKnobs is hand-maintained, and a knob declared in the schema but
// missing from it would be accepted on every arm and silently dropped by two
// of them. Read the registered schema rather than trusting the two to agree.
func TestNamespaceListKnobsCoversTheRegisteredSchema(t *testing.T) {
	s := newTestStore(t)
	a := New(s, "")
	srv := server.NewMCPServer("knob-drift-test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	tools := srv.ListTools()
	tool, ok := tools["context_registry_list"]
	if !ok {
		t.Fatal("context_registry_list is not registered")
	}

	// `kind` selects the arm and `name` selects the single-namespace shape;
	// every other declared argument shapes the list.
	structural := map[string]bool{"kind": true, "name": true}
	var declared []string
	for prop := range tool.Tool.InputSchema.Properties {
		if !structural[prop] {
			declared = append(declared, prop)
		}
	}
	sort.Strings(declared)

	known := map[string]bool{}
	for _, knob := range namespaceListKnobs {
		known[knob] = true
	}
	for _, prop := range declared {
		if !known[prop] {
			t.Errorf("context_registry_list declares %q but namespaceListKnobs does not list it — "+
				"the kind=types/views and name= arms would accept it and silently drop it", prop)
		}
	}
	for _, knob := range namespaceListKnobs {
		found := false
		for _, prop := range declared {
			if prop == knob {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("namespaceListKnobs lists %q but context_registry_list does not declare it", knob)
		}
	}
}

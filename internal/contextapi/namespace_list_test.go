package contextapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

type namespaceListResponse struct {
	Items []struct {
		Namespace string `json:"namespace"`
		OwnerType string `json:"owner_type"`
		OwnerID   string `json:"owner_id"`
	} `json:"items"`
	Count      int    `json:"count"`
	Truncated  bool   `json:"truncated"`
	NextCursor string `json:"next_cursor"`
}

func registerNamespace(t *testing.T, srv *Server, ns, ownerType, ownerID string) {
	t.Helper()
	res := performJSON(t, srv, http.MethodPost, "/v1/namespaces/register", map[string]any{
		"namespace":  ns,
		"owner_type": ownerType,
		"owner_id":   ownerID,
		"policy":     map[string]any{},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("register %s: status=%d body=%s", ns, res.Code, res.Body.String())
	}
}

func getNamespaceList(t *testing.T, srv *Server, query string) namespaceListResponse {
	t.Helper()
	res := performJSON(t, srv, http.MethodGet, "/v1/namespaces/list?"+query, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list %q: status=%d body=%s", query, res.Code, res.Body.String())
	}
	var out namespaceListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", query, err)
	}
	return out
}

// The defect: a caller that asks for a page and gets fewer rows than exist has
// no way to reach the rest. Paging until next_cursor is empty must yield every
// namespace exactly once, and count must report the whole set throughout.
func TestNamespacesListPagesToCompletion(t *testing.T) {
	srv := newTestServer(t)
	const total = 25
	for i := 0; i < total; i++ {
		registerNamespace(t, srv, fmt.Sprintf("user/chrispian/ns%03d", i), "user", "chrispian")
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
		q := url.Values{"limit": {"10"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp := getNamespaceList(t, srv, q.Encode())

		if resp.Count != total {
			t.Fatalf("page %d: count=%d, want %d (count is the whole match, not the page)", pages, resp.Count, total)
		}
		if resp.Truncated != (resp.NextCursor != "") {
			t.Fatalf("page %d: truncated=%t but next_cursor=%q — the two must agree", pages, resp.Truncated, resp.NextCursor)
		}
		for _, item := range resp.Items {
			if seen[item.Namespace] {
				t.Fatalf("namespace %s returned on more than one page", item.Namespace)
			}
			seen[item.Namespace] = true
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	if len(seen) != total {
		t.Fatalf("paged to %d namespaces, want %d", len(seen), total)
	}
}

func TestNamespacesListFilterAndSort(t *testing.T) {
	srv := newTestServer(t)
	registerNamespace(t, srv, "user/alice/memory/notes", "user", "alice")
	registerNamespace(t, srv, "user/alice/knowledge/docs", "user", "alice")
	registerNamespace(t, srv, "user/bob/memory/notes", "user", "bob")
	registerNamespace(t, srv, "app/indexer/state", "app", "indexer")

	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{"prefix still works", "prefix=user/alice/", []string{"user/alice/knowledge/docs", "user/alice/memory/notes"}},
		{"match defaults to prefix", "match=user/alice/", []string{"user/alice/knowledge/docs", "user/alice/memory/notes"}},
		{"contains", "match=memory&match_mode=contains", []string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"glob", "match=" + url.QueryEscape("user/*/memory/*") + "&match_mode=glob", []string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"owner_type", "owner_type=app", []string{"app/indexer/state"}},
		{"owner_id", "owner_id=bob", []string{"user/bob/memory/notes"}},
		{"sort desc", "prefix=user/bob&dir=desc", []string{"user/bob/memory/notes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := getNamespaceList(t, srv, tc.query)
			if len(resp.Items) != len(tc.want) {
				t.Fatalf("got %d items, want %d (%+v)", len(resp.Items), len(tc.want), resp.Items)
			}
			for i, want := range tc.want {
				if resp.Items[i].Namespace != want {
					t.Fatalf("item %d: got %s, want %s", i, resp.Items[i].Namespace, want)
				}
			}
			if resp.Count != len(tc.want) {
				t.Fatalf("count=%d, want %d", resp.Count, len(tc.want))
			}
		})
	}

	// Ordering is the caller's, not the store's default.
	asc := getNamespaceList(t, srv, "sort=namespace&dir=asc")
	desc := getNamespaceList(t, srv, "sort=namespace&dir=desc")
	if len(asc.Items) != 4 || len(desc.Items) != 4 {
		t.Fatalf("expected 4 items each, got %d and %d", len(asc.Items), len(desc.Items))
	}
	if asc.Items[0].Namespace != desc.Items[3].Namespace {
		t.Fatalf("desc is not the reverse of asc: %s vs %s", asc.Items[0].Namespace, desc.Items[3].Namespace)
	}
}

// A bad argument is the caller's mistake and must read as one. Before the
// store owned validation these would have surfaced as a 500.
func TestNamespacesListRejectsBadArguments(t *testing.T) {
	srv := newTestServer(t)
	registerNamespace(t, srv, "user/alice/memory", "user", "alice")

	for _, tc := range []struct{ name, query string }{
		{"unknown sort", "sort=owner_id"},
		{"unknown dir", "dir=sideways"},
		{"unknown match mode", "match=user/&match_mode=regex"},
		{"malformed cursor", "cursor=not-a-cursor"},
		{"prefix and match together", "prefix=user/&match=alice"},
		{"prefix with a non-prefix mode", "prefix=user/&match_mode=glob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := performJSON(t, srv, http.MethodGet, "/v1/namespaces/list?"+tc.query, nil)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("got status %d, want 400 (body=%s)", res.Code, res.Body.String())
			}
		})
	}

	// A cursor is bound to the ordering it was issued under.
	first := getNamespaceList(t, srv, "limit=1&sort=namespace")
	if first.NextCursor != "" {
		res := performJSON(t, srv, http.MethodGet,
			"/v1/namespaces/list?limit=1&sort=updated_at&cursor="+url.QueryEscape(first.NextCursor), nil)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("resuming a cursor under a different sort: got %d, want 400", res.Code)
		}
	}
}

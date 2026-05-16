package mcpadapter

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestNamespacesList_PrefixIsStringPrefixNotGlob locks the contract that the
// MCP `prefix` parameter is a string prefix (matching HTTP semantics), not a
// glob. Originally divergent — MCP used globsPermit while HTTP used HasPrefix
// — which made deep prefixes like `user/chrispian/project/` return 0 unless
// a `*` suffix was added. CW-20260428-0005 follow-up.
func TestNamespacesList_PrefixIsStringPrefixNotGlob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, ns := range []string{
		"user/chrispian/memory",
		"user/chrispian/project/conduit/memory",
		"user/chrispian/project/nanite/memory",
		"app/cortex/identity",
	} {
		if err := s.EnsureNamespaceRegistered(ctx, ns); err != nil {
			t.Fatalf("seed %s: %v", ns, err)
		}
	}

	a := New(s, "")

	cases := []struct {
		name   string
		prefix string
		want   []string
	}{
		{
			name:   "deep prefix returns matching namespaces",
			prefix: "user/chrispian/project/",
			want:   []string{"user/chrispian/project/conduit/memory", "user/chrispian/project/nanite/memory"},
		},
		{
			name:   "tier prefix returns all under tier",
			prefix: "app/",
			want:   []string{"app/cortex/identity"},
		},
		{
			name:   "literal-glob does NOT match (prefix is not a glob)",
			prefix: "user/*",
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"prefix": tc.prefix, "limit": float64(50)}
			res, err := a.handleNamespacesList(ctx, req)
			if err != nil {
				t.Fatalf("handle: %v", err)
			}
			body := parseResult(t, res)
			itemsRaw, _ := body["items"].([]any)
			got := make([]string, 0, len(itemsRaw))
			for _, it := range itemsRaw {
				m, _ := it.(map[string]any)
				if ns, _ := m["namespace"].(string); ns != "" {
					got = append(got, ns)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("prefix %q: got %v, want %v", tc.prefix, got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("prefix %q: items[%d] = %q, want %q", tc.prefix, i, got[i], want)
				}
			}
		})
	}
}

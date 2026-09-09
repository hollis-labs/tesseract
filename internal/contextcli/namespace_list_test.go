package contextcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func seedCLINamespaces(t *testing.T, cli *CLI, rows [][3]string) {
	t.Helper()
	for _, row := range rows {
		if err := cli.Store.UpsertNamespacePolicy(context.Background(), contextstore.NamespacePolicyEntry{
			Namespace: row[0], OwnerType: row[1], OwnerID: row[2],
		}); err != nil {
			t.Fatalf("seed %s: %v", row[0], err)
		}
	}
}

type cliNamespaceList struct {
	Items []struct {
		Namespace string `json:"namespace"`
		OwnerType string `json:"owner_type"`
		OwnerID   string `json:"owner_id"`
	} `json:"items"`
	Count      int    `json:"count"`
	Truncated  bool   `json:"truncated"`
	NextCursor string `json:"next_cursor"`
}

// The CLI is the surface with no response budget to protect, so it is the one
// surface that returns the whole registry by default. A default that truncated
// would put the original bug on the command line too.
func TestNamespaceListReturnsEverythingByDefault(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	rows := make([][3]string, 0, 120)
	for i := 0; i < 120; i++ {
		rows = append(rows, [3]string{fmt.Sprintf("user/chrispian/ns%03d", i), "user", "chrispian"})
	}
	seedCLINamespaces(t, cli, rows)

	if code := cli.Run(context.Background(), []string{"context", "namespace", "list"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var resp cliNamespaceList
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (output %q)", err, out.String())
	}
	if len(resp.Items) != 120 {
		t.Fatalf("got %d namespaces, want 120", len(resp.Items))
	}
	if resp.Count != 120 {
		t.Fatalf("count = %d, want 120", resp.Count)
	}
	if resp.Truncated || resp.NextCursor != "" {
		t.Fatalf("an unpaged listing must not report itself truncated: truncated=%t next_cursor=%q",
			resp.Truncated, resp.NextCursor)
	}
}

func TestNamespaceListPagesWhenAsked(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	rows := make([][3]string, 0, 25)
	for i := 0; i < 25; i++ {
		rows = append(rows, [3]string{fmt.Sprintf("user/chrispian/ns%03d", i), "user", "chrispian"})
	}
	seedCLINamespaces(t, cli, rows)

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 25 {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
		out.Reset()
		args := []string{"context", "namespace", "list", "-limit", "10"}
		if cursor != "" {
			args = append(args, "-cursor", cursor)
		}
		if code := cli.Run(context.Background(), args); code != 0 {
			t.Fatalf("page %d exit: %s", pages, errOut.String())
		}
		var resp cliNamespaceList
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("page %d unmarshal: %v", pages, err)
		}
		if resp.Count != 25 {
			t.Fatalf("page %d: count = %d, want 25 (the whole match)", pages, resp.Count)
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
	if len(seen) != 25 {
		t.Fatalf("paged to %d namespaces, want 25", len(seen))
	}
}

func TestNamespaceListFiltersSortsAndValidates(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	seedCLINamespaces(t, cli, [][3]string{
		{"user/alice/memory/notes", "user", "alice"},
		{"user/bob/memory/notes", "user", "bob"},
		{"app/indexer/state", "app", "indexer"},
	})

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"prefix", []string{"-prefix", "user/"}, []string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"contains", []string{"-match", "memory", "-match-mode", "contains"}, []string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"glob", []string{"-match", "user/*/memory/*", "-match-mode", "glob"}, []string{"user/alice/memory/notes", "user/bob/memory/notes"}},
		{"owner-type", []string{"-owner-type", "app"}, []string{"app/indexer/state"}},
		{"owner-id", []string{"-owner-id", "bob"}, []string{"user/bob/memory/notes"}},
		{"desc", []string{"-dir", "desc"}, []string{"user/bob/memory/notes", "user/alice/memory/notes", "app/indexer/state"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out.Reset()
			args := append([]string{"context", "namespace", "list"}, tc.args...)
			if code := cli.Run(context.Background(), args); code != 0 {
				t.Fatalf("exit: %s", errOut.String())
			}
			var resp cliNamespaceList
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(resp.Items) != len(tc.want) {
				t.Fatalf("got %d items, want %d", len(resp.Items), len(tc.want))
			}
			for i, want := range tc.want {
				if resp.Items[i].Namespace != want {
					t.Fatalf("item %d: got %s, want %s", i, resp.Items[i].Namespace, want)
				}
			}
		})
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown sort", []string{"-sort", "owner_id"}},
		{"unknown dir", []string{"-dir", "sideways"}},
		{"unknown match mode", []string{"-match", "user/", "-match-mode", "regex"}},
		{"prefix and match together", []string{"-prefix", "user/", "-match", "alice"}},
		{"malformed cursor", []string{"-cursor", "not-a-cursor"}},
		{"bad output", []string{"-output", "yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out.Reset()
			errOut.Reset()
			args := append([]string{"context", "namespace", "list"}, tc.args...)
			if code := cli.Run(context.Background(), args); code == 0 {
				t.Fatalf("expected a non-zero exit, got 0 (output %q)", out.String())
			}
		})
	}
}

// A table is read by a human, which is exactly where a partial set passes for
// a complete one. The page must say so in the output, not only in JSON.
func TestNamespaceListTableSaysWhenItIsPartial(t *testing.T) {
	cli, out, errOut := newTestCLI(t)
	rows := make([][3]string, 0, 15)
	for i := 0; i < 15; i++ {
		rows = append(rows, [3]string{fmt.Sprintf("user/chrispian/ns%03d", i), "user", "chrispian"})
	}
	seedCLINamespaces(t, cli, rows)

	if code := cli.Run(context.Background(),
		[]string{"context", "namespace", "list", "-limit", "5", "-output", "table"}); code != 0 {
		t.Fatalf("exit: %s", errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "NAMESPACE") {
		t.Fatalf("expected a table header, got %q", text)
	}
	if !strings.Contains(text, "5 of 15 shown") {
		t.Fatalf("expected the table to say it is partial, got %q", text)
	}
	if !strings.Contains(text, "-cursor ") {
		t.Fatalf("expected the table to name the cursor flag, got %q", text)
	}

	// The complete case says nothing extra.
	out.Reset()
	if code := cli.Run(context.Background(),
		[]string{"context", "namespace", "list", "-output", "table"}); code != 0 {
		t.Fatalf("exit: %s", errOut.String())
	}
	if strings.Contains(out.String(), "shown") {
		t.Fatalf("a complete listing should not announce a page: %q", out.String())
	}
}

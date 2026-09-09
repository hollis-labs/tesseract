package memory

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestBuildNamespaceClause(t *testing.T) {
	cases := []struct {
		name     string
		input    []string
		wantSQL  string
		wantArgs []interface{}
	}{
		{
			name:     "empty rejects everything",
			input:    nil,
			wantSQL:  "1=0",
			wantArgs: nil,
		},
		{
			name:     "single exact 4-seg",
			input:    []string{"user/x/memory/notes"},
			wantSQL:  "(r.namespace = ?)",
			wantArgs: []interface{}{"user/x/memory/notes"},
		},
		{
			name:     "single legacy-flat treated as prefix",
			input:    []string{"user/x/memory"},
			wantSQL:  "(r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/memory/%"},
		},
		{
			name:     "session legacy-flat treated as prefix",
			input:    []string{"user/x/session/s1/memory"},
			wantSQL:  "(r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/session/s1/memory/%"},
		},
		{
			name:     "explicit wildcard treated as prefix",
			input:    []string{"user/x/memory/*"},
			wantSQL:  "(r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/memory/%"},
		},
		{
			name:     "mixed exact + prefix",
			input:    []string{"user/x/memory/notes", "user/y/memory"},
			wantSQL:  "(r.namespace = ? OR r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/memory/notes", "user/y/memory/%"},
		},
		{
			name:     "knowledge namespace stays exact",
			input:    []string{"user/x/knowledge/portfolio"},
			wantSQL:  "(r.namespace = ?)",
			wantArgs: []interface{}{"user/x/knowledge/portfolio"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := buildNamespaceClause(tc.input)
			if sql != tc.wantSQL {
				t.Errorf("sql = %q, want %q", sql, tc.wantSQL)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

// TestBuildNamespaceClause_ManyNamespacesStaysShallow is the regression guard
// for the memory review queue, which recalls across every registered
// namespace. At 1000 namespaces the old flat OR chain produced an expression
// exactly at SQLite's SQLITE_MAX_EXPR_DEPTH, and the whole query failed to
// prepare with "Expression tree is too large (maximum depth 1000)".
//
// Depth is measured on the rendered SQL rather than asserted on its text: the
// grouping is an implementation detail, the height is the contract.
func TestBuildNamespaceClause_ManyNamespacesStaysShallow(t *testing.T) {
	const sqliteMaxExprDepth = 1000

	cases := []struct {
		name string
		make func(i int) string
	}{
		{"all exact", func(i int) string { return fmt.Sprintf("user/u%d/memory/notes", i) }},
		{"all prefix", func(i int) string { return fmt.Sprintf("user/u%d/memory", i) }},
		{"mixed", func(i int) string {
			if i%2 == 0 {
				return fmt.Sprintf("user/u%d/memory/notes", i)
			}
			return fmt.Sprintf("user/u%d/memory", i)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range []int{1000, 5000} {
				input := make([]string, n)
				for i := range input {
					input[i] = tc.make(i)
				}
				sql, args := buildNamespaceClause(input)
				if got := strings.Count(sql, "?"); got != n {
					t.Fatalf("n=%d: %d placeholders, want %d", n, got, n)
				}
				if len(args) != n {
					t.Fatalf("n=%d: %d args, want %d", n, len(args), n)
				}
				if depth := parenDepth(sql); depth >= sqliteMaxExprDepth {
					t.Errorf("n=%d: nesting depth %d, want < %d", n, depth, sqliteMaxExprDepth)
				}
			}
		})
	}
}

func TestBuildNamespaceClause_GroupsExactIntoINList(t *testing.T) {
	sql, args := buildNamespaceClause([]string{
		"user/x/memory/notes",
		"user/y/memory",
		"user/z/knowledge/framework",
	})
	// Exact matches collapse into one IN list and are bound first; prefixes
	// keep their own LIKE term.
	wantSQL := "(r.namespace IN (?,?) OR r.namespace LIKE ?)"
	if sql != wantSQL {
		t.Errorf("sql = %q, want %q", sql, wantSQL)
	}
	wantArgs := []interface{}{"user/x/memory/notes", "user/z/knowledge/framework", "user/y/memory/%"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestOrTree_BalancedAndAssociative(t *testing.T) {
	if got := orTree([]string{"a"}); got != "a" {
		t.Errorf("orTree(1) = %q, want %q", got, "a")
	}
	if got := orTree([]string{"a", "b"}); got != "(a OR b)" {
		t.Errorf("orTree(2) = %q, want %q", got, "(a OR b)")
	}
	if got := orTree([]string{"a", "b", "c"}); got != "(a OR (b OR c))" {
		t.Errorf("orTree(3) = %q, want %q", got, "(a OR (b OR c))")
	}
	// Pins the balanced shape the package doc advertises.
	if got := orTree([]string{"a", "b", "c", "d"}); got != "((a OR b) OR (c OR d))" {
		t.Errorf("orTree(4) = %q, want %q", got, "((a OR b) OR (c OR d))")
	}
}

// TestBuildNamespaceClause_DocumentedShapes pins every fragment shape named in
// buildNamespaceClause's doc comment. The comment previously promised a
// `(... OR ...)` chain unconditionally, which stopped being true once exact
// matches collapsed into an IN list; this keeps the documentation and the code
// from drifting apart again.
func TestBuildNamespaceClause_DocumentedShapes(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  string
	}{
		{"one exact", []string{"user/x/memory/notes"}, "(r.namespace = ?)"},
		{"several exact, no prefixes", []string{
			"user/x/memory/notes", "user/y/memory/decisions", "user/z/knowledge/f",
		}, "(r.namespace IN (?,?,?))"},
		{"one prefix", []string{"user/x/memory"}, "(r.namespace LIKE ?)"},
		{"mixed", []string{
			"user/x/memory/notes", "user/y/memory/decisions", "user/z/memory",
		}, "(r.namespace IN (?,?) OR r.namespace LIKE ?)"},
		{"many prefixes, balanced", []string{
			"user/a/memory", "user/b/memory", "user/c/memory", "user/d/memory",
		}, "((r.namespace LIKE ? OR r.namespace LIKE ?) OR " +
			"(r.namespace LIKE ? OR r.namespace LIKE ?))"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := buildNamespaceClause(tc.input)
			if got != tc.want {
				t.Errorf("sql = %q, want %q", got, tc.want)
			}
		})
	}
}

// parenDepth returns the maximum parenthesis nesting depth of s, which for a
// fragment built only from parenthesized OR groupings is the height SQLite
// checks against SQLITE_MAX_EXPR_DEPTH.
func parenDepth(s string) int {
	depth, deepest := 0, 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
			if depth > deepest {
				deepest = depth
			}
		case ')':
			depth--
		}
	}
	return deepest
}

func TestMemoryPrefix(t *testing.T) {
	cases := []struct {
		input  string
		want   string
		wantOk bool
	}{
		{"user/x/memory", "user/x/memory", true},
		{"user/x/memory/*", "user/x/memory", true},
		{"user/x/project/p/memory", "user/x/project/p/memory", true},
		{"user/x/session/s/memory", "user/x/session/s/memory", true},
		{"user/x/memory/notes", "", false},
		{"user/x/knowledge/something", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := memoryPrefix(tc.input)
			if ok != tc.wantOk || got != tc.want {
				t.Errorf("memoryPrefix(%q) = (%q, %v), want (%q, %v)",
					tc.input, got, ok, tc.want, tc.wantOk)
			}
		})
	}
}

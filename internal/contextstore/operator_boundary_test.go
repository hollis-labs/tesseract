package contextstore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AsOperatorCLI selects the local-operator actor. Go's internal/ visibility
// cannot restrict an import to one sibling package, so the boundary is enforced
// here: no request-serving code may reference it. Allowed: contextcli (the CLI
// entry point) and, in cmd/tesseract, only the promote-record dispatch case.
func TestAsOperatorCLIIsNotReachableFromServingCode(t *testing.T) {
	roots := []string{"../contextapi", "../mcpadapter", "../authn", "../../cmd/tesseract"}
	for _, root := range roots {
		// Recursive, so a future subpackage (contextapi/foo) is covered too.
		var files []string
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil || len(files) == 0 {
			t.Fatalf("no Go files under %s (%v): the boundary test would pass vacuously", root, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, f, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			var stack []ast.Node
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "AsOperatorCLI" {
					return true
				}
				if strings.HasSuffix(filepath.ToSlash(f), "cmd/tesseract/main.go") && inPromoteRecordCase(stack) {
					return true
				}
				t.Errorf("%s: references AsOperatorCLI; only internal/contextcli and the promote-record dispatch may mark a context as the operator CLI",
					fset.Position(sel.Pos()))
				return true
			})
		}
	}
}

// inPromoteRecordCase reports whether the node stack sits inside
// `case "promote-record":`.
func inPromoteRecordCase(stack []ast.Node) bool {
	for _, n := range stack {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, e := range cc.List {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Value == `"promote-record"` {
				return true
			}
		}
	}
	return false
}

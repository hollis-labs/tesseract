package memory_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Every production entry into memory_revisions is listed here with the next
// call in its chain. The table is intentionally architectural rather than a
// list of tests: it makes review answer "which public doors can persist
// facets?" without relying on a repository grep and it fails if a named door
// disappears or stops reaching the authoritative boundary.
var revisionWriteSurfaces = []struct {
	class, file, function, delegatesTo string
}{
	{"persistence", "internal/memory/write.go", "WriteRevision", "ExecContext"},
	{"derived store API", "internal/memory/promote.go", "Promote", "WriteRevision"},
	{"knowledge store API", "internal/knowledge/store.go", "Write", "WriteRevision"},
	{"root public facade", "tesseract.go", "WriteMemory", "WriteRevision"},
	{"HTTP memory", "internal/contextapi/memory_handler.go", "handleMemoryWrite", "WriteRevision"},
	{"HTTP knowledge", "internal/contextapi/knowledge_handler.go", "handleKnowledgeWrite", "Write"},
	{"MCP memory", "internal/mcpadapter/memory_tools.go", "handleMemoryWrite", "WriteRevision"},
	{"MCP knowledge", "internal/mcpadapter/knowledge_tools.go", "handleKnowledgeWrite", "Write"},
}

func TestRevisionWriteSurfacesReachAuthoritativeBoundary(t *testing.T) {
	if len(revisionWriteSurfaces) < 8 {
		t.Fatalf("write surface inventory has only %d entries; it is vacuous or incomplete", len(revisionWriteSurfaces))
	}
	root := repositoryRoot(t)
	for _, surface := range revisionWriteSurfaces {
		t.Run(surface.class, func(t *testing.T) {
			path := filepath.Join(root, surface.file)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", surface.file, err)
			}
			fn := findFunction(f, surface.function)
			if fn == nil {
				t.Fatalf("%s no longer declares %s; update the inventory", surface.file, surface.function)
			}
			if !callsNamed(fn.Body, surface.delegatesTo) {
				t.Fatalf("%s.%s no longer calls %s; facet validation may be bypassed",
					surface.file, surface.function, surface.delegatesTo)
			}
			t.Logf("%s: %s.%s -> %s", surface.class, surface.file, surface.function, surface.delegatesTo)
		})
	}
}

// TestMemoryRevisionsHasOneProductionInsert proves the inventory converges on
// one physical insert. Intentional corrupt/legacy fixtures live in _test.go and
// therefore do not weaken this production-source assertion.
func TestMemoryRevisionsHasOneProductionInsert(t *testing.T) {
	root := repositoryRoot(t)
	insertRevision := regexp.MustCompile(`(?is)INSERT\s+INTO\s+memory_revisions\s*\(`)
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// #nosec G304,G122 -- path is supplied by WalkDir rooted at this repository; repository contents are trusted test input.
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if insertRevision.Match(body) {
			found = append(found, strings.TrimPrefix(path, root+string(filepath.Separator)))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk production Go files: %v", err)
	}
	if len(found) != 1 || found[0] != "internal/memory/write.go" {
		t.Fatalf("production memory_revisions inserts = %v, want only internal/memory/write.go", found)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func findFunction(f *ast.File, name string) *ast.FuncDecl {
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func callsNamed(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			found = found || fn.Name == name
		case *ast.SelectorExpr:
			found = found || fn.Sel.Name == name
		}
		return !found
	})
	return found
}

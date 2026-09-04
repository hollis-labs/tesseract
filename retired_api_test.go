package tesseract_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestRetiredSimilarityErrorAbsent prevents the removed compatibility name
// from returning in a facade or handler while allowing prose to describe the
// v0.9 migration.
func TestRetiredSimilarityErrorAbsent(t *testing.T) {
	forbidden := "ErrSimilarity" + "Unavailable"
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == forbidden {
				t.Errorf("%s restores retired API identifier %q", path, forbidden)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go source: %v", err)
	}
}

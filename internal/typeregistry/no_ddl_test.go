package typeregistry_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// The registry loader must never be able to reach schema.
//
// [[tesseract_registry_index_ddl_constrained]] — operator gate G2, answered
// "constrain". A type DECLARES hot_fields; indexes materialize through the
// ordered migration list in internal/contextstore, where a human reviews the
// statement before it runs. Two config files must never produce two schemas
// from one binary: that is what makes the store reproducible and a restore
// deterministic.
//
// The gate note said this is "the kind of decision that gets made accidentally
// by whoever writes the registry loader first", which is why it is a test and
// not a convention. Seer invented hot_fields and still materialized by
// hand-written migration; the precedent is the whole argument.
//
// These tests are structural rather than behavioral on purpose. You cannot
// assert "the loader did not emit DDL" by loading a config — a loader that
// never does is indistinguishable from one that did not happen to this time.
// What you CAN assert is that the package has no way to: no database handle
// reachable from it, and no statement in it.

// TestRegistryLoaderCannotReachADatabase asserts the package imports nothing
// that could execute a statement.
//
// This is the load-bearing half. A package with no sql.DB, no driver and no
// store dependency cannot emit DDL however its code is later edited — the
// failure mode is caught at the import line, in review, rather than in a
// statement buried in a loader.
func TestRegistryLoaderCannotReachADatabase(t *testing.T) {
	// Any import path containing one of these cannot appear in this package.
	// "sql" as a path segment covers database/sql and every driver that names
	// itself after it; contextstore is the only package here that owns schema.
	banned := []string{
		"database/sql",
		"sqlite",
		"internal/contextstore",
		"internal/sqlitedsn",
	}

	for path, file := range parsePackage(t) {
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, b := range banned {
				if strings.Contains(p, b) {
					t.Errorf("%s imports %q: the type registry must have no path to schema "+
						"(see [[tesseract_registry_index_ddl_constrained]])", path, p)
				}
			}
		}
	}
}

// TestRegistryLoaderContainsNoDDL asserts no source file in the package spells
// a schema statement, whether or not it could execute one.
//
// The import test above is the real guard; this one catches the intermediate
// step — a loader that builds a CREATE INDEX string and hands it somewhere
// else to run. A registry that GENERATES the statement has already taken the
// decision the gate refused, even if another package executes it.
func TestRegistryLoaderContainsNoDDL(t *testing.T) {
	ddl := []string{
		"create table", "create index", "create unique index", "create virtual table",
		"alter table", "drop table", "drop index", "create trigger",
	}

	for path := range parsePackage(t) {
		// #nosec G304 -- path is a *.go filename this test just enumerated
		// from its own package directory.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lower := strings.ToLower(string(data))
		for _, stmt := range ddl {
			if strings.Contains(lower, stmt) {
				t.Errorf("%s contains %q: hot_fields are declarative, and indexes "+
					"materialize through the migration list in internal/contextstore",
					path, stmt)
			}
		}
	}
}

// TestHotFieldsAreDeclarativeOnly loads a config declaring a hot field and
// asserts the registry does exactly one thing with it: hand it back.
//
// The negative is covered structurally above. This is the positive half —
// declaring a hot field has to actually WORK, or the constrain option
// collapses into the reject option that was considered and turned down.
func TestHotFieldsAreDeclarativeOnly(t *testing.T) {
	r := typeregistry.NewRegistry()
	err := r.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: memory.type
    closed: true
    types:
      - type_id: notes
        hot_fields: [due_at, priority]
`))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	got, ok := r.Lookup(typeregistry.VocabMemoryType, "notes")
	if !ok {
		t.Fatal("notes not loaded")
	}
	if len(got.HotFields) != 2 || got.HotFields[0] != "due_at" || got.HotFields[1] != "priority" {
		t.Fatalf("hot fields = %v, want [due_at priority]", got.HotFields)
	}
}

// TestHotFieldMustBeAnIdentifier asserts a declared hot field cannot be
// anything but a bare column-shaped name.
//
// Nothing in this package emits DDL, so this is not what stops an injection
// today. It is what keeps the constraint true for whoever writes the migration
// generator that reads these names: a value that cannot be anything but an
// identifier cannot become a statement wherever it ends up.
func TestHotFieldMustBeAnIdentifier(t *testing.T) {
	for _, bad := range []string{
		"due_at, x); DROP TABLE records; --",
		"Due_At",
		"due-at",
		"",
		"1_due",
	} {
		r := typeregistry.NewRegistry()
		err := r.LoadFromBytes([]byte("vocabularies:\n" +
			"  - vocabulary_id: memory.type\n" +
			"    types:\n" +
			"      - type_id: notes\n" +
			"        hot_fields: [" + quoteYAML(bad) + "]\n"))
		if err == nil {
			t.Errorf("hot field %q was accepted; want rejection", bad)
		}
	}
}

func quoteYAML(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// parsePackage returns the parsed non-test source files of the typeregistry
// package, keyed by path.
//
// It walks the directory itself rather than calling parser.ParseDir, which is
// deprecated, and rather than pulling in go/packages for a guard that only
// needs import lines.
func parsePackage(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("typeregistry package has no source files; the guard would pass vacuously")
	}
	return files
}

package parity

import (
	"testing"

	"github.com/hollis-labs/tesseract/internal/surfacefields"
)

// The seam between the two tables.
//
// surfaceCatalog owns existence: which operations have a door on both surfaces.
// surfacefields owns shape: what those two doors accept. They describe the same
// operations at different granularity, and the failure mode they share is a
// row that names something no longer there — which is how the literal hint map
// surfacefields replaced went stale.
//
// So each table is checked against the thing it describes, and here they are
// checked against each other.

// TestSurfaceFieldsDoorsArePairedCatalogRows asserts every door names a pairing
// the catalog already records.
//
// A door whose (tool, method, path) is not a paired catalog row is describing
// something that either does not exist or is deliberately one-sided, and
// declaring the request shape of a door with only one side is a category error
// rather than a small mistake.
func TestSurfaceFieldsDoorsArePairedCatalogRows(t *testing.T) {
	paired := map[[3]string]bool{}
	for _, op := range surfaceCatalog {
		if op.MCP == "" || op.HTTPPath == "" {
			continue
		}
		paired[[3]string{op.MCP, op.HTTPMethod, op.HTTPPath}] = true
	}

	for _, door := range surfacefields.Doors {
		key := [3]string{door.MCPTool, door.HTTPMethod, door.HTTPPath}
		if !paired[key] {
			t.Errorf("surfacefields door %q declares the pairing %s ↔ %s %s, which is not a "+
				"paired row in surfaceCatalog. Either the catalog lost it, or the door names "+
				"an operation that is one-sided by design — in which case it has no request "+
				"shape to compare.",
				door.Name, door.MCPTool, door.HTTPMethod, door.HTTPPath)
		}
	}
}

// TestWriteDoorsAreAllCovered is the rule that makes `workspace` inherit rather
// than diverge.
//
// Every genuine divergence CW-20260912-0048 found came from adding a door or
// adding a field across doors, and CW-20260912-0079 adds a fourth write door.
// Nothing stops a new domain's write route from shipping with no row, so this
// states the obligation where a new route will trip over it: a POST route whose
// path ends in /write is a door that supplies a record's own fields, and those
// are the doors surfacefields exists to declare.
//
// Adding a domain therefore means adding a door here. That is the whole
// mechanism — the difference becomes a row someone wrote.
func TestWriteDoorsAreAllCovered(t *testing.T) {
	declared := map[string]bool{}
	for _, door := range surfacefields.Doors {
		declared[door.HTTPPath] = true
	}

	for _, op := range surfaceCatalog {
		if op.MCP == "" || op.HTTPPath == "" {
			continue
		}
		if !isDomainWriteRoute(op.HTTPPath) {
			continue
		}
		if !declared[op.HTTPPath] {
			t.Errorf("%s is a domain write route with an MCP peer (%s) and no surfacefields "+
				"door. Add one, so the new door inherits a declared shape instead of a "+
				"divergence nobody wrote down.", op.HTTPPath, op.MCP)
		}
	}
}

// isDomainWriteRoute reports whether a path is one of the write doors that take
// a record's own fields.
//
// It excludes /v1/context/write deliberately: that route serves the legacy
// records store, which takes an opaque payload rather than a field set, and is
// on CW-20260909-0037's retirement path. It has no shape to declare.
func isDomainWriteRoute(path string) bool {
	switch path {
	case "/v1/memory/write", "/v1/knowledge/write", "/v1/event/write":
		return true
	case "/v1/context/write":
		return false
	}
	// Any /v1/{domain}/write that is not one of the above is new, and new is
	// exactly the case this test is here for.
	return len(path) > len("/v1//write") &&
		path[:4] == "/v1/" && path[len(path)-len("/write"):] == "/write"
}

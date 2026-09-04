package mcpadapter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The json.RawMessage conversion and declared-variable guard.
//
// THE INVARIANT
//
//	No value derived from stored JSON by slicing, or by any other byte-level
//	transformation, may be assigned to a json.RawMessage.
//
// WHY IT EXISTS. A json.RawMessage is copied into the output verbatim, so it
// must hold a COMPLETE JSON document. The prefix of a JSON object is not one.
// `payload_mode=head_only` used to cut a stored payload mid-document and hand
// the fragment to a RawMessage; the enclosing json.Marshal then failed, and
// toolJSON once discarded that error, so an oversized record came back as an
// EMPTY tool result presented as success. See
// TestPayloadMaxBytes_HeadOnlyReturnedAnEmptyResultAndTheCapDoesNot.
//
// The current design holds the invariant BY CONSTRUCTION: capPayload does not
// shorten `payload`, it DELETES the key and reports `payload_head` as a plain
// JSON string. This guard is what keeps the next author from undoing that.
//
// WHY SITES AND NOT SYNTAX. Grepping for "[:" finds today's spelling of the
// defect and nothing else — a helper named head(), an append, a byte-buffer
// truncation all read differently and all break the invariant the same way.
// Destinations of the type are a FINITE list, so this enumerates the ones it
// covers and then decides, per site, whether the value being constructed can be
// shown to be a complete document — rather than grepping for a spelling.
//
// THE RULE IS A WHITELIST, not a blacklist of bad shapes, because a blacklist
// only knows the transformations someone already thought of. An operand shape
// this file does not recognize FAILS and someone has to look at it. That is the
// intended cost.
//
// ════════════════════════════════════════════════════════════════════════
// WHAT THIS GUARD COVERS
//
// COVERED — all seven direct destination shapes, enumerated and then checked:
//
//	json.RawMessage(x)                 the explicit conversion
//	var x json.RawMessage; x = expr    assignment to a declared variable
//	json.Unmarshal(b, &x)              decoder fill of a declared variable
//	writeRequest{Payload: payload[:n]}                  composite-literal field
//	req.Payload = payload[:n]                           selector on the left
//	func f() json.RawMessage { return payload[:n] }     return statement
//	EmitWrite(..., payload[:n])                         json.RawMessage parameter
//
// THE MECHANISM, because it is the part a future reader needs: json.RawMessage's
// underlying type is []byte, so Go requires NO CONVERSION to reach one. The
// conversion rule alone never fires on the last four. The package catalog below
// therefore records RawMessage struct fields, result positions, and parameter
// positions, then checks values placed at those typed destinations.
//
// THE POPULATION IS NOT EMPTY. internal/contextapi declares seven
// json.RawMessage struct fields, and it is the package writeJSON serves. The
// HTTP helper now marshals the complete body before it writes either headers or
// status. That prevents a malformed RawMessage from committing a success and
// then failing mid-response; this guard still matters because rejecting a bad
// representation during review is better than converting it to a runtime 500.
//
// STATED BOUNDARY, second and separate — DATAFLOW. A bare identifier or field
// selector is accepted as a whole-value pass-through, so a payload sliced into
// a []byte variable several lines earlier and merely named here is invisible.
// Closing that needs a type-and-flow analysis rather than an AST walk, and it is
// out of scope for both tickets.
// ════════════════════════════════════════════════════════════════════════

// ── Scanned packages ───────────────────────────────────────────────────

// rawMessageScannedPackages are the two packages that build tool and route
// RESPONSES from stored records. Stores and CLIs also name json.RawMessage; they
// hand whole payloads around rather than projecting them into a response, and
// including them would trade a larger allowlist for no additional coverage of
// the invariant.
var rawMessageScannedPackages = []string{
	"internal/mcpadapter",
	"internal/contextapi",
}

// contextstore is cataloged, but not treated as a response package. Its
// AppendInput fields and Emit* parameters are destinations used directly by
// both response packages and must be known to the syntax walk.
var rawMessageCatalogPackages = []string{
	"internal/mcpadapter",
	"internal/contextapi",
	"internal/contextstore",
}

// ── Allowed operand shapes ─────────────────────────────────────────────

// allowedRawMessageBuilders are calls whose result is a complete JSON document
// by construction. Each entry is a claim; the reason is stated because an entry
// added carelessly is a hole in the invariant.
var allowedRawMessageBuilders = map[string]string{
	"fmt.Sprintf":   "builds a whole object from a format string; the literal supplies both braces",
	"json.Marshal":  "produces a complete document or an error, never a fragment",
	"quoteJSON":     "contextapi helper: renders one Go string as a complete JSON string literal",
	"strconv.Quote": "renders one Go string as a complete quoted literal",
}

// rawMessageSite is one place a json.RawMessage value is constructed.
type rawMessageSite struct {
	Pos     string // pkg/file.go:line
	Package string
	Form    string // how the value is built, for the report
	Operand ast.Expr
}

type rawMessageCatalog struct {
	structFields map[string][]rawMessageField
	callParams   map[string]map[int]bool
}

type rawMessageField struct {
	Name string
	Raw  bool
}

// ── The enumeration ────────────────────────────────────────────────────

// rawMessageConstructionSites walks the response packages and returns all
// direct syntactic sites where a json.RawMessage value is produced. Struct
// field and parameter declarations are catalog entries, not construction
// sites; the values written into those destinations are the sites.
func rawMessageConstructionSites(t *testing.T) []rawMessageSite {
	t.Helper()
	root := moduleRoot(t)
	catalog := buildRawMessageCatalog(t, root)

	var sites []rawMessageSite
	for _, pkg := range rawMessageScannedPackages {
		dir := filepath.Join(root, pkg)
		fset, files := parseGoDir(t, dir)
		for _, f := range files {
			sites = append(sites, rawMessageSitesInFile(fset, pkg, f, catalog)...)
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Pos < sites[j].Pos })
	return sites
}

func buildRawMessageCatalog(t *testing.T, root string) rawMessageCatalog {
	t.Helper()
	c := rawMessageCatalog{
		structFields: map[string][]rawMessageField{},
		callParams:   map[string]map[int]bool{},
	}
	for _, pkg := range rawMessageCatalogPackages {
		_, files := parseGoDir(t, filepath.Join(root, pkg))
		for _, f := range files {
			addRawMessageDeclarations(f, c)
		}
	}
	return c
}

func addRawMessageDeclarations(f *ast.File, c rawMessageCatalog) {
	for _, decl := range f.Decls {
		switch node := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				c.structFields[ts.Name.Name] = rawMessageFields(st.Fields)
			}
		case *ast.FuncDecl:
			indexes := rawMessagePositions(node.Type.Params)
			if len(indexes) != 0 {
				c.callParams[node.Name.Name] = indexes
			}
		}
	}
}

func rawMessageFields(fields *ast.FieldList) []rawMessageField {
	if fields == nil {
		return nil
	}
	var out []rawMessageField
	for _, field := range fields.List {
		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{{Name: exprTypeName(field.Type)}}
		}
		for _, name := range names {
			out = append(out, rawMessageField{Name: name.Name, Raw: isRawMessageSelector(field.Type)})
		}
	}
	return out
}

func rawMessagePositions(fields *ast.FieldList) map[int]bool {
	out := map[int]bool{}
	if fields == nil {
		return out
	}
	index := 0
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			if isRawMessageSelector(field.Type) {
				out[index] = true
			}
			index++
		}
	}
	return out
}

func rawMessageSitesInFile(fset *token.FileSet, pkg string, f *ast.File, catalog rawMessageCatalog) []rawMessageSite {
	var sites []rawMessageSite
	add := func(pos token.Pos, form string, operand ast.Expr) {
		sites = append(sites, rawMessageSite{
			Pos: position(fset, pkg, pos), Package: pkg, Form: form, Operand: operand,
		})
	}

	// Explicit conversions are independent of function scope.
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && isRawMessageSelector(call.Fun) && len(call.Args) == 1 {
			add(call.Pos(), "json.RawMessage(...) conversion", call.Args[0])
		}
		return true
	})

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		declaredRaw := declaredRawMessageVars(fn.Body)
		declaredTypes := declaredVariableTypes(fn)
		returnRaw := rawMessagePositions(fn.Type.Results)

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				// json.Unmarshal validates the complete document before filling x.
				if callName(node.Fun) == "json.Unmarshal" && len(node.Args) == 2 {
					if u, ok := node.Args[1].(*ast.UnaryExpr); ok && u.Op == token.AND {
						if id, ok := u.X.(*ast.Ident); ok && declaredRaw[id.Name] {
							add(node.Pos(), "json.Unmarshal fill of a declared json.RawMessage", nil)
						}
					}
				}
				for i := range catalog.callParams[callableName(node.Fun)] {
					if i < len(node.Args) {
						add(node.Args[i].Pos(), "argument to a json.RawMessage parameter", node.Args[i])
					}
				}
			case *ast.CompositeLit:
				fields := fieldsForComposite(node.Type, catalog)
				for i, elt := range node.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						key, ok := kv.Key.(*ast.Ident)
						if ok && fieldIsRaw(fields, key.Name) {
							add(kv.Value.Pos(), "json.RawMessage composite-literal field", kv.Value)
						}
					} else if i < len(fields) && fields[i].Raw {
						add(elt.Pos(), "json.RawMessage positional composite-literal field", elt)
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range node.Lhs {
					operand := assignmentOperand(node, i)
					switch target := lhs.(type) {
					case *ast.Ident:
						if declaredRaw[target.Name] {
							add(node.Pos(), "assignment to a declared json.RawMessage variable", operand)
						}
					case *ast.SelectorExpr:
						if selectorIsRawField(target, declaredTypes, catalog) {
							add(node.Pos(), "assignment to a json.RawMessage selector field", operand)
						}
					}
				}
			case *ast.ReturnStmt:
				for i := range returnRaw {
					if i < len(node.Results) {
						add(node.Results[i].Pos(), "return as json.RawMessage result", node.Results[i])
					}
				}
			}
			return true
		})
	}
	return sites
}

func fieldsForComposite(expr ast.Expr, catalog rawMessageCatalog) []rawMessageField {
	if st, ok := expr.(*ast.StructType); ok {
		return rawMessageFields(st.Fields)
	}
	return catalog.structFields[exprTypeName(expr)]
}

func fieldIsRaw(fields []rawMessageField, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return field.Raw
		}
	}
	return false
}

func assignmentOperand(node *ast.AssignStmt, index int) ast.Expr {
	switch {
	case len(node.Rhs) == len(node.Lhs):
		return node.Rhs[index]
	case len(node.Rhs) == 1:
		return node.Rhs[0]
	default:
		return nil
	}
}

func declaredVariableTypes(fn *ast.FuncDecl) map[string]string {
	out := map[string]string{}
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				out[name.Name] = exprTypeName(field.Type)
			}
		}
	}
	addFields(fn.Recv)
	addFields(fn.Type.Params)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			if node.Type != nil {
				for _, name := range node.Names {
					out[name.Name] = exprTypeName(node.Type)
				}
			}
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE || len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, lhs := range node.Lhs {
				id, ok := lhs.(*ast.Ident)
				cl, isComposite := node.Rhs[i].(*ast.CompositeLit)
				if ok && isComposite {
					out[id.Name] = exprTypeName(cl.Type)
				}
			}
		}
		return true
	})
	return out
}

func selectorIsRawField(sel *ast.SelectorExpr, vars map[string]string, catalog rawMessageCatalog) bool {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return fieldIsRaw(catalog.structFields[vars[id.Name]], sel.Sel.Name)
}

func exprTypeName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		return node.Sel.Name
	case *ast.StarExpr:
		return exprTypeName(node.X)
	case *ast.IndexExpr:
		return exprTypeName(node.X)
	case *ast.IndexListExpr:
		return exprTypeName(node.X)
	}
	return ""
}

func callableName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		return node.Sel.Name
	}
	return ""
}

// declaredRawMessageVars collects names declared `var x json.RawMessage` inside
// one function body.
func declaredRawMessageVars(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || vs.Type == nil || !isRawMessageSelector(vs.Type) {
			return true
		}
		for _, name := range vs.Names {
			out[name.Name] = true
		}
		return true
	})
	return out
}

// ── The per-site rule ──────────────────────────────────────────────────

// rawMessageOperandProblem returns "" when the operand can be shown to be a
// complete JSON document, and otherwise says why it cannot.
func rawMessageOperandProblem(e ast.Expr) string {
	if e == nil {
		return "" // decoder-validated fill; nothing is constructed at the site
	}
	switch node := e.(type) {
	case *ast.BasicLit:
		if node.Kind == token.STRING {
			return ""
		}
		return "a non-string literal cannot be a JSON document"
	case *ast.Ident:
		// A whole value passed through. See STATED BOUNDARY above.
		return ""
	case *ast.SelectorExpr:
		return "" // a whole field, same boundary as Ident
	case *ast.ParenExpr:
		return rawMessageOperandProblem(node.X)
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "operator " + node.Op.String() + " does not build a JSON document"
		}
		if p := rawMessageOperandProblem(node.X); p != "" {
			return p
		}
		return rawMessageOperandProblem(node.Y)
	case *ast.CallExpr:
		if isRawMessageSelector(node.Fun) && len(node.Args) == 1 {
			return rawMessageOperandProblem(node.Args[0])
		}
		name := callName(node.Fun)
		if _, ok := allowedRawMessageBuilders[name]; ok {
			return ""
		}
		if name == "" {
			name = "an unnamed call"
		}
		return "the value comes from " + name + ", which is not a known whole-document builder; " +
			"if it returns complete JSON, add it to allowedRawMessageBuilders with the reason"
	case *ast.SliceExpr:
		return "SLICING a payload: a prefix of a JSON document is not a JSON document. " +
			"This is the head_only defect — delete the key and report the head as a plain string instead"
	case *ast.IndexExpr:
		return "indexing yields one element, not a document"
	}
	return "unrecognized operand shape; this guard cannot show the value is a complete JSON document"
}

// ── Tests ──────────────────────────────────────────────────────────────

// TestRawMessageDirectConstructionSitesHoldCompleteDocuments is the production
// guard over all seven direct destination forms listed in the file header.
func TestRawMessageDirectConstructionSitesHoldCompleteDocuments(t *testing.T) {
	sites := rawMessageConstructionSites(t)

	// A zero would mean the walk broke, not that the packages are clean.
	if len(sites) == 0 {
		t.Fatal("enumerated zero json.RawMessage construction sites across " +
			strings.Join(rawMessageScannedPackages, " and ") +
			" — the AST walk is wrong, so a clean run would mean nothing")
	}

	for _, s := range sites {
		if problem := rawMessageOperandProblem(s.Operand); problem != "" {
			t.Errorf("%s: %s — %s", s.Pos, s.Form, problem)
		}
	}
}

// TestRawMessageDirectConstructionSitesAreEnumerated prints the finite list
// the guard above is built on, so a reviewer can see the sites rather than take
// the count on faith, and fails if the enumeration collapses.
//
// It asserts a FLOOR, not an exact count: an equality would have to be edited
// on every legitimate new audit-emit call, and a number nobody can justify
// gets bumped rather than read.
func TestRawMessageDirectConstructionSitesAreEnumerated(t *testing.T) {
	sites := rawMessageConstructionSites(t)
	for _, s := range sites {
		t.Logf("%s — %s", s.Pos, s.Form)
	}
	const floor = 20
	if len(sites) < floor {
		t.Fatalf("enumerated only %d json.RawMessage direct construction sites, below the floor of %d; "+
			"either a large amount of code was deleted or the walk stopped matching", len(sites), floor)
	}
}

// TestContextAPIRawMessageSitesAreClassified makes the HTTP population visible
// and non-vacuous. In particular it anchors the response-bearing PacketItem
// selector assignment and the request/store composites and audit arguments;
// those were the real syntax classes omitted by the original guard.
func TestContextAPIRawMessageSitesAreClassified(t *testing.T) {
	counts := map[string]int{}
	for _, site := range rawMessageConstructionSites(t) {
		if site.Package != "internal/contextapi" {
			continue
		}
		counts[site.Form]++
		t.Logf("%s — %s", site.Pos, site.Form)
	}
	for _, form := range []string{
		"json.RawMessage(...) conversion",
		"json.RawMessage composite-literal field",
		"assignment to a json.RawMessage selector field",
		"argument to a json.RawMessage parameter",
	} {
		if counts[form] == 0 {
			t.Errorf("enumerated no contextapi sites classified as %q; counts=%v", form, counts)
		}
	}
}

// TestRawMessageGuardCoversEveryDirectDestinationShape is a synthetic,
// deliberately bad package. Each of the four formerly omitted destinations
// receives the same sliced JSON prefix and must be both enumerated and rejected.
func TestRawMessageGuardCoversEveryDirectDestinationShape(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", `package fixture
import "encoding/json"
type envelope struct { Payload json.RawMessage }
func consume(payload json.RawMessage) {}
func bad(payload []byte) json.RawMessage {
	value := envelope{Payload: payload[:4]}
	value.Payload = payload[:4]
	consume(payload[:4])
	return payload[:4]
}`, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	catalog := rawMessageCatalog{
		structFields: map[string][]rawMessageField{},
		callParams:   map[string]map[int]bool{},
	}
	addRawMessageDeclarations(f, catalog)
	sites := rawMessageSitesInFile(fset, "fixture", f, catalog)

	wantForms := map[string]bool{
		"json.RawMessage composite-literal field":        false,
		"assignment to a json.RawMessage selector field": false,
		"argument to a json.RawMessage parameter":        false,
		"return as json.RawMessage result":               false,
	}
	for _, site := range sites {
		if _, wanted := wantForms[site.Form]; !wanted {
			continue
		}
		wantForms[site.Form] = true
		if problem := rawMessageOperandProblem(site.Operand); problem == "" {
			t.Errorf("%s was enumerated but the sliced operand passed", site.Form)
		}
	}
	for form, seen := range wantForms {
		if !seen {
			t.Errorf("did not enumerate %s; got %#v", form, sites)
		}
	}
}

// TestRawMessageGuardRejectsByteLevelTransformations proves the per-site rule
// is not a no-op, and anchors it in snippets written out by hand.
//
// These are literals on purpose. The rule is a whitelist derived from
// allowedRawMessageBuilders, so a check that consulted only that map would
// agree with itself whatever the map said. Each mustFlag case is a way the
// head_only defect could come back wearing different syntax.
func TestRawMessageGuardRejectsByteLevelTransformations(t *testing.T) {
	mustFlag := []struct {
		name string
		expr string
	}{
		{"the head_only defect verbatim", "json.RawMessage(payload[:maxBytes])"},
		{"slicing through a conversion", "json.RawMessage([]byte(string(payload)[:maxBytes]))"},
		{"truncation hidden behind a helper", "json.RawMessage(head(payload, maxBytes))"},
		{"append of a truncated prefix", "json.RawMessage(append(prefix, payload[:n]...))"},
		{"a bytes package truncation", "json.RawMessage(bytes.TrimRight(payload, suffix))"},
		{"indexing a payload", "json.RawMessage(payloads[0:2])"},
	}
	for _, tc := range mustFlag {
		t.Run("flag/"+tc.name, func(t *testing.T) {
			operand := parseConversionOperand(t, tc.expr)
			if problem := rawMessageOperandProblem(operand); problem == "" {
				t.Fatalf("guard accepted %q; it must not", tc.expr)
			}
		})
	}

	mustPass := []struct {
		name string
		expr string
	}{
		{"audit metadata from a format string", `json.RawMessage(fmt.Sprintf("{\"source\":%q}", src))`},
		{"a whole stored payload", "json.RawMessage(rr.rec.Payload)"},
		{"a constant document", "json.RawMessage(`{\"source\":\"mcp\"}`)"},
		{"a marshaled value", "json.RawMessage(json.Marshal(v))"},
		{"literal concatenation with a quoted string", `json.RawMessage("{\"reason\":" + quoteJSON(req.Reason) + "}")`},
	}
	for _, tc := range mustPass {
		t.Run("pass/"+tc.name, func(t *testing.T) {
			operand := parseConversionOperand(t, tc.expr)
			if problem := rawMessageOperandProblem(operand); problem != "" {
				t.Fatalf("guard false-positived on %q: %s", tc.expr, problem)
			}
		})
	}
}

// ── Helpers ────────────────────────────────────────────────────────────

func parseConversionOperand(t *testing.T, src string) ast.Expr {
	t.Helper()
	e, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("parse fixture %q: %v", src, err)
	}
	call, ok := e.(*ast.CallExpr)
	if !ok || !isRawMessageSelector(call.Fun) || len(call.Args) != 1 {
		t.Fatalf("fixture %q is not a single-argument json.RawMessage conversion", src)
	}
	return call.Args[0]
}

func isRawMessageSelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "RawMessage" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "json"
}

// callName renders a call's function as "pkg.Fn" or "Fn"; anything else
// (a method value, a func literal) renders empty, which the rule treats as
// unrecognized.
func callName(e ast.Expr) string {
	switch fn := e.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
	}
	return ""
}

func parseGoDir(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("parsed zero non-test files in %s", dir)
	}
	return fset, files
}

func position(fset *token.FileSet, pkg string, p token.Pos) string {
	pos := fset.Position(p)
	return pkg + "/" + filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
}

// moduleRoot walks up from this package's working directory to the go.mod, and
// verifies it, so a moved test file fails loudly instead of scanning nothing.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q has no go.mod (%v) — this test's relative path is stale", root, err)
	}
	return root
}

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
// Read "WHAT THIS GUARD COVERS, AND WHAT IT DOES NOT" below before relying on
// a green run here: the covered set is three destination shapes, not all seven.
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
// toolJSON discards that error — so an oversized record came back as an EMPTY
// tool result presented as success. See
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
// WHAT THIS GUARD COVERS, AND WHAT IT DOES NOT
//
// COVERED — three destination shapes, enumerated and then checked:
//
//	json.RawMessage(x)                 the explicit conversion
//	var x json.RawMessage; x = expr    assignment to a declared variable
//	json.Unmarshal(b, &x)              decoder fill of a declared variable
//
// NOT COVERED — four more destination shapes, all of them real:
//
//	writeRequest{Payload: payload[:n]}                  composite-literal field
//	req.Payload = payload[:n]                           selector on the left
//	func f() json.RawMessage { return payload[:n] }     return statement
//	EmitWrite(..., payload[:n])                         json.RawMessage parameter
//
// THE MECHANISM, because it is the part a future reader needs: json.RawMessage's
// underlying type is []byte, so Go requires NO CONVERSION to reach one. The
// conversion rule above never fires on any of the four, and the assignment rule
// needs an *ast.Ident on the left, which rules out the field cases. Nothing at
// those destinations spells `json.RawMessage`, so an AST walk keyed on that
// spelling cannot see them.
//
// THIS IS NOT THE DATAFLOW GAP BELOW, and conflating the two sends you looking
// in the wrong place. No dataflow is involved in any of the four: the operand is
// built right there at the site. The guard misses them for a purely syntactic
// reason.
//
// THE POPULATION IS NOT EMPTY. internal/contextapi declares SEVEN
// json.RawMessage struct fields, and it is the package writeJSON serves:
//
//	w.WriteHeader(code)
//	_ = json.NewEncoder(w).Encode(value)
//
// The status is committed BEFORE the encode runs, so a fragment there yields a
// 200 OK with a truncated body and no remaining opportunity to signal anything
// — strictly worse than the toolJSON case, which at least fails before sending.
//
// OWNER: CW-20260826-0017, "the response path discards its serialization
// error", covering both doors with header-ordering as acceptance. The
// enumeration for these four shapes belongs beside that fix, not duplicated
// here, because the ordering constraint shapes it.
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
	Form    string // how the value is built, for the report
	Operand ast.Expr
}

// ── The enumeration ────────────────────────────────────────────────────

// rawMessageConstructionSites walks the scanned packages and returns the sites
// covered by the three shapes listed at the top of this file — NOT every site
// where a json.RawMessage value is produced. The four shapes it does not reach,
// and why, are stated there; CW-20260826-0017 owns closing them.
//
// Struct field and parameter DECLARATIONS are not sites under any reading: they
// say where such a value may live, not what goes into it.
func rawMessageConstructionSites(t *testing.T) []rawMessageSite {
	t.Helper()
	root := moduleRoot(t)

	var sites []rawMessageSite
	for _, pkg := range rawMessageScannedPackages {
		dir := filepath.Join(root, pkg)
		fset, files := parseGoDir(t, dir)
		for _, f := range files {
			// (1) Explicit conversions, anywhere in the file. Scope is
			// irrelevant: json.RawMessage(x) constructs one wherever it sits.
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isRawMessageSelector(call.Fun) || len(call.Args) != 1 {
					return true
				}
				sites = append(sites, rawMessageSite{
					Pos:     position(fset, pkg, call.Pos()),
					Form:    "json.RawMessage(...) conversion",
					Operand: call.Args[0],
				})
				return true
			})

			// (2) and (3) are about a NAMED variable of the type, so they are
			// walked per function. File scope would collide: handleTypedWrite
			// declares `var payload json.RawMessage` while handleSessionWrite
			// has an unrelated `payload := map[string]any{}` two hundred lines
			// away, and treating those as the same variable flags the wrong one.
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				declared := declaredRawMessageVars(fn.Body)
				if len(declared) == 0 {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.CallExpr:
						// json.Unmarshal(b, &x): the decoder validates before
						// it fills, so nothing is constructed at the site.
						if callName(node.Fun) == "json.Unmarshal" && len(node.Args) == 2 {
							if u, ok := node.Args[1].(*ast.UnaryExpr); ok && u.Op == token.AND {
								if id, ok := u.X.(*ast.Ident); ok && declared[id.Name] {
									sites = append(sites, rawMessageSite{
										Pos:     position(fset, pkg, node.Pos()),
										Form:    "json.Unmarshal fill of a declared json.RawMessage",
										Operand: nil,
									})
								}
							}
						}
					case *ast.AssignStmt:
						for i, lhs := range node.Lhs {
							id, ok := lhs.(*ast.Ident)
							if !ok || !declared[id.Name] {
								continue
							}
							operand := ast.Expr(nil)
							switch {
							case len(node.Rhs) == len(node.Lhs):
								operand = node.Rhs[i]
							case len(node.Rhs) == 1:
								operand = node.Rhs[0]
							}
							sites = append(sites, rawMessageSite{
								Pos:     position(fset, pkg, node.Pos()),
								Form:    "assignment to a declared json.RawMessage variable",
								Operand: operand,
							})
						}
					}
					return true
				})
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Pos < sites[j].Pos })
	return sites
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

// TestRawMessageConversionsAndDeclaredVarsHoldCompleteDocuments is the guard.
//
// The name is the coverage claim and it is deliberately narrower than
// "construction sites": it covers json.RawMessage CONVERSIONS and DECLARED
// VARIABLES. Four other destination shapes are out of its reach — see the
// header, and CW-20260826-0017.
func TestRawMessageConversionsAndDeclaredVarsHoldCompleteDocuments(t *testing.T) {
	sites := rawMessageConstructionSites(t)

	// A zero would mean the walk broke, not that the packages are clean.
	if len(sites) == 0 {
		t.Fatal("enumerated zero json.RawMessage conversion or declared-variable sites across " +
			strings.Join(rawMessageScannedPackages, " and ") +
			" — the AST walk is wrong, so a clean run would mean nothing")
	}

	for _, s := range sites {
		if problem := rawMessageOperandProblem(s.Operand); problem != "" {
			t.Errorf("%s: %s — %s", s.Pos, s.Form, problem)
		}
	}
}

// TestRawMessageConversionAndDeclaredVarSitesAreEnumerated prints the finite list
// the guard above is built on, so a reviewer can see the sites rather than take
// the count on faith, and fails if the enumeration collapses. It is the list for
// the THREE covered shapes only.
//
// It asserts a FLOOR, not an exact count: an equality would have to be edited
// on every legitimate new audit-emit call, and a number nobody can justify
// gets bumped rather than read.
func TestRawMessageConversionAndDeclaredVarSitesAreEnumerated(t *testing.T) {
	sites := rawMessageConstructionSites(t)
	for _, s := range sites {
		t.Logf("%s — %s", s.Pos, s.Form)
	}
	const floor = 20
	if len(sites) < floor {
		t.Fatalf("enumerated only %d json.RawMessage conversion and declared-variable sites, below the floor of %d; "+
			"either a large amount of code was deleted or the walk stopped matching", len(sites), floor)
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

package mcpadapter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/server"
)

// Error-code guards for the MCP tool surface. See errorcodes.go for why the
// codes are constants and — importantly — for what a constant does NOT prove.
//
// Everything here is DERIVED. The constants come from this package's own AST,
// not from a list restated in the test, so adding a code is one edit and the
// guards pick it up. The one thing deliberately NOT derived is the fixture set
// in TestErrorCodeDescriptionGuardCatchesAnUndefinedCode: those literals are
// stated independently so that rewording the vocabulary cannot reword the
// assertion along with it.

// ── AST plumbing ───────────────────────────────────────────────────────

// packageFiles parses every non-test .go file in this package.
//
// Test files are excluded on purpose: this very file names undefined codes in
// its fixtures, and scanning it would make the guard flag its own controls.
func packageFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("parsed zero non-test files in this package — the walk is wrong, not the code")
	}
	return fset, files
}

// definedErrorCodes returns constName → wire value for every constant declared
// with an explicit `errorCode` type in this package.
func definedErrorCodes(t *testing.T) map[string]string {
	t.Helper()
	_, files := packageFiles(t)
	out := map[string]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				ident, ok := vs.Type.(*ast.Ident)
				if !ok || ident.Name != "errorCode" {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Errorf("%s: errorCode constant is not a string literal", name.Name)
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Errorf("%s: cannot unquote %s: %v", name.Name, lit.Value, err)
						continue
					}
					out[name.Name] = v
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found zero errorCode constants — the AST walk stopped matching, so a clean run here would mean nothing")
	}
	return out
}

// toolErrorCallSite is one `toolError(...)` invocation.
type toolErrorCallSite struct {
	Pos     string // file:line
	CodeArg ast.Expr
}

func toolErrorCallSites(t *testing.T) []toolErrorCallSite {
	t.Helper()
	fset, files := packageFiles(t)
	var out []toolErrorCallSite
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "toolError" || len(call.Args) == 0 {
				return true
			}
			p := fset.Position(call.Pos())
			out = append(out, toolErrorCallSite{
				Pos:     filepath.Base(p.Filename) + ":" + strconv.Itoa(p.Line),
				CodeArg: call.Args[0],
			})
			return true
		})
	}
	return out
}

// ── Emission-side guards ───────────────────────────────────────────────

// TestEveryToolErrorCallSitePassesAnErrorCodeConstant is the guard that makes
// "a code that does not exist" unrepresentable at an emission site: the
// compiler rejects an undefined identifier, and this rejects a string literal
// that would slip past the compiler because errorCode's underlying type is
// string.
func TestEveryToolErrorCallSitePassesAnErrorCodeConstant(t *testing.T) {
	defined := definedErrorCodes(t)
	sites := toolErrorCallSites(t)

	// A zero here would be a broken walk, not a clean package.
	if len(sites) == 0 {
		t.Fatal("found zero toolError call sites — the AST walk is wrong, so a clean run would mean nothing")
	}

	for _, s := range sites {
		ident, ok := s.CodeArg.(*ast.Ident)
		if !ok {
			t.Errorf("%s: toolError's code argument is not an identifier; pass one of the errorCode constants from errorcodes.go", s.Pos)
			continue
		}
		if _, ok := defined[ident.Name]; !ok {
			t.Errorf("%s: toolError's code argument %q is not a declared errorCode constant", s.Pos, ident.Name)
		}
	}
}

// TestEveryErrorCodeConstantIsUsedAtAToolErrorCallSite is the other direction:
// a constant nothing emits is dead vocabulary that will eventually be copied
// into a description.
//
// NAME IS THE CLAIM: this checks that a constant appears at a call site in this
// package's source. It does NOT establish that the call site is reachable at
// run time, and it does not establish that the code is the right one for the
// condition it is emitted for.
func TestEveryErrorCodeConstantIsUsedAtAToolErrorCallSite(t *testing.T) {
	defined := definedErrorCodes(t)
	used := map[string]struct{}{}
	for _, s := range toolErrorCallSites(t) {
		if ident, ok := s.CodeArg.(*ast.Ident); ok {
			used[ident.Name] = struct{}{}
		}
	}
	var unused []string
	for name := range defined {
		if _, ok := used[name]; !ok {
			unused = append(unused, name+" ("+defined[name]+")")
		}
	}
	sort.Strings(unused)
	for _, u := range unused {
		t.Errorf("errorCode constant %s is never passed to toolError — emit it or delete it; "+
			"an unemitted code is a name a description can advertise and no caller will ever see", u)
	}
}

// ── Description-side guard ─────────────────────────────────────────────

// codeTokenRE matches a lower-snake_case identifier with at least one
// underscore — the shape every error code has.
var codeTokenRE = regexp.MustCompile(`[a-z][a-z0-9]*(?:_[a-z0-9]+)+`)

// nonCodeVocabulary lists code-SHAPED tokens that appear in shipped
// descriptions and are not error codes. Each entry says what it is instead.
//
// Membership is checked: TestNonCodeVocabularyIsCurrent fails when an entry
// stops appearing, so this cannot become a place a real violation hides.
var nonCodeVocabulary = map[string]string{
	"revision_scope": "argument on context_view and tesseract_recall (current|timeline); shares the trailing segment of insufficient_scope",
}

// suspectedErrorCodes returns tokens in text that look like error codes but are
// not defined ones.
//
// THE RECOGNITION RULE IS DERIVED, not hand-listed: a token is code-shaped when
// its trailing segment is the trailing segment of some DEFINED code. That is
// what makes it catch a code nobody defined — `service_unavailable` ends in
// `unavailable`, which `domain_unavailable` and `embedding_unavailable`
// contribute — while a hand-written suffix list would have to be guessed at and
// would rot as the vocabulary moves.
//
// STATED BOUNDARY: a fabricated code whose trailing segment matches nothing in
// the vocabulary (say `quota_exceeded`) is NOT caught. Widening the rule to
// every snake_case token would flag most argument names, which is how an
// allowlist grows past the size anyone reads.
func suspectedErrorCodes(text string, defined map[string]string, exempt map[string]struct{}) []string {
	values := map[string]struct{}{}
	tails := map[string]struct{}{}
	for _, v := range defined {
		values[v] = struct{}{}
		segs := strings.Split(v, "_")
		tails[segs[len(segs)-1]] = struct{}{}
	}

	seen := map[string]struct{}{}
	var out []string
	for _, tok := range codeTokenRE.FindAllString(text, -1) {
		if _, ok := seen[tok]; ok {
			continue
		}
		if _, ok := values[tok]; ok {
			continue // a defined code, named correctly
		}
		if _, ok := exempt[tok]; ok {
			continue
		}
		if _, ok := nonCodeVocabulary[tok]; ok {
			continue
		}
		segs := strings.Split(tok, "_")
		if _, ok := tails[segs[len(segs)-1]]; !ok {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// registeredToolText returns, per registered tool, its description followed by
// every parameter description.
//
// Parameter descriptions are IN scope here, unlike in the tool-name drift
// guard. That is deliberate and it is where the motivating defect lived:
// CW-20260825-0006 advertised `service_unavailable` in the `similarity_min`
// PARAMETER description, not in the tool description.
func registeredToolText(t *testing.T) map[string]string {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = knowledge.New(ms)

	srv := server.NewMCPServer("errorcode-guard", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	out := map[string]string{}
	for name, st := range srv.ListTools() {
		var b strings.Builder
		b.WriteString(st.Tool.Description)
		for _, raw := range st.Tool.InputSchema.Properties {
			prop, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if desc, ok := prop["description"].(string); ok {
				b.WriteString("\n")
				b.WriteString(desc)
			}
		}
		out[name] = b.String()
	}
	if len(out) == 0 {
		t.Fatal("registered zero tools — the adapter is not wired, so a clean result here would be meaningless")
	}
	return out
}

// argumentNames returns each registered tool's own parameter names, which are
// snake_case and would otherwise collide with the recognition rule.
func argumentNames(t *testing.T) map[string]map[string]struct{} {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, "")
	a.MemoryStore = ms
	a.KnowledgeStore = knowledge.New(ms)

	srv := server.NewMCPServer("errorcode-guard-args", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	out := map[string]map[string]struct{}{}
	for name, st := range srv.ListTools() {
		args := map[string]struct{}{}
		for arg := range st.Tool.InputSchema.Properties {
			args[arg] = struct{}{}
		}
		out[name] = args
	}
	return out
}

// TestToolDescriptionsNameOnlyDefinedErrorCodes is the check AC6 asks for:
// a shipped description may not advertise a code no constant defines.
func TestToolDescriptionsNameOnlyDefinedErrorCodes(t *testing.T) {
	defined := definedErrorCodes(t)
	texts := registeredToolText(t)
	args := argumentNames(t)

	names := make([]string, 0, len(texts))
	for name := range texts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for _, tok := range suspectedErrorCodes(texts[name], defined, args[name]) {
			t.Errorf("tool %q names %q where an error code is expected, and no errorCode constant defines it.\n"+
				"    Either the description is advertising a code no path emits (the CW-20260825-0006 defect), "+
				"or the token is not a code at all — in which case add it to nonCodeVocabulary with a note saying what it is.",
				name, tok)
		}
	}
}

// TestErrorCodeDescriptionGuardCatchesAnUndefinedCode proves the guard above is
// not a no-op, and anchors it in literals that do NOT move when the vocabulary
// moves.
//
// This matters more than usual here. The guard derives its recognition rule
// from the constants, so a check written only against the constants would agree
// with itself no matter what they said. These fixtures are stated by hand.
func TestErrorCodeDescriptionGuardCatchesAnUndefinedCode(t *testing.T) {
	defined := definedErrorCodes(t)
	none := map[string]struct{}{}

	mustFlag := []struct {
		name string
		text string
		want string
	}{
		{
			// The historical defect, verbatim in shape: CW-20260825-0006's
			// similarity_min description advertised a code the path never emits.
			name: "the CW-20260825-0006 defect",
			text: "It is a service_unavailable error when no embedder is configured.",
			want: "service_unavailable",
		},
		{
			name: "invented failure code",
			text: "Returns index_failed when the vector index rejects the write.",
			want: "index_failed",
		},
		{
			name: "invented not-permitted code",
			text: "Answers write_not_permitted if the token forbids it.",
			want: "write_not_permitted",
		},
		{
			name: "invented required code",
			text: "A missing header is a token_required.",
			want: "token_required",
		},
	}
	for _, tc := range mustFlag {
		t.Run(tc.name, func(t *testing.T) {
			got := suspectedErrorCodes(tc.text, defined, none)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("guard did not flag %q; got %v", tc.want, got)
			}
		})
	}

	mustPass := []struct {
		name string
		text string
	}{
		{"defined code", "Passing both is a validation_error rather than a silently ignored knob."},
		{"defined code with a different tail", "Answers domain_unavailable when no store is wired."},
		{"not_found as a response key", "Answers `{touched, not_found}` — not_found lists revision IDs that resolved to nothing."},
		{"argument name with no code tail", "Set payload_mode to keys, summary or full."},
		{"allowlisted non-code", "revision_scope is current or timeline."},
	}
	for _, tc := range mustPass {
		t.Run(tc.name, func(t *testing.T) {
			if got := suspectedErrorCodes(tc.text, defined, none); len(got) != 0 {
				t.Fatalf("guard false-positived on %q: %v", tc.text, got)
			}
		})
	}

	// The blind spot, asserted rather than only described. A fabricated code
	// whose trailing segment matches nothing in the vocabulary is invisible to
	// the recognition rule. Pinning it here means that widening the rule fails
	// this case and forces the doc comment on suspectedErrorCodes to be
	// re-read, instead of leaving a stale "STATED BOUNDARY" paragraph behind.
	knownBlindSpots := []string{
		"A shortage is a quota_exceeded.",
		"The upstream call ends in a gateway_timeout.",
	}
	for _, text := range knownBlindSpots {
		if got := suspectedErrorCodes(text, defined, none); len(got) != 0 {
			t.Errorf("the recognition rule now flags %q (%v) — that is an improvement, "+
				"but the STATED BOUNDARY paragraph on suspectedErrorCodes is now wrong; update it and move this case to mustFlag",
				text, got)
		}
	}
}

// TestNonCodeVocabularyIsCurrent keeps the exemption list from becoming a place
// drift hides — the same rule the tool-name allowlist lives under.
func TestNonCodeVocabularyIsCurrent(t *testing.T) {
	defined := definedErrorCodes(t)
	values := map[string]struct{}{}
	for _, v := range defined {
		values[v] = struct{}{}
	}

	var corpus strings.Builder
	for _, text := range registeredToolText(t) {
		corpus.WriteString(text)
		corpus.WriteString("\n")
	}
	seen := map[string]struct{}{}
	for _, tok := range codeTokenRE.FindAllString(corpus.String(), -1) {
		seen[tok] = struct{}{}
	}

	for token, why := range nonCodeVocabulary {
		if _, ok := values[token]; ok {
			t.Errorf("nonCodeVocabulary entry %q is now a defined error code — drop it (was: %s)", token, why)
		}
		if _, ok := seen[token]; !ok {
			t.Errorf("nonCodeVocabulary entry %q no longer appears in any registered tool or parameter description — "+
				"remove it so it cannot mask a future violation (was: %s)", token, why)
		}
	}
}

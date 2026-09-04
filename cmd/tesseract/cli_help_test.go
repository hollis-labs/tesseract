package main

import (
	"bytes"
	"context"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextcli"
)

// hermeticXDGRoot pins all four $XDG_*_HOME roots (and the two TESSERACT_*
// overrides that would otherwise win) into a fresh temp directory, and returns
// it. Unlike hermeticLayout it does NOT resolve the layout, because resolution
// materializes the directories — and these tests exist to prove that nothing
// materializes them.
func hermeticXDGRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("TESSERACT_DB_PATH", "")
	t.Setenv("TESSERACT_WORKSPACE", "")
	return base
}

// captureRun invokes run() with real files for stdout and stderr — run takes
// *os.File — and returns what each received. The files live in their own temp
// directory so they cannot be mistaken for a side effect on the XDG root.
func captureRun(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.CreateTemp(dir, "stdout-*")
	if err != nil {
		t.Fatalf("create stdout temp: %v", err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(dir, "stderr-*")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	defer errFile.Close()

	code = run(context.Background(), args, outFile, errFile)

	read := func(f *os.File) string {
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatalf("seek: %v", err)
		}
		buf := &bytes.Buffer{}
		if _, err := buf.ReadFrom(f); err != nil {
			t.Fatalf("read: %v", err)
		}
		return buf.String()
	}
	return code, read(outFile), read(errFile)
}

// entries lists everything under root, so a test can assert that a read-only
// command left it untouched.
func entries(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			found = append(found, strings.TrimPrefix(path, root))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// TestHelpAndVersionHaveNoFilesystemSideEffects is the point of the whole
// change. run() used to cold-boot before it looked at the arguments — OTel,
// layout materialization, config load, then contextstore.Open, which creates
// the records and index directories, the SQLite database, and runs every
// migration. A bare `tesseract` therefore built a user's entire data layout as
// a side effect of asking what the command was, and then exited 1.
func TestHelpAndVersionHaveNoFilesystemSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"bare", nil, 0, "usage:"},
		{"help-word", []string{"help"}, 0, "usage:"},
		{"help-short", []string{"-h"}, 0, "usage:"},
		{"help-long", []string{"--help"}, 0, "usage:"},
		{"version-word", []string{"version"}, 0, "tesseract "},
		{"version-long", []string{"--version"}, 0, "tesseract "},
		{"unknown-command", []string{"frobnicate"}, 1, ""},
		{"serve-help", []string{"serve", "--help"}, 0, "-addr"},
		{"mcp-help", []string{"mcp", "--help"}, 0, "-token"},
		{"context-help", []string{"context", "--help"}, 0, "context subcommands:"},
		{"context-put-help", []string{"context", "put", "--help"}, 0, "-namespace"},
		{"bare-context", []string{"context"}, 1, ""},
		{"verify-pointers-help", []string{"verify-pointers", "--help"}, 0, "-concurrency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hermeticXDGRoot(t)
			code, stdout, stderr := captureRun(t, tc.args)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d (stdout %q, stderr %q)", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantOut != "" && !strings.Contains(stdout, tc.wantOut) {
				t.Fatalf("stdout does not contain %q:\n%s", tc.wantOut, stdout)
			}
			if got := entries(t, root); len(got) != 0 {
				t.Fatalf("%v created %d filesystem entries: %v", tc.args, len(got), got)
			}
		})
	}
}

// TestUnknownCommandNamesItself — `tesseract frobnicate` used to print the
// generic context usage line, which said nothing about what was wrong.
func TestUnknownCommandNamesItself(t *testing.T) {
	hermeticXDGRoot(t)
	code, _, stderr := captureRun(t, []string{"frobnicate"})
	if code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Fatalf("stderr does not name the command: %s", stderr)
	}
	if !strings.Contains(stderr, "tesseract --help") {
		t.Fatalf("stderr does not point at help: %s", stderr)
	}
}

// TestTopLevelHelpListsEveryCommand — help must cover both levels, and must
// say that the second level exists at all. `tesseract put ...` looks like it
// should work and does not.
func TestTopLevelHelpListsEveryCommand(t *testing.T) {
	buf := &bytes.Buffer{}
	printUsage(buf)
	got := buf.String()

	for _, cmd := range topLevelCommands() {
		if !strings.Contains(got, cmd.Name) {
			t.Errorf("top-level help omits %q", cmd.Name)
		}
		if cmd.Summary == "" || cmd.Description == "" {
			t.Errorf("%q has an empty summary or description", cmd.Name)
		}
	}
	for _, cmd := range contextcli.Commands() {
		if !strings.Contains(got, cmd.Name) {
			t.Errorf("top-level help omits context subcommand %q", cmd.Name)
		}
	}
	for _, want := range []string{"tesseract context put", "help", "version"} {
		if !strings.Contains(got, want) {
			t.Errorf("top-level help omits %q:\n%s", want, got)
		}
	}
}

// TestSubcommandHelpPrintsEveryFlag — `serve --help` used to return
// flag.ErrHelp through the generic error path and print
// "error: flag: help requested" with exit 1.
func TestSubcommandHelpPrintsEveryFlag(t *testing.T) {
	hermeticXDGRoot(t)
	code, stdout, stderr := captureRun(t, []string{"serve", "--help"})
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	fs, _ := newServeFlagSet()
	fs.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(stdout, "-"+f.Name) {
			t.Errorf("serve --help omits -%s:\n%s", f.Name, stdout)
		}
	})
	if strings.Contains(stdout, "help requested") || strings.Contains(stderr, "help requested") {
		t.Errorf("help still surfaces as an error: %s%s", stdout, stderr)
	}
}

// TestTopLevelCommandsMatchDispatch keeps the command table and run()'s
// dispatch from drifting apart — a command in the table that run() never
// routes would be advertised and then rejected as unknown.
func TestTopLevelCommandsMatchDispatch(t *testing.T) {
	// The tokens run() answers before it consults the table.
	pseudo := map[string]bool{
		"": true, "help": true, "-h": true, "-help": true, "--help": true,
		"version": true, "-version": true, "--version": true,
	}

	routed := map[string]bool{}
	for _, lit := range commandLiteralsIn(t, "main.go", "run") {
		if !pseudo[lit] {
			routed[lit] = true
		}
	}

	documented := map[string]bool{}
	for _, cmd := range topLevelCommands() {
		documented[cmd.Name] = true
		if !routed[cmd.Name] {
			t.Errorf("%q is documented but run() never dispatches it", cmd.Name)
		}
	}
	for name := range routed {
		if !documented[name] {
			t.Errorf("run() dispatches %q but the command table omits it", name)
		}
	}
}

// TestDocumentedFlagsMatchSource covers the commands whose flagsets are built
// in files this one does not own, so PrintDefaults is out of reach and the
// help text has to repeat the flag names. This is what stops the repetition
// going stale.
func TestDocumentedFlagsMatchSource(t *testing.T) {
	for _, cmd := range topLevelCommands() {
		if cmd.FlagsSource == "" {
			continue
		}
		t.Run(cmd.Name, func(t *testing.T) {
			defined := map[string]bool{}
			for _, name := range flagsDefinedIn(t, cmd.FlagsSource) {
				defined[name] = true
			}
			documented := map[string]bool{}
			for _, line := range cmd.Flags {
				name := strings.TrimPrefix(strings.Fields(strings.TrimSpace(line))[0], "-")
				documented[name] = true
				if !defined[name] {
					t.Errorf("help documents -%s, but %s does not define it", name, cmd.FlagsSource)
				}
			}
			for name := range defined {
				if !documented[name] {
					t.Errorf("%s defines -%s, but `tesseract %s --help` does not list it", cmd.FlagsSource, name, cmd.Name)
				}
			}
		})
	}
}

// TestDocumentedSubcommandsMatchSource does the same for the commands whose
// sub-verbs are dispatched elsewhere.
func TestDocumentedSubcommandsMatchSource(t *testing.T) {
	for _, cmd := range topLevelCommands() {
		if cmd.SubcommandsSource == "" {
			continue
		}
		t.Run(cmd.Name, func(t *testing.T) {
			routed := map[string]bool{}
			for _, verb := range caseLiteralsIn(t, cmd.SubcommandsSource, cmd.SubcommandsFunc) {
				routed[verb] = true
			}
			for _, verb := range cmd.Subcommands {
				if !routed[verb] {
					t.Errorf("help lists %q, but %s does not route it", verb, cmd.SubcommandsSource)
				}
				delete(routed, verb)
			}
			for verb := range routed {
				t.Errorf("%s routes %q, but the help table omits it", cmd.SubcommandsSource, verb)
			}
		})
	}
}

// TestBuildVersionPrefersTheStamp — a release build's -ldflags value wins.
func TestBuildVersionPrefersTheStamp(t *testing.T) {
	previous := version
	t.Cleanup(func() { version = previous })
	version = "v9.9.9"
	if got := buildVersion(); got != "v9.9.9" {
		t.Fatalf("buildVersion() = %q, want the stamped value", got)
	}
}

// TestBuildVersionFallsBackToBuildInfo — an unstamped build still reports
// something specific, rather than a literal that would rot. Whatever it is, it
// must be a single token so `tesseract --version` stays parseable.
func TestBuildVersionFallsBackToBuildInfo(t *testing.T) {
	previous := version
	t.Cleanup(func() { version = previous })
	version = ""
	got := buildVersion()
	if got == "" || strings.ContainsAny(got, " \t\n") {
		t.Fatalf("buildVersion() = %q, want a single non-empty token", got)
	}
}

// ---- source inspection helpers ----

// commandLiteralsIn returns every string literal the named function compares
// the command name against — the case clauses of `switch cmd` and the right
// side of `cmd == "..."`.
func commandLiteralsIn(t *testing.T, file, funcName string) []string {
	t.Helper()
	fn := findFunc(t, file, funcName)
	var lits []string
	ast.Inspect(fn, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SwitchStmt:
			if ident, ok := node.Tag.(*ast.Ident); !ok || ident.Name != "cmd" {
				return true
			}
			for _, stmt := range node.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						lits = append(lits, strings.Trim(lit.Value, `"`))
					}
				}
			}
		case *ast.BinaryExpr:
			if node.Op != token.EQL {
				return true
			}
			ident, ok := node.X.(*ast.Ident)
			if !ok || ident.Name != "cmd" {
				return true
			}
			if lit, ok := node.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				lits = append(lits, strings.Trim(lit.Value, `"`))
			}
		}
		return true
	})
	if len(lits) == 0 {
		t.Fatalf("no command literals found in %s", funcName)
	}
	return lits
}

// flagsDefinedIn returns the flag names a file registers on a flagset.
func flagsDefinedIn(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	registrars := map[string]bool{
		"String": true, "Bool": true, "Int": true, "Int64": true,
		"Uint": true, "Float64": true, "Duration": true, "Func": true, "Var": true,
	}
	var names []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !registrars[sel.Sel.Name] {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "fs" {
			return true
		}
		arg := call.Args[0]
		if sel.Sel.Name == "Var" && len(call.Args) > 1 {
			arg = call.Args[1]
		}
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			names = append(names, strings.Trim(lit.Value, `"`))
		}
		return true
	})
	if len(names) == 0 {
		t.Fatalf("no flags found in %s", file)
	}
	return names
}

// caseLiteralsIn returns every case-clause string literal in one function.
func caseLiteralsIn(t *testing.T, file, funcName string) []string {
	t.Helper()
	var lits []string
	ast.Inspect(findFunc(t, file, funcName), func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				lits = append(lits, strings.Trim(lit.Value, `"`))
			}
		}
		return true
	})
	return lits
}

func findFunc(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("func %s not found in %s", name, file)
	return nil
}

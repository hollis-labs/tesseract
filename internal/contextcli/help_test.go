package contextcli

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// switchCases returns the string literals of the case clauses of the first
// switch statement in the named function.
func switchCases(t *testing.T, file, funcName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}
		var cases []string
		var found bool
		ast.Inspect(fn, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || found {
				return !found
			}
			found = true
			for _, stmt := range sw.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					cases = append(cases, strings.Trim(lit.Value, `"`))
				}
			}
			return false
		})
		if !found {
			t.Fatalf("%s in %s has no switch statement", funcName, file)
		}
		return cases
	}
	t.Fatalf("func %s not found in %s", funcName, file)
	return nil
}

// dispatchTargets maps each case literal of dispatch's switch to the handler
// method it calls, e.g. "namespace" -> "runNamespace".
func dispatchTargets(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "cli.go", nil, 0)
	if err != nil {
		t.Fatalf("parse cli.go: %v", err)
	}
	targets := map[string]string{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			continue
		}
		sw, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.SwitchStmt)
		if !ok {
			t.Fatalf("dispatch does not end in a switch")
		}
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok || len(clause.List) != 1 {
				continue
			}
			lit, ok := clause.List[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			verb := strings.Trim(lit.Value, `"`)
			ast.Inspect(clause, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				targets[verb] = sel.Sel.Name
				return false
			})
		}
	}
	if len(targets) == 0 {
		t.Fatal("no dispatch targets found")
	}
	return targets
}

// TestCommandsMatchDispatch is the anti-drift check for the help table. The
// usage string this table replaced had gone stale in both directions: it named
// the wrong binary and listed 15 of the 26 subcommands the switch routes.
func TestCommandsMatchDispatch(t *testing.T) {
	cases := switchCases(t, "cli.go", "dispatch")

	documented := make([]string, 0, len(Commands()))
	for _, cmd := range Commands() {
		documented = append(documented, cmd.Name)
	}

	if len(cases) != len(documented) {
		t.Fatalf("dispatch routes %d subcommands, Commands() documents %d\n dispatch: %v\n documented: %v",
			len(cases), len(documented), cases, documented)
	}
	for i := range cases {
		if cases[i] != documented[i] {
			t.Fatalf("subcommand %d differs: dispatch has %q, Commands() has %q", i, cases[i], documented[i])
		}
	}
}

// TestGroupSubcommandsMatchDispatch keeps the sub-verb lists in the table
// honest against the group handlers' own switches.
func TestGroupSubcommandsMatchDispatch(t *testing.T) {
	targets := dispatchTargets(t)
	for _, cmd := range Commands() {
		if len(cmd.Subcommands) == 0 {
			continue
		}
		handler, ok := targets[cmd.Name]
		if !ok {
			t.Fatalf("%s is not dispatched", cmd.Name)
		}
		routed := switchCases(t, "cli.go", handler)
		got := map[string]bool{}
		for _, verb := range routed {
			got[verb] = true
		}
		want := map[string]bool{}
		for _, verb := range cmd.Subcommands {
			want[verb] = true
			if !got[verb] {
				t.Errorf("context %s: documented sub-verb %q is not routed by %s", cmd.Name, verb, handler)
			}
		}
		for _, verb := range routed {
			if !want[verb] {
				t.Errorf("context %s: %s routes %q but the help table omits it", cmd.Name, handler, verb)
			}
		}
	}
}

// TestHelpNeedsNoStore walks every subcommand and sub-verb through the help
// path against a CLI with no store. It is the guard on the trick Help relies
// on: a handler builds its flagset and parses before it touches c.Store, so
// `--help` unwinds without dereferencing nil. A handler that reaches for the
// store first — the way types, views and ttl-cleanup do — must be marked
// NoFlags, and this test is what says so.
func TestHelpNeedsNoStore(t *testing.T) {
	for _, cmd := range Commands() {
		lines := [][]string{{cmd.Name, "--help"}}
		for _, sub := range cmd.Subcommands {
			lines = append(lines, []string{cmd.Name, sub, "--help"})
		}
		for _, verbs := range lines {
			t.Run(strings.Join(verbs, " "), func(t *testing.T) {
				stdout := &bytes.Buffer{}
				stderr := &bytes.Buffer{}
				code, handled := Help(context.Background(), stdout, stderr, verbs)
				if !handled {
					t.Fatalf("help request not handled")
				}
				if code != 0 {
					t.Fatalf("exit %d, stderr: %s", code, stderr.String())
				}
				if stdout.Len() == 0 {
					t.Fatal("no help output")
				}
				if stderr.Len() != 0 {
					t.Fatalf("unexpected stderr: %s", stderr.String())
				}
				if !strings.Contains(stdout.String(), "tesseract context "+cmd.Name) {
					t.Fatalf("help does not name the command:\n%s", stdout.String())
				}
			})
		}
	}
}

// TestSubcommandHelpPrintsItsFlags pins the behavior that was broken: the
// flagsets discard their output, so `--help` came back as flag.ErrHelp and was
// reported as "error: flag: help requested" with exit 1 and no flag list.
func TestSubcommandHelpPrintsItsFlags(t *testing.T) {
	for _, tc := range []struct {
		verbs []string
		flag  string
	}{
		{[]string{"put", "--help"}, "-namespace"},
		{[]string{"audit", "-h"}, "-event-type"},
		{[]string{"token", "issue", "--help"}, "-ttl"},
		{[]string{"typed-put", "--help"}, "-pointers"},
		{[]string{"context-pack", "--help"}, "-max-tokens"},
	} {
		t.Run(strings.Join(tc.verbs, " "), func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			code, handled := Help(context.Background(), stdout, stderr, tc.verbs)
			if !handled || code != 0 {
				t.Fatalf("handled=%v code=%d stderr=%s", handled, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.flag) {
				t.Fatalf("help does not list %s:\n%s", tc.flag, stdout.String())
			}
		})
	}
}

// TestHelpIsOnlyClaimedForHelpRequests makes sure an ordinary command line
// still falls through to the store-backed path.
func TestHelpIsOnlyClaimedForHelpRequests(t *testing.T) {
	for _, verbs := range [][]string{
		{"put", "--namespace", "app/x", "--key", "k"},
		{"get", "--namespace", "app/x", "--key", "help"},
		{"put", "--", "--help"},
	} {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		if code, handled := Help(context.Background(), stdout, stderr, verbs); handled {
			t.Fatalf("%v: claimed as help (code %d, out %q)", verbs, code, stdout.String())
		}
	}
}

// TestBareContextIsAnIncompleteCommand — `tesseract context` with no verb is
// not a question, so the usage block goes to stderr and the exit code is
// non-zero. It still must not need a store.
func TestBareContextIsAnIncompleteCommand(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code, handled := Help(context.Background(), stdout, stderr, nil)
	if !handled || code == 0 {
		t.Fatalf("handled=%v code=%d", handled, code)
	}
	if !strings.Contains(stderr.String(), "usage: tesseract context") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
}

// TestUnknownSubcommandNamesItself — a typo should say what was not
// understood, not print a bare usage line.
func TestUnknownSubcommandNamesItself(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code, handled := Help(context.Background(), stdout, stderr, []string{"frobnicate", "--help"})
	if !handled || code == 0 {
		t.Fatalf("handled=%v code=%d", handled, code)
	}
	if !strings.Contains(stderr.String(), "frobnicate") {
		t.Fatalf("unknown subcommand not named: %s", stderr.String())
	}
}

// TestUsageListsEverySubcommand — the failure that started this: a usage
// string that omitted 11 live subcommands.
func TestUsageListsEverySubcommand(t *testing.T) {
	buf := &bytes.Buffer{}
	Usage(buf)
	for _, cmd := range Commands() {
		if !strings.Contains(buf.String(), cmd.Name) {
			t.Errorf("usage omits %q", cmd.Name)
		}
		if cmd.Summary == "" {
			t.Errorf("%q has no summary", cmd.Name)
		}
	}
	if !strings.Contains(buf.String(), "tesseract context") {
		t.Errorf("usage does not name the binary:\n%s", buf.String())
	}
}

// TestParseFlagsStillReportsRealErrors — turning help into a success must not
// have turned parse failures into one.
func TestParseFlagsStillReportsRealErrors(t *testing.T) {
	cli, _, errOut := newTestCLI(t)
	code := cli.Run(context.Background(), []string{"context", "get", "--nonesuch"})
	if code == 0 {
		t.Fatal("expected non-zero exit for an undefined flag")
	}
	if !strings.Contains(errOut.String(), "error:") {
		t.Fatalf("unexpected stderr: %s", errOut.String())
	}
}

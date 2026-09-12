package memory_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// The discipline line, asserted rather than trusted.
//
// [[tesseract_consumer_state_separate_from_status]] rev 2:
//
//	Tesseract may INDEX declared fields and FILTER on them.
//	Tesseract must never BRANCH BEHAVIOR on a value.
//
// The decision names the trade-off it accepted: "A JSON object invites
// structure, and structure invites interpretation. A bare string needs no
// discipline; JSON needs the line above actively held. The pressure will arrive
// as reasonable-sounding requests — 'just skip decay for done todos' — each of
// which is Tesseract acquiring an opinion about a consumer's workflow."
//
// So the line is held by two tests rather than by a comment. The first is
// STRUCTURAL and catches the change in review: it parses this repository and
// fails on any non-test code that compares a consumer-state value to a literal.
// The second is BEHAVIORAL and catches what the structural one cannot — a
// branch expressed in SQL, or one reached through a helper: it writes two
// revisions differing only in their state values and asserts every observable
// answer is identical.
//
// Both are written to fail for the next person, not for the one who added the
// column.

// consumerStateIdentifiers are the names a consumer-state VALUE can be flowing
// through. A comparison involving one of these against a literal is the shape
// this test exists to refuse.
//
// The list is names rather than types because Go's type system cannot tell a
// json.RawMessage holding consumer state from any other one. It is
// deliberately generous — anything spelled consumerstate, statefilter or
// statevalue is in scope — because a future branch would have to be written
// against a value read out of the bag, and reading it out means naming it.
var consumerStateIdentifiers = []string{
	"consumerstate",
	"statefilter",
	"statevalue",
	// payload.data (CW-20260912-0036) is the second opaque bag and takes the
	// same walk rather than a second one, because it is the same rule: a
	// consumer's object, stored verbatim, never read. A local named
	// payloadData is caught by this substring; the `x.Payload.Data` selector
	// shape is caught by namesPayloadDataSelector below, because Go's idents
	// there are `Payload` and `Data` separately and neither alone should
	// implicate every field named Data in the repository.
	"payloaddata",
}

// mechanismSites are the functions allowed to handle consumer-state values at
// all. Everything here manipulates the bag WITHOUT reading a value: it
// marshals, binds, scans, checks well-formedness, checks key presence, or
// renders a comparison for SQLite to evaluate.
//
// normalizeStateValue is on the list and is the closest call. It switches on a
// filter value's JSON TYPE — string, bool, number — to bind it in a form
// json_extract's result compares equal to. That is type coercion, and the
// property that keeps it mechanism is that every value of one type is treated
// identically: nothing in it can distinguish "done" from "open", or true-for-
// completed from true-for-pinned. If a case ever appears there that tests the
// value rather than its type, this allowlist entry is the wrong place to fix
// it.
var mechanismSites = map[string]string{
	"validateConsumerState":    "well-formedness, object-ness and required-key PRESENCE; never reads a value",
	"validateConsumerStateFor": "resolves the declaring type and delegates; never reads a value",
	"normalizeStateValue":      "coerces a filter value by its JSON TYPE so it binds comparably; never by which value it is",
	"buildStateFilterClauses":  "renders `json_extract(...) IN (?)` and binds; SQLite evaluates, Go does not",
	"stateFilterFingerprint":   "renders filters to a string that is hashed, never parsed",
	"consumerStateArg":         "reads the write argument off the request; hands bytes to the store unexamined",
	"parseStateFiltersArg":     "decodes the filter argument; hands the typed shape to the store unexamined",
	"validatePayloadData":      "well-formedness, object-ness, byte ceiling and the claim's SHAPE; never reads a value",
	"payloadDataArg":           "reads the write argument off the request; hands bytes to the store unexamined",
}

// TestNoCodePathBranchesOnConsumerStateValue parses every non-test Go file in
// the repository and fails on a comparison or a switch that tests a
// consumer-state value against a literal.
//
// What it catches, concretely: `if state["status"] == "done"`, a
// `switch v := stateValue; v { case "open": }`, `strings.Contains(consumerState,
// "archived")`. Each of those is the first line of a feature that makes
// Tesseract's behavior depend on a consumer's vocabulary, and each would
// otherwise land looking entirely reasonable in review.
//
// What it does NOT catch, stated so nobody reads a pass as a proof: a branch
// written in SQL rather than in Go, and a branch that reaches the value
// through a variable named something else entirely. The behavioral test below
// is the guard for the first; nothing but review covers the second, which is
// why the identifier list above is generous rather than exact.
func TestNoCodePathBranchesOnConsumerStateValue(t *testing.T) {
	root := repositoryRoot(t)
	var violations []string
	checked := 0

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		checked++

		rel, _ := filepath.Rel(root, path)
		enclosing := ""
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				enclosing = node.Name.Name
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					return true
				}
				if _, ok := mechanismSites[enclosing]; ok {
					return true
				}
				if namesOpaqueBag(node.X) && isLiteral(node.Y) ||
					namesOpaqueBag(node.Y) && isLiteral(node.X) {
					violations = append(violations, describe(fset, rel, enclosing, node.Pos()))
				}
			case *ast.SwitchStmt:
				if _, ok := mechanismSites[enclosing]; ok {
					return true
				}
				if node.Tag != nil && namesOpaqueBag(node.Tag) {
					violations = append(violations, describe(fset, rel, enclosing, node.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked < 50 {
		t.Fatalf("only parsed %d non-test files; this guard is walking the wrong tree", checked)
	}

	for _, v := range violations {
		t.Errorf("%s\n"+
			"A value out of an OPAQUE BAG — consumer_state or payload.data — is being compared to a "+
			"literal. Tesseract may index consumer_state fields and filter on them; it must never "+
			"branch behavior on a value, and payload.data is not even indexed. "+
			"(Original wording follows.) Tesseract may index "+
			"consumer_state fields and filter on them; it must never branch behavior on a value "+
			"([[tesseract_consumer_state_separate_from_status]] rev 2).\n"+
			"Whatever this enables — skipping decay for done items, ranking pinned ones higher, "+
			"refusing a transition — is the CONSUMER's policy, and moving it here makes recall, "+
			"activation or retention depend on someone else's workflow vocabulary.\n"+
			"If the change is genuinely mechanism (it treats every value of a JSON type alike), add "+
			"the function to mechanismSites with a line saying why. Do not widen the allowlist to "+
			"make a feature fit.", v)
	}
}

// namesPayloadDataSelector reports whether e reads `.Data` off something that
// names a payload — `rev.Payload.Data`, `in.Payload.Data`, `payload.Data`.
//
// Separate from the substring list because `Data` alone is far too common a
// field name to implicate, and `Payload` alone is read legitimately everywhere
// (summary and body are prose and ARE read). The pair is the signal: selecting
// Data off a payload is the only way to get at the bag's bytes, and comparing
// what comes back to a literal is the first line of an opinion about content.
func namesPayloadDataSelector(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "Data" {
			return true
		}
		ast.Inspect(sel.X, func(inner ast.Node) bool {
			id, isIdent := inner.(*ast.Ident)
			if isIdent && strings.Contains(strings.ToLower(id.Name), "payload") {
				found = true
				return false
			}
			return true
		})
		return true
	})
	return found
}

// namesOpaqueBag is the union: either bag, reached either way.
func namesOpaqueBag(e ast.Expr) bool {
	return namesConsumerState(e) || namesPayloadDataSelector(e)
}

// namesConsumerState reports whether e reads through an identifier that names
// consumer state — a bare identifier, a selector, or an index expression.
func namesConsumerState(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		lower := strings.ToLower(id.Name)
		for _, want := range consumerStateIdentifiers {
			if strings.Contains(lower, want) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isLiteral reports whether e is a compile-time constant — a string, number or
// character. Comparing consumer state to another variable is not what this
// guard is about; comparing it to a value spelled in Tesseract's own source is
// exactly what it is about, because that literal is a consumer's vocabulary
// having been copied into the engine.
//
// The empty string is exempt, and only the empty string. `x != ""` asks whether
// a bag or a field is THERE, which is presence — the same question
// validateConsumerState is allowed to ask about a required key. No consumer
// vocabulary has "" as a meaningful value, so exempting it cannot hide a
// branch, while refusing it would only push the same presence check into a
// len() spelling and teach the next reader that the guard is about syntax.
// A numeric zero is NOT exempt: 0 is a perfectly good priority.
func isLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok {
		return false
	}
	if lit.Kind != token.STRING {
		return true
	}
	return lit.Value != `""` && lit.Value != "``"
}

func describe(fset *token.FileSet, file, fn string, pos token.Pos) string {
	where := fset.Position(pos)
	if fn == "" {
		fn = "(file scope)"
	}
	return file + ":" + itoa(where.Line) + " in " + fn
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestConsumerStateValuesDoNotChangeBehavior is the behavioral half.
//
// Two revisions are written that are identical in every field the engine reads
// — same namespace, same author, same confidence, same payload length, same
// tags, same status, same TTL — and differ ONLY in the values inside their
// consumer_state bags. One says it is a finished, archived, low-priority todo;
// the other says it is an open, pinned, urgent one. If Tesseract has an opinion
// about either, it shows up here.
//
// This is what the AST guard cannot see: a branch expressed in SQL, one reached
// through an indirection, or one that arrives as a different ORDERING rather
// than as an if.
func TestConsumerStateValuesDoNotChangeBehavior(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	write := func(key, state string) memory.Revision {
		t.Helper()
		in := sampleInput(key)
		in.Namespace = "user/chrispian/memory/todos"
		in.Payload = memory.Payload{Summary: "a todo item", Body: "same body for both"}
		in.ConsumerState = json.RawMessage(state)
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("WriteRevision(%s): %v", key, err)
		}
		return rev
	}

	// Deliberately the two ends of every lifecycle vocabulary a consumer might
	// bring: done vs open, archived vs live, lowest vs highest priority.
	done := write("todo.done", `{"kind":"todo","section":"anytime","completed":true,"archived":true,"priority":"low","pinned":false}`)
	open := write("todo.open", `{"kind":"todo","section":"now","completed":false,"archived":false,"priority":"urgent","pinned":true}`)

	// Status is Tesseract's epistemic ladder and must be untouched by either
	// bag. A consumer marking something "done" must not deprecate it.
	if done.Status != open.Status {
		t.Errorf("consumer_state changed status: %q vs %q — status is Tesseract's epistemic ladder, "+
			"and a consumer's completion flag must never move a record along it", done.Status, open.Status)
	}

	// Expiry: a "done" bag must not shorten retention.
	if (done.ExpiresAt == nil) != (open.ExpiresAt == nil) || done.TTLSeconds != open.TTLSeconds {
		t.Errorf("consumer_state changed retention: ttl %d/%d — deciding that finished items "+
			"expire sooner is the consumer's policy, not the store's",
			done.TTLSeconds, open.TTLSeconds)
	}

	// Activation: the named pressure in the decision is "just skip decay for
	// done todos". Both entries were written the same instant with the same
	// confidence, so their activation must be identical.
	doneState, err := ms.GetState(ctx, done.MemoryID)
	if err != nil {
		t.Fatalf("GetState(done): %v", err)
	}
	openState, err := ms.GetState(ctx, open.MemoryID)
	if err != nil {
		t.Fatalf("GetState(open): %v", err)
	}
	if doneState.Activation != openState.Activation {
		t.Errorf("consumer_state changed activation: %v vs %v — skipping or accelerating decay "+
			"because of a value in the bag is exactly the erosion "+
			"[[tesseract_consumer_state_separate_from_status]] names",
			doneState.Activation, openState.Activation)
	}

	// Recall: both must be candidates, and neither may be ranked, filtered or
	// hidden for what its bag says. An unfiltered recall returns both.
	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/todos"},
		Ranking:    memory.RankingActivation,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Revision.MemoryKey] = true
	}
	if !seen["todo.done"] || !seen["todo.open"] {
		t.Errorf("unfiltered recall returned %v; both entries must be candidates regardless of "+
			"what their bags say — hiding completed items by default is the consumer's view, "+
			"not the store's corpus", seen)
	}
	// And their scores must match, since every input to ranking is identical.
	var scores []float64
	for _, r := range results {
		if r.Score != nil {
			scores = append(scores, *r.Score)
		}
	}
	if len(scores) == 2 && scores[0] != scores[1] {
		t.Errorf("recall scored the two entries differently (%v) though they differ only in "+
			"consumer_state values; a per-value thumb on the ranking scale is unfixable from a "+
			"bad result, because nobody debugging a ranking complaint checks a consumer's bag",
			scores)
	}
}

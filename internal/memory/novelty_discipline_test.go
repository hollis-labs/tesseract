package memory_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// The shadow-mode line, asserted rather than trusted.
//
//	Tesseract may COMPUTE novelty, STORE it, and REPORT it.
//	Tesseract must never let it change what happens to a revision.
//
// CW-20260825-0018 shipped novelty in shadow mode precisely because the gate is
// unvalidated on this corpus, and because two of the published design's
// mechanisms were measured not to transfer to a 3072-dimensional embedding. A
// value that is collected but never acted on has exactly one failure mode: it
// quietly starts being acted on. The pressure will arrive as reasonable
// requests — rank the novel things higher, decay the redundant ones faster,
// skip the write when the gate says skip — and each is the gate being enabled
// without anyone deciding to enable it.
//
// Two tests hold the line, following the pattern
// consumerstate_discipline_test.go established for the same class of contract.
//
// The first is STRUCTURAL and is stricter than its predecessor, because it can
// be: consumer_state necessarily flows across the whole API surface, while
// novelty lives in one file and is deliberately not serialized onto Revision at
// all. So the guard is confinement rather than comparison-shape — novelty may
// be NAMED only where this file says, which catches a branch, a read, a sort
// key and a new caller with the same rule.
//
// The second is BEHAVIORAL, and catches what no AST walk can: a branch
// expressed in SQL, or one reached through an indirection.
//
// And because a guard nobody has watched fail is not known to work, the
// structural walker is a helper that a third test aims at a planted violation.
// It proves the guard fires on every run rather than on the day someone
// remembers to check.

// noveltyIdentifiers are the spellings a novelty value can be flowing under.
// Deliberately few and deliberately distinctive: the column prefix is
// `novelty_` and the Go names all carry `novelty`, precisely so that grepping
// for one word is a complete answer. If a future change introduces a synonym,
// it belongs on this list on the same commit.
var noveltyIdentifiers = []string{
	"novelty",
	"vmf",
	"kappahat",
	"shadowroute",
}

// noveltyCarrierSites is the complete list of places novelty may be named, and
// why each one is mechanism rather than policy.
//
// The value is a function name, or "*" for a whole file. Everything here
// COMPUTES the measurement, PERSISTS it, or declares its storage; nothing here
// lets it influence a revision's fate.
//
// Adding an entry is how the shadow mode ends. Do not add one to make a feature
// fit — enabling the gate is a separate decision (CW-20260825-0018 lists it as
// out of scope), and novelty as a ranking signal is CW-20260910-0074.
var noveltyCarrierSites = map[string]map[string]string{
	"internal/memory/novelty.go": {
		"*": "the scorer itself: computes, routes to a string, and writes six columns",
	},
	"internal/memory/embed.go": {
		"EmbedRevision": "the single call site; scores after the vector lands and discards the error",
	},
	"internal/contextstore/store.go": {
		"migrate": "migration 20's ADD COLUMN statements; DDL, not behavior",
	},
}

// noveltyViolation is one place novelty is named outside the carrier list.
type noveltyViolation struct {
	file string
	line int
	fn   string
	what string
}

func (v noveltyViolation) String() string {
	fn := v.fn
	if fn == "" {
		fn = "(file scope)"
	}
	return v.file + ":" + itoa(v.line) + " in " + fn + " — " + v.what
}

// scanNoveltyMentions walks root and reports every mention of a novelty
// identifier or a `novelty_` string literal in non-test Go source that the
// carrier list does not authorize.
//
// It checks string literals as well as identifiers because the column names are
// reachable from SQL text that contains no Go identifier at all: a
// `ORDER BY novelty_score DESC` spliced into a recall query is the exact
// change this guard exists to stop, and it would be invisible to an
// identifier-only walk.
//
// Returns the count of files parsed so a caller can tell "clean" from "walked
// the wrong tree".
func scanNoveltyMentions(t *testing.T, root string) (violations []noveltyViolation, parsed int) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "dist", "testdata":
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
		parsed++

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		allowed := noveltyCarrierSites[rel]

		record := func(pos token.Pos, fn, what string) {
			if allowed != nil {
				if _, ok := allowed["*"]; ok {
					return
				}
				if _, ok := allowed[fn]; ok {
					return
				}
			}
			violations = append(violations, noveltyViolation{
				file: rel, line: fset.Position(pos).Line, fn: fn, what: what,
			})
		}

		enclosing := ""
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				enclosing = node.Name.Name
			case *ast.Ident:
				lower := strings.ToLower(node.Name)
				for _, want := range noveltyIdentifiers {
					if strings.Contains(lower, want) {
						record(node.Pos(), enclosing, "identifier "+node.Name)
						return true
					}
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				if strings.Contains(strings.ToLower(node.Value), "novelty_") {
					record(node.Pos(), enclosing, "string literal naming a novelty column")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file != violations[j].file {
			return violations[i].file < violations[j].file
		}
		return violations[i].line < violations[j].line
	})
	return violations, parsed
}

// TestNoveltyIsConfinedToItsScorer fails if novelty is named anywhere the
// carrier list does not authorize.
//
// What it catches, concretely: `if rev.NoveltyScore < 0.05 { return }` in the
// write path, `ORDER BY novelty_score DESC` spliced into recall, a decay pass
// that reads the column, a projection that serializes it onto the API, or
// simply a second caller of the scorer. Each of those is the gate being
// switched on, and each would look entirely reasonable in review.
//
// What it does NOT catch, stated so nobody reads a pass as a proof: a branch
// written in SQL inside novelty.go itself, a value carried under a name this
// list does not anticipate, and anything in a test file. The behavioral test
// below covers the first; TestNoveltyConfinementGuardFires proves this walker
// works at all.
func TestNoveltyIsConfinedToItsScorer(t *testing.T) {
	violations, parsed := scanNoveltyMentions(t, repositoryRoot(t))
	if parsed < 50 {
		t.Fatalf("only parsed %d non-test files; this guard is walking the wrong tree", parsed)
	}
	for _, v := range violations {
		t.Errorf("%s\n"+
			"Novelty is named outside the sites authorized to carry it. Tesseract may COMPUTE "+
			"novelty, STORE it and REPORT it; it must never let the value change what happens to a "+
			"revision (CW-20260825-0018, shipped in shadow mode).\n"+
			"Whatever this enables — ranking novel entries higher, skipping a write the gate calls "+
			"redundant, decaying a low score faster — is the gate being ENABLED, which is a separate "+
			"decision that has not been taken, on a parameterisation measured NOT to transfer to this "+
			"corpus. Novelty as a ranking signal is CW-20260910-0074.\n"+
			"If the change is genuinely mechanism (it computes, persists, or declares storage without "+
			"letting the value influence anything), add the file and function to noveltyCarrierSites "+
			"with a line saying why. Do not widen the list to make a feature fit.", v)
	}
}

// TestNoveltyConfinementGuardFires aims the walker at a tree containing a
// planted violation and fails if it comes back clean.
//
// This is the fault injection, made permanent. The guard above passes today
// because the code is correct; it would also pass if the walker silently
// stopped matching — if the identifier list drifted from the names in use, if
// the carrier list grew a "*" by accident, or if a refactor moved the scan
// somewhere it no longer reaches source. Each of those turns a guard into a
// decoration that still reports success, which is the worst state for a test
// whose whole job is to fail for someone in the future.
//
// The planted file is the shape the guard actually exists to stop: recall
// ordering by the column, and a write path skipping on the route.
func TestNoveltyConfinementGuardFires(t *testing.T) {
	root := t.TempDir()
	// Enough files to clear the parsed-count floor the real guard asserts, so
	// this test exercises the same walk rather than a special case of it.
	for i := 0; i < 60; i++ {
		name := filepath.Join(root, "clean"+itoa(i)+".go")
		if err := os.WriteFile(name, []byte("package planted\n\nfunc F"+itoa(i)+"() int { return "+itoa(i)+" }\n"), 0o600); err != nil {
			t.Fatalf("write clean file: %v", err)
		}
	}
	planted := "package planted\n\n" +
		"const rankSQL = `SELECT revision_id FROM memory_revisions ORDER BY novelty_score DESC`\n\n" +
		"func shouldWrite(noveltyRoute string) bool {\n" +
		"\treturn noveltyRoute != \"skip\"\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(root, "violation.go"), []byte(planted), 0o600); err != nil {
		t.Fatalf("write planted file: %v", err)
	}

	violations, parsed := scanNoveltyMentions(t, root)
	if parsed < 50 {
		t.Fatalf("planted tree parsed only %d files; the fixture is not exercising the real walk", parsed)
	}
	if len(violations) == 0 {
		t.Fatal("the confinement guard did not fire on a file that orders recall by novelty_score " +
			"and branches a write on a novelty route. TestNoveltyIsConfinedToItsScorer is therefore " +
			"reporting success without checking anything — fix the walker, not this test.")
	}

	// Both shapes must be caught, not just whichever one happens to come first.
	var sawLiteral, sawIdent bool
	for _, v := range violations {
		if strings.Contains(v.what, "string literal") {
			sawLiteral = true
		}
		if strings.Contains(v.what, "identifier") {
			sawIdent = true
		}
	}
	if !sawLiteral {
		t.Error("the guard missed `ORDER BY novelty_score` in a string literal; a SQL-only change " +
			"would pass review and pass this suite")
	}
	if !sawIdent {
		t.Error("the guard missed a Go identifier carrying a novelty route; only the SQL half is " +
			"being checked")
	}
}

// TestNoveltyScoreDoesNotChangeBehavior is the behavioral half.
//
// Two revisions are written that are identical in every field the engine reads
// — same namespace, same author, same confidence, same payload, same tags, same
// status, same TTL — and are then given OPPOSITE novelty measurements directly
// in SQL: one maximally novel and routed to write, one maximally redundant and
// routed to skip. If Tesseract has an opinion about either, it shows up here.
//
// The columns are set directly rather than engineered through content, for the
// same reason the consumer_state test hand-writes its two bags: holding every
// other input identical is what makes the difference attributable. Content that
// scored differently would also rank differently, and legitimately so.
//
// This is what the AST guard cannot see: a branch expressed in SQL, one reached
// through an indirection, or one that arrives as a different ORDERING rather
// than as an if.
func TestNoveltyScoreDoesNotChangeBehavior(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	write := func(key string) memory.Revision {
		t.Helper()
		in := sampleInput(key)
		in.Namespace = "user/chrispian/memory/notes"
		in.Payload = memory.Payload{Summary: "a captured note", Body: "same body for both"}
		rev, err := ms.WriteRevision(ctx, in)
		if err != nil {
			t.Fatalf("WriteRevision(%s): %v", key, err)
		}
		return rev
	}

	novel := write("note.novel")
	redundant := write("note.redundant")

	// The two ends of the measurement: nothing to be explained by, versus
	// perfectly explained. A gate acting on either would act hardest here.
	set := func(rev memory.Revision, score, cosine float64, route string) {
		t.Helper()
		_, err := ms.DB().ExecContext(ctx, `
UPDATE memory_revisions
   SET novelty_score = ?, novelty_top1_cosine = ?, novelty_scope_n = ?,
       novelty_kappa = ?, novelty_route = ?, novelty_gate_version = ?
 WHERE revision_id = ?`,
			score, cosine, 500, 3300.0, route, "test-gate", rev.RevisionID)
		if err != nil {
			t.Fatalf("set novelty columns: %v", err)
		}
	}
	set(novel, 1.0, -1.0, "write")
	set(redundant, 0.0, 1.0, "skip")

	// Status is Tesseract's epistemic ladder. A gate calling something
	// redundant must not move a revision along it.
	got := func(rev memory.Revision) memory.Revision {
		t.Helper()
		out, err := ms.GetRevisionByID(ctx, rev.RevisionID)
		if err != nil {
			t.Fatalf("GetRevisionByID: %v", err)
		}
		return out
	}
	novelNow, redundantNow := got(novel), got(redundant)
	if novelNow.Status != redundantNow.Status {
		t.Errorf("novelty changed status: %q vs %q — a derived measurement must never move a "+
			"revision along the epistemic ladder", novelNow.Status, redundantNow.Status)
	}

	// Retention: a redundant score must not shorten a revision's life. This is
	// the most plausible first feature — "expire the duplicates sooner" — and
	// it is the gate being enabled.
	if (novelNow.ExpiresAt == nil) != (redundantNow.ExpiresAt == nil) ||
		novelNow.TTLSeconds != redundantNow.TTLSeconds {
		t.Errorf("novelty changed retention: ttl %d/%d — deciding that redundant captures expire "+
			"sooner is the gate acting, and the gate is not enabled",
			novelNow.TTLSeconds, redundantNow.TTLSeconds)
	}

	// Activation: both were written the same instant with the same confidence,
	// so decay and reinforcement must not have been able to tell them apart.
	novelState, err := ms.GetState(ctx, novel.MemoryID)
	if err != nil {
		t.Fatalf("GetState(novel): %v", err)
	}
	redundantState, err := ms.GetState(ctx, redundant.MemoryID)
	if err != nil {
		t.Fatalf("GetState(redundant): %v", err)
	}
	if novelState.Activation != redundantState.Activation {
		t.Errorf("novelty changed activation: %v vs %v — decaying a low-novelty entry faster is "+
			"the single most reasonable-sounding way this gate switches itself on",
			novelState.Activation, redundantState.Activation)
	}

	// Recall: both must be candidates, and neither may be ranked, filtered or
	// hidden for its score. This is the CW-20260910-0074 pressure, asserted
	// here so it cannot land quietly in this task.
	results, err := ms.Recall(ctx, memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingActivation,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	seen := map[string]bool{}
	var scores []float64
	for _, r := range results {
		seen[r.Revision.MemoryKey] = true
		if r.Score != nil {
			scores = append(scores, *r.Score)
		}
	}
	if !seen["note.novel"] || !seen["note.redundant"] {
		t.Errorf("unfiltered recall returned %v; both entries must be candidates regardless of "+
			"novelty — dropping the redundant one is the gate deciding what the corpus contains", seen)
	}
	if len(scores) == 2 && scores[0] != scores[1] {
		t.Errorf("recall scored the two entries differently (%v) though they differ only in their "+
			"novelty columns; a thumb on the ranking scale is unfixable from a bad result, because "+
			"nobody debugging a ranking complaint checks a derived column nothing is supposed to read",
			scores)
	}

	// And the revision the API hands back must not carry the measurement at
	// all. Nothing can branch on a value it was never given, and keeping
	// novelty off the wire is why the structural guard above can be a
	// confinement rule rather than a comparison-shape one.
	if strings.Contains(strings.ToLower(revisionJSON(t, novelNow)), "novelty") {
		t.Error("a serialized Revision names novelty; the column is Tesseract's own derived value " +
			"and exposing it invites exactly the consumer-side branching this task deferred")
	}
}

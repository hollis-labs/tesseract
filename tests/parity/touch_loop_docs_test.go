package parity

// CW-20260825-0008 ships in two halves, and the docs half is the one that
// decides whether any of it works: tesseract_touch is the only input activation
// has, so if agents do not learn to call it, activation stays flat and the tool
// was added for nothing.
//
// The code half defends itself — a broken curve fails a test. The docs half had
// nothing. Deleting the touch-loop paragraph from memory_recall's description
// compiled cleanly and passed every test in the repo, which is exactly how a
// docs half quietly stops shipping.
//
// This file is that guard, on both the MCP tool descriptions an agent reads at
// the call site and the two skills the ticket names.
//
// Anchoring: every phrase below is written out here as a literal rather than
// compared against the shared touchLoopDescription constant the tools render.
// Asserting that the constant contains itself would pass no matter what the
// constant said — the failure mode this ticket is most exposed to.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// touchLoopClaims are the claims the ticket requires the caller-facing surface
// to make. Each is matched case-insensitively as a substring, so wording around
// them can change without churn here, but dropping the claim cannot.
var touchLoopClaims = []struct {
	fragment string
	why      string
}{
	{"tesseract_touch", "names the tool, or the reader cannot act on any of this"},
	{"unreinforced", "states the fact: a recall result carries no reinforcement until touched"},
	{"under-reporting is fine", "the caller guidance, verbatim from the ticket"},
	{"over-reporting is worse than silence", "the other half of that guidance — the half that keeps noise out of the ranking"},
}

// rankedResultTools are the tools that return ranked results and therefore owe
// the reader the loop. Both are listed rather than one: they share a rendered
// constant today, and a future edit that inlines one of them must not be able to
// drop the claim from that one alone.
var rankedResultTools = []string{"memory_recall", "tesseract_lookup"}

func TestRankedResultToolsDocumentTheTouchLoop(t *testing.T) {
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("touch-loop-docs-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	tools := srv.ListTools()
	for _, name := range rankedResultTools {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("tool %q is not registered; the loop cannot be documented on a tool that does not exist", name)
			continue
		}
		desc := strings.ToLower(tool.Tool.Description)
		for _, claim := range touchLoopClaims {
			if !strings.Contains(desc, strings.ToLower(claim.fragment)) {
				t.Errorf("tool %q description does not carry %q — %s",
					name, claim.fragment, claim.why)
			}
		}
	}
}

// TestTouchToolDocumentsTheTiming pins the one thing tesseract_touch's own
// description cannot afford to leave out. An agent that calls it right after the
// search rather than after the reasoning reinforces the ranker's guesses, which
// is the failure recall.go refuses and the reason this is a tool and not a knob.
func TestTouchToolDocumentsTheTiming(t *testing.T) {
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("touch-loop-docs-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	tool, ok := srv.ListTools()["tesseract_touch"]
	if !ok {
		t.Fatal("tesseract_touch is not registered")
	}
	desc := strings.ToLower(tool.Tool.Description)
	for _, want := range []string{
		"after the work, not after the search",
		"under-reporting is fine",
		"over-reporting is worse than silence",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("tesseract_touch description does not carry %q", want)
		}
	}
}

// ── The skills the ticket names ──────────────────────────────────────────────

// TestNamedSkillsCarryTheWorkedLoop checks the two skill bodies the ticket names
// by name. The ticket requires the loop be presented as the DEFAULT workflow
// rather than as an option, so each skill must show it as a worked sequence, not
// merely mention that the tool exists.
func TestNamedSkillsCarryTheWorkedLoop(t *testing.T) {
	for _, skill := range []string{"memory", "recall-and-ranking"} {
		path := filepath.Join(repoRoot(t), "internal", "mcpadapter", "skills", skill+".md")
		raw, err := os.ReadFile(path) //nolint:gosec // fixed path under the repo
		if err != nil {
			t.Fatalf("read skill %s: %v", skill, err)
		}
		body := strings.ToLower(string(raw))

		for _, claim := range touchLoopClaims {
			if !strings.Contains(body, strings.ToLower(claim.fragment)) {
				t.Errorf("skill %q does not carry %q — %s", skill, claim.fragment, claim.why)
			}
		}
		// A worked loop names all three steps in order. "Mentions the tool
		// somewhere" is what this is distinguishing itself from.
		for _, step := range []string{"recall", "use", "touch"} {
			if !strings.Contains(body, step) {
				t.Errorf("skill %q does not name the %q step of the loop", skill, step)
			}
		}
		if !strings.Contains(body, "revision_id") {
			t.Errorf("skill %q does not say what to pass to tesseract_touch", skill)
		}
	}
}

package skills

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestKnowledgeSkillExamplesDoNotPointAtNothing guards the acceptance
// criterion from CW-20260825-0015: the knowledge skill's example must use a
// path that exists, or `nil:`.
//
// It exists because the previous example shipped an absolute path under a
// directory tree that had been retired — the skill teaching pointer-first
// knowledge was itself demonstrating a dead pointer, and nothing noticed.
//
// The rule is deliberately narrow. A `file:` example is checked against the
// filesystem, which only means anything on a machine that has the path; a
// `nil:` or `https:` example is accepted without a filesystem check, since
// neither makes a claim this test can settle. That is enough to catch the
// specific regression: someone pasting a local absolute path back into the
// shipped example.
func TestKnowledgeSkillExamplesDoNotPointAtNothing(t *testing.T) {
	body, err := Get("knowledge")
	if err != nil {
		t.Fatalf("Get(knowledge): %v", err)
	}

	blocks := fencedJSONBlocks(body)
	if len(blocks) == 0 {
		t.Fatal("found no fenced json blocks in the knowledge skill — the scanner is wrong, not the skill")
	}

	checked := 0
	for i, block := range blocks {
		var doc map[string]any
		if err := json.Unmarshal([]byte(block), &doc); err != nil {
			t.Errorf("block %d is not valid JSON (an agent copies these verbatim): %v", i, err)
			continue
		}
		scheme, _ := doc["pointer_scheme"].(string)
		locator, _ := doc["pointer_locator"].(string)
		if scheme == "" {
			continue
		}
		checked++
		if scheme != "file" {
			continue
		}
		if locator == "" {
			t.Errorf("block %d: pointer_scheme is file with an empty locator", i)
			continue
		}
		if _, statErr := os.Stat(locator); statErr != nil {
			t.Errorf("block %d: the knowledge skill's file example points at %q, which does not resolve (%v).\n"+
				"    The skill that teaches pointer-first knowledge must not ship a dead pointer.\n"+
				"    Use pointer_scheme \"nil\" for an example whose body is the artifact.",
				i, locator, statErr)
		}
	}
	if checked == 0 {
		t.Error("no example in the knowledge skill declares a pointer_scheme — this guard would pass vacuously")
	}
}

// TestKnowledgeSkillTeachesBodyAsArtifact pins the guidance change itself, so
// a later edit cannot quietly revert the skill to pointer-first framing while
// leaving the example alone.
func TestKnowledgeSkillTeachesBodyAsArtifact(t *testing.T) {
	body, err := Get("knowledge")
	if err != nil {
		t.Fatalf("Get(knowledge): %v", err)
	}
	for _, want := range []string{
		"The body is the durable half",
		"first-class pattern",
		"pointer_health",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("knowledge skill no longer contains %q", want)
		}
	}
}

// fencedJSONBlocks returns the contents of every ```json fence in md.
func fencedJSONBlocks(md string) []string {
	var out []string
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```json" {
			continue
		}
		var buf []string
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
				i = j
				break
			}
			buf = append(buf, lines[j])
		}
		out = append(out, strings.Join(buf, "\n"))
	}
	return out
}

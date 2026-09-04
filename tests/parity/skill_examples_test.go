// Guards for the payloads shipped inside the skill corpus.
//
// CW-20260514-0024 put copy-pasteable request shapes into the skills, on both
// the MCP surface and HTTP, because the two genuinely differ — knowledge_write
// takes `pointer_scheme` / `pointer_locator` flat over MCP and
// `pointer: {scheme, locator}` over HTTP, and a doc that shows only one form
// actively misleads callers of the other.
//
// An example is only worth shipping if it still works. Prose rots quietly; a
// payload rots into a validation_error in someone else's session. These three
// checks are the difference between "we wrote examples" and "the examples are
// true":
//
//   - every fenced json block parses, because an agent pastes it verbatim;
//   - every curl names a route the router actually serves;
//   - the write-side skills each carry both surfaces, so neither half can be
//     dropped in a later edit while the other stays.
//
// What they deliberately do NOT check is that a payload is semantically
// accepted end to end. That would need a live store per example and would fail
// on the placeholder revision IDs the examples use on purpose. The field-name
// claims are pinned by review against the handler structs; these guard the
// failure modes that rot silently.
package parity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/hollis-labs/tesseract/internal/mcpadapter/skills"
)

// skillsDir is the embedded corpus tesseract_skills serves.
const skillsDir = "internal/mcpadapter/skills"

// writeSideSkills are the skills a write tool's description points at before a
// caller composes a body. Each owes the reader a request shape on BOTH
// surfaces — that is the whole reason the ticket touched them.
//
// start-here is on the list because thirteen tools point at it and it is where
// the context-domain write shapes live. audit and revisions are on it because
// their subject matter is reached from a write (what a write emitted, what a
// write superseded) and both shipped with zero code blocks.
var writeSideSkills = []string{
	"start-here",
	"memory",
	"knowledge",
	"promotion",
	"revisions",
	"audit",
}

// TestSkillJSONExamplesParse: every ```json fence in the corpus is valid JSON.
//
// The knowledge skill already had this check for its own file. The examples
// this ticket added are the reason it now covers all of them: a JSON body with
// a stray comma is worse than no example, because the reader trusts it.
func TestSkillJSONExamplesParse(t *testing.T) {
	total := 0
	for _, sk := range skillFiles(t) {
		body := readSkill(t, sk)
		for i, block := range fencedBlocks(body, "json") {
			total++
			var doc any
			if err := json.Unmarshal([]byte(block), &doc); err != nil {
				t.Errorf("%s.md: json block %d does not parse (%v) — an agent pastes these verbatim:\n%s",
					sk, i, err, block)
			}
		}
	}
	if total == 0 {
		t.Fatal("found no fenced json blocks anywhere in the skill corpus — the scanner is wrong, not the corpus")
	}
}

// TestSkillCurlExamplesHitRealRoutes probes every HTTP example in the corpus
// against a wired server and fails when the router does not know the path.
//
// This is the guard that makes the HTTP half trustworthy. A curl in a doc is
// unrunnable by CI in the general case, but "does this route exist" is exactly
// the half that rots on a rename — and it is the half a reader cannot check
// without a running server.
//
// Any status but endpoint-not-found passes: 400 and 401 mean the route
// resolved and rejected the probe for reasons this test is not about.
func TestSkillCurlExamplesHitRealRoutes(t *testing.T) {
	srv := newFullyWiredHTTPServer(t)

	seen := map[string]struct{}{}
	for _, sk := range skillFiles(t) {
		for _, call := range curlCalls(readSkill(t, sk)) {
			key := call.method + " " + call.path
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			req := httptest.NewRequest(call.method, call.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			if rr.Code == http.StatusNotFound && strings.Contains(rr.Body.String(), `"endpoint not found"`) {
				t.Errorf("%s.md documents %s %s, which the router does not serve", sk, call.method, call.path)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("found no curl examples in the skill corpus — the corpus documented HTTP in prose only, " +
			"which is the state CW-20260514-0024 fixed")
	}
}

// TestWriteSideSkillsShowBothSurfaces: the skills a write tool routes to must
// carry an MCP payload AND an HTTP call, not one or the other.
//
// One surface alone is worse than none here. The shapes differ in structure —
// nesting, string-vs-array — so a reader on the undocumented surface copies
// something that looks right and is not.
func TestWriteSideSkillsShowBothSurfaces(t *testing.T) {
	for _, sk := range writeSideSkills {
		body := readSkill(t, sk)
		if len(fencedBlocks(body, "json")) == 0 {
			t.Errorf("skill %q carries no json example — a write tool points at it for the request shape", sk)
		}
		if len(curlCalls(body)) == 0 {
			t.Errorf("skill %q carries no curl example — the MCP and HTTP shapes differ, so documenting one is documenting half", sk)
		}
	}
}

// ── Progressive discovery reaches the caller before the write ──────────

// writeTools are every mutating tool on the surface, paired with the skill its
// description must route to.
//
// The pairing is the point. Before CW-20260514-0024, thirteen tools closed
// with the identical sentence "See `tesseract_skills start-here` for the
// primitive model" regardless of what they did — a pointer that carries no
// routing signal is indistinguishable from no pointer, and agents behaved
// accordingly: they wrote first and read the skill after the validation_error.
//
// Three tools still route to start-here, and that is a real answer rather than
// a leftover: start-here is where the context-domain request shapes live, and
// all three write context records.
var writeTools = map[string]string{
	"memory_write":               "memory",
	"memory_promote":             "promotion",
	"knowledge_write":            "knowledge",
	"context_write":              "start-here",
	"context_typed_write":        "start-here",
	"context_ingest":             "start-here",
	"context_session_write":      "context-packet",
	"context_status_set":         "promotion",
	"context_promote":            "promotion",
	"context_namespace_register": "namespaces",
}

// TestWriteToolsRouteToTheirOwnSkill: each mutating tool names the skill that
// carries ITS request shape, not a generic one.
func TestWriteToolsRouteToTheirOwnSkill(t *testing.T) {
	tools := registeredToolDescriptions(t)
	for tool, skill := range writeTools {
		desc, ok := tools[tool]
		if !ok {
			t.Errorf("write tool %q is not registered", tool)
			continue
		}
		if want := "`tesseract_skills " + skill + "`"; !strings.Contains(desc, want) {
			t.Errorf("%s does not name %s — a write tool must route to the skill carrying its own request shape", tool, want)
		}
	}
}

// TestWriteToolsFrontLoadTheSkillPointer: the pointer is near the top, not in
// the footer.
//
// Position is the whole finding. A pointer an agent reaches only after reading
// past the argument list is a pointer it reaches after deciding to write. The
// bound is the description's midpoint — loose enough that wording can move,
// tight enough that "last bullet" cannot come back.
func TestWriteToolsFrontLoadTheSkillPointer(t *testing.T) {
	tools := registeredToolDescriptions(t)
	for tool := range writeTools {
		desc, ok := tools[tool]
		if !ok {
			continue // reported by TestWriteToolsRouteToTheirOwnSkill
		}
		at := strings.Index(desc, "tesseract_skills")
		if at < 0 {
			t.Errorf("%s names no skill at all", tool)
			continue
		}
		if half := len(desc) / 2; at > half {
			t.Errorf("%s buries its skill pointer at byte %d of %d — past the midpoint is the footer, "+
				"and a footer pointer is read after the decision to write, not before it", tool, at, len(desc))
		}
	}
}

// TestSkillPointersResolve: every `tesseract_skills <name>` reference in a tool
// description or a skill body names an embedded skill.
//
// This is the tool-name drift guard's blind spot. That one checks tool names in
// docs against the live tool surface; nothing checked SKILL names, so a renamed
// or deleted skill would leave working-looking pointers that answer
// skill_not_found. v0.8.0 shipped exactly this failure one level up, with a
// skill body naming a tool no adapter registered.
func TestSkillPointersResolve(t *testing.T) {
	metas, err := skills.List()
	if err != nil {
		t.Fatalf("skills.List: %v", err)
	}
	known := map[string]struct{}{}
	for _, m := range metas {
		known[m.Name] = struct{}{}
	}

	sources := map[string]string{}
	for _, sk := range skillFiles(t) {
		sources["skill "+sk+".md"] = readSkill(t, sk)
	}
	for name, desc := range registeredToolDescriptions(t) {
		sources["tool "+name] = desc
	}

	found := 0
	for where, body := range sources {
		for _, m := range skillPointerRE.FindAllStringSubmatch(body, -1) {
			found++
			if _, ok := known[m[1]]; !ok {
				t.Errorf("%s points at `tesseract_skills %s`, which is not an embedded skill", where, m[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("found no skill pointers anywhere — the scanner is wrong, not the surface")
	}
}

// skillPointerRE matches a backticked pointer with a skill name after it.
// Requiring the closing backtick is what keeps prose like "`tesseract_skills`
// with no args" out of the results: there the name slot is not part of the
// code span, so it is not a pointer.
var skillPointerRE = regexp.MustCompile("`tesseract_skills ([a-z][a-z-]*)`")

// registeredToolDescriptions reads descriptions off the live tool surface, the
// same way the drift guard reads names off it: never from a constant, so a
// description cannot pass by containing itself.
func registeredToolDescriptions(t *testing.T) map[string]string {
	t.Helper()
	adapter := newFullyWiredAdapter(t)
	srv := server.NewMCPServer("skill-examples-test", "0.0.0", server.WithToolCapabilities(true))
	adapter.RegisterAllTools(srv)

	out := map[string]string{}
	for name, st := range srv.ListTools() {
		out[name] = st.Tool.Description
	}
	return out
}

// ── Scanning ───────────────────────────────────────────────────────────

// curlCall is one HTTP example extracted from a shipped skill.
type curlCall struct {
	method string
	path   string
}

// curlURLRE finds the request URL in a curl line. Examples in this corpus
// write the base as $TESSERACT_URL (defined once, in start-here), so a literal
// host in a skill is invisible here — deliberately: a pinned host is its own
// defect, and the drift guard has no way to probe someone's machine.
var curlURLRE = regexp.MustCompile(`\$TESSERACT_URL(/[^"'\s\\]*)`)

// curlCalls extracts (method, path) from every curl invocation in md.
//
// The method comes from the same line as the URL, which is how every example
// in the corpus is written. That is a constraint on the examples, not a
// limitation of the scanner: a curl whose flags are split across lines from its
// URL is harder to copy correctly, so keeping them together is the right shape
// to enforce anyway.
func curlCalls(md string) []curlCall {
	var out []curlCall
	for _, line := range strings.Split(md, "\n") {
		m := curlURLRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		method := http.MethodGet
		if strings.Contains(line, "-X POST") {
			method = http.MethodPost
		}
		out = append(out, curlCall{method: method, path: m[1]})
	}
	return out
}

// fencedBlocks returns the body of every ```<lang> fence in md.
func fencedBlocks(md, lang string) []string {
	var out []string
	lines := strings.Split(md, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```"+lang {
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

// skillFiles lists the corpus by skill name, sorted for stable failure output.
func skillFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), skillsDir))
	if err != nil {
		t.Fatalf("read %s: %v", skillsDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	if len(names) == 0 {
		t.Fatalf("no skills found under %s — this test's path is stale", skillsDir)
	}
	sort.Strings(names)
	return names
}

func readSkill(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), skillsDir, name+".md")
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path under the repo
	if err != nil {
		t.Fatalf("read skill %s: %v", name, err)
	}
	return string(raw)
}

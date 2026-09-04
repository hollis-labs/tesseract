// Binary-name drift guard.
//
// CW-20260825-0013 renamed the daemon binary to `tesseract`, the last surface
// that still carried the old identity after the MCP tool IDs, the `/v1/*`
// routes, the Go module path and the release artifacts had all moved. A grep
// at merge would have proved the tree was clean at that instant and nothing
// more. Wave 3 lands four lanes concurrently and any of them can reintroduce
// the name in a doc afterwards, so the check is a test.
//
// It is the binary-name counterpart of toolname_drift_test.go and follows the
// same construction: derive the corpus rather than fixing a glob list, keep a
// named exclusion list where every entry says why, and prove non-vacuity so a
// green result cannot mean the scanner stopped matching.
//
// SCOPE. This guard is about the shipped *name*. It does not and cannot assert
// that the retired single-root environment variable stopped working — a string
// can be removed while the behavior survives under a new spelling. That claim
// belongs to cmd/tesseract/retired_env_test.go, which asserts the behavior
// directly.
package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── What is forbidden ──────────────────────────────────────────────────

// retiredBinaryName is the name the daemon binary was renamed away from.
//
// It is assembled from two literals so that this file — which the guard scans
// like every other file in the repo — does not itself contain the token it
// forbids. The alternative is a third entry on the exclusion list below, and
// an exclusion that exists only to accommodate the guard is strictly worse: it
// is a real hole in the coverage, opened to solve a cosmetic problem.
//
// The risk this trades into is that a wrong value here would make the guard
// pass over a repo full of violations. TestRetiredNameScannerFindsKnownUses
// closes it with an external positive control rather than a restatement.
const retiredBinaryName = "context" + "d"

// retiredRootEnvVar is the retired single-root environment variable. It is
// derived, not spelled, for the same reason as retiredBinaryName.
var retiredRootEnvVar = strings.ToUpper(retiredBinaryName) + "_ROOT"

// ── The exclusion list ─────────────────────────────────────────────────
//
// A guard's exclusions are as load-bearing as its assertions and completely
// invisible in a passing result: an unstated exclusion is indistinguishable
// from a miss, and a list that grows to make a red test go green eventually
// covers a real one. Two rules keep this one honest.
//
// First, an entry may be scoped to the exact identifiers it permits. A
// blanket entry allows the file to say anything; a scoped entry allows only
// the listed tokens and still fails on every other form of the name. Prefer
// scoped — blanket is for files where the set genuinely cannot be enumerated.
//
// Second, entries expire. TestRetiredNameExclusionsAreCurrent fails when an
// excluded file no longer contains the name, or when a scoped entry lists a
// token that is no longer there, so a stale entry cannot sit in the list
// silently absorbing a future violation of the same name.

type nameExclusion struct {
	// Tokens are the exact identifiers this file may contain. nil means every
	// occurrence is permitted — a blanket exclusion, which must justify why
	// the set cannot be enumerated.
	Tokens []string
	Why    string
}

var binaryNameExclusions = map[string]nameExclusion{
	"CHANGELOG.md": {
		Tokens: nil,
		Why: "Release notes describe identifier changes, so they legitimately name superseded " +
			"identifiers — including, in the entry for this very rename, the name that was " +
			"renamed away from. Scanning them would turn this guard red on exactly the commit " +
			"that ships the rename, and the natural way to satisfy it would be deleting the " +
			"history that makes the release notes useful. Blanket rather than scoped because " +
			"the changelog names the retired binary in every form it ever took — bare, as a " +
			"subcommand, as a source path, and as the environment variable — and every future " +
			"release note may add another. toolname_drift_test.go excludes this same file for " +
			"this same reason.",
	},
	"cmd/tesseract/retired_env_test.go": {
		Tokens: []string{retiredRootEnvVar},
		Why: "The test that proves the retired single-root environment variable is no longer " +
			"honored has to name the variable in order to set it. Scoped to that one token: " +
			"this file may name the retired environment variable and nothing else, so a " +
			"stray reference to the old binary name in it still fails the guard.",
	},
	"docs/guides/tesseract-adoption-and-v0.9-migration.md": {
		Tokens: []string{retiredBinaryName, retiredRootEnvVar},
		Why: "The v0.9 migration guide must identify the retired binary and single-root " +
			"environment variable so operators can remove them safely. Scoped to those two " +
			"tokens: any other identifier containing the retired name still fails the guard.",
	},
}

// nonBinaryIdentifiers are whole identifiers that contain the retired name as
// a substring by coincidence and do not refer to the binary. Matching is exact
// and case-sensitive: these are Go identifiers, and a different casing is a
// different thing that has not been examined.
//
// This is the narrow counterpart to the file exclusions above. A file
// exclusion forgives a location; an entry here forgives a *name*, everywhere
// it appears, which is the right granularity for a coincidence — the collision
// is a property of how the word is spelled, not of where it is written.
//
// Adding an entry is a claim that the identifier names something other than
// the daemon binary. Say what, so the next reader can check it rather than
// trust it. TestRetiredNameExclusionsAreCurrent expires entries that no longer
// appear anywhere in the corpus, so this cannot quietly accumulate.
var nonBinaryIdentifiers = map[string]string{
	"EventContextDeleted": "internal/plugin/events.go: the plugin event constant for \"context.deleted\". " +
		"\"Context\" + \"Deleted\" spells the retired name by coincidence; Context is one of the three " +
		"memory domains and this constant is correctly named.",
	"EmitContextDeleted": "internal/plugin/events.go: the Host method that emits the event constant above, " +
		"and the same coincidental spelling.",
}

// ── Scanning ───────────────────────────────────────────────────────────

// nameHit is one occurrence of the retired name.
type nameHit struct {
	File  string // repo-relative
	Line  int    // 1-based line within the file; 0 when the hit is in the PATH, not the content
	Token string // the full identifier the match sits inside
}

// findRetiredName reports every occurrence of the retired name in text, as the
// full identifier surrounding it.
//
// Matching is a case-insensitive PLAIN SUBSTRING, with no bound on either
// side. Every occurrence is then resolved to the whole [A-Za-z0-9_] identifier
// it sits inside, and forgiven only if that identifier is enumerated in
// nonBinaryIdentifiers.
//
// Case-insensitive, because the retired identity appears in more than one
// case — lowercase as the binary and its source directory, uppercase as the
// environment variable — and a sentence that opens with the name capitalized
// is the same stale reference as one that does not.
//
// This rule replaces an earlier one that skipped a match when the next
// character was a letter or a digit. That version was written to spare
// internal/plugin/events.go, which declares EventContextDeleted for the
// "context.deleted" event — "Context" followed by "Deleted" spells the retired
// name by pure coincidence, and Context is one of the three memory domains, so
// that constant is correctly named. Sharpening the rule was the right instinct
// and the wrong shape: it also spared every identifier of the form
// <name><Letter>, and one of those was real. The deprecation shim this ticket
// removed had an `apply<Name>RootShim` helper that carried the retired binary
// name in a Go identifier at three sites, and the boundary rule was blind to
// all three. Those sites are absent from the tree now because the shim was
// deleted, not because the guard could ever see them.
//
// The lesson is symmetric and worth keeping: a sharpened rule can be
// under-inclusive in exactly the way an over-broad exclusion list is
// over-inclusive, and both are invisible in a passing result. An enumerated
// identifier allowlist is precise AND reviewable — what it forgives is written
// down, and it expires the same way the file exclusions do.
func findRetiredName(file, content string) []nameHit {
	var hits []nameHit
	lower := strings.ToLower(content)
	lineNo := 1
	prev := 0
	for i := 0; i+len(retiredBinaryName) <= len(lower); i++ {
		if lower[i:i+len(retiredBinaryName)] != retiredBinaryName {
			continue
		}
		token := identifierAround(content, i, i+len(retiredBinaryName))
		if _, ok := nonBinaryIdentifiers[token]; ok {
			continue // enumerated as something other than the binary
		}
		lineNo += strings.Count(content[prev:i], "\n")
		prev = i
		hits = append(hits, nameHit{File: file, Line: lineNo, Token: token})
	}
	return hits
}

// identifierAround expands a match to the full [A-Za-z0-9_] identifier it sits
// inside, so a scoped exclusion can name the exact token it permits.
func identifierAround(s string, start, end int) string {
	for start > 0 && isIdentRune(s[start-1]) {
		start--
	}
	for end < len(s) && isIdentRune(s[end]) {
		end++
	}
	return s[start:end]
}

func isIdentRune(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// scanCorpus returns every occurrence of the retired name across the files
// this repository ships — in their contents and in their paths.
//
// The corpus is `git ls-files --cached --others --exclude-standard`: tracked
// files plus untracked ones that are not ignored. Walking the filesystem
// instead would sweep in genuinely ignored build output — frontend/dist,
// the Go build caches — which no commit can ever fix. Including
// untracked-but-not-ignored files means a doc added in the same commit is
// covered before it is staged.
//
// What this scope does NOT do is spare a developer whose worktree still holds
// artifacts from before the rename. A stale ./<name> binary from a pre-rename
// `make build` is no longer matched by .gitignore — this ticket repointed that
// pattern at the current binary — so it appears under --others and the guard
// fails on its path. The same is true of .tesseract/tmp/<name>/, the old
// contract-test root. That is a true positive, not a false one: both are the
// retired identity still sitting in the tree, and `rm` fixes both. It is
// called out because the failure arrives with a path that looks like local
// dirt, and the wrong reaction to it is an exclusion entry.
//
// CI is unaffected either way — a fresh checkout has neither artifact.
//
// Paths are scanned as well as contents: the rename moved a source directory
// and two scripts, and a filename is a reference to the binary exactly as much
// as a line of prose is.
func scanCorpus(t *testing.T) []nameHit {
	t.Helper()
	root := repoRoot(t)

	var hits []nameHit
	for _, rel := range corpusFiles(t, root) {
		// A hit in the path has no line to point at; Line 0 says so, and the
		// error formatter drops the ":N" suffix for these.
		for _, h := range findRetiredName(rel, rel) {
			h.Line = 0
			hits = append(hits, h)
		}

		// #nosec G304 -- rel comes from `git ls-files` inside the module root.
		body, readErr := os.ReadFile(filepath.Join(root, rel))
		if readErr != nil {
			// A listed-but-unreadable file is a broken assumption, not a pass.
			t.Fatalf("read %s: %v", rel, readErr)
		}
		if bytesHaveNUL(body) {
			// A binary blob. Its path was scanned above, which is the part
			// that can name the binary meaningfully.
			continue
		}
		hits = append(hits, findRetiredName(rel, string(body))...)
	}
	return hits
}

// corpusFiles lists the repo-relative path of every file this repository
// ships, sorted. Shared by the guard and by the expiry test so the two cannot
// disagree about what "the corpus" means.
func corpusFiles(t *testing.T, root string) []string {
	t.Helper()

	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// Skipping would be worse than failing: a guard that quietly opts out
		// is indistinguishable from a guard that passes.
		t.Fatalf("enumerate the repo corpus with git: %v", err)
	}

	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("git listed no files — the corpus is empty, so a clean result here means nothing")
	}
	sort.Strings(files)
	return files
}

// corpusIdentifiers returns every identifier across the corpus that contains
// the retired name, WITHOUT applying nonBinaryIdentifiers.
//
// The unfiltered form is the point. An allowlist entry cannot be checked
// against the filtered scan, because being removed from that scan is precisely
// what the entry does — asking whether it still appears there would always
// answer no, and the expiry check would fire on every entry forever.
func corpusIdentifiers(t *testing.T, root string) map[string]bool {
	t.Helper()

	seen := map[string]bool{}
	for _, rel := range corpusFiles(t, root) {
		for _, id := range retiredNameIdentifiers(rel) {
			seen[id] = true
		}
		// #nosec G304 -- rel comes from `git ls-files` inside the module root.
		body, readErr := os.ReadFile(filepath.Join(root, rel))
		if readErr != nil {
			t.Fatalf("read %s: %v", rel, readErr)
		}
		if bytesHaveNUL(body) {
			continue
		}
		for _, id := range retiredNameIdentifiers(string(body)) {
			seen[id] = true
		}
	}
	return seen
}

// retiredNameIdentifiers returns every identifier in text containing the
// retired name, with no allowlist applied. See corpusIdentifiers for why the
// unfiltered form has to exist separately from findRetiredName.
func retiredNameIdentifiers(content string) []string {
	var out []string
	lower := strings.ToLower(content)
	for i := 0; i+len(retiredBinaryName) <= len(lower); i++ {
		if lower[i:i+len(retiredBinaryName)] != retiredBinaryName {
			continue
		}
		out = append(out, identifierAround(content, i, i+len(retiredBinaryName)))
	}
	return out
}

func bytesHaveNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// allowed reports whether a hit is covered by a stated exclusion.
func allowed(h nameHit) bool {
	ex, ok := binaryNameExclusions[h.File]
	if !ok {
		return false
	}
	if ex.Tokens == nil {
		return true
	}
	for _, tok := range ex.Tokens {
		if strings.EqualFold(tok, h.Token) {
			return true
		}
	}
	return false
}

// ── Tests ──────────────────────────────────────────────────────────────

// TestNoShippedFileNamesTheRetiredBinary is the guard.
func TestNoShippedFileNamesTheRetiredBinary(t *testing.T) {
	var bad []nameHit
	for _, h := range scanCorpus(t) {
		if !allowed(h) {
			bad = append(bad, h)
		}
	}
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].File != bad[j].File {
			return bad[i].File < bad[j].File
		}
		return bad[i].Line < bad[j].Line
	})
	for _, h := range bad {
		where := h.File
		if h.Line > 0 {
			where = h.File + ":" + strconv.Itoa(h.Line)
		}
		t.Errorf("%s: %q names the retired daemon binary. The binary is `tesseract` (CW-20260825-0013).\n"+
			"    Fix the reference. Add an entry to binaryNameExclusions only if this file MUST keep the "+
			"old name to do its job, and say why — an unstated exclusion is indistinguishable from a miss.",
			where, h.Token)
	}
}

// TestRetiredNameScannerFindsKnownUses is the positive control.
//
// Every other test in this file reports the absence of something, and an
// absence is equally produced by a scanner that no longer matches anything —
// a mistyped retiredBinaryName, a boundary rule tightened until it excludes
// everything. Restating the constant would not detect either; the control has
// to come from outside.
//
// CHANGELOG.md supplies it. It is the one file guaranteed to contain the
// retired name, because it carries the release note for this rename and
// cannot describe the rename without naming what was renamed — the same fact
// that puts it on the exclusion list makes it the control.
func TestRetiredNameScannerFindsKnownUses(t *testing.T) {
	root := repoRoot(t)
	// #nosec G304 -- the filename is a compile-time constant joined to the
	// verified module root; no external input reaches this path.
	body, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	hits := findRetiredName("CHANGELOG.md", string(body))
	if len(hits) == 0 {
		t.Fatal("the scanner found no occurrence of the retired name in CHANGELOG.md, which documents the " +
			"rename and must name it. The scanner is broken — retiredBinaryName or the boundary rule is " +
			"wrong — and every clean result this file reports is meaningless.")
	}
}

// TestRetiredNameGuardCatchesAReintroduction pins the matching rule from both
// sides, so neither a loosened nor a tightened rule can pass quietly.
//
// The flag fixtures are composed from retiredBinaryName rather than written
// out, for the reason given on that constant. The pass fixtures are real
// strings from this repo — what the rule must not false-positive on.
//
// The camelCase cases are the regression fixtures. An earlier rule skipped a
// match followed by a letter, which silently spared the removed shim's
// `apply<Name>RootShim` helper — a genuine reference to the retired binary
// that lived in this tree at three sites. Anything of the form <name><Letter>
// must flag unless the whole identifier is enumerated in nonBinaryIdentifiers.
func TestRetiredNameGuardCatchesAReintroduction(t *testing.T) {
	mustFlag := []struct {
		name string
		text string
	}{
		{"bare invocation", "Run `" + retiredBinaryName + " serve` to start the daemon.\n"},
		{"subcommand", retiredBinaryName + " path\n"},
		{"source directory path", "go install ./cmd/" + retiredBinaryName + "/\n"},
		{"script filename", "scripts/" + retiredBinaryName + "-smoke.sh\n"},
		{"gitignore pattern", "/" + retiredBinaryName + "\n"},
		{"environment variable", "export " + retiredRootEnvVar + "=/tmp/x\n"},
		{"capitalized in prose", "The " + strings.ToUpper(retiredBinaryName[:1]) + retiredBinaryName[1:] + " daemon starts on :8089.\n"},
		{"end of sentence", "The binary was called " + retiredBinaryName + ".\n"},

		// Regression: the shim identifier the previous rule could not see.
		{
			name: "camelCase Go identifier — the real miss",
			text: "func apply" + strings.ToUpper(retiredBinaryName[:1]) + retiredBinaryName[1:] + "RootShim(stderr *os.File) {\n",
		},
		{"camelCase call site", "\tapply" + strings.ToUpper(retiredBinaryName[:1]) + retiredBinaryName[1:] + "RootShim(stderr)\n"},
		{"lowerCamel variable", "\t" + retiredBinaryName + "Path := \"/usr/local/bin\"\n"},
		{"plural", "We shipped two " + retiredBinaryName + "s.\n"},
		{"unseparated caps", "export " + strings.ToUpper(retiredBinaryName) + "ROOT=/tmp/x\n"},
	}
	for _, tc := range mustFlag {
		t.Run(tc.name, func(t *testing.T) {
			if hits := findRetiredName("fixture", tc.text); len(hits) == 0 {
				t.Fatalf("guard did not flag %q", tc.text)
			}
		})
	}

	mustPass := []struct {
		name string
		text string
	}{
		{
			// Forgiven by name, not by location — the entry in
			// nonBinaryIdentifiers is what makes this pass.
			name: "plugin event constant that merely contains the letters",
			text: "EventContextDeleted = \"context.deleted\"\n",
		},
		{"emit helper for the same event", "func (h *Host) EmitContextDeleted(namespace, key string) {\n"},
		{"the current binary name", "Run `tesseract serve` to start the daemon.\n"},
		{"the context domain packages", "internal/contextapi, internal/contextstore, internal/contextcli\n"},
		{"the context tool family", "context_write, context_head, context_promote_request\n"},
	}
	for _, tc := range mustPass {
		t.Run(tc.name, func(t *testing.T) {
			if hits := findRetiredName("fixture", tc.text); len(hits) != 0 {
				t.Fatalf("guard false-positived on %q: %+v", tc.text, hits)
			}
		})
	}
}

// TestRetiredNameExclusionsAreCurrent expires the exclusion list.
//
// An entry whose file no longer contains the name is dead weight that would
// silently absorb a future real violation in that file, and a scoped entry
// listing a token that is no longer present has the same effect for that
// token. Both must come out, and the only way that reliably happens is a test
// that fails until they do.
func TestRetiredNameExclusionsAreCurrent(t *testing.T) {
	root := repoRoot(t)

	for path, ex := range binaryNameExclusions {
		if strings.TrimSpace(ex.Why) == "" {
			t.Errorf("binaryNameExclusions[%q]: Why is empty — an exclusion with no stated reason is "+
				"indistinguishable from a miss", path)
		}

		// #nosec G304 -- path is a key of binaryNameExclusions, a compile-time
		// map literal in this file; no external input reaches this path.
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Errorf("binaryNameExclusions[%q]: file cannot be read (%v) — drop the entry or fix the path; "+
				"an exclusion for a file that is not there covers nothing and hides the next file of that name", path, err)
			continue
		}

		hits := findRetiredName(path, string(body))
		if len(hits) == 0 {
			t.Errorf("binaryNameExclusions[%q] no longer contains the retired name — remove the entry so it "+
				"cannot mask a future violation (was: %s)", path, ex.Why)
			continue
		}

		if ex.Tokens == nil {
			continue
		}
		present := map[string]bool{}
		for _, h := range hits {
			present[strings.ToLower(h.Token)] = true
		}
		for _, tok := range ex.Tokens {
			if !present[strings.ToLower(tok)] {
				t.Errorf("binaryNameExclusions[%q] permits token %q, which no longer appears in the file — "+
					"narrow the entry (was: %s)", path, tok, ex.Why)
			}
		}
	}

	// The identifier allowlist expires on the same terms. An entry for a name
	// that is gone from the tree forgives nothing today and would silently
	// forgive a future identifier that happens to reuse the spelling.
	inCorpus := corpusIdentifiers(t, root)
	for id, why := range nonBinaryIdentifiers {
		if strings.TrimSpace(why) == "" {
			t.Errorf("nonBinaryIdentifiers[%q]: reason is empty — say what the identifier names instead", id)
		}
		if !inCorpus[id] {
			t.Errorf("nonBinaryIdentifiers[%q] no longer appears anywhere in the corpus — remove it so it "+
				"cannot forgive a future identifier of the same name (was: %s)", id, why)
		}
		if !strings.Contains(strings.ToLower(id), retiredBinaryName) {
			t.Errorf("nonBinaryIdentifiers[%q] does not contain the retired name, so the guard would never "+
				"have flagged it — the entry does nothing", id)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// loadTypeRegistry's failure posture is a design decision, so it is pinned
// here rather than left to whoever next edits the boot sequence.
//
// The rule: absent is fine, malformed is fatal. That is deliberately harsher
// than config.Load, which warns and falls back to defaults, and the asymmetry
// is the mitigation for what CW-20260909-0034 traded away — enforcement
// authority now lives in a file a bad edit can change without a compile
// ([[config_is_policy_code_is_engine]]).

func TestLoadTypeRegistry_AbsentFileKeepsTheDefaults(t *testing.T) {
	before := typeregistry.Default().Values(typeregistry.VocabKnowledgeFacetKind)

	if err := loadTypeRegistry(filepath.Join(t.TempDir(), "types.yaml")); err != nil {
		t.Fatalf("a missing types.yaml is normal and must not error: %v", err)
	}

	after := typeregistry.Default().Values(typeregistry.VocabKnowledgeFacetKind)
	if strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("vocabulary changed with no file: %v -> %v", before, after)
	}
}

// TestLoadTypeRegistry_MalformedFileIsFatal is the case worth being harsh
// about. A typo in config.yaml costs a payload_mode; a typo here means the
// process enforces a DIFFERENT vocabulary than the operator declared —
// silently widening what can be written, or narrowing it. Refusing to start is
// recoverable in the time it takes to fix the file. Writing rows under a
// vocabulary nobody chose is not.
func TestLoadTypeRegistry_MalformedFileIsFatal(t *testing.T) {
	for name, body := range map[string]string{
		"unparseable":       "\tnot: [valid\n  yaml\n",
		"unknown field":     "vocabularies:\n  - vocabulary_id: memory.type\n    close: true\n    types: []\n",
		"no vocabulary_id":  "vocabularies:\n  - closed: true\n    types: []\n",
		"dropped rank knob": "vocabularies:\n  - vocabulary_id: context.record_type\n    types:\n      - type_id: decision/adr\n        retrieval_rank_bias: 1.4\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "types.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			err := loadTypeRegistry(path)
			if err == nil {
				t.Fatal("a malformed types.yaml was accepted; the daemon would run on a vocabulary nobody declared")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the file an operator has to fix", err)
			}
		})
	}
}

// TestLoadTypeRegistry_InstallsTheDeclaredVocabulary is the positive half:
// what the file says is what the process enforces. Without this, "malformed is
// fatal" would be satisfiable by a loader that validated and then discarded.
func TestLoadTypeRegistry_InstallsTheDeclaredVocabulary(t *testing.T) {
	restore := typeregistry.Install(typeregistry.NewRegistry())
	t.Cleanup(restore)

	path := filepath.Join(t.TempDir(), "types.yaml")
	if err := os.WriteFile(path, []byte(
		"vocabularies:\n"+
			"  - vocabulary_id: memory.type\n"+
			"    closed: true\n"+
			"    types:\n"+
			"      - type_id: field_notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadTypeRegistry(path); err != nil {
		t.Fatalf("loadTypeRegistry: %v", err)
	}

	reg := typeregistry.Default()
	if !reg.Allows(typeregistry.VocabMemoryType, "field_notes") {
		t.Error("the declared type is not in force")
	}
	if reg.Allows(typeregistry.VocabMemoryType, "decisions") {
		t.Error("a default the file replaced is still in force")
	}
	// A vocabulary the file did not name keeps its shipped values.
	if !reg.Allows(typeregistry.VocabKnowledgeFacetKind, "wiki_page") {
		t.Error("loading one vocabulary disturbed another")
	}
}

// TestPathReportsTheTypesFile: an optional config file nobody can find is a
// config file nobody writes.
func TestPathReportsTheTypesFile(t *testing.T) {
	hermeticLayout(t)

	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	if code := runPath(stdout, stderr); code != 0 {
		t.Fatalf("tesseract path exited %d", code)
	}
	out, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "types-file") {
		t.Errorf("tesseract path does not report types-file:\n%s", out)
	}
	if !strings.Contains(string(out), "types.yaml") {
		t.Errorf("tesseract path does not name types.yaml:\n%s", out)
	}
}

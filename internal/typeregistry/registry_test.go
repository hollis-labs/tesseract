package typeregistry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// ---- The three vocabularies ----------------------------------------------

// TestEveryShippedVocabularyIsRegistered is the anti-regression for the point
// of this package. A registry that serves only context types is the state
// CW-20260909-0034 existed to leave: four mechanisms defining what a type can
// be, with the config-driven one aimed at the least-used store.
//
// The list is hand-stated rather than read back from DefaultVocabularies, so a
// vocabulary appearing or vanishing is a deliberate edit in two places.
// `event.type` joined it with the Event domain (CW-20260909-0035).
func TestEveryShippedVocabularyIsRegistered(t *testing.T) {
	r := typeregistry.NewRegistry()
	want := []string{
		typeregistry.VocabContextRecordType,
		typeregistry.VocabEventType,
		typeregistry.VocabKnowledgeFacetKind,
		typeregistry.VocabMemoryType,
	}
	got := r.VocabularyIDs()
	if len(got) != len(want) {
		t.Fatalf("vocabularies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vocabularies = %v, want %v", got, want)
		}
	}
}

// TestEventTypeVocabulary states the event {type} segment's values by hand.
//
// Two, not three: `friction` is the obvious third and is deliberately absent
// until something writes it. See defaultEventTypes for why a vocabulary entry
// no producer can fill is worse than a gap.
func TestEventTypeVocabulary(t *testing.T) {
	r := typeregistry.NewRegistry()
	want := []string{"journal", "reasoning"}
	got := r.Values(typeregistry.VocabEventType)
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event types = %v, want %v", got, want)
		}
	}
	if !r.Allows(typeregistry.VocabEventType, "journal") {
		t.Error("journal should be allowed")
	}
	if r.Allows(typeregistry.VocabEventType, "friction") {
		t.Error("friction is not in the shipped vocabulary yet")
	}
}

func TestMemoryTypeVocabulary(t *testing.T) {
	r := typeregistry.NewRegistry()
	want := []string{
		"decisions", "feedback", "followups", "learnings",
		"limitations", "notes", "outcomes", "todos",
	}
	got := r.Values(typeregistry.VocabMemoryType)
	if len(got) != len(want) {
		t.Fatalf("memory types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("memory types = %v, want %v", got, want)
		}
	}
	if !r.IsClosed(typeregistry.VocabMemoryType) {
		t.Error("memory.type should declare closed: true")
	}
}

// TestKnowledgeKindVocabularyIsClosedAndCarriesApprovedKinds covers both halves
// of what this slice does to the knowledge kinds: closure survives the move
// from a Go map to a declaration, and the two operator-approved kinds are in
// the set.
//
// Both landed ahead of their first write, which is the documented exception to
// the "a producer already emits it" bar rather than a lapse in it. wiki_page
// was approved 2026-09-09 because Loom is built and blocked waiting for it
// ([[tesseract_wiki_page_kind_approved]]); boot_prompt was approved 2026-09-10
// on Chrispian's direction, with the skill that writes it shipping in the same
// change (CW-20260910-0068).
func TestKnowledgeKindVocabularyIsClosedAndCarriesApprovedKinds(t *testing.T) {
	r := typeregistry.NewRegistry()
	if !r.IsClosed(typeregistry.VocabKnowledgeFacetKind) {
		t.Fatal("knowledge.facet_kind must declare closed: true")
	}
	kinds := r.Values(typeregistry.VocabKnowledgeFacetKind)
	if len(kinds) != 12 {
		t.Fatalf("knowledge kinds = %d (%v), want 12", len(kinds), kinds)
	}
	if !r.Allows(typeregistry.VocabKnowledgeFacetKind, "wiki_page") {
		t.Error("wiki_page is not in the vocabulary; Loom stays blocked")
	}
	if !r.Allows(typeregistry.VocabKnowledgeFacetKind, "boot_prompt") {
		t.Error("boot_prompt is not in the vocabulary; the boot-prompt skill cannot write")
	}
	// The retired and mis-cased spellings stay out.
	// "learning" was retired by CW-20260910-0067: it duplicated the `learnings`
	// memory type one letter apart, across a domain boundary a record cannot be
	// moved back over.
	for _, gone := range []string{"issue/bug", "learning", "mcp-server", "session-close", "wiki-page", "boot-prompt"} {
		if r.Allows(typeregistry.VocabKnowledgeFacetKind, gone) {
			t.Errorf("vocabulary accepts %q", gone)
		}
	}
}

// TestClosedVocabularyRejectsUndeclaredValues is the property the fold had to
// preserve. Closure is now a declaration rather than a consequence of the
// container, and the observable behavior has to be the same either way.
func TestClosedVocabularyRejectsUndeclaredValues(t *testing.T) {
	r := typeregistry.NewRegistry()
	for _, vocab := range []string{
		typeregistry.VocabMemoryType,
		typeregistry.VocabKnowledgeFacetKind,
	} {
		if r.Allows(vocab, "definitely_not_a_declared_value") {
			t.Errorf("%s accepted an undeclared value", vocab)
		}
		if r.Allows(vocab, "") {
			t.Errorf("%s accepted the empty value", vocab)
		}
	}
}

// TestUnknownVocabularyAllowsNothing pins the fail-closed reading of a typo'd
// vocabulary ID. "No such vocabulary" must not read as "no rules here" — that
// is how a validation call silently becomes a no-op.
func TestUnknownVocabularyAllowsNothing(t *testing.T) {
	r := typeregistry.NewRegistry()
	if r.Allows("memory.types", "notes") { // note the plural typo
		t.Error("an unknown vocabulary permitted a value")
	}
	if r.IsClosed("memory.types") {
		t.Error("an unknown vocabulary reported itself closed")
	}
	if got := r.Values("memory.types"); got != nil {
		t.Errorf("Values on an unknown vocabulary = %v, want nil", got)
	}
}

// ---- Loading -------------------------------------------------------------

// TestLoadReplacesAVocabularyRatherThanMergingIt is what makes an operator's
// file authoritative. A merge would make it impossible to REMOVE a value by
// editing types.yaml, and narrowing a vocabulary is the case that has to work
// — otherwise "users provide the policy" means "users may only add to ours."
func TestLoadReplacesAVocabularyRatherThanMergingIt(t *testing.T) {
	r := typeregistry.NewRegistry()
	if err := r.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: memory.type
    closed: true
    types:
      - type_id: notes
      - type_id: decisions
`)); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if got := r.Values(typeregistry.VocabMemoryType); len(got) != 2 {
		t.Fatalf("memory types = %v, want exactly the two declared", got)
	}
	if r.Allows(typeregistry.VocabMemoryType, "outcomes") {
		t.Error("a type the file dropped is still allowed")
	}
	// A vocabulary the file did not name keeps its defaults.
	if !r.Allows(typeregistry.VocabKnowledgeFacetKind, "investigation") {
		t.Error("loading one vocabulary disturbed another")
	}
}

// TestLoadCanOpenAClosedVocabulary is the accepted cost of moving enforcement
// authority to config, asserted rather than left implicit.
//
// Under [[config_is_policy_code_is_engine]] the engine enforces and the
// operator owns the list, so a file CAN open what governance closed. That is a
// real weakening versus a Go map needing a compile; it was accepted knowingly.
// If this test ever has to be deleted to make something else pass, the
// decision changed and [[kinds_taxonomy]] needs revising with it.
func TestLoadCanOpenAClosedVocabulary(t *testing.T) {
	r := typeregistry.NewRegistry()
	if err := r.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: knowledge.facet_kind
    closed: false
    types:
      - type_id: note
`)); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if r.IsClosed(typeregistry.VocabKnowledgeFacetKind) {
		t.Fatal("the file declared the vocabulary open and it reports closed")
	}
	if !r.Allows(typeregistry.VocabKnowledgeFacetKind, "anything_at_all") {
		t.Error("an open vocabulary rejected an undeclared value")
	}
}

// TestLoadIsAtomic asserts one bad entry changes nothing. A partially applied
// vocabulary would leave the process enforcing a list that exists in neither
// the defaults nor the file, which is the state nobody can reason about.
func TestLoadIsAtomic(t *testing.T) {
	r := typeregistry.NewRegistry()
	before := r.Values(typeregistry.VocabMemoryType)

	err := r.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: memory.type
    types:
      - type_id: notes
  - vocabulary_id: knowledge.facet_kind
    types:
      - type_id: ""
`))
	if err == nil {
		t.Fatal("a type entry with no type_id was accepted")
	}
	if got := r.Values(typeregistry.VocabMemoryType); len(got) != len(before) {
		t.Errorf("a rejected load still changed memory.type: %v", got)
	}
}

func TestLoadRejectsMalformedConfig(t *testing.T) {
	for name, body := range map[string]string{
		"no vocabulary_id": "vocabularies:\n  - closed: true\n    types: []\n",
		"duplicate vocabulary": "vocabularies:\n" +
			"  - vocabulary_id: memory.type\n    types: []\n" +
			"  - vocabulary_id: memory.type\n    types: []\n",
		"no view_id":         "views:\n  - types: [\"task/spec\"]\n",
		"schema_ref half":    "vocabularies:\n  - vocabulary_id: memory.type\n    types:\n      - type_id: notes\n        schema_ref:\n          source_path: /tmp/x.json\n",
		"schema hash junk":   "vocabularies:\n  - vocabulary_id: memory.type\n    types:\n      - type_id: notes\n        schema_ref:\n          source_path: /tmp/x.json\n          schema_hash: nothex\n",
		"negative summary":   "vocabularies:\n  - vocabulary_id: memory.type\n    types:\n      - type_id: notes\n        max_summary_bytes: -1\n",
		"not yaml or json":   "\tthis: [is: not\n  valid\n",
		"unknown type field": "vocabularies:\n  - vocabulary_id: memory.type\n    types:\n      - type_id: notes\n        icon: \"\\U0001F5C2\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := typeregistry.NewRegistry().LoadFromBytes([]byte(body)); err == nil {
				t.Error("malformed config accepted")
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(path, []byte(`
vocabularies:
  - vocabulary_id: memory.type
    closed: true
    types:
      - type_id: notes
        default_ttl: "24h"
        max_summary_bytes: 512
        schema_ref:
          source_path: schemas/notes.json
          schema_hash: 0000000000000000000000000000000000000000000000000000000000000000
`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := typeregistry.NewRegistry()
	if err := r.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	got, ok := r.Lookup(typeregistry.VocabMemoryType, "notes")
	if !ok {
		t.Fatal("notes not loaded")
	}
	if got.ParseDefaultTTL() != 24*time.Hour {
		t.Errorf("default TTL = %v, want 24h", got.ParseDefaultTTL())
	}
	if got.MaxSummaryBytes != 512 {
		t.Errorf("max_summary_bytes = %d, want 512", got.MaxSummaryBytes)
	}
	if got.SchemaRef == nil || got.SchemaRef.SourcePath != "schemas/notes.json" {
		t.Errorf("schema_ref = %+v, want the declared path", got.SchemaRef)
	}
}

// TestLoadFromFileNamesThePath keeps a bad registry file diagnosable. The
// process refuses to boot on this error, so the message is the whole of what
// an operator gets.
func TestLoadFromFileNamesThePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(path, []byte("vocabularies:\n  - closed: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := typeregistry.NewRegistry().LoadFromFile(path)
	if err == nil {
		t.Fatal("malformed file accepted")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
}

// ---- Type declaration ----------------------------------------------------

// TestDroppedKnobsAreRefusedRatherThanIgnored asserts the two culled fields
// cannot come back through the config door.
//
// A lenient YAML loader would accept a file declaring retrieval_rank_bias and
// do nothing with it — which reads, to whoever wrote it, exactly like a knob
// that works. The drops are close to a one-way door per
// [[tesseract_type_declaration_field_set]], so the file has to say so.
func TestDroppedKnobsAreRefusedRatherThanIgnored(t *testing.T) {
	for _, knob := range []string{
		"        retrieval_rank_bias: 9.9",
		`        promotion_rules: ["draft->reviewed:requires_human_approval"]`,
	} {
		err := typeregistry.NewRegistry().LoadFromBytes([]byte(
			"vocabularies:\n" +
				"  - vocabulary_id: context.record_type\n" +
				"    types:\n" +
				"      - type_id: decision/adr\n" +
				knob + "\n"))
		if err == nil {
			t.Errorf("a config declaring %q loaded clean; the knob is gone, and a "+
				"file naming it must be told rather than quietly ignored", strings.TrimSpace(knob))
		}
	}
}

// TestMisspelledGovernanceKeyIsRefused is the case strict decoding exists for.
// `close: true` for `closed: true` would leave a vocabulary OPEN while reading,
// to whoever wrote it, exactly like closing it.
func TestMisspelledGovernanceKeyIsRefused(t *testing.T) {
	err := typeregistry.NewRegistry().LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: knowledge.facet_kind
    close: true
    types:
      - type_id: note
`))
	if err == nil {
		t.Fatal("`close:` was accepted as `closed:`")
	}
}

func TestTypeHasAllowedStatus(t *testing.T) {
	ct := typeregistry.Type{TypeID: "test", AllowedStatuses: []string{"draft", "reviewed"}}
	if !ct.HasAllowedStatus("draft") {
		t.Error("draft should be allowed")
	}
	if ct.HasAllowedStatus("canonical") {
		t.Error("canonical should not be allowed")
	}
	open := typeregistry.Type{TypeID: "open"}
	if !open.HasAllowedStatus("canonical") {
		t.Error("an empty AllowedStatuses should permit every valid status")
	}
	if open.HasAllowedStatus("bogus") {
		t.Error("an empty AllowedStatuses should still reject an invalid status")
	}
}

// ---- The process registry ------------------------------------------------

func TestInstallAndRestore(t *testing.T) {
	custom := typeregistry.NewRegistry()
	if err := custom.LoadFromBytes([]byte(`
vocabularies:
  - vocabulary_id: memory.type
    closed: true
    types:
      - type_id: onlythis
`)); err != nil {
		t.Fatal(err)
	}

	restore := typeregistry.Install(custom)
	if !typeregistry.Default().Allows(typeregistry.VocabMemoryType, "onlythis") {
		t.Error("Install did not take effect")
	}
	if typeregistry.Default().Allows(typeregistry.VocabMemoryType, "decisions") {
		t.Error("the installed registry still carries the default vocabulary")
	}
	restore()
	if !typeregistry.Default().Allows(typeregistry.VocabMemoryType, "decisions") {
		t.Error("restore did not put the previous registry back")
	}
}

// TestShippedExampleMatchesTheDefaults keeps examples/types.yaml honest.
//
// The example restates two vocabularies in full, which is the only way to show
// what "replace, not merge" means. That makes it a second copy of the shipped
// list, and a second copy drifts — an operator who copies it after a kind is
// added would silently REMOVE that kind from their install.
func TestShippedExampleMatchesTheDefaults(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "types.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shipped example missing: %v", err)
	}
	loaded := typeregistry.NewRegistry()
	if err := loaded.LoadFromFile(path); err != nil {
		t.Fatalf("the shipped example does not load: %v", err)
	}

	defaults := typeregistry.NewRegistry()
	for _, vocab := range []string{
		typeregistry.VocabKnowledgeFacetKind,
		typeregistry.VocabMemoryType,
	} {
		want := defaults.Values(vocab)
		got := loaded.Values(vocab)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("examples/types.yaml %s = %v, want the shipped %v", vocab, got, want)
		}
		if !loaded.IsClosed(vocab) {
			t.Errorf("examples/types.yaml leaves %s open; the shipped vocabulary is closed", vocab)
		}
		// Hot fields drift the same way the type list does, and worse: a
		// declaration is what tells an operator which fields carry an index,
		// so an example that names a different set than the defaults is a
		// performance claim about a query nobody indexed. Compared per type
		// rather than as a flat set, because which TYPE declares a field is
		// half of what the declaration says.
		for _, typeID := range want {
			wantType, _ := defaults.Lookup(vocab, typeID)
			gotType, ok := loaded.Lookup(vocab, typeID)
			if !ok {
				continue // the Values comparison above already reported this
			}
			if strings.Join(gotType.HotFields, ",") != strings.Join(wantType.HotFields, ",") {
				t.Errorf("examples/types.yaml %s type %q hot_fields = %v, want the shipped %v",
					vocab, typeID, gotType.HotFields, wantType.HotFields)
			}
		}
	}
}

// TestLoadRejectsAnUnparseableDefaultTTL. ParseDefaultTTL swallows a parse
// error and answers zero, so an accepted-but-invalid duration means NO EXPIRY
// — a retention change from a typo, on a file where a misspelled key is
// already fatal. The loader is where an operator can still be told.
func TestLoadRejectsAnUnparseableDefaultTTL(t *testing.T) {
	for _, bad := range []string{"24hr", "1 day", "336", "-336h", "forever"} {
		err := typeregistry.NewRegistry().LoadFromBytes([]byte(
			"vocabularies:\n" +
				"  - vocabulary_id: memory.type\n" +
				"    types:\n" +
				"      - type_id: notes\n" +
				"        default_ttl: \"" + bad + "\"\n"))
		if err == nil {
			t.Errorf("default_ttl %q was accepted; it would silently mean no expiry", bad)
			continue
		}
		if !strings.Contains(err.Error(), "default_ttl") {
			t.Errorf("default_ttl %q rejected with %q, which does not name the field", bad, err)
		}
	}

	// The valid case still loads, so this narrowed rather than closed.
	r := typeregistry.NewRegistry()
	if err := r.LoadFromBytes([]byte(
		"vocabularies:\n" +
			"  - vocabulary_id: memory.type\n" +
			"    types:\n" +
			"      - type_id: notes\n" +
			"        default_ttl: 336h\n")); err != nil {
		t.Fatalf("a valid duration was rejected: %v", err)
	}
	got, _ := r.Lookup(typeregistry.VocabMemoryType, "notes")
	if got.ParseDefaultTTL() != 336*time.Hour {
		t.Errorf("default TTL = %v, want 336h", got.ParseDefaultTTL())
	}
}

// TestLoadRejectsADuplicateView, for the same reason a duplicate vocabulary is
// refused: last-one-wins on a policy file means the effective config is not the
// one an operator reads top to bottom.
func TestLoadRejectsADuplicateView(t *testing.T) {
	err := typeregistry.NewRegistry().LoadFromBytes([]byte(`
views:
  - view_id: task_exec
    types: ["task/spec"]
  - view_id: task_exec
    types: ["runbook"]
`))
	if err == nil {
		t.Fatal("a duplicate view_id was accepted")
	}
	if !strings.Contains(err.Error(), "task_exec") {
		t.Errorf("error %q does not name the duplicated view", err)
	}
}

// TestShippedDefaultsParse covers the one path validateConfig does not gate:
// a Type built as a Go literal in defaults.go. ParseDefaultTTL answers zero for
// an unparseable value, so a typo there would silently drop a TTL that the
// declaration says exists.
func TestShippedDefaultsParse(t *testing.T) {
	r := typeregistry.NewRegistry()
	for _, vocab := range r.VocabularyIDs() {
		for _, ty := range r.Types(vocab) {
			if ty.DefaultTTL == "" {
				continue
			}
			if ty.ParseDefaultTTL() <= 0 {
				t.Errorf("%s %q declares default_ttl %q which parses to %v",
					vocab, ty.TypeID, ty.DefaultTTL, ty.ParseDefaultTTL())
			}
		}
	}
}

package knowledge_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/mcpadapter/skills"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newTestStore(t *testing.T) *knowledge.Store {
	t.Helper()
	s, _ := newTestStoreWithMemory(t)
	return s
}

// newTestStoreWithMemory also hands back the underlying revision store, for the
// tests that must write a NON-knowledge revision to prove the filter works.
func newTestStoreWithMemory(t *testing.T) (*knowledge.Store, *memory.Store) {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	mem := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return knowledge.New(mem), mem
}

func validInput() knowledge.WriteInput {
	return knowledge.WriteInput{
		Namespace: "user/chrispian/knowledge/framework",
		Key:       "framework.go-providers",
		Kind:      "package",
		Source:    "filesystem",
		Pointer:   memory.Pointer{Scheme: "file", Locator: "/abs/path"},
		Summary:   "go-providers: multi-provider AI adapter",
		Author:    memory.Author{AgentID: "indexer", AgentVersion: "1.0"},
		SessionID: "indexer:01HX",
	}
}

func TestWrite_Success(t *testing.T) {
	s := newTestStore(t)
	rev, err := s.Write(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rev.Domain != domains.Knowledge {
		t.Errorf("Domain = %q, want %q", rev.Domain, domains.Knowledge)
	}
	if rev.Facets.Kind != "package" {
		t.Errorf("Facets.Kind = %q, want package", rev.Facets.Kind)
	}
	if rev.Facets.Pointer == nil || rev.Facets.Pointer.ResolvedAt == nil {
		t.Error("ResolvedAt should be auto-populated when nil")
	}
}

func TestWrite_MissingKindRejected(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Kind = ""
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
	if !errors.Is(err, memory.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput wrap", err)
	}
}

func TestWrite_UnknownKindRejected(t *testing.T) {
	s := newTestStore(t)
	for _, kind := range []string{
		"deployment_record", // never in the vocabulary
		"mcp-server",        // the retired hyphenated slug
		"issue/bug",         // the retired Torque-domain kind
		"Doc",               // case matters
		"session close",     // whitespace is not a separator
	} {
		in := validInput()
		in.Kind = kind
		_, err := s.Write(context.Background(), in)
		if err == nil {
			t.Errorf("kind %q: expected rejection", kind)
			continue
		}
		if !errors.Is(err, memory.ErrInvalidInput) {
			t.Errorf("kind %q: err = %v, want ErrInvalidInput wrap", kind, err)
		}
		// The rejection must name the allowed set — a caller that guessed
		// wrong should not have to go read the source to find the right value.
		for _, want := range []string{"doc", "session_close", "mcp_server", "investigation"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("kind %q: error %q does not name allowed kind %q", kind, err, want)
			}
		}
	}
}

// TestWrite_EveryCanonicalKindAccepted is the converse of the rejection test:
// enforcement must not make any canonical kind unwritable. The three
// unpopulated kinds (playbook, learning, handoff) matter most here — they have
// no corpus entries, so nothing else would catch it if they were excluded.
func TestWrite_EveryCanonicalKindAccepted(t *testing.T) {
	s := newTestStore(t)
	vocab := memory.KnowledgeKindVocabulary()
	if len(vocab) != 11 {
		t.Fatalf("vocabulary size = %d, want 11; got %v", len(vocab), vocab)
	}
	for i, kind := range vocab {
		in := validInput()
		in.Kind = kind
		in.Key = fmt.Sprintf("vocab.probe.%d", i)
		rev, err := s.Write(context.Background(), in)
		if err != nil {
			t.Errorf("canonical kind %q rejected: %v", kind, err)
			continue
		}
		if rev.Facets.Kind != kind {
			t.Errorf("stored kind = %q, want %q", rev.Facets.Kind, kind)
		}
	}
}

// TestWrite_MissingKindNamesAllowedSet keeps the empty-kind rejection as
// informative as the unknown-kind one.
func TestWrite_MissingKindNamesAllowedSet(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Kind = ""
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
	if !strings.Contains(err.Error(), "playbook") {
		t.Errorf("error %q does not name the allowed set", err)
	}
}

// skillKindAnchors says, per shipped skill, how to find the passage that
// actually advertises the kind vocabulary and how a kind appears inside it.
//
// The anchors are structural on purpose. A bare "does `doc` appear anywhere in
// the file" check is satisfied by an incidental prose mention, so it would
// notice neither a kind dropping out of the real list nor one being invented
// into it.
var skillKindAnchors = map[string]struct {
	start   string // the passage begins after this literal
	end     string // and ends at this literal (empty => the next markdown heading)
	pattern *regexp.Regexp
}{
	// The table under the vocabulary heading; only a row's first cell counts.
	"facets-and-kinds": {
		start:   "## The `kind` vocabulary",
		pattern: regexp.MustCompile("(?m)^\\|\\s*`([a-z_]+)`\\s*\\|"),
	},
	// The inline list on the `kind` parameter line.
	"knowledge": {
		start:   "**closed vocabulary**:",
		end:     "Anything else",
		pattern: regexp.MustCompile("`([a-z_]+)`"),
	},
}

// documentedKinds extracts the kind set a shipped skill actually advertises.
func documentedKinds(t *testing.T, skill string) map[string]bool {
	t.Helper()
	anchor, ok := skillKindAnchors[skill]
	if !ok {
		t.Fatalf("no kind anchor defined for skill %q", skill)
	}
	body, err := skills.Get(skill)
	if err != nil {
		t.Fatalf("skills.Get(%s): %v", skill, err)
	}

	i := strings.Index(body, anchor.start)
	if i < 0 {
		t.Fatalf("skill %q: anchor %q not found — the doc was restructured and this test can no longer see the vocabulary; update the anchor",
			skill, anchor.start)
	}
	seg := body[i+len(anchor.start):]
	end := anchor.end
	if end == "" {
		end = "\n## "
	}
	if j := strings.Index(seg, end); j >= 0 {
		seg = seg[:j]
	}

	got := map[string]bool{}
	for _, m := range anchor.pattern.FindAllStringSubmatch(seg, -1) {
		got[m[1]] = true
	}
	// Without this the test passes vacuously when the anchor still matches but
	// the passage's shape changed out from under the pattern.
	if len(got) == 0 {
		t.Fatalf("skill %q: found the vocabulary passage but extracted no kinds from it; the pattern no longer matches the doc",
			skill)
	}
	return got
}

// TestShippedSkillsMatchEnforcedVocabulary keeps the enforced enum and the
// shipped agent-facing docs in agreement — in BOTH directions.
//
// An agent reads the skill to learn what it may write. If the skill omits a
// kind, a usable value looks unavailable. If the skill advertises a kind the
// enum rejects, the agent follows the doc and the write fails. Checking only
// that every enum member is documented verifies containment, not agreement,
// and would miss the second case entirely.
func TestShippedSkillsMatchEnforcedVocabulary(t *testing.T) {
	want := map[string]bool{}
	for _, k := range memory.KnowledgeKindVocabulary() {
		want[k] = true
	}

	for _, name := range []string{"facets-and-kinds", "knowledge"} {
		got := documentedKinds(t, name)

		for k := range want {
			if !got[k] {
				t.Errorf("skill %q does not document canonical kind %q", name, k)
			}
		}
		for k := range got {
			if !want[k] {
				t.Errorf("skill %q advertises kind %q, which knowledge_write rejects", name, k)
			}
		}

		// Rejected spellings must not appear anywhere in the file, including
		// prose outside the vocabulary passage. The hyphenated forms matter
		// most: hyphen guidance is what put a hyphenated kind in the corpus.
		body, err := skills.Get(name)
		if err != nil {
			t.Fatalf("skills.Get(%s): %v", name, err)
		}
		for _, stale := range []string{"`mcp-server`", "`issue/bug`", "`decision-record`", "`session-close`"} {
			if strings.Contains(body, stale) {
				t.Errorf("skill %q still presents %s, which knowledge_write rejects", name, stale)
			}
		}
	}
}

func TestWrite_MissingPointerRejected(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Pointer = memory.Pointer{}
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for missing pointer")
	}
}

func TestWrite_MemoryNamespaceRejected(t *testing.T) {
	s := newTestStore(t)
	in := validInput()
	in.Namespace = "user/chrispian/memory"
	_, err := s.Write(context.Background(), in)
	if err == nil {
		t.Fatal("expected knowledge-policy rejection for memory namespace")
	}
}

func TestGetCurrent_Success(t *testing.T) {
	s := newTestStore(t)
	written, err := s.Write(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.GetCurrent(context.Background(), validInput().Namespace, validInput().Key)
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if got.RevisionID != written.RevisionID {
		t.Errorf("RevisionID = %q, want %q", got.RevisionID, written.RevisionID)
	}
	if got.Domain != domains.Knowledge {
		t.Errorf("Domain = %q, want knowledge", got.Domain)
	}
}

func TestGetCurrent_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetCurrent(context.Background(), "user/chrispian/knowledge/missing", "no-such-key")
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound wrap", err)
	}
}

// TestGetCurrent_RefusesAMemoryRevision exercises the contract GetCurrent's doc
// comment states: "callers should not see cross-domain reads."
//
// The comment has said that since the method was written; nothing checked it.
// The sibling read on the memory side did not implement it at all, and a
// memory-domain read of a knowledge namespace returned the knowledge revision
// and reinforced it. Both sides now share one implementation, so this test and
// the memory-side cases in internal/mcpadapter/crossdomain_parity_test.go are
// each other's cross-check rather than two copies of one assertion.
func TestGetCurrent_RefusesAMemoryRevision(t *testing.T) {
	s, mem := newTestStoreWithMemory(t)
	ctx := context.Background()

	// A memory revision at a memory namespace. The mirror arrangement — a
	// memory revision parked under a knowledge namespace — is unreachable:
	// memory.WriteRevision requires "memory" as the penultimate segment, so the
	// write path refuses it. Asking the knowledge store about a memory
	// namespace is the direction that can actually happen.
	const ns, key = "user/chrispian/memory/notes", "cross.domain.probe"
	if _, err := mem.WriteRevision(ctx, memory.WriteInput{
		Domain:     domains.Memory,
		Namespace:  ns,
		MemoryKey:  key,
		Author:     memory.Author{AgentID: "claude"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "sess-xd",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusCanonical,
		Payload:    memory.Payload{Summary: "a memory revision, asked of the knowledge store"},
	}); err != nil {
		t.Fatalf("seed memory revision: %v", err)
	}

	// Positive control: the row really is there and resolvable unfiltered, so
	// the ErrNotFound below is the domain filter and not an empty fixture.
	if _, err := mem.GetCurrent(ctx, ns, key); err != nil {
		t.Fatalf("unfiltered resolve found nothing, so this test proves nothing: %v", err)
	}

	if _, err := s.GetCurrent(ctx, ns, key); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("GetCurrent returned a memory revision to a knowledge caller: err = %v", err)
	}
	if _, err := s.GetHistory(ctx, ns, key); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("GetHistory returned memory revisions to a knowledge caller: err = %v", err)
	}
}

func TestGetHistory_MultipleRevisions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.Write(ctx, validInput())
	if err != nil {
		t.Fatalf("Write first: %v", err)
	}
	in2 := validInput()
	in2.Supersedes = first.RevisionID
	in2.Summary = "go-providers: updated summary"
	second, err := s.Write(ctx, in2)
	if err != nil {
		t.Fatalf("Write second: %v", err)
	}
	revs, err := s.GetHistory(ctx, validInput().Namespace, validInput().Key)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("len(revs) = %d, want 2", len(revs))
	}
	if revs[0].RevisionID != second.RevisionID {
		t.Errorf("newest-first broken: revs[0] = %q, want %q", revs[0].RevisionID, second.RevisionID)
	}
	for _, rev := range revs {
		if rev.Domain != domains.Knowledge {
			t.Errorf("revision %q has Domain %q, want knowledge", rev.RevisionID, rev.Domain)
		}
	}
}

// TestKnowledgeWriteEmitsKnowledgeWriteEvent verifies that knowledge.Write
// (routing through memory.WriteRevision with Domain=Knowledge) emits a
// knowledge.write audit event when the memory store has an audit sink wired.
func TestKnowledgeWriteEmitsKnowledgeWriteEvent(t *testing.T) {
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ms.SetAuditSink(cs)
	ks := knowledge.New(ms)

	_, err = ks.Write(context.Background(), knowledge.WriteInput{
		Namespace: "user/alice/knowledge",
		Key:       "pkg/react",
		Kind:      "package",
		Source:    "npm",
		Pointer:   memory.Pointer{Scheme: "https", Locator: "https://www.npmjs.com/package/react"},
		Summary:   "React UI library",
		Author:    memory.Author{AgentID: "indexer"},
		SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("knowledge.Write: %v", err)
	}

	events, err := cs.ListAuditEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].EventType != contextstore.EventKnowledgeWrite {
		t.Errorf("event_type: got %q, want %q", events[0].EventType, contextstore.EventKnowledgeWrite)
	}
}

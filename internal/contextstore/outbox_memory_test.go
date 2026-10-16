package contextstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newMemoryWithSink(t *testing.T) (*Store, *memory.Store) {
	t.Helper()
	s := newTestStore(t)
	ms := memory.NewStore(s.DB(), nil, "", 0, memory.NoopQueue{})
	ms.SetAuditSink(s)
	ms.SetNamespaceRegistrar(s)
	return s, ms
}

func memSessionInput(key string) memory.WriteInput {
	return memory.WriteInput{
		Domain: domains.Memory, Actor: "user", Namespace: "user/chrispian/session/sess123/memory/notes",
		MemoryKey: key, Author: memory.Author{AgentID: "test-agent", AgentVersion: "1.0"},
		Trigger: memory.TriggerExplicit, SessionID: "manual:01HXXXXX", DerivedFrom: memory.DerivedFromUser,
		Confidence: 0.9, Status: memory.StatusDraft, Summary: "s", Body: "b",
	}
}

// memoryOutbox returns the audit.recorded entries about memory items (not the
// namespace.register events a new namespace also produces).
func memoryOutbox(s *Store) []OutboxEntry {
	all, _ := s.ReadOutbox(context.Background(), 0, 100)
	var out []OutboxEntry
	for _, e := range all {
		if strings.HasPrefix(e.Subject, "msg://memory/") {
			out = append(out, e)
		}
	}
	return out
}

func payloadOf(t *testing.T, e OutboxEntry) map[string]any {
	t.Helper()
	var p map[string]any
	_ = json.Unmarshal(readEnvelope(t, e).Payload, &p)
	return p
}

// A memory write's audit event is recorded in the revision's own transaction,
// with the revision id in ref.
func TestMemoryWriteAuditInRevisionTx(t *testing.T) {
	s, ms := newMemoryWithSink(t)
	rev, err := ms.WriteRevision(context.Background(), memSessionInput("prefs.x"))
	if err != nil {
		t.Fatal(err)
	}
	out := memoryOutbox(s)
	if len(out) != 1 {
		t.Fatalf("memory outbox rows = %d, want 1", len(out))
	}
	// Interim subject form (namespace/key); the item_id switch is the next PR.
	wantSubject := "msg://memory/tesseract/user/chrispian/session/sess123/memory/notes/prefs.x"
	if out[0].Subject != wantSubject {
		t.Fatalf("subject = %s, want %s", out[0].Subject, wantSubject)
	}
	p := payloadOf(t, out[0])
	if p["action"] != "memory.write" || p["ref"] != rev.RevisionID {
		t.Fatalf("payload = %v", p)
	}
	if err := readEnvelope(t, out[0]).Validate(); err != nil {
		t.Fatal(err)
	}
	// The audit row exists too, once.
	n := 0
	evs, _ := s.ListAuditEvents(context.Background(), 100)
	for _, e := range evs {
		if e.EventType == "memory.write" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("memory.write audit rows = %d, want 1 (no double emit after commit)", n)
	}
}

// Promote is one transaction with three events: target write, source
// deprecate, umbrella promote.
func TestMemoryPromoteIsOneTxWithThreeEvents(t *testing.T) {
	s, ms := newMemoryWithSink(t)
	src, err := ms.WriteRevision(context.Background(), memSessionInput("prefs.y"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(memoryOutbox(s))
	promoted, err := ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/session/sess123/memory/notes", SourceMemoryID: src.MemoryID,
		TargetNamespace: "user/chrispian/memory/notes", ActorAgentID: "test-agent", ActorVersion: "1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := memoryOutbox(s)[before:]
	var actions []string
	subjects := map[string]bool{}
	for _, e := range out {
		actions = append(actions, payloadOf(t, e)["action"].(string))
		subjects[e.Subject] = true
	}
	if len(out) != 3 {
		t.Fatalf("promote events = %v, want 3", actions)
	}
	if !subjects["msg://memory/tesseract/user/chrispian/session/sess123/memory/notes/prefs.y"] ||
		!subjects["msg://memory/tesseract/user/chrispian/memory/notes/prefs.y"] {
		t.Fatalf("subjects = %v, want the source and promoted namespace/key", subjects)
	}
	_ = promoted
	want := map[string]bool{"memory.write": false, "memory.deprecate": false, "memory.promote": false}
	for _, a := range actions {
		want[a] = true
	}
	for a, seen := range want {
		if !seen {
			t.Fatalf("missing %s in %v", a, actions)
		}
	}
}

// A failed promote leaves neither the promoted copy nor any event for it.
func TestMemoryPromoteFailureLeavesNothingBehind(t *testing.T) {
	s, ms := newMemoryWithSink(t)
	src, err := ms.WriteRevision(context.Background(), memSessionInput("prefs.z"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(memoryOutbox(s))
	// Fail the source deprecation, which happens AFTER the target write inside
	// the same transaction, so a leftover would show up if they were separate.
	if _, err := s.DB().Exec(`CREATE TRIGGER fail_source_deprecation BEFORE UPDATE OF status ON memory_revisions
		BEGIN SELECT RAISE(ABORT, 'injected deprecation failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = ms.Promote(context.Background(), memory.PromoteInput{
		SourceNamespace: "user/chrispian/session/sess123/memory/notes", SourceMemoryID: src.MemoryID,
		TargetNamespace: "user/chrispian/memory/notes", ActorAgentID: "x", ActorVersion: "1",
	})
	if err == nil || !strings.Contains(err.Error(), "deprecate source revision") {
		t.Fatalf("promote error = %v, want the injected deprecation failure", err)
	}
	if got := len(memoryOutbox(s)); got != before {
		t.Fatalf("a failed promote left %d events behind", got-before)
	}
	if _, gerr := ms.GetCurrent(context.Background(), "user/chrispian/memory/notes", "prefs.z"); gerr == nil {
		t.Fatal("a failed promote left the promoted copy behind")
	}
}

// Deprecate records its event in its own transaction.
func TestMemoryDeprecateAuditInTx(t *testing.T) {
	s, ms := newMemoryWithSink(t)
	rev, err := ms.WriteRevision(context.Background(), memSessionInput("prefs.d"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(memoryOutbox(s))
	if err := ms.Deprecate(context.Background(), rev.RevisionID); err != nil {
		t.Fatal(err)
	}
	out := memoryOutbox(s)[before:]
	if len(out) != 1 || payloadOf(t, out[0])["action"] != "memory.deprecate" || out[0].Subject != "msg://memory/tesseract/user/chrispian/session/sess123/memory/notes/prefs.d" {
		t.Fatalf("deprecate events = %+v", out)
	}
}

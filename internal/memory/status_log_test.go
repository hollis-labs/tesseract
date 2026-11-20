package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const statusTestNS = "project/status-test/event/status"

func statusInput(writer, state, workstream string) memory.WriteInput {
	in := eventInput("Transition " + state)
	in.Namespace = statusTestNS
	in.Author.AgentID = writer
	in.WorkstreamID = &workstream
	in.ConsumerState = json.RawMessage(`{"state":"` + state + `","subject_urn":"msg://agent/test/worker","task":"T-1","run":"run-1"}`)
	in.Data = json.RawMessage(`{"session":"session-1","ref":"commit:abc"}`)
	return in
}

// This fixture is the bounded test-namespace demonstration requested by the
// task: latest N, UTC day, Registry workstream, writer and subject/state reads.
func TestStatusLogTestNamespaceReadsAndCursorBinding(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.Add(24*time.Hour - time.Nanosecond)
	for _, in := range []memory.WriteInput{
		statusInput("dispatcher", "started", "registry-ws-1"),
		statusInput("dispatcher", "done", "registry-ws-1"),
		statusInput("other", "blocked", "registry-ws-2"),
		statusInput("other", "blocked", "registry-ws-2"),
	} {
		if _, err := ms.WriteRevision(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		input memory.EventLogInput
		want  int
	}{
		{"tail", memory.EventLogInput{Limit: 1}, 1},
		{"day", memory.EventLogInput{Since: &start, Until: &end}, 4},
		{"workstream", memory.EventLogInput{WorkstreamID: "registry-ws-1"}, 2},
		{"writer before LIMIT", memory.EventLogInput{AuthorAgentID: "dispatcher", Limit: 1}, 1},
		{"state and subject", memory.EventLogInput{StateFilters: []memory.StateFilter{{Field: "state", Values: []any{"done"}}, {Field: "subject_urn", Values: []any{"msg://agent/test/worker"}}}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.input.Namespaces = []string{statusTestNS}
			page, err := ms.ReadEventLog(ctx, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Entries) != tc.want {
				t.Fatalf("got %d want %d", len(page.Entries), tc.want)
			}
			t.Logf("test namespace %s: %s returned=%d has_more=%v", statusTestNS, tc.name, len(page.Entries), page.Manifest.HasMore)
			if tc.input.AuthorAgentID != "" && page.Entries[0].Author.AgentID != "dispatcher" {
				t.Fatal("author filtering happened after LIMIT")
			}
		})
	}
	in := memory.EventLogInput{Namespaces: []string{statusTestNS}, Limit: 1, AuthorAgentID: "dispatcher", StateFilters: []memory.StateFilter{{Field: "state", Values: []any{"started", "done"}}}}
	first, err := ms.ReadEventLog(ctx, in)
	if err != nil || first.Manifest.NextCursor == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	in.Cursor = *first.Manifest.NextCursor
	for _, change := range []func(*memory.EventLogInput){
		func(i *memory.EventLogInput) { i.AuthorAgentID = "other" },
		func(i *memory.EventLogInput) {
			i.StateFilters = []memory.StateFilter{{Field: "state", Values: []any{"done"}}}
		},
		func(i *memory.EventLogInput) {
			i.StateFilters = []memory.StateFilter{{Field: "subject_urn", Values: []any{"msg://agent/test/worker"}}}
		},
	} {
		altered := in
		change(&altered)
		if _, err := ms.ReadEventLog(ctx, altered); !errors.Is(err, memory.ErrInvalidCursor) {
			t.Fatalf("changed filter accepted cursor: %v", err)
		}
	}
	in.StateFilters = []memory.StateFilter{{Field: "state", Values: []any{"done", "started"}}}
	second, err := ms.ReadEventLog(ctx, in)
	if err != nil || len(second.Entries) != 1 || first.Entries[0].RevisionID == second.Entries[0].RevisionID {
		t.Fatalf("equivalent reordered filter paging: %+v %v", second, err)
	}
}

func TestStatusLogFiltersExcludeRetractedExpiredAndInvalidState(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	live, err := ms.WriteRevision(ctx, statusInput("a", "blocked", "ws"))
	if err != nil {
		t.Fatal(err)
	}
	retracted, err := ms.WriteRevision(ctx, statusInput("a", "blocked", "ws"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Deprecate(ctx, retracted.RevisionID); err != nil {
		t.Fatal(err)
	}
	exp := statusInput("a", "blocked", "ws")
	exp.TTL = time.Nanosecond
	if _, err := ms.WriteRevision(ctx, exp); err != nil {
		t.Fatal(err)
	}
	page, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{statusTestNS}, AuthorAgentID: "a", StateFilters: []memory.StateFilter{{Field: "state", Values: []any{"blocked"}}}})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].RevisionID != live.RevisionID {
		t.Fatalf("expired/deprecated leaked: %+v %v", page, err)
	}
	for _, filters := range [][]memory.StateFilter{
		{{Field: "bad.path", Values: []any{"x"}}}, {{Field: "state"}}, {{Field: "state", Values: []any{nil}}}, {{Field: "state", Values: []any{map[string]any{"x": 1}}}},
	} {
		if _, err := ms.ReadEventLog(ctx, memory.EventLogInput{Namespaces: []string{statusTestNS}, StateFilters: filters}); !errors.Is(err, memory.ErrInvalidInput) {
			t.Fatalf("invalid filters accepted: %v", err)
		}
	}
	missing := statusInput("a", "done", "ws")
	missing.ConsumerState = json.RawMessage(`{"state":"done"}`)
	if _, err := ms.WriteRevision(ctx, missing); !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("missing subject accepted: %v", err)
	}
}

func TestStatusKeylessRetryDedupAndWriterPrincipal(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	es := event.New(ms)
	in := event.WriteInput{Namespace: statusTestNS, Summary: "done", Author: memory.Author{AgentID: "trusted-dispatcher"}, SessionID: "s", ConsumerState: json.RawMessage(`{"state":"done","subject_urn":"msg://agent/test/a"}`), IdempotencyKey: "retry-1"}
	a, err := es.Write(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := es.Write(ctx, in)
	if err != nil || a.RevisionID != b.RevisionID || a.ItemID != b.ItemID {
		t.Fatalf("retry appended: %+v %v", b, err)
	}
	if a.WriterPrincipal != "" || b.WriterPrincipal != "" {
		t.Fatal("claimed trusted author acquired authenticated provenance")
	}
	changed := in
	changed.Summary = "changed"
	if _, err := es.Write(ctx, changed); !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("conflicting key accepted: %v", err)
	}
	in.Key = "a.key"
	if _, err := es.Write(ctx, in); !errors.Is(err, memory.ErrInvalidInput) {
		t.Fatalf("keyed idempotency accepted: %v", err)
	}
	in.Key = ""
	authenticated := auth.WithPrincipal(ctx, auth.Principal{Verified: true, ID: "msg://service/test/untrusted"})
	c, err := es.Write(authenticated, in)
	if err != nil {
		t.Fatal(err)
	}
	if c.RevisionID == a.RevisionID || c.WriterPrincipal != "msg://service/test/untrusted" {
		t.Fatalf("wrong authenticated receipt: %+v", c)
	}
	fetched, err := ms.GetRevisionByID(ctx, c.RevisionID)
	if err != nil || fetched.WriterPrincipal != c.WriterPrincipal {
		t.Fatalf("receipt not durable: %+v %v", fetched, err)
	}
	unverified := auth.WithPrincipal(ctx, auth.Principal{Verified: false, ID: "msg://service/test/dispatcher"})
	in.IdempotencyKey = "retry-2"
	d, err := es.Write(unverified, in)
	if err != nil || d.WriterPrincipal != "" {
		t.Fatalf("unverified context trusted: %+v %v", d, err)
	}
	in.IdempotencyKey = ""
	e, err := es.Write(ctx, in)
	if err != nil || e.RevisionID == a.RevisionID {
		t.Fatal("omitted retry key did not append")
	}
}

type statusEffectCounter struct{ queued, audited int }

func (c *statusEffectCounter) Enqueue(context.Context, memory.Job) error { c.queued++; return nil }
func (c *statusEffectCounter) EmitRevision(context.Context, string, string, string, string, string, string, json.RawMessage) error {
	c.audited++
	return nil
}

func TestStatusRetryHasNoEffectsAndRolledBackReceiptDoesNotDedup(t *testing.T) {
	original, cleanup := newTestStore(t)
	defer cleanup()
	effects := &statusEffectCounter{}
	ms := memory.NewStore(original.DB(), nil, "", 0, effects)
	ms.SetAuditSink(effects)
	ctx := context.Background()
	in := statusInput("writer", "done", "registry-ws")
	in.IdempotencyKey = "atomic-retry"
	tx, err := ms.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := ms.WriteRevisionInTx(ctx, tx, in)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	first, err := ms.WriteRevision(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionID == rolledBack.RevisionID {
		t.Fatal("rolled-back receipt survived")
	}
	retry, err := ms.WriteRevision(ctx, in)
	if err != nil || retry.RevisionID != first.RevisionID {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if effects.queued != 1 || effects.audited != 1 {
		t.Fatalf("retry repeated effects: queued=%d audit=%d", effects.queued, effects.audited)
	}
	var revisions, receipts int
	if err := ms.DB().QueryRowContext(ctx, `SELECT count(*) FROM memory_revisions WHERE namespace=?`, statusTestNS).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := ms.DB().QueryRowContext(ctx, `SELECT count(*) FROM event_write_receipts WHERE namespace=?`, statusTestNS).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 || receipts != 1 {
		t.Fatalf("atomic dedup: revisions=%d receipts=%d", revisions, receipts)
	}
}

func TestStatusCursorStateValuesCannotCollideOnPunctuation(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		in := statusInput("a", "a,s:b", "ws")
		if _, err := ms.WriteRevision(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	in := memory.EventLogInput{Namespaces: []string{statusTestNS}, Limit: 1, StateFilters: []memory.StateFilter{{Field: "state", Values: []any{"a,s:b"}}}}
	page, err := ms.ReadEventLog(ctx, in)
	if err != nil || page.Manifest.NextCursor == nil {
		t.Fatalf("first: %+v %v", page, err)
	}
	in.Cursor = *page.Manifest.NextCursor
	in.StateFilters = []memory.StateFilter{{Field: "state", Values: []any{"a", "b"}}}
	if _, err := ms.ReadEventLog(ctx, in); !errors.Is(err, memory.ErrInvalidCursor) {
		t.Fatalf("distinct value sets shared cursor fingerprint: %v", err)
	}
}

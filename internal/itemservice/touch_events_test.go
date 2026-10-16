package itemservice_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/itemservice"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// Touch events (CW-20261016-0140 O1 step 2): one audit.recorded action=touch
// per distinct workstream, plus one without a workstream, all in the touch's
// own transaction and sharing one correlation id.

func touchService(t *testing.T) (*contextstore.Store, *memory.Store, *itemservice.Service) {
	t.Helper()
	cs, ms, _, svc := newService(t)
	ms.SetTouchSink(cs)
	return cs, ms, svc
}

func writeInWorkstream(t *testing.T, ms *memory.Store, key, workstream string) string {
	t.Helper()
	var wsID *string
	if workstream != "" {
		wsID = &workstream
	}
	rev, err := ms.WriteRevision(context.Background(), memory.WriteInput{
		Domain: domains.Memory, Actor: "user", Namespace: "project/tesseract/memory/notes", MemoryKey: key,
		Summary: "s " + key, WorkstreamID: wsID,
		Author: memory.Author{AgentID: "test"}, SessionID: "touch",
		Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rev.MemoryID
}

type touchEvent struct {
	Subject string
	Scope   map[string]any
	Corr    map[string]any
	ID      string
	Payload struct {
		Action  string `json:"action"`
		Subject struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"subject"`
		ItemIDs   []string `json:"item_ids"`
		ItemCount int      `json:"item_count"`
		Truncated bool     `json:"truncated"`
	}
}

func touchEvents(t *testing.T, cs *contextstore.Store) []touchEvent {
	t.Helper()
	entries, err := cs.ReadOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []touchEvent
	for _, e := range entries {
		var env struct {
			ID      string          `json:"id"`
			Subject string          `json:"subject"`
			Scope   map[string]any  `json:"scope"`
			Corr    map[string]any  `json:"corr"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(e.Envelope, &env); err != nil {
			t.Fatal(err)
		}
		var ev touchEvent
		if err := json.Unmarshal(env.Payload, &ev.Payload); err != nil {
			t.Fatal(err)
		}
		if ev.Payload.Action != "touch" {
			continue
		}
		ev.ID, ev.Subject, ev.Scope, ev.Corr = env.ID, env.Subject, env.Scope, env.Corr
		out = append(out, ev)
	}
	return out
}

func touch(t *testing.T, svc *itemservice.Service, ids ...string) {
	t.Helper()
	metas := make([]itemservice.Metadata, len(ids))
	for i, id := range ids {
		metas[i] = itemservice.Metadata{ItemID: id, Domain: string(domains.Memory)}
	}
	if _, err := svc.TouchItems(context.Background(), metas); err != nil {
		t.Fatal(err)
	}
}

func TestTouchEmitsOneEventPerWorkstreamPlusUnscopedLast(t *testing.T) {
	cs, ms, svc := touchService(t)
	b := writeInWorkstream(t, ms, "b", "ws-b")
	a1 := writeInWorkstream(t, ms, "a1", "ws-a")
	a2 := writeInWorkstream(t, ms, "a2", "ws-a")
	n := writeInWorkstream(t, ms, "n", "")

	touch(t, svc, n, b, a2, a1)

	evs := touchEvents(t, cs)
	if len(evs) != 3 {
		t.Fatalf("touch events = %d, want 3 (ws-a, ws-b, none)", len(evs))
	}
	if evs[0].Scope["workstream_id"] != "ws-a" || evs[1].Scope["workstream_id"] != "ws-b" {
		t.Fatalf("order = %v, %v; want workstream ascending", evs[0].Scope, evs[1].Scope)
	}
	if _, has := evs[2].Scope["workstream_id"]; has {
		t.Fatalf("no-workstream event carries scope.workstream_id: %v", evs[2].Scope)
	}
	want := []string{a1, a2}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if !reflect.DeepEqual(evs[0].Payload.ItemIDs, want) {
		t.Fatalf("ws-a item_ids = %v, want %v", evs[0].Payload.ItemIDs, want)
	}
	corr := evs[0].Corr["correlation_id"]
	ids := map[string]bool{}
	for _, e := range evs {
		if corr == nil || corr == "" || e.Corr["correlation_id"] != corr {
			t.Fatalf("events do not share one correlation id: %v", evs)
		}
		wantSubject := "msg://touch/tesseract/" + corr.(string)
		if e.Subject != wantSubject || e.Payload.Subject.Type != "touch" || e.Payload.Subject.ID != corr {
			t.Fatalf("subject = %q payload %+v, want %q and {touch %v}", e.Subject, e.Payload.Subject, wantSubject, corr)
		}
		if e.Payload.ItemCount != len(e.Payload.ItemIDs) {
			t.Fatalf("item_count %d != item_ids %d", e.Payload.ItemCount, len(e.Payload.ItemIDs))
		}
		if ids[e.ID] {
			t.Fatalf("duplicate event id %s", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestTouchWithNoWorkstreamEmitsOneEventWithoutScope(t *testing.T) {
	cs, ms, svc := touchService(t)
	touch(t, svc, writeInWorkstream(t, ms, "x", ""), writeInWorkstream(t, ms, "y", ""))

	evs := touchEvents(t, cs)
	if len(evs) != 1 {
		t.Fatalf("touch events = %d, want 1", len(evs))
	}
	if ws, has := evs[0].Scope["workstream_id"]; has {
		t.Fatalf("scope.workstream_id present (%v); must be omitted, never empty", ws)
	}
	if len(evs[0].Payload.ItemIDs) != 2 {
		t.Fatalf("item_ids = %v", evs[0].Payload.ItemIDs)
	}
}

func TestTouchEventsOrderIsStableAcrossRequestOrder(t *testing.T) {
	cs, ms, svc := touchService(t)
	var ids []string
	for i := 0; i < 6; i++ {
		ids = append(ids, writeInWorkstream(t, ms, fmt.Sprintf("k%d", i), fmt.Sprintf("ws-%d", i%3)))
	}
	rev := make([]string, len(ids))
	for i, id := range ids {
		rev[len(ids)-1-i] = id
	}
	touch(t, svc, ids...)
	touch(t, svc, rev...)

	evs := touchEvents(t, cs)
	if len(evs) != 6 {
		t.Fatalf("touch events = %d, want 6", len(evs))
	}
	for i := 0; i < 3; i++ {
		first, second := evs[i], evs[i+3]
		if first.Scope["workstream_id"] != second.Scope["workstream_id"] ||
			!reflect.DeepEqual(first.Payload.ItemIDs, second.Payload.ItemIDs) {
			t.Fatalf("call 1 event %d %v differs from call 2 event %d %v", i, first, i, second)
		}
	}
}

func TestTouchEventCapsItemIDsAndMarksTruncated(t *testing.T) {
	cs, _, _ := touchService(t)
	ids := make([]string, 0, 101)
	ws := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		ids = append(ids, fmt.Sprintf("item-%03d", i))
		ws = append(ws, "ws-big")
	}
	tx, err := cs.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.RecordTouches(context.Background(), tx, "01TESTCORRELATIONID00000000", ids, ws); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	evs := touchEvents(t, cs)
	if len(evs) != 1 || len(evs[0].Payload.ItemIDs) != 100 || evs[0].Payload.ItemCount != 101 || !evs[0].Payload.Truncated {
		t.Fatalf("capped event = %+v", evs)
	}
}

func TestTouchEventsRollBackWithTheTransaction(t *testing.T) {
	cs, _, _ := touchService(t)
	tx, err := cs.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.RecordTouches(context.Background(), tx, "01ROLLBACKCORRELATION00000", []string{"i"}, []string{"ws"}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if evs := touchEvents(t, cs); len(evs) != 0 {
		t.Fatalf("rolled-back touch left %d events", len(evs))
	}
}

func TestTouchOfNothingMovedEmitsNothing(t *testing.T) {
	cs, _, svc := touchService(t)
	touch(t, svc, "no-such-item")
	if evs := touchEvents(t, cs); len(evs) != 0 {
		t.Fatalf("not-found touch emitted %d events", len(evs))
	}
}

func TestTouchRevisionsDoorEmitsPerWorkstreamEvents(t *testing.T) {
	cs, ms, _ := touchService(t)
	var revIDs []string
	for _, w := range []string{"ws-z", "ws-y"} {
		wsID := w
		rev, err := ms.WriteRevision(context.Background(), memory.WriteInput{
			Domain: domains.Memory, Actor: "user", Namespace: "project/tesseract/memory/notes", MemoryKey: strings.ReplaceAll(w, "-", "_"),
			Summary: "s", WorkstreamID: &wsID, Author: memory.Author{AgentID: "test"}, SessionID: "touch",
			Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
		})
		if err != nil {
			t.Fatal(err)
		}
		revIDs = append(revIDs, rev.RevisionID)
	}
	if _, err := ms.TouchRevisions(context.Background(), revIDs); err != nil {
		t.Fatal(err)
	}
	evs := touchEvents(t, cs)
	if len(evs) != 2 || evs[0].Scope["workstream_id"] != "ws-y" || evs[1].Scope["workstream_id"] != "ws-z" {
		t.Fatalf("events = %+v", evs)
	}
}

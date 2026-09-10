package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/typeregistry"
)

// todoInput returns a well-formed write into the todos namespace carrying the
// given consumer_state bag.
func todoInput(key, state string) memory.WriteInput {
	in := sampleInput(key)
	in.Namespace = "user/chrispian/memory/todos"
	in.Payload = memory.Payload{Summary: "buy milk", Body: "the errand"}
	if state != "" {
		in.ConsumerState = json.RawMessage(state)
	}
	return in
}

func TestConsumerStateRoundTrips(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	bag := `{"kind":"todo","section":"now","completed":false,"priority":"high","external_ref":"fe-doc-991"}`
	written, err := ms.WriteRevision(ctx, todoInput("todo.milk", bag))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if string(written.ConsumerState) != bag {
		t.Errorf("write echoed consumer_state as %q, want %q", written.ConsumerState, bag)
	}

	read, err := ms.GetRevisionByID(ctx, written.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	// Compared as parsed JSON rather than as bytes: the store must preserve the
	// content, and pinning the exact byte sequence would make a future column
	// normalization look like data loss.
	var gotBag, wantBag map[string]any
	if err := json.Unmarshal(read.ConsumerState, &gotBag); err != nil {
		t.Fatalf("stored consumer_state is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(bag), &wantBag); err != nil {
		t.Fatal(err)
	}
	if len(gotBag) != len(wantBag) {
		t.Fatalf("read back %v, want %v", gotBag, wantBag)
	}
	for k, v := range wantBag {
		if gotBag[k] != v {
			t.Errorf("consumer_state[%q] = %v, want %v", k, gotBag[k], v)
		}
	}
}

// A revision written without a bag reads back with none — not with an empty
// object. Absent and `{}` are different claims and the store must not
// manufacture the second from the first.
func TestConsumerStateAbsentStaysAbsent(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rev, err := ms.WriteRevision(ctx, sampleInput("prefs.plain"))
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	read, err := ms.GetRevisionByID(ctx, rev.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if read.ConsumerState != nil {
		t.Errorf("consumer_state = %q, want nil for a write that carried none", read.ConsumerState)
	}
	// And it must not appear on the wire, so a reader can tell "no bag" from
	// "empty bag" by shape.
	blob, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "consumer_state") {
		t.Errorf("serialized revision names consumer_state though there is none: %s", blob)
	}
}

// The complete validation surface: well-formed JSON, and an object. Anything
// past that is the consumer's business.
func TestConsumerStateValidationIsWellFormednessAndObjectnessOnly(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	refused := map[string]string{
		"not JSON at all":     `{"section": now}`,
		"a JSON array":        `["now","soon"]`,
		"a bare string":       `"now"`,
		"a bare number":       `42`,
		"a JSON null":         `null`,
		"a truncated object":  `{"section":`,
		"trailing garbage":    `{"a":1} nope`,
		"a duplicated object": `{"a":1}{"b":2}`,
	}
	for name, bag := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			_, err := ms.WriteRevision(ctx, todoInput("todo.bad", bag))
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Errorf("writing %s returned %v, want ErrInvalidInput — a bag that is not a JSON "+
					"object has no field to index, which is a fact about the mechanism", bag, err)
			}
		})
	}

	// Everything a consumer might mean is accepted, because Tesseract has no
	// opinion about any of it. Each of these would be a plausible place for a
	// vocabulary check to creep in.
	accepted := map[string]string{
		"an empty bag":                  `{}`,
		"a status nothing declares":     `{"status":"marinating"}`,
		"a transition backwards":        `{"completed":false,"was_completed":true}`,
		"a nonsense section":            `{"section":"someday-maybe"}`,
		"a nested object under a key":   `{"recurrence_rule":{"every":"week","on":["mon"]}}`,
		"a field no type ever declared": `{"vibes":"good"}`,
	}
	for name, bag := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			if _, err := ms.WriteRevision(ctx, todoInput("todo.ok", bag)); err != nil {
				t.Errorf("writing %s failed with %v; Tesseract validates well-formed JSON and an "+
					"object, and nothing else — no value vocabulary, no transition checking, ever",
					bag, err)
			}
		})
	}
}

// required_fields is presence, not value, and it comes from the type the write
// names rather than from anything hardcoded here.
func TestConsumerStateRequiredFieldsArePresenceOnly(t *testing.T) {
	reg := typeregistry.NewRegistry()
	if err := reg.LoadVocabulary(typeregistry.Vocabulary{
		VocabularyID: typeregistry.VocabMemoryType,
		Closed:       true,
		Types: []typeregistry.Type{
			{TypeID: "notes"},
			{TypeID: "todos", RequiredFields: []string{"kind", "section"}, HotFields: []string{"section"}},
		},
	}); err != nil {
		t.Fatalf("LoadVocabulary: %v", err)
	}
	restore := typeregistry.Install(reg)
	defer restore()

	ms, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := ms.WriteRevision(ctx, todoInput("todo.partial", `{"kind":"todo"}`)); err == nil {
		t.Error("a bag missing a declared required field was accepted")
	} else if !strings.Contains(err.Error(), "section") {
		t.Errorf("error %v does not name the missing field", err)
	}

	// Present is enough. A required field set to null, false or "" satisfies
	// the declaration — the type said the key must be THERE, and what it holds
	// is not Tesseract's business.
	for _, bag := range []string{
		`{"kind":"todo","section":"now"}`,
		`{"kind":null,"section":null}`,
		`{"kind":"","section":false}`,
	} {
		if _, err := ms.WriteRevision(ctx, todoInput("todo.present", bag)); err != nil {
			t.Errorf("bag %s was refused with %v; required_fields is presence, never value", bag, err)
		}
	}

	// A type that declares none is unconstrained, which is the shipped `todos`
	// declaration's actual state.
	in := sampleInput("note.plain")
	in.ConsumerState = json.RawMessage(`{}`)
	if _, err := ms.WriteRevision(ctx, in); err != nil {
		t.Errorf("a type declaring no required_fields refused an empty bag: %v", err)
	}
}

// The shipped `todos` type declares no required fields, deliberately: NIL has
// not migrated, and requiring a field ahead of a consumer refuses exactly the
// rows the migration exists to move.
func TestShippedTodosTypeRequiresNothing(t *testing.T) {
	todos, ok := typeregistry.NewRegistry().Lookup(typeregistry.VocabMemoryType, "todos")
	if !ok {
		t.Fatal("todos is not declared in the shipped memory.type vocabulary")
	}
	if len(todos.RequiredFields) != 0 {
		t.Errorf("todos declares required_fields %v; requiring a field is a decision to take once a "+
			"consumer is writing, not one to ship ahead of it", todos.RequiredFields)
	}
	if len(todos.HotFields) == 0 {
		t.Error("todos declares no hot_fields; it is the first structured object and the " +
			"declaration is what tells an operator which fields carry an index")
	}
}

// ── Filtering ──────────────────────────────────────────────────────────────

func seedTodos(t *testing.T, ms *memory.Store) {
	t.Helper()
	ctx := context.Background()
	for key, bag := range map[string]string{
		"todo.now_open":    `{"kind":"todo","section":"now","completed":false,"priority":1,"external_ref":"fe-1"}`,
		"todo.now_done":    `{"kind":"todo","section":"now","completed":true,"priority":1,"external_ref":"fe-2"}`,
		"todo.soon_open":   `{"kind":"todo","section":"soon","completed":false,"priority":2,"external_ref":"fe-3"}`,
		"note.now_open":    `{"kind":"note","section":"now","completed":false,"priority":3,"external_ref":"fe-4"}`,
		"todo.no_bag_here": ``,
	} {
		if _, err := ms.WriteRevision(ctx, todoInput(key, bag)); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
}

func recallKeys(t *testing.T, ms *memory.Store, filters []memory.StateFilter) []string {
	t.Helper()
	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/todos"},
		Ranking:    memory.RankingChronological,
		Limit:      50,
		Filters:    memory.RecallFilters{StateFilters: filters},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	keys := make([]string, 0, len(results))
	for _, r := range results {
		keys = append(keys, r.Revision.MemoryKey)
	}
	return keys
}

func TestStateFiltersSelectByFieldValue(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedTodos(t, ms)

	cases := []struct {
		name    string
		filters []memory.StateFilter
		want    []string
	}{
		{
			name:    "a string field",
			filters: []memory.StateFilter{{Field: "section", Values: []any{"soon"}}},
			want:    []string{"todo.soon_open"},
		},
		{
			// The case a naive implementation gets silently wrong: json_extract
			// answers SQLite, so a JSON `false` comes back as the integer 0 and
			// binding the Go bool without coercion matches nothing — which is
			// indistinguishable from a corpus with no open todos.
			name:    "a boolean field, false",
			filters: []memory.StateFilter{{Field: "completed", Values: []any{false}}},
			want:    []string{"note.now_open", "todo.now_open", "todo.soon_open"},
		},
		{
			name:    "a boolean field, true",
			filters: []memory.StateFilter{{Field: "completed", Values: []any{true}}},
			want:    []string{"todo.now_done"},
		},
		{
			// JSON numbers decode as float64 and json_extract returns an
			// integer; SQLite compares those numerically, so 1 finds 1.
			name:    "a numeric field",
			filters: []memory.StateFilter{{Field: "priority", Values: []any{float64(2)}}},
			want:    []string{"todo.soon_open"},
		},
		{
			name:    "several values OR together",
			filters: []memory.StateFilter{{Field: "section", Values: []any{"now", "soon"}}},
			want:    []string{"note.now_open", "todo.now_done", "todo.now_open", "todo.soon_open"},
		},
		{
			name: "several fields AND together",
			filters: []memory.StateFilter{
				{Field: "kind", Values: []any{"todo"}},
				{Field: "completed", Values: []any{false}},
			},
			want: []string{"todo.now_open", "todo.soon_open"},
		},
		{
			// The declaration governs which fields carry an INDEX, not which
			// ones a caller may name. `priority` is not among todos' hot
			// fields and filtering on it still answers correctly.
			name:    "an undeclared field still answers",
			filters: []memory.StateFilter{{Field: "priority", Values: []any{float64(3)}}},
			want:    []string{"note.now_open"},
		},
		{
			name:    "a field no row carries",
			filters: []memory.StateFilter{{Field: "nonexistent", Values: []any{"x"}}},
			want:    []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recallKeys(t, ms, tc.filters)
			if !sameSet(got, tc.want) {
				t.Errorf("filter %+v returned %v, want %v", tc.filters, got, tc.want)
			}
			// A revision with no bag at all must never match a state filter:
			// SQL NULL is not a value, and every revision written before
			// migration 19 carries NULL.
			for _, k := range got {
				if k == "todo.no_bag_here" {
					t.Error("a revision carrying no consumer_state matched a state filter")
				}
			}
		})
	}
}

func TestStateFiltersRefuseMalformedRequests(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedTodos(t, ms)

	for name, filters := range map[string][]memory.StateFilter{
		"an uppercase field":     {{Field: "Section", Values: []any{"now"}}},
		"a dotted path":          {{Field: "a.b", Values: []any{"x"}}},
		"a quote in the field":   {{Field: `a'b`, Values: []any{"x"}}},
		"a JSON path expression": {{Field: "$.section", Values: []any{"x"}}},
		"an empty field":         {{Field: "", Values: []any{"x"}}},
		"no values":              {{Field: "section", Values: nil}},
		"a null value":           {{Field: "section", Values: []any{nil}}},
		"an object value":        {{Field: "section", Values: []any{map[string]any{"a": 1}}}},
		"the same field twice": {
			{Field: "section", Values: []any{"now"}},
			{Field: "section", Values: []any{"soon"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ms.Recall(context.Background(), memory.RecallInput{
				Namespaces: []string{"user/chrispian/memory/todos"},
				Ranking:    memory.RankingChronological,
				Filters:    memory.RecallFilters{StateFilters: filters},
			})
			if !errors.Is(err, memory.ErrInvalidInput) {
				t.Errorf("recall with %s returned %v, want ErrInvalidInput — an empty page from a "+
					"malformed filter is indistinguishable from a clean corpus", name, err)
			}
		})
	}
}

// A state filter must narrow the candidate set in SQL, BEFORE limit. Applied
// after the fetch, "the open todos" would return however many open ones
// happened to rank in the top N — a query enumerating a population cannot be
// sampled by an unrelated ranking.
func TestStateFiltersApplyBeforeLimit(t *testing.T) {
	ms, cleanup := newTestStore(t)
	defer cleanup()
	seedTodos(t, ms)

	results, err := ms.Recall(context.Background(), memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/todos"},
		Ranking:    memory.RankingChronological,
		Limit:      1,
		Filters: memory.RecallFilters{StateFilters: []memory.StateFilter{
			{Field: "completed", Values: []any{true}},
		}},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 || results[0].Revision.MemoryKey != "todo.now_done" {
		t.Fatalf("limit=1 over a filtered set returned %v; the filter must run in SQL so the one "+
			"row returned is the top of the MATCHING set, not the top of the corpus filtered after",
			results)
	}
}

// The cursor fingerprint must move when a state filter does. A cursor is an
// offset into an ordering, and resuming one across a changed candidate set
// returns rows that look right and are wrong.
func TestStateFilterChangesTheCursorFingerprint(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/todos"},
		Ranking:    memory.RankingChronological,
	}
	withNow := base
	withNow.Filters.StateFilters = []memory.StateFilter{{Field: "section", Values: []any{"now"}}}
	withSoon := base
	withSoon.Filters.StateFilters = []memory.StateFilter{{Field: "section", Values: []any{"soon"}}}

	none := memory.RecallOrderingFingerprint(base)
	now := memory.RecallOrderingFingerprint(withNow)
	soon := memory.RecallOrderingFingerprint(withSoon)

	if now == none {
		t.Error("adding a state filter did not change the fingerprint")
	}
	if now == soon {
		t.Error("changing a state filter's VALUE did not change the fingerprint; the two select " +
			"different candidate sets, so a cursor must not carry between them")
	}

	// Order must not matter: the same question asked two ways is one ordering,
	// and fingerprinting them apart would restart paging for no reason.
	a := base
	a.Filters.StateFilters = []memory.StateFilter{
		{Field: "kind", Values: []any{"todo", "note"}},
		{Field: "section", Values: []any{"now"}},
	}
	b := base
	b.Filters.StateFilters = []memory.StateFilter{
		{Field: "section", Values: []any{"now"}},
		{Field: "kind", Values: []any{"note", "todo"}},
	}
	if memory.RecallOrderingFingerprint(a) != memory.RecallOrderingFingerprint(b) {
		t.Error("the same filters in a different order fingerprinted differently")
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

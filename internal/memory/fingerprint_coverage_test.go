package memory_test

// A cursor is an offset into an ordering. If any input that changes the
// ordered candidate set is missing from the cursor's fingerprint, resuming
// across a change to that input returns rows that look right and are wrong,
// with no error — the exact failure the fingerprint exists to prevent.
//
// The hand-written list in orderingKey cannot defend itself. It was already
// caught once: CW-20260825-0015 added RecallFilters.PointerHealth — a filter
// applied in SQL, so it genuinely changes the candidate set — while this lane
// was in flight, and the merge brought the two together with the new field
// absent from the fingerprint and nothing failing.
//
// These tests are the structural guard. The first enumerates RecallFilters by
// reflection, so a field added later is covered the day it lands rather than
// the day someone remembers. The second pins the fields that must NOT be in
// the fingerprint, so the guard cannot be satisfied by hashing everything.

import (
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// nonZero returns a non-zero value for a RecallFilters field type, so the
// fingerprint has something to notice.
func nonZero(t *testing.T, f reflect.Value, name string) reflect.Value {
	t.Helper()
	ty := f.Type()
	switch ty.Kind() {
	case reflect.Slice:
		elem := reflect.New(ty.Elem()).Elem()
		switch elem.Kind() {
		case reflect.String:
			elem.SetString("fingerprint-probe")
		default:
			t.Fatalf("field %s: unhandled slice element kind %s — teach nonZero about it",
				name, elem.Kind())
		}
		s := reflect.MakeSlice(ty, 1, 1)
		s.Index(0).Set(elem)
		return s
	case reflect.Float64:
		return reflect.ValueOf(0.5).Convert(ty)
	case reflect.Int:
		return reflect.ValueOf(7).Convert(ty)
	case reflect.String:
		return reflect.ValueOf("fingerprint-probe").Convert(ty)
	case reflect.Pointer:
		if ty.Elem() == reflect.TypeOf(time.Time{}) {
			ts := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
			p := reflect.New(ty.Elem())
			p.Elem().Set(reflect.ValueOf(ts))
			return p
		}
		t.Fatalf("field %s: unhandled pointer type %s — teach nonZero about it", name, ty)
	default:
		t.Fatalf("field %s: unhandled kind %s — teach nonZero about it", name, ty.Kind())
	}
	return reflect.Value{}
}

// Every field of RecallFilters must change the ordering fingerprint. A filter
// narrows the candidate set, so two calls whose filters differ are not the
// same ordering and a cursor must not carry across them.
func TestFingerprint_CoversEveryRecallFilter(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}
	baseline := memory.RecallOrderingFingerprint(base)

	ft := reflect.TypeOf(memory.RecallFilters{})
	if ft.NumField() == 0 {
		t.Fatal("RecallFilters has no fields; this guard is not testing anything")
	}

	for i := 0; i < ft.NumField(); i++ {
		field := ft.Field(i)
		if field.PkgPath != "" {
			continue // unexported: not part of the caller-visible query
		}
		t.Run(field.Name, func(t *testing.T) {
			probe := base
			filters := reflect.New(ft).Elem()
			filters.FieldByName(field.Name).Set(
				nonZero(t, filters.FieldByName(field.Name), field.Name))
			probe.Filters = filters.Interface().(memory.RecallFilters)

			if got := memory.RecallOrderingFingerprint(probe); got == baseline {
				t.Errorf("changing RecallFilters.%s did not change the ordering fingerprint.\n"+
					"A cursor issued before the change will be silently accepted after it, and the "+
					"caller gets rows from a different candidate set with no error.\n"+
					"Add %s to orderingKey and RecallOrderingFingerprint in paging.go.",
					field.Name, field.Name)
			}
		})
	}
}

// The query-shaping fields outside Filters must be covered too.
func TestFingerprint_CoversQueryShape(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}
	baseline := memory.RecallOrderingFingerprint(base)

	for _, tc := range []struct {
		name   string
		mutate func(memory.RecallInput) memory.RecallInput
	}{
		{"Namespaces", func(in memory.RecallInput) memory.RecallInput {
			in.Namespaces = []string{"user/chrispian/memory/decisions"}
			return in
		}},
		{"Ranking", func(in memory.RecallInput) memory.RecallInput {
			in.Ranking = memory.RankingActivation
			return in
		}},
		{"RevisionScope", func(in memory.RecallInput) memory.RecallInput {
			in.RevisionScope = memory.RevisionScopeTimeline
			return in
		}},
		{"Query", func(in memory.RecallInput) memory.RecallInput {
			in.Query = "something"
			return in
		}},
		{"Reranker", func(in memory.RecallInput) memory.RecallInput {
			in.Reranker = "x"
			return in
		}},
		{"RerankerTopK", func(in memory.RecallInput) memory.RecallInput {
			in.Reranker = "x"
			in.RerankerTopK = 3
			return in
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memory.RecallOrderingFingerprint(tc.mutate(base)); got == baseline {
				t.Errorf("changing %s did not change the ordering fingerprint", tc.name)
			}
		})
	}
}

// The guard above must not be satisfiable by hashing the whole input. Limit
// and Offset window the sequence rather than determine it, so binding them
// would reject legitimate paging — continuing the same query at a different
// page size, or the offset the cursor itself carries — for no correctness gain.
func TestFingerprint_ExcludesWindowing(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}
	baseline := memory.RecallOrderingFingerprint(base)

	for _, tc := range []struct {
		name   string
		mutate func(memory.RecallInput) memory.RecallInput
	}{
		{"Limit", func(in memory.RecallInput) memory.RecallInput {
			in.Limit = 99
			return in
		}},
		{"Offset", func(in memory.RecallInput) memory.RecallInput {
			in.Offset = 42
			return in
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memory.RecallOrderingFingerprint(tc.mutate(base)); got != baseline {
				t.Errorf("%s changed the ordering fingerprint; it windows the sequence "+
					"rather than determining it, so binding it rejects legitimate paging",
					tc.name)
			}
		})
	}
}

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
		// 0.5 rather than 0 only for consistency with the Float64 case above.
		// &0.0 would serve as a probe here too — a pointer float fingerprints
		// as null when nil and 0 when it points at zero, so both differ from
		// the nil baseline. That is a load-bearing property rather than an
		// incidental one, so TestFingerprint_DistinguishesAbsentFromZeroFloor
		// asserts it directly instead of leaving it to this probe.
		if ty.Elem().Kind() == reflect.Float64 {
			p := reflect.New(ty.Elem())
			p.Elem().SetFloat(0.5)
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
		// SearchMode alone, with nothing else touched: the mutation must be
		// the only thing that could move the fingerprint, or the case passes
		// for the wrong reason.
		{"SearchMode", func(in memory.RecallInput) memory.RecallInput {
			in.SearchMode = memory.SearchModeLexical
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

// recallInputFingerprintExclusions names the RecallInput fields that must NOT
// be enumerated by the reflection guard below, each with the reason. Anything
// not listed here has to move the fingerprint.
//
// This is the list a future lane has to argue with. Adding a field to it is a
// claim that the field cannot change the ordered candidate sequence — which is
// checkable — rather than a way to make a failing test pass.
var recallInputFingerprintExclusions = map[string]string{
	"Filters": "covered field-by-field by TestFingerprint_CoversEveryRecallFilter, " +
		"which reflects over RecallFilters itself",
	"Limit":  "windows the sequence rather than determining it; TestFingerprint_ExcludesWindowing pins it OUT",
	"Offset": "the position the cursor itself carries; TestFingerprint_ExcludesWindowing pins it OUT",
}

// Every RecallInput field outside Filters must change the ordering
// fingerprint, enumerated structurally rather than by hand.
//
// TestFingerprint_CoversQueryShape lists those fields by name, and a list
// cannot notice a field nobody added it to: CW-20260825-0006 put SearchMode on
// RecallInput — a knob that selects which retrieval arms run, so it changes
// both which rows are candidates and their order — and the hand-written list
// went on passing with it absent. Reflection closes that the same way
// TestFingerprint_CoversEveryRecallFilter closed it for RecallFilters after
// PointerHealth slipped through.
//
// The two tests are kept side by side rather than merged: this one catches a
// field the moment it lands, and the named one documents what each field means
// for the ordering. Losing either loses something.
func TestFingerprint_CoversEveryRecallInputField(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}
	baseline := memory.RecallOrderingFingerprint(base)

	it := reflect.TypeOf(memory.RecallInput{})
	// An exclusion for a field that no longer exists is a guard with a hole in
	// it: the field was renamed and its coverage silently lapsed.
	for name := range recallInputFingerprintExclusions {
		if _, ok := it.FieldByName(name); !ok {
			t.Errorf("recallInputFingerprintExclusions names %q, which RecallInput no longer has — "+
				"remove the entry, or point it at the field's new name", name)
		}
	}

	covered := 0
	for i := 0; i < it.NumField(); i++ {
		field := it.Field(i)
		if field.PkgPath != "" {
			continue // unexported: not part of the caller-visible query
		}
		if _, skip := recallInputFingerprintExclusions[field.Name]; skip {
			continue
		}
		covered++
		t.Run(field.Name, func(t *testing.T) {
			probe := reflect.New(it).Elem()
			probe.Set(reflect.ValueOf(base))
			f := probe.FieldByName(field.Name)
			f.Set(nonZero(t, f, field.Name))

			if got := memory.RecallOrderingFingerprint(probe.Interface().(memory.RecallInput)); got == baseline {
				t.Errorf("changing RecallInput.%s did not change the ordering fingerprint.\n"+
					"A cursor issued before the change will be silently accepted after it, and the "+
					"caller gets rows from a different candidate set with no error.\n"+
					"Add %s to orderingKey and RecallOrderingFingerprint in paging.go — or, if it "+
					"genuinely cannot reorder anything, to recallInputFingerprintExclusions with the reason.",
					field.Name, field.Name)
			}
		})
	}
	if covered == 0 {
		t.Fatal("every RecallInput field was excluded; this guard is not testing anything")
	}
}

// A similarity floor of 0.0 and no floor at all select different candidate
// sets, so they must fingerprint differently — otherwise a cursor issued
// without a floor is silently accepted when resumed with a floor of zero, and
// the caller pages into a shorter sequence with no error.
//
// This is the cursor-side half of the reason SimilarityMin is a pointer.
// TestFingerprint_CoversEveryRecallFilter cannot show it: that guard probes one
// non-zero value against a nil baseline, which passes whether or not the zero
// case is distinguishable.
func TestFingerprint_DistinguishesAbsentFromZeroFloor(t *testing.T) {
	base := memory.RecallInput{
		Namespaces: []string{"user/chrispian/memory/notes"},
		Ranking:    memory.RankingChronological,
	}

	withFloor := func(v *float64) string {
		in := base
		in.Filters.SimilarityMin = v
		return memory.RecallOrderingFingerprint(in)
	}

	zero, half := 0.0, 0.5
	absent := withFloor(nil)
	atZero := withFloor(&zero)
	atHalf := withFloor(&half)

	if absent == atZero {
		t.Errorf("similarity_min absent and similarity_min=0.0 fingerprint identically (%s).\n"+
			"They are different queries: a floor of 0.0 drops every orthogonal and opposed "+
			"result, while an absent floor keeps them. A cursor must not carry between them.",
			absent)
	}
	// The positive control for the assertion above: the fingerprint does move
	// for a floor it certainly reads, so a passing zero-case is evidence about
	// the zero case rather than about the field being read at all.
	if absent == atHalf {
		t.Errorf("similarity_min=0.5 did not change the fingerprint at all (%s); "+
			"the zero-versus-absent check above cannot mean anything until this does", absent)
	}
	if atZero == atHalf {
		t.Errorf("similarity_min=0.0 and similarity_min=0.5 fingerprint identically (%s); "+
			"the fingerprint is reading presence rather than value", atZero)
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

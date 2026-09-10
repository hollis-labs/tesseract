package domains

import "testing"

func TestDomainValid(t *testing.T) {
	cases := []struct {
		d    Domain
		want bool
	}{
		{Memory, true},
		{Knowledge, true},
		{"", false},
		{"unknown", false},
	}
	for _, c := range cases {
		if got := c.d.Valid(); got != c.want {
			t.Errorf("Domain(%q).Valid() = %v, want %v", c.d, got, c.want)
		}
	}
}

func TestAllStableOrder(t *testing.T) {
	got := All()
	want := []Domain{Memory, Knowledge}
	if len(got) != len(want) {
		t.Fatalf("All() length = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("All()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAllReturnsACopy guards the registry against a caller that sorts or
// overwrites what All() hands it. All() reads a package-level slice now rather
// than building a fresh literal per call, so aliasing it would let one caller
// reorder every later caller's view — including Valid's membership scan.
func TestAllReturnsACopy(t *testing.T) {
	first := All()
	first[0] = Domain("clobbered")

	second := All()
	if second[0] != Memory {
		t.Errorf("All()[0] = %q after a caller mutated an earlier result, want %q", second[0], Memory)
	}
	if !Memory.Valid() {
		t.Error("Memory.Valid() = false after a caller mutated an earlier All() result")
	}
}

// TestValidAgreesWithAll keeps the two readers of the registry honest: a
// domain that enumerates must validate, and this package cannot grow a domain
// that is listed but not recognized.
func TestValidAgreesWithAll(t *testing.T) {
	for _, d := range All() {
		if !d.Valid() {
			t.Errorf("All() returned %q but Domain(%q).Valid() = false", d, d)
		}
	}
}

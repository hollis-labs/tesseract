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

func TestMemoryPolicyValidateNamespace(t *testing.T) {
	p, err := Memory.Policy()
	if err != nil {
		t.Fatalf("Memory.Policy(): %v", err)
	}
	if err := p.ValidateNamespace("user/alice/memory"); err != nil {
		t.Errorf("memory namespace rejected: %v", err)
	}
	if err := p.ValidateNamespace(""); err == nil {
		t.Error("empty namespace accepted; want error")
	}
}

func TestKnowledgePolicyValidateNamespace(t *testing.T) {
	p, err := Knowledge.Policy()
	if err != nil {
		t.Fatalf("Knowledge.Policy(): %v", err)
	}
	ok := []string{
		"user/alice/knowledge",
		"user/alice/knowledge/framework",
		"app/ingester/knowledge/obsidian/work",
	}
	for _, ns := range ok {
		if err := p.ValidateNamespace(ns); err != nil {
			t.Errorf("knowledge namespace %q rejected: %v", ns, err)
		}
	}
	bad := []string{
		"",
		"user/alice/memory",
		"user/alice/notes",
	}
	for _, ns := range bad {
		if err := p.ValidateNamespace(ns); err == nil {
			t.Errorf("knowledge namespace %q accepted; want error", ns)
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

func TestPolicyUnknown(t *testing.T) {
	if _, err := Domain("made-up").Policy(); err == nil {
		t.Error("Policy() on unknown domain returned nil error")
	}
}

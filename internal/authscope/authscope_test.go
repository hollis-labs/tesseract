package authscope

import (
	"reflect"
	"testing"
)

func TestFromLegacyDefaultManagedToken(t *testing.T) {
	// contextstore.defaultTokenScopes: what a token minted without --scopes holds.
	got := FromLegacy([]string{"write", "promote.request", "promote.approve", "promote.apply", "packet", "repair", "namespace.register"})
	want := []string{Read, Write, Promote, Maintain}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default token = %v, want %v (never admin)", got, want)
	}
}

func TestFromLegacyHubStyleMemoryToken(t *testing.T) {
	// The per-machine ssh-stdio tokens: memory:read, memory:write, packet, promote.request.
	got := FromLegacy([]string{"memory:read", "memory:write", "packet", "promote.request"})
	want := []string{Read, Write, Promote}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFromLegacyAlwaysReadAndNeverInventsAuthority(t *testing.T) {
	if got := FromLegacy(nil); !reflect.DeepEqual(got, []string{Read}) {
		t.Fatalf("empty token = %v, want only read", got)
	}
	if got := FromLegacy([]string{"memory:read"}); !reflect.DeepEqual(got, []string{Read}) {
		t.Fatalf("read-only token = %v", got)
	}
	for _, g := range FromLegacy([]string{"bogus.scope", "tesseract.admin", "*"}) {
		if g == Admin {
			t.Fatal("an unknown or already-canonical string in a legacy scope list must not grant admin")
		}
	}
	if got := FromLegacy([]string{"admin"}); !contains(got, Admin) || contains(got, Write) {
		t.Fatalf("admin legacy scope = %v", got)
	}
}

func TestToLegacyExpandsOnlyWhatTheVerbContains(t *testing.T) {
	got := ToLegacy([]string{Read})
	if !reflect.DeepEqual(got, []string{"memory:read", "packet"}) {
		t.Fatalf("read -> %v", got)
	}
	if got := ToLegacy([]string{Write}); !reflect.DeepEqual(got, []string{"memory:write", "write"}) {
		t.Fatalf("write -> %v", got)
	}
	if got := ToLegacy([]string{Promote}); !reflect.DeepEqual(got, []string{"promote.apply", "promote.approve", "promote.request"}) {
		t.Fatalf("promote -> %v", got)
	}
	if got := ToLegacy([]string{"torque.write", "tether.operate", "tesseract.bogus"}); len(got) != 0 {
		t.Fatalf("other apps and unknown verbs must expand to nothing, got %v", got)
	}
}

func TestToLegacyWildcards(t *testing.T) {
	for _, g := range []string{"*", "tesseract.*"} {
		got := ToLegacy([]string{g})
		for _, must := range []string{"admin", "write", "memory:read", "promote.apply", "repair", "namespace.admin"} {
			if !contains(got, must) {
				t.Errorf("%s did not expand to %s: %v", g, must, got)
			}
		}
	}
}

func TestEveryLegacyScopeRoundTripsWithinItsVerb(t *testing.T) {
	for legacy, verb := range legacyOwner {
		if !contains(ToLegacy([]string{verb}), legacy) {
			t.Errorf("%s -> %s does not expand back to %s", legacy, verb, legacy)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

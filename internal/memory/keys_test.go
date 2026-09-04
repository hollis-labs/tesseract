package memory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestValidateKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"simple single segment", "user", false},
		{"two segments", "user.preferences", false},
		{"three segments", "user.preferences.verbosity", false},
		{"six segments max", "a.b.c.d.e.f", false},
		{"seven segments too many", "a.b.c.d.e.f.g", true},
		{"digits allowed", "project.tesseract_v2.decision_01", false},
		{"underscores allowed", "user.pref_set.key_name", false},
		{"uppercase rejected", "User.Preferences", true},
		{"hyphen rejected", "user-preferences", true},
		{"trailing dot rejected", "user.preferences.", true},
		{"leading dot rejected", ".user.preferences", true},
		{"double dot rejected", "user..preferences", true},
		{"empty rejected", "", true},
		{"whitespace rejected", "user preferences", true},
		{"segment too long rejected", "user." + strings.Repeat("a", 65), true},
		{"segment at 64 chars ok", "user." + strings.Repeat("a", 64), false},
		{"total too long rejected", strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 60) + "." + strings.Repeat("e", 60), true},
		{"unicode rejected", "user.préférences", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := memory.ValidateKey(tc.key)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ValidateKey(%q): expected error, got nil", tc.key)
					return
				}
				// Every validation failure must wrap ErrInvalidKey so callers
				// can errors.Is() against the exported sentinel.
				if !errors.Is(err, memory.ErrInvalidKey) {
					t.Errorf("ValidateKey(%q): error does not wrap ErrInvalidKey: %v", tc.key, err)
				}
			} else if err != nil {
				t.Errorf("ValidateKey(%q): expected no error, got %v", tc.key, err)
			}
		})
	}
}

func TestIsReservedPrefix(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"user.preferences", true},
		{"project.tesseract.decision", true},
		{"session.abc123.summary", true},
		{"contact.alice.role", true},
		{"agent.claude.trait", true},
		{"custom.thing", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := memory.IsReservedPrefix(tc.key); got != tc.want {
				t.Errorf("IsReservedPrefix(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// TestValidateKeyErrorTeachesTheRule pins the rejection's job: a memory key is
// refused, never silently normalized (CW-20260514-0022), so the refusal has to
// carry the rule and the valid spelling of what was passed.
func TestValidateKeyErrorTeachesTheRule(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		suggestion string
	}{
		{"hyphen", "user-preferences", "user_preferences"},
		{"uppercase", "User.Preferences", "user.preferences"},
		{"whitespace", "user preferences", "user_preferences"},
		{"slash", "user/preferences", "user_preferences"},
		{"double dot", "user..preferences", "user.preferences"},
		{"nested hyphen", "user.output-style", "user.output_style"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := memory.ValidateKey(tc.key)
			if err == nil {
				t.Fatalf("ValidateKey(%q): expected rejection", tc.key)
			}
			if !errors.Is(err, memory.ErrInvalidKey) {
				t.Fatalf("error does not wrap ErrInvalidKey: %v", err)
			}
			msg := err.Error()
			// The allowed charset and the segment rules, not just the char
			// that broke.
			for _, want := range []string{"a-z, 0-9 and _", "max 6 segments"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error does not state the rule (%q missing): %s", want, msg)
				}
			}
			if !strings.Contains(msg, `did you mean "`+tc.suggestion+`"?`) {
				t.Errorf("error does not suggest %q: %s", tc.suggestion, msg)
			}
			// Suggest, never apply: the key itself is still rejected.
			if memory.ValidateKey(tc.key) == nil {
				t.Error("suggestion turned into acceptance")
			}
		})
	}
}

// TestValidateKeyOmitsUnhelpfulSuggestions: a wrong suggestion teaches worse
// than none, so shape violations normalization cannot repair get the rule
// without a "did you mean".
func TestValidateKeyOmitsUnhelpfulSuggestions(t *testing.T) {
	for _, key := range []string{"a.b.c.d.e.f.g", "---", ""} {
		err := memory.ValidateKey(key)
		if err == nil {
			t.Fatalf("ValidateKey(%q): expected rejection", key)
		}
		if strings.Contains(err.Error(), "did you mean") {
			t.Errorf("ValidateKey(%q) offered a suggestion it cannot stand behind: %v", key, err)
		}
	}
}

func TestSuggestKey(t *testing.T) {
	cases := []struct {
		key  string
		want string
		ok   bool
	}{
		{"user-preferences", "user_preferences", true},
		{"User.Preferences", "user.preferences", true},
		{"user - preferences", "user_preferences", true},
		{"user:preferences", "user_preferences", true},
		{"user.préférences", "user.prfrences", true},
		{"user.preferences", "", false}, // already valid
		{"", "", false},                 // nothing to suggest
		{"---", "", false},              // folds to nothing
		{"a.b.c.d.e.f.g", "", false},    // still invalid after folding
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got, ok := memory.SuggestKey(tc.key)
			if ok != tc.ok || got != tc.want {
				t.Errorf("SuggestKey(%q) = (%q, %v), want (%q, %v)", tc.key, got, ok, tc.want, tc.ok)
			}
			if ok && memory.ValidateKey(got) != nil {
				t.Errorf("SuggestKey(%q) suggested an invalid key %q", tc.key, got)
			}
		})
	}
}

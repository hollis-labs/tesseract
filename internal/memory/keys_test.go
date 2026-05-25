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

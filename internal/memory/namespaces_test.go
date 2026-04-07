package memory_test

import (
	"errors"
	"testing"

	"github.com/hollis-labs/cortex/internal/memory"
)

func TestParseNamespace(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantScope memory.Scope
		wantUser  string
		wantProj  string
		wantSess  string
		wantErr   bool
	}{
		{"user root", "user/chrispian/memory", memory.ScopeUser, "chrispian", "", "", false},
		{"project", "user/chrispian/project/cortex/memory", memory.ScopeProject, "chrispian", "cortex", "", false},
		{"session", "user/chrispian/session/abc123/memory", memory.ScopeSession, "chrispian", "", "abc123", false},
		{"trailing slash rejected", "user/chrispian/memory/", memory.ScopeUnknown, "", "", "", true},
		{"missing memory suffix", "user/chrispian", memory.ScopeUnknown, "", "", "", true},
		{"unknown scope", "user/chrispian/foo/bar/memory", memory.ScopeUnknown, "", "", "", true},
		{"empty user_id", "user//memory", memory.ScopeUnknown, "", "", "", true},
		{"wrong root", "app/nanite/memory", memory.ScopeUnknown, "", "", "", true},
		{"user_id with slash", "user/ch/rispian/memory", memory.ScopeUnknown, "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, err := memory.ParseNamespace(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.input)
				}
				if !errors.Is(err, memory.ErrInvalidNamespace) {
					t.Errorf("error for %q does not wrap ErrInvalidNamespace: %v", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if ns.Scope != tc.wantScope {
				t.Errorf("Scope: got %v, want %v", ns.Scope, tc.wantScope)
			}
			if ns.UserID != tc.wantUser {
				t.Errorf("UserID: got %q, want %q", ns.UserID, tc.wantUser)
			}
			if ns.ProjectID != tc.wantProj {
				t.Errorf("ProjectID: got %q, want %q", ns.ProjectID, tc.wantProj)
			}
			if ns.SessionID != tc.wantSess {
				t.Errorf("SessionID: got %q, want %q", ns.SessionID, tc.wantSess)
			}
			if ns.String() != tc.input {
				t.Errorf("round-trip mismatch: got %q, want %q", ns.String(), tc.input)
			}
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	if err := memory.ValidateNamespace("user/chrispian/memory"); err != nil {
		t.Errorf("expected valid namespace, got %v", err)
	}
	if err := memory.ValidateNamespace("app/nanite/memory"); err == nil {
		t.Error("expected invalid namespace")
	}
}

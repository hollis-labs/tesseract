package memory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
)

func TestParseNamespace(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantScope memory.Scope
		wantUser  string
		wantProj  string
		wantSess  string
		wantType  string
		wantErr   bool
	}{
		// ── valid 4-seg user scope ────────────────────────────────────────────
		{"user decisions", "user/chrispian/memory/decisions", memory.ScopeUser, "chrispian", "", "", "decisions", false},
		{"user followups", "user/chrispian/memory/followups", memory.ScopeUser, "chrispian", "", "", "followups", false},
		{"user notes", "user/chrispian/memory/notes", memory.ScopeUser, "chrispian", "", "", "notes", false},
		{"user feedback", "user/chrispian/memory/feedback", memory.ScopeUser, "chrispian", "", "", "feedback", false},

		// ── valid 6-seg project scope ─────────────────────────────────────────
		{"project decisions", "user/chrispian/project/tesseract/memory/decisions", memory.ScopeProject, "chrispian", "tesseract", "", "decisions", false},
		{"project notes", "user/chrispian/project/tesseract/memory/notes", memory.ScopeProject, "chrispian", "tesseract", "", "notes", false},

		// ── valid 6-seg session scope ─────────────────────────────────────────
		{"session learnings", "user/chrispian/session/abc123/memory/learnings", memory.ScopeSession, "chrispian", "", "abc123", "learnings", false},
		{"session outcomes", "user/chrispian/session/sess-001/memory/outcomes", memory.ScopeSession, "chrispian", "", "sess-001", "outcomes", false},

		// ── shape rejections ─────────────────────────────────────────────────
		{"legacy flat user shape rejected", "user/chrispian/memory", memory.ScopeUnknown, "", "", "", "", true},
		{"legacy flat project shape rejected", "user/chrispian/project/tesseract/memory", memory.ScopeUnknown, "", "", "", "", true},
		{"legacy flat session shape rejected", "user/chrispian/session/abc/memory", memory.ScopeUnknown, "", "", "", "", true},
		{"trailing slash rejected", "user/chrispian/memory/decisions/", memory.ScopeUnknown, "", "", "", "", true},
		{"missing memory segment", "user/chrispian", memory.ScopeUnknown, "", "", "", "", true},
		{"unknown scope segment", "user/chrispian/foo/bar/memory/decisions", memory.ScopeUnknown, "", "", "", "", true},
		{"empty user_id", "user//memory/decisions", memory.ScopeUnknown, "", "", "", "", true},
		{"wrong root", "app/nanite/memory/decisions", memory.ScopeUnknown, "", "", "", "", true},
		{"5-seg shape rejected", "user/chrispian/project/tesseract/memory", memory.ScopeUnknown, "", "", "", "", true},
		{"3-seg shape rejected", "user/chrispian/memory", memory.ScopeUnknown, "", "", "", "", true},

		// ── type validation ──────────────────────────────────────────────────
		{"unknown type rejected", "user/chrispian/memory/decision", memory.ScopeUnknown, "", "", "", "", true},   // singular form
		{"unknown type rejected 2", "user/chrispian/memory/random", memory.ScopeUnknown, "", "", "", "", true},   // not in allowlist
		{"uppercase type rejected", "user/chrispian/memory/Decisions", memory.ScopeUnknown, "", "", "", "", true}, // case
		{"empty type rejected", "user/chrispian/memory/", memory.ScopeUnknown, "", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, err := memory.ParseNamespace(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil (parsed: %+v)", tc.input, ns)
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
			if ns.Type != tc.wantType {
				t.Errorf("Type: got %q, want %q", ns.Type, tc.wantType)
			}
			if ns.String() != tc.input {
				t.Errorf("round-trip mismatch: got %q, want %q", ns.String(), tc.input)
			}
		})
	}
}

func TestNamespacePrefix(t *testing.T) {
	cases := []struct {
		input  string
		want   string
	}{
		{"user/chrispian/memory/decisions", "user/chrispian/memory"},
		{"user/chrispian/project/tesseract/memory/notes", "user/chrispian/project/tesseract/memory"},
		{"user/chrispian/session/sess-1/memory/feedback", "user/chrispian/session/sess-1/memory"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			ns, err := memory.ParseNamespace(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := ns.Prefix(); got != tc.want {
				t.Errorf("Prefix() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	if err := memory.ValidateNamespace("user/chrispian/memory/decisions"); err != nil {
		t.Errorf("expected valid namespace, got %v", err)
	}
	if err := memory.ValidateNamespace("user/chrispian/memory"); err == nil {
		t.Error("expected legacy flat namespace to be rejected")
	}
	if err := memory.ValidateNamespace("app/nanite/memory/decisions"); err == nil {
		t.Error("expected non-'user/' root to be rejected")
	}
}

func TestTypeAllowlist(t *testing.T) {
	// Default allowlist contains all 8 locked types.
	expect := []string{"decisions", "feedback", "followups", "learnings", "limitations", "notes", "outcomes", "references"}
	got := memory.TypeAllowlist()
	if strings.Join(got, ",") != strings.Join(expect, ",") {
		t.Errorf("TypeAllowlist() = %v, want %v", got, expect)
	}
	if !memory.IsValidType("decisions") {
		t.Error("decisions must be valid")
	}
	if memory.IsValidType("nope") {
		t.Error("nope must not be valid")
	}
}

func TestSetTypeAllowlist_TestOverride(t *testing.T) {
	restore := memory.SetTypeAllowlist([]string{"custom"})
	defer restore()

	if !memory.IsValidType("custom") {
		t.Error("custom should be valid under override")
	}
	if memory.IsValidType("decisions") {
		t.Error("decisions should NOT be valid under override")
	}

	// Restore + verify.
	restore()
	if !memory.IsValidType("decisions") {
		t.Error("decisions should be valid again after restore")
	}
	if memory.IsValidType("custom") {
		t.Error("custom should no longer be valid after restore")
	}
}

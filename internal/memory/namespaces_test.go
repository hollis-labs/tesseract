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
		// wantCanonical is String()'s output. It differs from input ONLY for
		// legacy nested shapes, which render in the new grammar on purpose —
		// that rendering is N6's migration mapping.
		wantCanonical string
		wantLegacy    bool
		wantErr       bool
	}{
		// ── the scope-type-rooted grammar (CW-20260912-0078) ──────────────────
		{"user", "user/chrispian/memory/decisions", memory.ScopeUser, "chrispian", "", "", "decisions", "user/chrispian/memory/decisions", false, false},
		{"project", "project/tesseract/memory/decisions", memory.ScopeProject, "", "tesseract", "", "decisions", "project/tesseract/memory/decisions", false, false},
		{"app", "app/nanite/memory/notes", memory.ScopeApp, "", "", "", "notes", "app/nanite/memory/notes", false, false},
		{"org", "org/hollis-labs/memory/notes", memory.ScopeOrg, "", "", "", "notes", "org/hollis-labs/memory/notes", false, false},
		{"session", "session/abc123/memory/learnings", memory.ScopeSession, "", "", "abc123", "learnings", "session/abc123/memory/learnings", false, false},

		// system is the singleton: its head is ONE segment, so the whole
		// namespace is one shorter than every other scope's.
		{"system", "system/memory/notes", memory.ScopeSystem, "", "", "", "notes", "system/memory/notes", false, false},
		{"system takes no id", "system/os/memory/notes", memory.ScopeUnknown, "", "", "", "", "", false, true},

		// ── legacy nested shapes still parse during the transition ───────────
		// They render CANONICALLY, not as they arrived: parse-then-String is
		// the migration mapping N6 applies.
		{"legacy project", "user/chrispian/project/tesseract/memory/decisions", memory.ScopeProject, "chrispian", "tesseract", "", "decisions", "project/tesseract/memory/decisions", true, false},
		{"legacy session", "user/chrispian/session/sess-001/memory/outcomes", memory.ScopeSession, "chrispian", "", "sess-001", "outcomes", "session/sess-001/memory/outcomes", true, false},

		// ── shape rejections ─────────────────────────────────────────────────
		{"bare domain segment rejected", "user/chrispian/memory", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"legacy bare project rejected", "user/chrispian/project/tesseract/memory", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"trailing slash rejected", "user/chrispian/memory/decisions/", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"head only", "user/chrispian", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"unknown scope keyword", "tenant/acme/memory/decisions", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"empty id", "user//memory/decisions", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"extra depth past type", "project/tesseract/memory/notes/extra", memory.ScopeUnknown, "", "", "", "", "", false, true},

		// ── type validation ──────────────────────────────────────────────────
		{"unknown type rejected", "user/chrispian/memory/decision", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"uppercase type rejected", "user/chrispian/memory/Decisions", memory.ScopeUnknown, "", "", "", "", "", false, true},
		{"empty type rejected", "user/chrispian/memory/", memory.ScopeUnknown, "", "", "", "", "", false, true},
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
			if ns.UserID() != tc.wantUser {
				t.Errorf("UserID: got %q, want %q", ns.UserID(), tc.wantUser)
			}
			if ns.ProjectID() != tc.wantProj {
				t.Errorf("ProjectID: got %q, want %q", ns.ProjectID(), tc.wantProj)
			}
			if ns.SessionID() != tc.wantSess {
				t.Errorf("SessionID: got %q, want %q", ns.SessionID(), tc.wantSess)
			}
			if ns.Type != tc.wantType {
				t.Errorf("Type: got %q, want %q", ns.Type, tc.wantType)
			}
			if ns.Legacy != tc.wantLegacy {
				t.Errorf("Legacy: got %v, want %v", ns.Legacy, tc.wantLegacy)
			}
			if got := ns.String(); got != tc.wantCanonical {
				t.Errorf("String() = %q, want %q", got, tc.wantCanonical)
			}
		})
	}
}

// TestParseNamespace_LegacyCarriesTheUserIDForward pins the one piece of
// information the canonical rendering drops.
//
// The path is losing its claim to encode ownership, which is the point — but
// N6 has to seed the registry row that replaces it, and the owner it seeds is
// the user id the legacy path asserted. Dropping it on parse would mean
// reconstructing it from the raw string later, in a second place that has to
// agree with this parser.
func TestParseNamespace_LegacyCarriesTheUserIDForward(t *testing.T) {
	ns, err := memory.ParseNamespace("user/chrispian/project/tesseract/memory/notes")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ns.LegacyUserID != "chrispian" {
		t.Errorf("LegacyUserID = %q, want %q", ns.LegacyUserID, "chrispian")
	}
	if ns.ScopeID != "tesseract" {
		t.Errorf("ScopeID = %q, want %q — the SCOPE id is the project, not the user", ns.ScopeID, "tesseract")
	}

	// A new-grammar namespace carries no legacy user id, including under user
	// scope: there the id IS the scope id.
	fresh, err := memory.ParseNamespace("user/chrispian/memory/notes")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fresh.LegacyUserID != "" {
		t.Errorf("LegacyUserID = %q on a new-grammar namespace, want empty", fresh.LegacyUserID)
	}
	if fresh.UserID() != "chrispian" {
		t.Errorf("UserID() = %q, want chrispian", fresh.UserID())
	}
}

// TestParseKnowledgeNamespace covers the genuinely new grammar: a fixed-depth
// head with FREE DEPTH after it.
//
// Knowledge was excluded from the scoped grammar because its namespaces have
// free depth and no fixed shape to parse. The head is now fixed and therefore
// parseable; the tail keeps its freedom. That split is the whole design.
func TestParseKnowledgeNamespace(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		wantScope memory.Scope
		wantHead  string
		wantTail  string
		wantErr   bool
	}{
		{"one tail segment", "user/chrispian/knowledge/ideas", memory.ScopeUser, "user/chrispian", "ideas", false},
		{"deep tail", "project/tether/knowledge/adr/2026/09/namespaces", memory.ScopeProject, "project/tether", "adr/2026/09/namespaces", false},
		{"app scope", "app/nanite/knowledge/architecture", memory.ScopeApp, "app/nanite", "architecture", false},
		{"system singleton", "system/knowledge/glossary/terms", memory.ScopeSystem, "system", "glossary/terms", false},

		// The head alone names a scope, not a place to put a record. The
		// GRAMMAR requires a tail; the write policy deliberately does not —
		// user/chrispian/knowledge holds a canonical record today and is not
		// being invalidated here.
		{"bare head rejected by the grammar", "user/chrispian/knowledge", memory.ScopeUnknown, "", "", true},
		{"empty tail segment", "user/chrispian/knowledge//ideas", memory.ScopeUnknown, "", "", true},
		{"trailing slash", "user/chrispian/knowledge/ideas/", memory.ScopeUnknown, "", "", true},
		{"wrong domain segment", "user/chrispian/memory/notes", memory.ScopeUnknown, "", "", true},
		{"unknown scope", "tenant/acme/knowledge/ideas", memory.ScopeUnknown, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns, err := memory.ParseKnowledgeNamespace(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.input, ns)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if ns.Scope != tc.wantScope {
				t.Errorf("Scope: got %v, want %v", ns.Scope, tc.wantScope)
			}
			if got := ns.Head(); got != tc.wantHead {
				t.Errorf("Head() = %q, want %q", got, tc.wantHead)
			}
			if ns.Tail != tc.wantTail {
				t.Errorf("Tail = %q, want %q", ns.Tail, tc.wantTail)
			}
			if got := ns.String(); got != tc.input {
				t.Errorf("round-trip: String() = %q, want %q", got, tc.input)
			}
		})
	}
}

// TestNamespaceHead pins the unit the ADR calls the fixed-depth head,
// including system's one-segment irregularity.
func TestNamespaceHead(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"user/chrispian/memory/notes", "user/chrispian"},
		{"project/tesseract/memory/notes", "project/tesseract"},
		{"session/abc/memory/learnings", "session/abc"},
		{"system/memory/notes", "system"},
		// A legacy shape's head is its REAL scope, not the user it nested under.
		{"user/chrispian/project/tesseract/memory/notes", "project/tesseract"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			ns, err := memory.ParseNamespace(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := ns.Head(); got != tc.want {
				t.Errorf("Head() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNamespacePrefix(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"user/chrispian/memory/decisions", "user/chrispian/memory"},
		{"project/tesseract/memory/notes", "project/tesseract/memory"},
		{"session/sess-1/memory/feedback", "session/sess-1/memory"},
		{"system/memory/notes", "system/memory"},
		// Legacy in, canonical out — same rule String() follows.
		{"user/chrispian/project/tesseract/memory/notes", "project/tesseract/memory"},
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
	// `app/` is VALID now, and this assertion is the one the grammar change
	// inverts. It used to read "expected non-'user/' root to be rejected",
	// which was the path asserting ownership: every namespace had to begin
	// with the owner. Under CW-20260912-0078 the first segment names a SCOPE
	// TYPE, so five more roots are legal and ownership moved to the registry.
	if err := memory.ValidateNamespace("app/nanite/memory/decisions"); err != nil {
		t.Errorf("expected app/ scope to be valid under the scope-type-rooted grammar, got %v", err)
	}
	// A root that is not a scope type is still rejected — the vocabulary is
	// closed, it just got bigger.
	if err := memory.ValidateNamespace("tenant/acme/memory/decisions"); err == nil {
		t.Error("expected an unknown scope keyword to be rejected")
	}
}

func TestTypeAllowlist(t *testing.T) {
	// The shipped memory.type vocabulary, in registry order. `todos` joined it
	// in CW-20260909-0036 as the first structured object.
	expect := []string{"decisions", "feedback", "followups", "learnings", "limitations", "notes", "outcomes", "todos"}
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

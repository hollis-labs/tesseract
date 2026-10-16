package nsglob

import "testing"

func TestPermittedAndPatternWithin(t *testing.T) {
	globs := []string{"project/a/*", "app/exact"}
	for ns, want := range map[string]bool{
		"project/a/x": true, "project/a/x/y": true, "project/b/x": false,
		"app/exact": true, "app/exact/sub": false, "user/x": false, "": false,
	} {
		if got := Permitted(globs, ns); got != want {
			t.Errorf("Permitted(%q) = %v, want %v", ns, got, want)
		}
	}
	for p, want := range map[string]bool{
		"project/a/*": true, "project/a/x/*": true, "project/*": false,
		"*": false, "app/exact": true, "app/*": false, "user/*": false,
	} {
		if got := PatternWithin(globs, p); got != want {
			t.Errorf("PatternWithin(%q) = %v, want %v", p, got, want)
		}
	}
	if Permitted(nil, "x") || !Permitted([]string{"*"}, "any/thing") {
		t.Error("empty globs permit nothing; * permits everything")
	}
	if !Unrestricted([]string{"a/*", " * "}) || Unrestricted([]string{"a/*"}) {
		t.Error("Unrestricted")
	}
}

func TestMalformedAndInteriorWildcardGlobsFailClosed(t *testing.T) {
	cases := []struct {
		name        string
		globs       []string
		namespace   string // checked with Permitted
		pattern     string // checked with PatternWithin
		wantPermit  bool
		wantPattern bool
	}{
		{"malformed glob grants nothing", []string{"[/*"}, "[/x", "[/x", false, false},
		{"malformed glob does not disable valid ones", []string{"[/*", "ok/*"}, "ok/x", "ok/x", true, true},
		{"interior wildcard not widened to deeper descendants", []string{"project/*/memory/*"}, "project/a/memory/private/x", "project/*/memory/private/*", false, false},
		{"interior wildcard still matches one segment", []string{"project/*/memory/*"}, "project/a/memory/x", "project/a/memory/x", true, true},
		{"literal prefix glob covers descendants", []string{"project/a/*"}, "project/a/x/y", "project/a/x/*", true, true},
		{"empty globs deny", nil, "project/a/x", "project/a/x", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Permitted(tc.globs, tc.namespace); got != tc.wantPermit {
				t.Errorf("Permitted(%v, %q) = %v, want %v", tc.globs, tc.namespace, got, tc.wantPermit)
			}
			if got := PatternWithin(tc.globs, tc.pattern); got != tc.wantPattern {
				t.Errorf("PatternWithin(%v, %q) = %v, want %v", tc.globs, tc.pattern, got, tc.wantPattern)
			}
		})
	}
}

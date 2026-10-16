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

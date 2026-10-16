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
	// A glob path.Match cannot parse grants nothing, prefix fallback included.
	if Permitted([]string{"[/*"}, "[/x") || PatternWithin([]string{"[/*"}, "[/x") {
		t.Error("malformed glob granted access")
	}
	if Permitted([]string{"[/*", "ok/*"}, "ok/x") != true {
		t.Error("a malformed glob must not disable the valid ones")
	}
	// An interior-wildcard grant matches one segment per star and is never
	// widened to descendants by string prefix.
	g := []string{"project/*/memory/*"}
	if Permitted(g, "project/a/memory/private/x") {
		t.Error("interior-wildcard glob granted a deeper descendant")
	}
	if PatternWithin(g, "project/*/memory/private/*") {
		t.Error("PatternWithin disagrees with Permitted for an interior-wildcard glob")
	}
	if !Permitted(g, "project/a/memory/x") {
		t.Error("single-segment match under the interior wildcard should still work")
	}
}

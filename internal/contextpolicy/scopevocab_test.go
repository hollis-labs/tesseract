package contextpolicy

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestScopeVocabularyMatchesTheParser keeps contextpolicy's copy of the scope
// vocabulary in step with the parser's.
//
// The two cannot share a constant: internal/memory imports this package's
// peers, so importing memory from here is an import cycle. What crosses the
// boundary instead is six strings — and six strings in two files is exactly
// the shape that drifts silently, because each side passes its own tests while
// disagreeing. A scope added to the parser and not here would be parseable and
// unprotected, which is the failure direction that matters.
//
// Reading the source rather than importing it is deliberate: the point is to
// fail when the OTHER file changes, and only the other file's text can say
// that.
func TestScopeVocabularyMatchesTheParser(t *testing.T) {
	const parserPath = "../memory/namespaces.go"
	src, err := os.ReadFile(parserPath)
	if err != nil {
		t.Fatalf("read %s: %v", parserPath, err)
	}

	block := regexp.MustCompile(`(?s)var scopeKeywords = map\[string\]Scope\{(.*?)\n\}`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("could not find scopeKeywords in %s — if it was renamed, this guard needs the new name, "+
			"because a guard that silently stops finding its subject is worse than no guard", parserPath)
	}
	entries := regexp.MustCompile(`"([a-z]+)":`).FindAllSubmatch(block[1], -1)
	if len(entries) == 0 {
		t.Fatalf("parsed zero scope keywords out of %s", parserPath)
	}

	fromParser := make([]string, 0, len(entries))
	for _, m := range entries {
		fromParser = append(fromParser, string(m[1]))
	}
	sort.Strings(fromParser)

	fromPolicy := make([]string, 0, len(scopeTypes))
	for scope := range scopeTypes {
		fromPolicy = append(fromPolicy, scope)
	}
	sort.Strings(fromPolicy)

	if strings.Join(fromParser, ",") != strings.Join(fromPolicy, ",") {
		t.Fatalf("scope vocabulary drift.\n  parser:        %v\n  contextpolicy: %v\n"+
			"A scope the parser accepts and this package does not know is parseable and unprotected.",
			fromParser, fromPolicy)
	}
}

// TestCanWrite_UnregisteredUserScopeStillRequiresUserActor is the fence
// CW-20260912-0078 deliberately did NOT remove.
//
// The path's claim to encode an OWNER is gone — `app/nanite` no longer means
// "owned by nanite". The claim that user SCOPE is protected is not an
// ownership claim and stays, because registration is a side effect of writing:
// the first write to any namespace arrives unregistered, so deleting this
// branch would have let any actor create a namespace in the one tier the ADR
// exists to protect. N4 replaces it with declared registration.
//
// There were no tests on CanWrite at all before this one.
func TestCanWrite_UnregisteredUserScopeStillRequiresUserActor(t *testing.T) {
	e := New()

	if err := e.CanWrite("agent-1", "app:agent-1", "user/chrispian/memory/notes"); err == nil {
		t.Error("an unregistered user-scoped namespace accepted a non-user actor — " +
			"this is the first-write hole the scope fence exists to close")
	}
	if err := e.CanWrite("", "user", "user/chrispian/memory/notes"); err != nil {
		t.Errorf("actor=user refused on a user-scoped namespace: %v", err)
	}
}

// TestCanWrite_AppScopeFenceIsCrossAppIsolation pins the rule
// CW-20260912-0078 set out to remove and then did not.
//
// The plan was to delete the path's claim to encode ownership, `app/{id}`
// included. Measured against the suite, that claim is currently the ONLY thing
// stopping app `editor` from writing `app/other/session`: the namespace is
// unregistered, so the registry has no owner to consult, and three tests plus
// the golden API error contract assert a 403 there. Removing it before N4
// supplies declared registration would have turned cross-app isolation off for
// every namespace that does not exist yet — which is all of them, on first
// write.
//
// So the fence stayed and only its SPELLING changed: it reads the scope off
// the shared vocabulary instead of matching a string prefix. Ownership still
// moves to the registry; that move is N4's, and it needs declared registration
// to land first.
func TestCanWrite_AppScopeFenceIsCrossAppIsolation(t *testing.T) {
	e := New()

	if err := e.CanWrite("editor", "app:editor", "app/other/session"); err == nil {
		t.Error("app `editor` wrote another app's unregistered namespace — " +
			"this is the cross-app isolation the golden error contract asserts")
	}
	if err := e.CanWrite("editor", "app:editor", "app/editor/session"); err != nil {
		t.Errorf("an app was refused its own namespace: %v", err)
	}
}

// TestCanWrite_RegistryOutranksTheFence is the part of "ownership moves to the
// registry" that IS live: a declared owner is consulted first and settles the
// question, and the scope fence is only the fallback for a namespace nobody
// has declared.
func TestCanWrite_RegistryOutranksTheFence(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("app/nanite/memory/notes", "app", "indexer", nil); err != nil {
		t.Fatalf("RegisterNamespace: %v", err)
	}
	// The DECLARED owner is `indexer`, not the `nanite` in the path. Under the
	// old prefix rule the path won and indexer was refused its own namespace.
	if err := e.CanWrite("indexer", "app:indexer", "app/nanite/memory/notes"); err != nil {
		t.Errorf("declared owner refused; the path is still outranking the registry: %v", err)
	}
	if err := e.CanWrite("nanite", "app:nanite", "app/nanite/memory/notes"); err == nil {
		t.Error("the path's id was accepted over the declared owner")
	}
}

// TestCanWrite_SystemSentinelFallsThroughToTheFence covers a passthrough that
// is easy to lose when the registry check moves first.
//
// RegisterNamespace documents owner_type "system" as a sentinel for namespaces
// that never fit the user|app shape, and says it is NOT consulted for
// enforcement. Treating it as "registered" would let a sentinel row silently
// disable the user fence.
func TestCanWrite_SystemSentinelFallsThroughToTheFence(t *testing.T) {
	e := New()
	if err := e.RegisterNamespace("user/chrispian/memory/notes", "system", "tesseract", nil); err != nil {
		t.Fatalf("RegisterNamespace: %v", err)
	}
	if err := e.CanWrite("agent-1", "app:agent-1", "user/chrispian/memory/notes"); err == nil {
		t.Error("a system sentinel registration disabled the user-scope fence")
	}
}

// TestCanWrite_NewScopesAreNotAccidentallyProtected covers the four scopes
// that carry no fence: project, org, session and system are governed by the
// registry alone, so an unregistered one is writable. That is the current
// design and N4's starting point, stated here so a change to it is visible.
func TestCanWrite_NewScopesAreNotAccidentallyProtected(t *testing.T) {
	e := New()
	for _, ns := range []string{
		"project/tether/memory/notes",
		"org/hollis-labs/memory/notes",
		"session/s-1/memory/notes",
		"system/memory/notes",
	} {
		if err := e.CanWrite("agent-1", "app:agent-1", ns); err != nil {
			t.Errorf("CanWrite(%q) = %v, want nil — these scopes are registry-governed, "+
				"and N4 is what gives them declared owners", ns, err)
		}
	}
}

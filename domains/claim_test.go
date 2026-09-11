package domains

import "testing"

// TestClaimedByRecognizesTheCuratedGrammars pins the positions a domain name
// may occupy. The flat cases are the common ones; the project and session
// cases are why domainSegments carries 4 as well as 2.
func TestClaimedByRecognizesTheCuratedGrammars(t *testing.T) {
	cases := []struct {
		ns   string
		want Domain
	}{
		{"user/chrispian/memory/decisions", Memory},
		{"user/chrispian/knowledge/portfolio", Knowledge},
		{"user/chrispian/event/session", Event},
		{"app/nanite/knowledge/api", Knowledge},
		{"user/chrispian/project/torque/memory/decisions", Memory},
		{"user/chrispian/session/s-1/event/reasoning", Event},
		// The bare prefix form, which eight of the stranded rows use.
		{"user/chrispian/memory", Memory},
	}
	for _, c := range cases {
		claim, ok := ClaimedBy(c.ns)
		if !ok {
			t.Errorf("ClaimedBy(%q) = not claimed, want %s", c.ns, c.want)
			continue
		}
		if claim.Domain != c.want {
			t.Errorf("ClaimedBy(%q).Domain = %s, want %s", c.ns, claim.Domain, c.want)
		}
	}
}

// TestClaimedByLeavesTheContextStoreItsOwnNamespaces uses the namespaces that
// actually hold records today. Every one of these has a live writer, and a
// guard that caught any of them would break a working caller — which is the
// failure mode this test exists to catch, not a hypothetical one.
func TestClaimedByLeavesTheContextStoreItsOwnNamespaces(t *testing.T) {
	for _, ns := range []string{
		"app/editor/session",
		"system/sessions/PRJ-20260419-0001",
		"app/nanite/diagnostics",
		"app/mentat/projects",
		"app/volon/architecture",
		"global/system",
		"issues",
		"adr",
		// Two segments: outside both grammars, and the legacy home of five
		// 2026-03 rows. Not claimed, so those rows stay writable.
		"user/memory",
		// Contains a domain word, but not at a domain position.
		"app/memory-tools/config",
		"user/chrispian/notes/memory",
	} {
		if claim, ok := ClaimedBy(ns); ok {
			t.Errorf("ClaimedBy(%q) = claimed by %s, want unclaimed", ns, claim.Domain)
		}
	}
}

// TestEveryDomainHasAClaim keeps the surface map honest against the registry.
// A domain added to domains.go without a claim here would be unreachable in
// the refusal message — the caller would be told "no" without being told where
// the write belonged, which is the whole point of carrying the pair.
func TestEveryDomainHasAClaim(t *testing.T) {
	for _, d := range All() {
		c, ok := claims[d]
		if !ok {
			t.Errorf("domain %q has no entry in claims", d)
			continue
		}
		if c.MCPTool == "" || c.HTTPPath == "" {
			t.Errorf("domain %q claim is incomplete: %+v", d, c)
		}
		if c.Domain != d {
			t.Errorf("claims[%q].Domain = %q, want %q", d, c.Domain, d)
		}
	}
}

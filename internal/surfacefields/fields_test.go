package surfacefields

import (
	"regexp"
	"testing"
)

// taskID is the shape a Pending marker must have. A marker that names nothing
// is an exemption with no owner, which is the failure mode this table exists to
// stop — the literal it replaces was also nobody's in particular.
var taskID = regexp.MustCompile(`^CW-\d{8}-\d{4}$`)

func TestDoorsAreWellFormed(t *testing.T) {
	seenDoor := map[string]bool{}
	for _, d := range Doors {
		if d.Name == "" || d.MCPTool == "" || d.HTTPPath == "" || d.HTTPMethod == "" {
			t.Errorf("door %q: Name, MCPTool, HTTPMethod and HTTPPath are all required", d.Name)
		}
		if seenDoor[d.Name] {
			t.Errorf("door %q: declared twice", d.Name)
		}
		seenDoor[d.Name] = true

		seenConcept := map[string]bool{}
		seenMCP := map[string]bool{}
		seenHTTP := map[string]bool{}
		for _, f := range d.Fields {
			switch {
			case f.Concept == "":
				t.Errorf("door %q: a field has no Concept", d.Name)
			case f.MCP == "" && f.HTTP == "":
				t.Errorf("door %q concept %q: accepted on neither surface, so it is not a field", d.Name, f.Concept)
			}
			if seenConcept[f.Concept] {
				t.Errorf("door %q: concept %q declared twice", d.Name, f.Concept)
			}
			seenConcept[f.Concept] = true

			for _, name := range append([]string{f.MCP}, f.MCPAliases...) {
				if name == "" {
					continue
				}
				if seenMCP[name] {
					t.Errorf("door %q: MCP name %q is claimed by two concepts", d.Name, name)
				}
				seenMCP[name] = true
			}
			if f.HTTP != "" {
				if seenHTTP[f.HTTP] {
					t.Errorf("door %q: HTTP path %q is claimed by two concepts", d.Name, f.HTTP)
				}
				seenHTTP[f.HTTP] = true
			}
		}
	}
}

// TestDivergencesAreExplained is the rule that makes the table worth having. A
// difference between the surfaces is allowed; a difference nobody wrote a
// sentence about is not.
func TestDivergencesAreExplained(t *testing.T) {
	for _, d := range Doors {
		for _, f := range d.Fields {
			switch {
			case f.Diverges() && f.Why == "" && f.Pending == "":
				t.Errorf("door %q concept %q: MCP says %q and HTTP says %q, which is not the "+
					"flat/nested projection. Add a Why if the difference should stay, or a "+
					"Pending naming the task that removes it.",
					d.Name, f.Concept, f.MCP, f.HTTP)
			case f.OneSided() && f.Why == "":
				t.Errorf("door %q concept %q: accepted on one surface only. That is a "+
					"capability difference and needs a Why, even if the Why is that nobody "+
					"has ruled on it.", d.Name, f.Concept)
			}
			if f.Pending != "" && !taskID.MatchString(f.Pending) {
				t.Errorf("door %q concept %q: Pending %q is not a task ID", d.Name, f.Concept, f.Pending)
			}
		}
	}
}

// TestPendingRowsStillDiverge is what keeps the allowlist from becoming the
// thing it tolerates.
//
// A Pending row is a divergence we have agreed to carry until a named task
// removes it. When that task lands, the two spellings agree and the marker is
// dead — and a dead marker is indistinguishable from a live one to every reader
// after the fact. So the same check that permits the row requires it to still
// be needed: clearing the divergence without clearing the marker fails here,
// with the task named.
func TestPendingRowsStillDiverge(t *testing.T) {
	for _, d := range Doors {
		for _, f := range d.Fields {
			if f.Pending == "" {
				continue
			}
			if !f.Diverges() {
				t.Errorf("door %q concept %q: Pending names %s, but MCP and HTTP now agree "+
					"(%q / %q). The divergence is gone — delete the Pending marker.",
					d.Name, f.Concept, f.Pending, f.MCP, f.HTTP)
			}
		}
	}
}

// TestSpellingLookupsRoundTrip pins the two directions against each other, so a
// row cannot be readable from one surface and not the other.
func TestSpellingLookupsRoundTrip(t *testing.T) {
	for _, d := range Doors {
		for _, f := range d.Fields {
			if f.MCP == "" || f.HTTP == "" {
				continue
			}
			got, ok := d.HTTPSpellingFor(f.MCP)
			if !ok || got != f.HTTP {
				t.Errorf("door %q: HTTPSpellingFor(%q) = %q, %v; want %q, true",
					d.Name, f.MCP, got, ok, f.HTTP)
			}
			back, ok := d.MCPSpellingFor(f.HTTP)
			if !ok || back != f.MCP {
				t.Errorf("door %q: MCPSpellingFor(%q) = %q, %v; want %q, true",
					d.Name, f.HTTP, back, ok, f.MCP)
			}
			for _, alias := range f.MCPAliases {
				if got, ok := d.HTTPSpellingFor(alias); !ok || got != f.HTTP {
					t.Errorf("door %q: alias %q resolves to %q, %v; want %q, true",
						d.Name, alias, got, ok, f.HTTP)
				}
			}
		}
	}
}

// TestHintExpressesFlatToFlat is the capability the literal map did not have,
// asserted directly rather than left implicit in the data.
//
// The map it replaces was flat-to-nested only: its values were dotted paths and
// the caller-facing message said "this endpoint nests it as". That shape cannot
// state knowledge.write's answer, which is that the same fact is taken flat
// under a different name.
func TestHintExpressesFlatToFlat(t *testing.T) {
	memory, ok := DoorByName("memory.write")
	if !ok {
		t.Fatal("memory.write door is missing")
	}
	if got, _ := memory.HTTPSpellingFor("payload_data"); got != "payload.data" {
		t.Errorf("memory.write payload_data → %q, want payload.data (the nested answer)", got)
	}

	knowledge, ok := DoorByName("knowledge.write")
	if !ok {
		t.Fatal("knowledge.write door is missing")
	}
	got, ok := knowledge.HTTPSpellingFor("payload_data")
	if !ok {
		t.Fatal("knowledge.write cannot answer for payload_data — the exact gap this table replaced")
	}
	if got != "data" {
		t.Errorf("knowledge.write payload_data → %q, want data (the flat answer)", got)
	}
}

// TestDoorByMCPToolRefusesAmbiguity documents that a tool serving several doors
// has no single argument set, so callers must name the door.
func TestDoorByMCPToolRefusesAmbiguity(t *testing.T) {
	if _, ok := DoorByMCPTool("tesseract_get"); ok {
		t.Error("tesseract_get serves three doors; resolving it to one would pick an arm at random")
	}
	if _, ok := DoorByMCPTool("memory_write"); !ok {
		t.Error("memory_write serves exactly one door and should resolve")
	}
}

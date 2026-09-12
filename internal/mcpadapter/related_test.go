package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

// CW-20260825-0017. The MCP surface of the `related` recall expansion.
//
// The graph logic is tested in internal/memory; what these cover is the door:
// that `related_to` is spelled the same way in the tool schema and the handler,
// and that a bad relation is refused rather than answered with an empty page.
// A misspelled argument name is read as absent, so the failure is a
// well-formed page of unnarrowed rows with no error anywhere.

const relNamespace = "user/chrispian/memory/notes"

func relatedAdapter(t *testing.T) *Adapter {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ctx := context.Background()

	tok, _, err := cs.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label:  "test",
		Scopes: []string{"memory:read", "memory:write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	write := func(key, body string) {
		t.Helper()
		if _, wErr := ms.WriteRevision(ctx, memory.WriteInput{
			Domain:      domains.Memory,
			Namespace:   relNamespace,
			MemoryKey:   key,
			Author:      memory.Author{AgentID: "test", AgentVersion: "1"},
			Trigger:     memory.TriggerExplicit,
			SessionID:   "sess-rel",
			DerivedFrom: memory.DerivedFromUser,
			Confidence:  0.9,
			Status:      memory.StatusCanonical,
			Payload:     memory.Payload{Summary: "probe " + key, Body: body},
		}); wErr != nil {
			t.Fatalf("write %s: %v", key, wErr)
		}
	}
	write("rel.anchor", "cites [[rel.outbound]]")
	write("rel.outbound", "a leaf")
	write("rel.inbound", "refers to [[rel.anchor]]")
	write("rel.unrelated", "mentions nobody")

	a := New(cs, tok)
	a.MemoryStore = ms
	return a
}

func TestRecallRelatedToNarrowsToTheNeighborhood(t *testing.T) {
	a := relatedAdapter(t)

	raw := lookupRaw(t, a, map[string]any{
		"namespaces": `["` + relNamespace + `"]`,
		"ranking":    "chronological",
		"related_to": `["rel.anchor"]`,
	})

	if !strings.Contains(raw, "rel.outbound") {
		t.Errorf("outbound neighbor missing:\n%s", raw)
	}
	if !strings.Contains(raw, "rel.inbound") {
		t.Errorf("inbound neighbor missing — the expansion is directional:\n%s", raw)
	}
	if strings.Contains(raw, "rel.unrelated") {
		t.Errorf("related_to narrowed nothing — check the argument name:\n%s", raw)
	}
}

func TestRecallRelatedRelationsRejectsUnknownValue(t *testing.T) {
	a := relatedAdapter(t)

	raw := lookupRaw(t, a, map[string]any{
		"namespaces":        `["` + relNamespace + `"]`,
		"ranking":           "chronological",
		"related_to":        `["rel.anchor"]`,
		"related_relations": `["mentions"]`,
	})
	if !strings.Contains(raw, string(codeValidationError)) {
		t.Fatalf("unknown relation was not refused:\n%s", raw)
	}
	if !strings.Contains(raw, "references") {
		t.Errorf("error should render the vocabulary:\n%s", raw)
	}
}

func TestRecallRelatedRelationsWithoutAnchorRejected(t *testing.T) {
	a := relatedAdapter(t)

	raw := lookupRaw(t, a, map[string]any{
		"namespaces":        `["` + relNamespace + `"]`,
		"ranking":           "chronological",
		"related_relations": `["references"]`,
	})
	if !strings.Contains(raw, string(codeValidationError)) {
		t.Fatalf("a relation filter with no anchor was accepted:\n%s", raw)
	}
}

// The schema must advertise what the handler reads. lookupRaw calls the
// handler directly, so the behavioral tests above would still pass if the
// tool declared a different spelling — and an agent reading the tool list
// would never learn the knob exists.
func TestRecallRelatedArgumentsAreDeclared(t *testing.T) {
	tools := registeredToolSchemas(t)
	props, ok := tools["tesseract_recall"]
	if !ok {
		t.Fatal("tesseract_recall is not registered")
	}
	for _, arg := range []string{"related_to", "related_relations"} {
		if _, ok := props[arg]; !ok {
			t.Errorf("tesseract_recall does not declare %q", arg)
		}
	}
}

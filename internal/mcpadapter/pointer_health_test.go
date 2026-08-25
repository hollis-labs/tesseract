package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// CW-20260825-0015. Pointer health is surfaced on lookup results and is
// filterable. These tests cover the MCP surface specifically — the derivation
// itself is tested in internal/memory.

const phNamespace = "user/chrispian/knowledge/ph"

func pointerHealthAdapter(t *testing.T) (*Adapter, string, string) {
	t.Helper()
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	ks := knowledge.New(ms)
	ctx := context.Background()

	tok, _, err := cs.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label:  "test",
		Scopes: []string{"memory:read", "memory:write"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	write := func(key, scheme, locator string) memory.Revision {
		rev, wErr := ks.Write(ctx, knowledge.WriteInput{
			Namespace: phNamespace, Key: key, Kind: "note", Source: "manual",
			Pointer: memory.Pointer{Scheme: scheme, Locator: locator},
			Summary: "pointer health probe " + key,
			Author:  memory.Author{AgentID: "test", AgentVersion: "1"}, SessionID: "sess-ph",
		})
		if wErr != nil {
			t.Fatalf("knowledge write %s: %v", key, wErr)
		}
		return rev
	}
	dead := write("ph.dead", "file", "/tmp/ph-dead")
	live := write("ph.live", "file", "/tmp/ph-live")

	now := time.Now().UTC()
	if _, err := memory.ApplyVerificationPlan(ctx, cs.DB(), memory.VerificationPlan{
		Rows: []memory.PointerObservation{
			{RevisionID: dead.RevisionID, Scheme: "file", Locator: "/tmp/ph-dead",
				Outcome: memory.OutcomeUnresolvable, Detail: "not_found", CheckedAt: now},
			{RevisionID: live.RevisionID, Scheme: "file", Locator: "/tmp/ph-live",
				Outcome: memory.OutcomeResolved, Detail: "stat_ok", CheckedAt: now},
		},
	}); err != nil {
		t.Fatalf("apply observations: %v", err)
	}

	a := New(cs, tok)
	a.MemoryStore = ms
	a.KnowledgeStore = ks
	return a, dead.RevisionID, live.RevisionID
}

func lookupRaw(t *testing.T, a *Adapter, args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := a.handleTesseractLookup(context.Background(), req)
	if err != nil {
		t.Fatalf("handleTesseractLookup: %v", err)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return text.Text
}

// TestLookupSurfacesPointerHealthUnderDefaultProjection is the discoverability
// requirement: the signal has to be there without the caller asking for a
// wider projection, because nobody opts in to look for a problem they have not
// been told about.
func TestLookupSurfacesPointerHealthUnderDefaultProjection(t *testing.T) {
	a, deadID, _ := pointerHealthAdapter(t)

	raw := lookupRaw(t, a, map[string]any{
		"namespaces": `["` + phNamespace + `"]`,
		"ranking":    "chronological",
	})
	if !strings.Contains(raw, "pointer_health") {
		t.Fatalf("default-projection lookup carries no pointer_health:\n%s", raw)
	}
	if !strings.Contains(raw, string(memory.PointerHealthUnresolvable)) {
		t.Errorf("default-projection lookup does not report the dead pointer (%s):\n%s", deadID, raw)
	}

	// keys stays identity-only.
	keysRaw := lookupRaw(t, a, map[string]any{
		"namespaces":   `["` + phNamespace + `"]`,
		"ranking":      "chronological",
		"payload_mode": "keys",
	})
	if strings.Contains(keysRaw, "pointer_health") {
		t.Errorf("keys projection carries pointer_health; keys is identity-only:\n%s", keysRaw)
	}
}

func TestLookupPointerHealthFilterSelects(t *testing.T) {
	a, deadID, liveID := pointerHealthAdapter(t)

	raw := lookupRaw(t, a, map[string]any{
		"namespaces":     `["` + phNamespace + `"]`,
		"ranking":        "chronological",
		"pointer_health": `["unresolvable"]`,
	})
	if !strings.Contains(raw, deadID) {
		t.Errorf("filtered lookup omits the dead revision %s:\n%s", deadID, raw)
	}
	if strings.Contains(raw, liveID) {
		t.Errorf("filtered lookup includes the resolved revision %s; the filter is not filtering:\n%s", liveID, raw)
	}

	var doc struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Results) != 1 {
		t.Errorf("filtered lookup returned %d result(s), want 1", len(doc.Results))
	}
}

// TestLookupPointerHealthRejectsUnknownStatus matters because the failure
// mode of a silently-ignored typo is an empty result set, which reads exactly
// like a clean corpus.
func TestLookupPointerHealthRejectsUnknownStatus(t *testing.T) {
	a, _, _ := pointerHealthAdapter(t)
	raw := lookupRaw(t, a, map[string]any{
		"namespaces":     `["` + phNamespace + `"]`,
		"pointer_health": `["broken"]`,
	})
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("an unknown pointer_health value was accepted; a typo must not read as a clean corpus:\n%s", raw)
	}
	for _, status := range memory.PointerHealthStatusVocabulary() {
		if !strings.Contains(raw, status) {
			t.Errorf("validation error does not name allowed status %q:\n%s", status, raw)
		}
	}
}

// TestLookupToolDescribesPointerHealthVocabulary keeps the call-site
// description from advertising a set the filter does not accept. It is
// rendered from the vocabulary rather than restated; this asserts the
// rendering reaches the registered tool.
func TestLookupToolDescribesPointerHealthVocabulary(t *testing.T) {
	a, _, _ := pointerHealthAdapter(t)
	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)

	st, ok := srv.ListTools()["tesseract_lookup"]
	if !ok {
		t.Fatal("tesseract_lookup not registered")
	}
	schema, err := st.Tool.InputSchema.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	desc := string(schema)
	for _, status := range memory.PointerHealthStatusVocabulary() {
		if !strings.Contains(desc, status) {
			t.Errorf("tesseract_lookup pointer_health description omits %q", status)
		}
	}
	// The reading that prevents the field from being misused.
	if !strings.Contains(desc, "NOT evidence of death") {
		t.Error("the description does not warn that unverifiable is not evidence the pointer is dead")
	}
}

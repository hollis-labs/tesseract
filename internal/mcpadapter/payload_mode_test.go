package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// CW-20260825-0003. payload_mode projects recall/lookup results to keys,
// summary, or full. These tests are written to fail if the projection is
// reverted: each one asserts on content that only a *working* projection
// removes, rather than on a byte threshold that a no-op projection could
// also satisfy.

const (
	projTestSummary = "summary text for the projection probe"
	projTestBody    = "BODY-SENTINEL body text that projection must drop entirely"
)

// projAdapter seeds one memory carrying both a summary and a body.
func projAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "proj.one",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-proj",
		"origin":          "user",
		"confidence":      0.77,
		"tags":            `["alpha","beta"]`,
		"payload_summary": projTestSummary,
		"payload_body":    projTestBody,
	})
	return a
}

// recallRaw returns the raw JSON text of a memory_recall response.
func recallRaw(t *testing.T, a *Adapter, args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := a.handleMemoryRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMemoryRecall: %v", err)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return text.Text
}

func recallArgs(mode string) map[string]any {
	args := map[string]any{
		"namespaces": `["user/chrispian/memory/notes"]`,
		"ranking":    "activation",
	}
	if mode != "" {
		args["payload_mode"] = mode
	}
	return args
}

// ── The body actually leaves / stays ─────────────────────────────────────

// The load-bearing assertion for the whole ticket: the body sentinel must be
// absent from the wire under keys and summary, and present under full. A
// projection that forgot to drop Payload.Body fails here.
func TestPayloadMode_BodyPresenceByMode(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		wantBody bool
	}{
		{"keys", false},
		{"summary", false},
		{"full", true},
		{"", false}, // config default
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			a := projAdapter(t)
			raw := recallRaw(t, a, recallArgs(tc.mode))
			gotBody := strings.Contains(raw, projTestBody)
			if gotBody != tc.wantBody {
				t.Errorf("body sentinel present = %v, want %v; raw=%s", gotBody, tc.wantBody, raw)
			}
		})
	}
}

// summary keeps the summary; keys drops it too.
func TestPayloadMode_SummaryPresenceByMode(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		wantSummary bool
	}{
		{"keys", false},
		{"summary", true},
		{"full", true},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			a := projAdapter(t)
			raw := recallRaw(t, a, recallArgs(tc.mode))
			gotSummary := strings.Contains(raw, projTestSummary)
			if gotSummary != tc.wantSummary {
				t.Errorf("summary present = %v, want %v; raw=%s", gotSummary, tc.wantSummary, raw)
			}
		})
	}
}

// ── The acceptance criteria ──────────────────────────────────────────────

// Acceptance: every result carries revision_id in every mode, so a caller
// can always hydrate via memory_get_revision.
func TestPayloadMode_RevisionIDAlwaysPresent(t *testing.T) {
	for _, mode := range []string{"keys", "summary", "full"} {
		t.Run("mode="+mode, func(t *testing.T) {
			a := projAdapter(t)
			var out []map[string]any
			raw := recallRaw(t, a, recallArgs(mode))
			if err := json.Unmarshal(recallResultsJSON(t, raw), &out); err != nil {
				t.Fatalf("unmarshal: %v (raw=%s)", err, raw)
			}
			if len(out) == 0 {
				t.Fatalf("no results; raw=%s", raw)
			}
			rev, ok := out[0]["revision"].(map[string]any)
			if !ok {
				t.Fatalf("revision is %T, want object", out[0]["revision"])
			}
			id, _ := rev["revision_id"].(string)
			if id == "" {
				t.Errorf("mode=%s: revision_id empty or absent; revision=%v", mode, rev)
			}
		})
	}
}

// Acceptance: the tool description states the just-in-time pattern and names
// the hydration tool that actually exists today. Also guards that the
// payload_mode argument is really declared in the schema — a knob agents
// cannot discover is not a knob.
func TestPayloadMode_ToolDescriptionStatesHydratePattern(t *testing.T) {
	cs := newTestStore(t)
	ms := memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	a := New(cs, "")
	a.MemoryStore = ms

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	a.RegisterAllTools(srv)
	registered := srv.ListTools()

	for _, name := range []string{"memory_recall", "tesseract_lookup"} {
		st, ok := registered[name]
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
		desc := st.Tool.Description
		for _, want := range []string{"payload_mode", "recall → choose → hydrate", "memory_get_revision"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s description missing %q", name, want)
			}
		}
		// The hydration tool named must be one that exists on this surface.
		// tesseract_get_revision is wave-3 naming and must not appear yet.
		if strings.Contains(desc, "tesseract_get_revision") {
			t.Errorf("%s description names tesseract_get_revision, which does not exist on this surface", name)
		}
		if _, ok := st.Tool.InputSchema.Properties["payload_mode"]; !ok {
			t.Errorf("%s does not declare a payload_mode argument in its input schema", name)
		}
	}
}

// ── Projected results are self-describing ────────────────────────────────

// Payload.Body carries `omitempty`, so a withheld body and an empty body
// serialize identically. The payload_mode marker on each projected result is
// what makes them distinguishable — without it, a caller that edits what it
// reads cannot tell it is about to drop a body. Full mode omits the marker
// so its response stays exactly what it was before projection existed.
func TestPayloadMode_ProjectedResultsCarryTheMarker(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		wantMarker string
	}{
		{"keys", "keys"},
		{"summary", "summary"},
		{"full", ""},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			a := projAdapter(t)
			var out []map[string]any
			raw := recallRaw(t, a, recallArgs(tc.mode))
			if err := json.Unmarshal(recallResultsJSON(t, raw), &out); err != nil {
				t.Fatalf("unmarshal: %v (raw=%s)", err, raw)
			}
			got, _ := out[0]["payload_mode"].(string)
			if got != tc.wantMarker {
				t.Errorf("payload_mode marker = %q, want %q; raw=%s", got, tc.wantMarker, raw)
			}
		})
	}
}

// Confidence 0 is a legitimate value — write validates it into [0, 1.0]
// inclusive — so summary mode must serialize it rather than treat it as an
// empty field. A float64+omitempty projection drops the field on exactly the
// lowest-confidence revisions, which are the ones a triage pass is looking
// for. Same defect class as a value-typed Score.
func TestPayloadMode_SummaryKeepsZeroConfidence(t *testing.T) {
	a := newMemoryAdapter(t, "memory:write", "memory:read")
	writeViaHandler(t, a, map[string]any{
		"namespace":       "user/chrispian/memory/notes",
		"memory_key":      "proj.zero",
		"author_agent_id": "claude",
		"trigger":         "explicit",
		"session_id":      "sess-proj",
		"origin":          "user",
		"confidence":      0.0,
		"payload_summary": "zero confidence probe",
	})

	var out []map[string]any
	raw := recallRaw(t, a, recallArgs("summary"))
	if err := json.Unmarshal(recallResultsJSON(t, raw), &out); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, raw)
	}
	rev, ok := out[0]["revision"].(map[string]any)
	if !ok {
		t.Fatalf("revision is %T, want object", out[0]["revision"])
	}
	got, present := rev["confidence"]
	if !present {
		t.Fatalf("confidence absent under summary mode; a valid 0 was dropped. revision=%v", rev)
	}
	if got != float64(0) {
		t.Errorf("confidence = %v, want 0", got)
	}
}

// ── Defaults and overrides (D4) ──────────────────────────────────────────

// The per-call argument overrides the config-wired default in both
// directions — a config default of full must still yield a projected
// response when the call asks for summary, and vice versa.
func TestPayloadMode_PerCallOverridesConfigDefault(t *testing.T) {
	t.Run("config=full, call=summary", func(t *testing.T) {
		a := projAdapter(t)
		a.DefaultPayloadMode = memory.PayloadModeFull
		if raw := recallRaw(t, a, recallArgs("summary")); strings.Contains(raw, projTestBody) {
			t.Errorf("per-call summary did not override config full; raw=%s", raw)
		}
	})
	t.Run("config=summary, call=full", func(t *testing.T) {
		a := projAdapter(t)
		a.DefaultPayloadMode = memory.PayloadModeSummary
		if raw := recallRaw(t, a, recallArgs("full")); !strings.Contains(raw, projTestBody) {
			t.Errorf("per-call full did not override config summary; raw=%s", raw)
		}
	})
	t.Run("config=full, no call arg", func(t *testing.T) {
		a := projAdapter(t)
		a.DefaultPayloadMode = memory.PayloadModeFull
		if raw := recallRaw(t, a, recallArgs("")); !strings.Contains(raw, projTestBody) {
			t.Errorf("config default full not honored; raw=%s", raw)
		}
	})
}

// An unset adapter default falls back to memory.DefaultPayloadMode rather
// than to full — a deployment that never wires config must not silently get
// the unprojected firehose.
func TestPayloadMode_UnsetAdapterDefaultIsNotFull(t *testing.T) {
	a := projAdapter(t)
	a.DefaultPayloadMode = ""
	if raw := recallRaw(t, a, recallArgs("")); strings.Contains(raw, projTestBody) {
		t.Errorf("unset adapter default served full; raw=%s", raw)
	}
}

// The config default literal and the memory-package constant are two
// separate literals by design (config stays dependency-free). This binds
// them so they cannot drift.
func TestPayloadMode_ConfigDefaultMatchesMemoryDefault(t *testing.T) {
	if got := config.Defaults().Read.PayloadMode; got != string(memory.DefaultPayloadMode) {
		t.Errorf("config.Defaults().Read.PayloadMode = %q, want %q (memory.DefaultPayloadMode)",
			got, memory.DefaultPayloadMode)
	}
}

// An unrecognized per-call value is a validation_error, not a silent
// fallback: quietly serving a different projection than the one requested is
// the failure this contract exists to prevent.
func TestPayloadMode_InvalidArgumentIsValidationError(t *testing.T) {
	a := projAdapter(t)
	raw := recallRaw(t, a, recallArgs("brief"))
	if !strings.Contains(raw, "validation_error") {
		t.Errorf("invalid payload_mode did not produce validation_error; raw=%s", raw)
	}
	if !strings.Contains(raw, "keys|summary|full") {
		t.Errorf("validation_error does not state the accepted vocabulary; raw=%s", raw)
	}
}

// ── tesseract_lookup carries the same knob with the same semantics ───────

func TestPayloadMode_LookupProjectsAndKeepsFacets(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		wantBody bool
	}{
		{"keys", false},
		{"summary", false},
		{"full", true},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			a := projAdapter(t)
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"namespaces":   `["user/chrispian/memory/notes"]`,
				"ranking":      "activation",
				"payload_mode": tc.mode,
			}
			res, err := a.handleTesseractLookup(context.Background(), req)
			if err != nil {
				t.Fatalf("handleTesseractLookup: %v", err)
			}
			raw := res.Content[0].(mcp.TextContent).Text

			if got := strings.Contains(raw, projTestBody); got != tc.wantBody {
				t.Errorf("body present = %v, want %v; raw=%s", got, tc.wantBody, raw)
			}

			// Facets are computed over the unprojected match set, so they
			// must be identical in every mode.
			var envelope struct {
				Facets map[string]map[string]int `json:"facets"`
			}
			if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if envelope.Facets["domains"]["memory"] != 1 {
				t.Errorf("facets.domains.memory = %d, want 1 under mode=%s",
					envelope.Facets["domains"]["memory"], tc.mode)
			}
		})
	}
}

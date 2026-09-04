package mcpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

func seedDeprecatedCurrentParity(t *testing.T, store *memory.Store) (terminal, superseded, replacement, active string) {
	t.Helper()
	ctx := context.Background()
	write := func(key string) memory.Revision {
		t.Helper()
		rev, err := store.WriteRevision(ctx, memory.WriteInput{
			Namespace:  "user/chrispian/memory/notes",
			MemoryKey:  key,
			Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
			Trigger:    memory.TriggerExplicit,
			SessionID:  "deprecated-current-parity",
			Origin:     memory.OriginUser,
			Confidence: 0.9,
			Status:     memory.StatusDraft,
			Payload:    memory.Payload{Summary: "deprecated current parity probe"},
		})
		if err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
		return rev
	}

	terminalRev := write("deprecated.parity.terminal")
	if err := store.Deprecate(ctx, terminalRev.RevisionID); err != nil {
		t.Fatalf("deprecate terminal: %v", err)
	}

	supersededRev := write("deprecated.parity.superseded")
	replacementIn := memory.WriteInput{
		Namespace:  "user/chrispian/memory/notes",
		MemoryKey:  "deprecated.parity.superseded",
		Supersedes: supersededRev.RevisionID,
		Author:     memory.Author{AgentID: "test", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "deprecated-current-parity",
		Origin:     memory.OriginUser,
		Confidence: 0.9,
		Status:     memory.StatusDraft,
		Payload:    memory.Payload{Summary: "deprecated current parity probe replacement"},
	}
	replacementRev, err := store.WriteRevision(ctx, replacementIn)
	if err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	activeRev := write("deprecated.parity.active")

	return terminalRev.RevisionID, supersededRev.RevisionID, replacementRev.RevisionID, activeRev.RevisionID
}

func deprecatedParityResultIDs(t *testing.T, raw string) map[string]bool {
	t.Helper()
	var envelope struct {
		Results []struct {
			Revision struct {
				RevisionID string `json:"revision_id"`
			} `json:"revision"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode recall envelope: %v\nraw=%s", err, raw)
	}
	ids := make(map[string]bool, len(envelope.Results))
	for _, result := range envelope.Results {
		ids[result.Revision.RevisionID] = true
	}
	return ids
}

func TestDeprecatedCurrent_MCPAndHTTPContracts(t *testing.T) {
	adapter, server := bothSurfaces(t, 0)
	terminal, superseded, replacement, active := seedDeprecatedCurrentParity(t, adapter.MemoryStore)
	const namespaceJSON = `["user/chrispian/memory/notes"]`

	mcpCall := func(t *testing.T, explicitDeprecated bool) string {
		t.Helper()
		args := map[string]any{
			"namespaces":   namespaceJSON,
			"ranking":      "chronological",
			"payload_mode": "keys",
		}
		if explicitDeprecated {
			args["statuses"] = `["deprecated"]`
		}
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		result, err := adapter.handleTesseractRecall(context.Background(), req)
		if err != nil {
			t.Fatalf("MCP recall: %v", err)
		}
		return result.Content[0].(mcp.TextContent).Text
	}
	httpCall := func(path string) func(*testing.T, bool) string {
		return func(t *testing.T, explicitDeprecated bool) string {
			t.Helper()
			body := `{"namespaces":` + namespaceJSON + `,"ranking":"chronological","payload_mode":"keys"`
			if explicitDeprecated {
				if path == "/v1/memory/recall" {
					body += `,"filters":{"statuses":["deprecated"]}`
				} else {
					body += `,"statuses":["deprecated"]`
				}
			}
			body += `}`
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
			}
			return recorder.Body.String()
		}
	}

	doors := []struct {
		name string
		call func(*testing.T, bool) string
	}{
		{name: "MCP tesseract_recall", call: mcpCall},
		{name: "HTTP memory recall", call: httpCall("/v1/memory/recall")},
		{name: "HTTP cross-domain recall", call: httpCall("/v1/tesseract/lookup")},
	}
	for _, door := range doors {
		t.Run(door.name, func(t *testing.T) {
			explicit := deprecatedParityResultIDs(t, door.call(t, true))
			if len(explicit) != 1 || !explicit[terminal] {
				t.Fatalf("explicit current deprecated ids=%v, want only terminal %s", explicit, terminal)
			}
			if explicit[superseded] {
				t.Fatalf("explicit current deprecated exposed superseded predecessor %s", superseded)
			}

			defaults := deprecatedParityResultIDs(t, door.call(t, false))
			if len(defaults) != 2 || !defaults[replacement] || !defaults[active] {
				t.Fatalf("default current ids=%v, want replacement %s and active %s",
					defaults, replacement, active)
			}
			if defaults[terminal] || defaults[superseded] {
				t.Fatalf("default current recall exposed deprecated ids: %v", defaults)
			}
		})
	}
}

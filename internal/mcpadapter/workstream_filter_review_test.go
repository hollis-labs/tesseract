package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func TestMCPWorkstreamReadFilterRejectsMalformedValuesWithoutAResultEnvelope(t *testing.T) {
	a := referenceAdapter(t, []string{"*"})
	for _, domain := range []domains.Domain{domains.Memory, domains.Event} {
		namespace := "project/tesseract/" + string(domain) + "/notes"
		if domain == domains.Event {
			namespace = "project/tesseract/event/reasoning"
		}
		for _, workstreamID := range []string{"ws-a", "ws-b"} {
			if _, err := a.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
				Domain: domain, Namespace: namespace, MemoryKey: "filter." + strings.ReplaceAll(workstreamID, "-", "_"),
				WorkstreamID: &workstreamID, Summary: "filter " + workstreamID,
				Author: memory.Author{AgentID: "test"}, SessionID: "test",
				Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	for tool, namespace := range map[string]string{
		"tesseract_recall": "project/tesseract/memory/notes",
		"event_list":       "project/tesseract/event/reasoning",
	} {
		t.Run(tool+"/positive-and-omitted", func(t *testing.T) {
			filtered := wantNoError(t, mustCallRegistered(t, a, tool, map[string]any{
				"namespaces": []string{namespace}, "workstream_id": "ws-a",
			}))
			unfiltered := wantNoError(t, mustCallRegistered(t, a, tool, map[string]any{
				"namespaces": []string{namespace},
			}))
			if tool == "tesseract_recall" {
				if filtered["manifest"].(map[string]any)["results_total"] != float64(1) ||
					unfiltered["manifest"].(map[string]any)["results_total"] != float64(2) {
					t.Fatalf("filtered=%v unfiltered=%v", filtered, unfiltered)
				}
			} else if filtered["manifest"].(map[string]any)["returned"] != float64(1) ||
				unfiltered["manifest"].(map[string]any)["returned"] != float64(2) {
				t.Fatalf("filtered=%v unfiltered=%v", filtered, unfiltered)
			}
		})

		for name, value := range map[string]any{
			"null": nil, "boolean": true, "number": 7,
			"object": map[string]any{"value": "ws-a"}, "array": []string{"ws-a"},
			"empty": "", "padded": " ws-a",
		} {
			t.Run(tool+"/"+name, func(t *testing.T) {
				body := mustCallRegistered(t, a, tool, map[string]any{
					"namespaces": []string{namespace}, "workstream_id": value,
					"payload_mode": "keys",
				})
				wantErrorCode(t, body, "validation_error")
				for _, forbidden := range []string{"results", "entries", "manifest", "facets", "cursor"} {
					if _, ok := body[forbidden]; ok {
						t.Errorf("invalid filter returned %s: %v", forbidden, body)
					}
				}
			})
		}
	}
}

func TestMCPWorkspaceOnlyValidationUsesTheRecallValidationError(t *testing.T) {
	a := referenceAdapter(t, []string{"*"})
	namespace := "project/tesseract/workspace/workstream-validation"
	if _, err := a.WorkspaceStore.Create(context.Background(), workspace.CreateInput{
		Namespace: namespace, Key: "item", Summary: "workspace validation",
		Author: memory.Author{AgentID: "test"}, SessionID: "test",
	}); err != nil {
		t.Fatal(err)
	}
	body := mustCallRegistered(t, a, "tesseract_recall", map[string]any{
		"namespaces": []string{namespace}, "domains": []string{"workspace"},
		"state_filters": []map[string]any{{"field": "not-valid", "values": []any{"x"}}},
	})
	wantErrorCode(t, body, "validation_error")
}

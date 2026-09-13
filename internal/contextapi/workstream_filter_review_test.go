package contextapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/workspace"
)

func seedHTTPWorkstreamFilters(t *testing.T, srv *Server) (string, string) {
	t.Helper()
	ctx := context.Background()
	memoryNamespace := "project/tesseract/memory/notes"
	workspaceNamespace := "project/tesseract/workspace/workstream-filter"
	for _, workstreamID := range []string{"ws-a", "ws-b"} {
		if _, err := srv.MemoryStore.WriteRevision(ctx, memory.WriteInput{
			Domain: domains.Memory, Namespace: memoryNamespace, MemoryKey: "memory." + strings.ReplaceAll(workstreamID, "-", "_"),
			WorkstreamID: &workstreamID, Summary: "memory " + workstreamID,
			Author: memory.Author{AgentID: "test"}, SessionID: "test",
			Trigger: memory.TriggerManual, DerivedFrom: memory.DerivedFromProject,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.WorkspaceStore.Create(ctx, workspace.CreateInput{
			Namespace: workspaceNamespace, Key: "workspace." + workstreamID,
			WorkstreamID: &workstreamID, Summary: "workspace " + workstreamID,
			Author: memory.Author{AgentID: "test"}, SessionID: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return memoryNamespace, workspaceNamespace
}

func TestHTTPWorkstreamReadFilterRejectsMalformedValuesWithoutAResultEnvelope(t *testing.T) {
	srv := referenceHTTPServer(t)
	memoryNamespace, _ := seedHTTPWorkstreamFilters(t, srv)
	for _, route := range []string{"/v1/memory/recall", "/v1/tesseract/lookup"} {
		for name, value := range map[string]any{
			"null": nil, "boolean": true, "number": 7,
			"object": map[string]any{"value": "ws-a"}, "array": []string{"ws-a"},
			"empty": "", "padded": " ws-a",
		} {
			t.Run(route+"/"+name, func(t *testing.T) {
				body := map[string]any{
					"namespaces": []string{memoryNamespace}, "workstream_id": value,
					"payload_mode": "keys", "estimate_only": true,
				}
				rr := performJSON(t, srv, http.MethodPost, route, body)
				decoded := decodeHTTPJSON(t, rr.Body.Bytes())
				if rr.Code != http.StatusBadRequest || decoded["code"] != "validation_error" {
					t.Fatalf("status=%d body=%v", rr.Code, decoded)
				}
				for _, forbidden := range []string{"results", "manifest", "facets", "cursor"} {
					if _, ok := decoded[forbidden]; ok {
						t.Errorf("invalid filter returned %s: %v", forbidden, decoded)
					}
				}
			})
		}
	}
}

func TestHTTPMemoryRecallRejectsNestedWorkstreamFilterWithGuidance(t *testing.T) {
	srv := referenceHTTPServer(t)
	memoryNamespace, _ := seedHTTPWorkstreamFilters(t, srv)
	for _, topLevel := range []any{nil, "ws-b"} {
		body := map[string]any{
			"namespaces": []string{memoryNamespace},
			"filters":    map[string]any{"workstream_id": "ws-a"},
		}
		if topLevel != nil {
			body["workstream_id"] = topLevel
		}
		rr := performJSON(t, srv, http.MethodPost, "/v1/memory/recall", body)
		decoded := decodeHTTPJSON(t, rr.Body.Bytes())
		message, _ := decoded["message"].(string)
		if rr.Code != http.StatusBadRequest || decoded["code"] != "validation_error" ||
			!strings.Contains(message, "top-level workstream_id") {
			t.Fatalf("status=%d body=%v", rr.Code, decoded)
		}
	}
}

func TestHTTPWorkstreamFilterPreservesMixedDomainProjectionEstimateAndOmission(t *testing.T) {
	srv := referenceHTTPServer(t)
	memoryNamespace, workspaceNamespace := seedHTTPWorkstreamFilters(t, srv)
	for _, route := range []string{"/v1/memory/recall", "/v1/tesseract/lookup"} {
		base := map[string]any{
			"namespaces":    []string{memoryNamespace, workspaceNamespace},
			"workstream_id": "ws-a", "ranking": "chronological", "payload_mode": "keys",
		}
		if route == "/v1/memory/recall" {
			base["filters"] = map[string]any{"Domains": []string{"memory", "workspace"}}
		} else {
			base["domains"] = []string{"memory", "workspace"}
		}

		t.Run(route+"/filtered-keys", func(t *testing.T) {
			rr := performJSON(t, srv, http.MethodPost, route, base)
			body := decodeHTTPJSON(t, rr.Body.Bytes())
			results, _ := body["results"].([]any)
			manifest, _ := body["manifest"].(map[string]any)
			if rr.Code != http.StatusOK || len(results) != 2 || manifest["results_total"] != float64(2) {
				t.Fatalf("status=%d body=%v", rr.Code, body)
			}
			if strings.Contains(fmt.Sprint(results), "ws-b") {
				t.Fatalf("filter admitted ws-b: %v", results)
			}
		})

		t.Run(route+"/estimate", func(t *testing.T) {
			request := cloneMap(base)
			request["estimate_only"] = true
			rr := performJSON(t, srv, http.MethodPost, route, request)
			body := decodeHTTPJSON(t, rr.Body.Bytes())
			manifest, _ := body["manifest"].(map[string]any)
			if rr.Code != http.StatusOK || body["estimate_only"] != true || manifest["results_total"] != float64(2) {
				t.Fatalf("status=%d body=%v", rr.Code, body)
			}
			if _, ok := body["results"]; ok {
				t.Fatalf("estimate returned results: %v", body)
			}
		})

		t.Run(route+"/omitted", func(t *testing.T) {
			request := cloneMap(base)
			delete(request, "workstream_id")
			rr := performJSON(t, srv, http.MethodPost, route, request)
			body := decodeHTTPJSON(t, rr.Body.Bytes())
			manifest, _ := body["manifest"].(map[string]any)
			if rr.Code != http.StatusOK || manifest["results_total"] != float64(4) {
				t.Fatalf("status=%d body=%v", rr.Code, body)
			}
		})
	}
}

func TestHTTPWorkspaceValidationAndGETWorkstreamFiltersStayValidationErrors(t *testing.T) {
	srv := referenceHTTPServer(t)
	workspaceNamespace := "project/tesseract/workspace/workstream-validation"
	for _, route := range []string{"/v1/memory/recall", "/v1/tesseract/lookup"} {
		body := map[string]any{
			"namespaces":    []string{workspaceNamespace},
			"state_filters": []map[string]any{{"field": "not-valid", "values": []any{"x"}}},
		}
		if route == "/v1/memory/recall" {
			body["filters"] = map[string]any{
				"Domains":      []string{"workspace"},
				"StateFilters": []map[string]any{{"field": "not-valid", "values": []any{"x"}}},
			}
			delete(body, "state_filters")
		} else {
			body["domains"] = []string{"workspace"}
		}
		rr := performJSON(t, srv, http.MethodPost, route, body)
		decoded := decodeHTTPJSON(t, rr.Body.Bytes())
		if rr.Code != http.StatusBadRequest || decoded["code"] != "validation_error" {
			t.Fatalf("%s status=%d body=%v", route, rr.Code, decoded)
		}
	}

	for _, path := range []string{
		"/v1/recall?namespace=" + url.QueryEscape(workspaceNamespace) + "&domains=workspace&workstream_id=" + url.QueryEscape(" ws-a"),
		"/v1/event/log?namespace=" + url.QueryEscape("project/tesseract/event/reasoning") + "&workstream_id=",
	} {
		rr := performJSON(t, srv, http.MethodGet, path, nil)
		decoded := decodeHTTPJSON(t, rr.Body.Bytes())
		if rr.Code != http.StatusBadRequest || decoded["code"] != "validation_error" {
			t.Fatalf("%s status=%d body=%v", path, rr.Code, decoded)
		}
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

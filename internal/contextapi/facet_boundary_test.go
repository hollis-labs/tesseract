package contextapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPWriteEndpointsEnforceDomainFacetContract(t *testing.T) {
	validMemory := `{
		"namespace":"user/chrispian/memory/notes",
		"memory_key":"facet.http",
		"author":{"agent_id":"test"},
		"trigger":"explicit",
		"session_id":"s1",
		"origin":"user",
		"confidence":0.9,
		"payload":{"summary":"valid memory"}
	}`
	validKnowledge := `{
		"namespace":"user/chrispian/knowledge/docs",
		"key":"facet-http",
		"kind":"doc",
		"source":"filesystem",
		"pointer":{"scheme":"file","locator":"/tmp/doc"},
		"summary":"valid knowledge",
		"author":{"agent_id":"test"},
		"session_id":"s1"
	}`
	tests := []struct {
		name, path, body, wantCode string
		wantStatus                 int
	}{
		{"valid memory", "/v1/memory/write", validMemory, "", http.StatusOK},
		{"memory rejects facet kind", "/v1/memory/write", bytesReplace(validMemory, `"payload":`, `"facets":{"kind":"note"},"payload":`), "validation_error", http.StatusBadRequest},
		{"memory rejects pointer object", "/v1/memory/write", bytesReplace(validMemory, `"payload":`, `"facets":{"pointer":{"scheme":"nil","locator":"inline"}},"payload":`), "validation_error", http.StatusBadRequest},
		{"valid knowledge", "/v1/knowledge/write", validKnowledge, "", http.StatusOK},
		{"knowledge rejects unknown kind", "/v1/knowledge/write", bytesReplace(validKnowledge, `"kind":"doc"`, `"kind":"mcp-server"`), "validation_error", http.StatusBadRequest},
		{"knowledge rejects missing source", "/v1/knowledge/write", bytesReplace(validKnowledge, `"source":"filesystem"`, `"source":""`), "validation_error", http.StatusBadRequest},
		{"knowledge rejects missing pointer scheme", "/v1/knowledge/write", bytesReplace(validKnowledge, `"scheme":"file"`, `"scheme":""`), "validation_error", http.StatusBadRequest},
		{"knowledge rejects missing pointer locator", "/v1/knowledge/write", bytesReplace(validKnowledge, `"locator":"/tmp/doc"`, `"locator":""`), "validation_error", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newKnowledgeTestServer(t)
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantCode == "" {
				var rev struct {
					RevisionID string `json:"revision_id"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil || rev.RevisionID == "" {
					t.Fatalf("valid control returned no revision: body=%s err=%v", rr.Body.String(), err)
				}
				return
			}
			var failure struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &failure); err != nil {
				t.Fatalf("decode failure: %v; body=%s", err, rr.Body.String())
			}
			if failure.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", failure.Code, tc.wantCode)
			}
			var count int
			if err := srv.MemoryStore.DB().QueryRow(`SELECT COUNT(*) FROM memory_revisions`).Scan(&count); err != nil {
				t.Fatalf("count revisions: %v", err)
			}
			if count != 0 {
				t.Fatalf("rejected request persisted %d revisions", count)
			}
		})
	}
}

func bytesReplace(body, old, replacement string) string {
	return string(bytes.Replace([]byte(body), []byte(old), []byte(replacement), 1))
}

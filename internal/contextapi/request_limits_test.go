package contextapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestRequestBodyLimitRejectsOversizedJSON checks the cap is enforced and that
// the caller is told what happened. An over-limit body must not surface as an
// opaque JSON parse failure.
func TestRequestBodyLimitRejectsOversizedJSON(t *testing.T) {
	srv := newTestServer(t)

	// A syntactically valid document that runs past the cap: the decoder
	// cannot finish it without reading beyond maxRequestBodyBytes.
	var buf bytes.Buffer
	buf.WriteString(`{"client_id":"editor","actor":"app:editor","namespace":"app/editor/session","key":"summary","payload":{"blob":"`)
	buf.Write(bytes.Repeat([]byte("a"), maxRequestBodyBytes+1024))
	buf.WriteString(`"}}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/context/write", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest && res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body got status %d, want 400 or 413; body=%s", res.Code, res.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal error envelope: %v (body=%s)", err, res.Body.String())
	}
	if envelope["code"] != "validation_error" {
		t.Fatalf("expected code=validation_error, got %v", envelope["code"])
	}
	message, _ := envelope["message"].(string)
	if !strings.Contains(message, strconv.Itoa(maxRequestBodyBytes)) {
		t.Fatalf("error message should name the %d byte limit, got %q", maxRequestBodyBytes, message)
	}
	if strings.Contains(message, "unexpected EOF") || strings.Contains(message, "invalid character") {
		t.Fatalf("error message leaked a raw JSON parse failure: %q", message)
	}
}

// TestRequestBodyLimitAppliesToDirectDecoders covers the handlers that decode
// r.Body themselves instead of going through decodeJSON. ServeHTTP wraps the
// body for every route, so they are capped too — they just report it in their
// own words.
func TestRequestBodyLimitAppliesToDirectDecoders(t *testing.T) {
	srv := newMemoryTestServer(t)

	var buf bytes.Buffer
	buf.WriteString(`{"namespace":"app/editor/session","memory_key":"summary","payload":{"summary":"`)
	buf.Write(bytes.Repeat([]byte("a"), maxRequestBodyBytes+1024))
	buf.WriteString(`"}}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)

	if res.Code == http.StatusOK {
		t.Fatalf("oversized body was accepted: %s", res.Body.String())
	}
	if res.Code >= http.StatusInternalServerError {
		t.Fatalf("oversized body should be a client error, got %d: %s", res.Code, res.Body.String())
	}
}

// TestRequestBodyLimitAllowsRealisticPayloads guards against a cap set so
// tight it breaks the largest legitimate request: a full 100-item bulk ingest.
func TestRequestBodyLimitAllowsRealisticPayloads(t *testing.T) {
	srv := newTestServer(t)

	items := make([]map[string]any, 0, 100)
	for i := range 100 {
		items = append(items, map[string]any{
			"namespace": "app/editor/session",
			"key":       fmt.Sprintf("bulk-%03d", i),
			"actor":     "app:editor",
			"payload":   map[string]any{"note": strings.Repeat("x", 4096)},
		})
	}
	body := map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"items":     items,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal bulk body: %v", err)
	}
	if len(raw) >= maxRequestBodyBytes {
		t.Fatalf("test payload (%d bytes) is not below the cap (%d)", len(raw), maxRequestBodyBytes)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/context/bulk-ingest", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("a 100-item bulk ingest under the cap was rejected: %d %s", res.Code, res.Body.String())
	}
}

// TestNormalRequestsUnaffectedByBodyLimit is the regression guard: ordinary
// payloads must decode exactly as before the cap existed.
func TestNormalRequestsUnaffectedByBodyLimit(t *testing.T) {
	srv := newTestServer(t)

	write := performJSON(t, srv, http.MethodPost, "/v1/context/write", map[string]any{
		"client_id": "editor",
		"actor":     "app:editor",
		"namespace": "app/editor/session",
		"key":       "summary",
		"payload":   map[string]any{"note": strings.Repeat("y", 64*1024)},
	})
	if write.Code != http.StatusOK {
		t.Fatalf("64KiB payload rejected: %d %s", write.Code, write.Body.String())
	}
	head := performJSON(t, srv, http.MethodGet, "/v1/context/head?namespace=app/editor/session&key=summary", nil)
	if head.Code != http.StatusOK {
		t.Fatalf("head after write: %d %s", head.Code, head.Body.String())
	}
}

// TestMaxRequestBodyBytesHeadroom pins the constant's intent rather than its
// exact value: it has to clear a full bulk-ingest batch with room to spare.
func TestMaxRequestBodyBytesHeadroom(t *testing.T) {
	const bulkIngestWorstCase = 100 * 64 * 1024 // 100 items, 64KiB each
	if maxRequestBodyBytes <= bulkIngestWorstCase {
		t.Fatalf("maxRequestBodyBytes=%d leaves no room for a 100-item bulk ingest (%d)",
			maxRequestBodyBytes, bulkIngestWorstCase)
	}
}

package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesIndexHTML(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Tesseract") {
		t.Errorf("index.html should contain 'Tesseract', got:\n%s", body[:min(len(body), 200)])
	}
}

func TestHandler_SPAFallback(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/explorer", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Tesseract") {
		t.Errorf("SPA fallback should serve index.html")
	}
}

func TestHandler_ServesStaticAssets(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/assets/nonexistent.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Non-existent asset should fall back to index.html (SPA pattern)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback on missing asset, got %d", w.Code)
	}
}

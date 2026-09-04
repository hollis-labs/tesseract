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
	// The bundle's own asset names are content-hashed, so read the one
	// index.html actually references rather than hard-coding a hash that
	// every frontend rebuild would invalidate.
	asset := assetPathFromIndex(t)

	h := Handler()
	req := httptest.NewRequest("GET", asset, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for embedded asset %s, got %d", asset, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("asset %s should be served as JavaScript, got Content-Type %q", asset, ct)
	}
}

// TestHandler_MissingAssetIs404 pins the one routing behaviour that changed
// when this package moved onto go-webui (CW-20260515-0137). The hand-rolled
// handler answered *every* unmatched path with index.html, so a stale or
// mistyped bundle URL returned HTML under a .js name with a 200 — which the
// browser reports as "Unexpected token '<'" rather than as the missing file
// it is. go-webui splits the two cases: an extension-less path is a client
// route and still falls back to index.html (TestHandler_SPAFallback), while
// a path with an extension is an asset request and a miss is a 404.
func TestHandler_MissingAssetIs404(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/assets/nonexistent.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a missing asset, got %d", w.Code)
	}
}

// assetPathFromIndex pulls the first /assets/... URL out of the embedded
// index.html.
func assetPathFromIndex(t *testing.T) string {
	t.Helper()

	index, err := distFS.ReadFile("dist/index.html")
	if err != nil {
		t.Fatalf("reading embedded index.html: %v", err)
	}
	_, rest, found := strings.Cut(string(index), `src="/assets/`)
	if !found {
		t.Fatalf("embedded index.html references no /assets/ script:\n%s", index)
	}
	name, _, found := strings.Cut(rest, `"`)
	if !found {
		t.Fatalf("embedded index.html has an unterminated asset src")
	}
	return "/assets/" + name
}

package contextapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func newMemoryTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := NewServer(cs, contextpolicy.New())
	srv.MemoryStore = memory.NewStore(cs.DB(), nil, "", 0, memory.NoopQueue{})
	return srv
}

func TestMemoryWrite_ReturnsRevisionWithDomain(t *testing.T) {
	srv := newMemoryTestServer(t)

	body := `{
		"namespace":"user/chrispian/memory",
		"memory_key":"prefs.output_style",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"status":"draft",
		"payload":{"summary":"terse output"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var rev memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rev.RevisionID == "" {
		t.Error("empty revision_id")
	}
	if rev.Domain != "memory" {
		t.Errorf("domain = %q, want memory", rev.Domain)
	}
}

func TestMemoryWrite_NoStoreReturns503(t *testing.T) {
	root := t.TempDir()
	cs, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	srv := NewServer(cs, contextpolicy.New())
	// No MemoryStore wired.

	req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(`{"namespace":"user/x/memory"}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMemoryHistory_RoundtripViaHTTP(t *testing.T) {
	srv := newMemoryTestServer(t)

	write := func(body string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/memory/write", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("write status = %d; body=%s", rr.Code, rr.Body.String())
		}
	}
	base := `{
		"namespace":"user/chrispian/memory",
		"memory_key":"prefs.history_test",
		"author":{"agent_id":"test","agent_version":"1.0"},
		"trigger":"explicit",
		"session_id":"manual:01HX",
		"origin":"user",
		"confidence":0.9,
		"status":"draft",
		"payload":{"summary":"v%d"}
	}`
	for i := 1; i <= 2; i++ {
		write(bytesFormat(base, i))
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/memory/history?namespace=user/chrispian/memory&memory_key=prefs.history_test", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("history status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var revs []memory.Revision
	if err := json.Unmarshal(rr.Body.Bytes(), &revs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("history len = %d, want 2", len(revs))
	}
}

// bytesFormat is a minimal fmt.Sprintf stand-in that keeps this test file
// free of the extra import when a single %d substitution is enough.
func bytesFormat(tmpl string, n int) string {
	s := tmpl
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if i+1 < len(s) && s[i] == '%' && s[i+1] == 'd' {
			out = append(out, byte('0'+n))
			i++
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

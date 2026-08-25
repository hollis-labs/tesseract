package memory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
)

// --- file: resolution ---------------------------------------------------

func TestResolveFile_Discriminates(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.md")
	if err := os.WriteFile(present, []byte("hello"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	absent := filepath.Join(dir, "absent.md")

	r := memory.NewPointerResolver(time.Second, false)
	ctx := context.Background()

	// The zero-rule check in test form: a resolver that cannot say "resolved"
	// is not a resolver, and one that cannot say "unresolvable" is a rubber
	// stamp. Both directions are asserted against the same code path.
	if got, detail := r.Resolve(ctx, memory.SchemeFile, present); got != memory.OutcomeResolved {
		t.Errorf("existing file: got %q (%s), want %q", got, detail, memory.OutcomeResolved)
	}
	if got, detail := r.Resolve(ctx, memory.SchemeFile, absent); got != memory.OutcomeUnresolvable {
		t.Errorf("missing file: got %q (%s), want %q", got, detail, memory.OutcomeUnresolvable)
	}
	// A directory resolves — knowledge pointers legitimately name package
	// directories, not only files.
	if got, _ := r.Resolve(ctx, memory.SchemeFile, dir); got != memory.OutcomeResolved {
		t.Errorf("directory: got %q, want %q", got, memory.OutcomeResolved)
	}
	if got, _ := r.Resolve(ctx, memory.SchemeFile, "   "); got != memory.OutcomeUnverifiable {
		t.Errorf("blank locator: got %q, want %q", got, memory.OutcomeUnverifiable)
	}
}

// TestResolveFile_UnreadableParentIsUnverifiableNotDead is the file-side guard
// against the failure this design exists to prevent: a path we cannot look at
// must never be reported as a path that is not there.
//
// An unreadable parent directory stands in for the real-world case — an
// unmounted volume — which would otherwise brand every pointer on it dead in a
// single sweep.
func TestResolveFile_UnreadableParentIsUnverifiableNotDead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission bits do not apply")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(locked, "thing.md")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// 0o700 on the way out, not 0o600: a directory needs the execute bit or
	// t.TempDir cleanup cannot descend into it to remove the file.
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) }) //nolint:gosec // directory, needs +x to be removable

	got, detail := memory.NewPointerResolver(time.Second, false).Resolve(context.Background(), memory.SchemeFile, target)
	if got == memory.OutcomeUnresolvable {
		t.Fatalf("a file behind an unreadable directory was reported dead (detail %q); "+
			"only a definitive negative may produce %q", detail, memory.OutcomeUnresolvable)
	}
	if got != memory.OutcomeUnverifiable {
		t.Errorf("got %q (%s), want %q", got, detail, memory.OutcomeUnverifiable)
	}
}

// --- http: resolution ---------------------------------------------------

// TestResolveHTTP_StatusClassification pins the transient-vs-dead mapping
// case by case.
func TestResolveHTTP_StatusClassification(t *testing.T) {
	cases := []struct {
		status int
		want   memory.PointerOutcome
		why    string
	}{
		{http.StatusOK, memory.OutcomeResolved, "we saw it"},
		{http.StatusNoContent, memory.OutcomeResolved, "2xx is 2xx"},
		{http.StatusUnauthorized, memory.OutcomeUnverifiable, "origin is up and refused us; says nothing about the target"},
		{http.StatusForbidden, memory.OutcomeUnverifiable, "reachable but not to us"},
		{http.StatusNotFound, memory.OutcomeUnresolvable, "an origin that answered told us it is gone"},
		{http.StatusGone, memory.OutcomeUnresolvable, "explicitly gone"},
		{http.StatusRequestTimeout, memory.OutcomeUnverifiable, "transient"},
		{http.StatusTooManyRequests, memory.OutcomeUnverifiable, "rate limited — our problem, not the target's"},
		{http.StatusInternalServerError, memory.OutcomeUnverifiable, "origin is broken"},
		{http.StatusBadGateway, memory.OutcomeUnverifiable, "origin is broken"},
		{http.StatusServiceUnavailable, memory.OutcomeUnverifiable, "origin is down"},
		{http.StatusBadRequest, memory.OutcomeUnverifiable, "malformed request is ours to fix"},
	}

	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		got, detail := memory.NewPointerResolver(2*time.Second, true).
			Resolve(context.Background(), memory.SchemeHTTP, srv.URL)
		srv.Close()
		if got != tc.want {
			t.Errorf("status %d: got %q (%s), want %q — %s", tc.status, got, detail, tc.want, tc.why)
		}
	}
}

// TestResolveHTTP_OnlyDefinitiveNegativesAreDead sweeps every 4xx/5xx status
// and asserts that exactly two of them produce `unresolvable`.
//
// This is the guard against the most tempting simplification in the file —
// collapsing the switch to "not 2xx means dead". That change passes a
// hand-written table of a dozen cases if the table happens to omit the right
// code; it cannot pass this.
func TestResolveHTTP_OnlyDefinitiveNegativesAreDead(t *testing.T) {
	definitive := map[int]bool{http.StatusNotFound: true, http.StatusGone: true}

	for status := 400; status <= 599; status++ {
		// 405/501 trigger the documented GET retry; the retry hits the same
		// handler and returns the same status, so they stay in the sweep.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		got, detail := memory.NewPointerResolver(2*time.Second, true).
			Resolve(context.Background(), memory.SchemeHTTP, srv.URL)
		srv.Close()

		isDead := got == memory.OutcomeUnresolvable
		if isDead != definitive[status] {
			if isDead {
				t.Errorf("status %d produced %q (%s) — only 404 and 410 are definitive negatives",
					status, got, detail)
			} else {
				t.Errorf("status %d produced %q (%s), want %q", status, got, detail, memory.OutcomeUnresolvable)
			}
		}
	}
}

func TestResolveHTTP_HeadRefusedFallsBackToGet(t *testing.T) {
	var sawGet bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		sawGet = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got, detail := memory.NewPointerResolver(2*time.Second, true).
		Resolve(context.Background(), memory.SchemeHTTPS, srv.URL)
	if !sawGet {
		t.Error("HEAD returned 405 but no GET retry was attempted")
	}
	if got != memory.OutcomeResolved {
		t.Errorf("got %q (%s), want %q — a host that refuses HEAD still serves the resource",
			got, detail, memory.OutcomeResolved)
	}
}

func TestResolveHTTP_RedirectToLiveTargetResolves(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusMovedPermanently)
	}))
	defer redir.Close()

	if got, detail := memory.NewPointerResolver(2*time.Second, true).
		Resolve(context.Background(), memory.SchemeHTTP, redir.URL); got != memory.OutcomeResolved {
		t.Errorf("got %q (%s), want %q", got, detail, memory.OutcomeResolved)
	}
}

func TestResolveHTTP_RedirectLoopIsUnverifiable(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	defer srv.Close()

	got, detail := memory.NewPointerResolver(2*time.Second, true).
		Resolve(context.Background(), memory.SchemeHTTP, srv.URL)
	if got != memory.OutcomeUnverifiable {
		t.Errorf("redirect loop: got %q (%s), want %q — a loop is the server's problem, not evidence of absence",
			got, detail, memory.OutcomeUnverifiable)
	}
}

func TestResolveHTTP_TimeoutIsUnverifiable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer func() { close(release); srv.Close() }()

	got, detail := memory.NewPointerResolver(50*time.Millisecond, true).
		Resolve(context.Background(), memory.SchemeHTTP, srv.URL)
	if got != memory.OutcomeUnverifiable {
		t.Fatalf("timeout: got %q (%s), want %q", got, detail, memory.OutcomeUnverifiable)
	}
	if detail != "timeout" {
		t.Errorf("timeout detail = %q, want \"timeout\" — the detail is what separates one silence from another", detail)
	}
}

func TestResolveHTTP_ConnectionRefusedIsUnverifiable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := srv.URL
	srv.Close() // nothing is listening now

	got, detail := memory.NewPointerResolver(time.Second, true).
		Resolve(context.Background(), memory.SchemeHTTP, dead)
	if got != memory.OutcomeUnresolvable {
		// Expected path. Assert it is unverifiable, not dead.
		if got != memory.OutcomeUnverifiable {
			t.Errorf("got %q (%s), want %q", got, detail, memory.OutcomeUnverifiable)
		}
		return
	}
	t.Fatalf("a refused connection was reported dead (detail %q); silence is not a negative answer", detail)
}

// --- scheme dispatch ----------------------------------------------------

func TestResolve_UnsupportedSchemeIsUnverifiableAndNamed(t *testing.T) {
	r := memory.NewPointerResolver(time.Second, true)
	got, detail := r.Resolve(context.Background(), "conduit", "memory://user/x/memory/decisions.y:01ABC")
	if got != memory.OutcomeUnverifiable {
		t.Errorf("got %q, want %q — a scheme we cannot check is not a scheme that failed", got, memory.OutcomeUnverifiable)
	}
	if detail != "unsupported_scheme:conduit" {
		t.Errorf("detail = %q, want it to name the scheme so the corpus can be asked how many such entries exist", detail)
	}
}

func TestResolve_NetworkDisabledIsRecordedNotHidden(t *testing.T) {
	got, detail := memory.NewPointerResolver(time.Second, false).
		Resolve(context.Background(), memory.SchemeHTTPS, "https://example.invalid/thing")
	if got != memory.OutcomeUnverifiable || detail != "network_disabled" {
		t.Errorf("got %q/%q, want %q/\"network_disabled\" — a pointer nobody fetched must say so, not vanish",
			got, detail, memory.OutcomeUnverifiable)
	}
}

package authn

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
)

// CW-20261020-0004: auth.mode, auth.jwt and auth.deny_list reload while the
// daemon serves, and a bad reload changes nothing.

func anonymousStatus(s served) int {
	return s.do("GET", "/v1/namespaces/list", "", nil).Code
}

func TestReloadRotatesTheIssuerKeyWithoutARestart(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t) // a second issuer key, same issuer name
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, nil)
	oldTok := f.jwt("tesseract.read", nil)
	newTok := other.jwt("tesseract.read", nil)
	wantStatus(t, s.do("GET", "/v1/namespaces/list", oldTok, nil), 200, "token from the original key")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", newTok, nil), 401, "token from the other key before the reload")

	if err := s.rt.Reload(config.AuthConfig{Mode: "enforce", JWT: other.jwtConfig()}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", newTok, nil), 200, "token from the new key after the reload")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", oldTok, nil), 401, "token from the replaced key after the reload")
	if !strings.Contains(s.out.String(), "Auth reloaded") {
		t.Fatalf("reload was not announced:\n%s", s.out.String())
	}
}

func TestReloadMovesTheModeLive(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "permissive"}, nil)
	if got := anonymousStatus(s); got != 200 {
		t.Fatalf("permissive anonymous = %d", got)
	}
	if err := s.rt.Reload(config.AuthConfig{Mode: "enforce"}); err != nil {
		t.Fatalf("Reload to enforce: %v", err)
	}
	if got := anonymousStatus(s); got != 401 {
		t.Fatalf("enforce anonymous after reload = %d, want 401", got)
	}
	if s.rt.Mode() != "enforce" || s.rt.Source() != SourceConfig {
		t.Fatalf("live mode %q source %q", s.rt.Mode(), s.rt.Source())
	}
	w := s.rt.WhoamiFor(context.Background(), auth.Principal{}, false)
	if w.Mode != "enforce" || w.Auth != "auth=enforce" {
		t.Fatalf("whoami after reload: %+v", w)
	}
	if err := s.rt.Reload(config.AuthConfig{Mode: "permissive"}); err != nil {
		t.Fatalf("Reload back to permissive: %v", err)
	}
	if got := anonymousStatus(s); got != 200 {
		t.Fatalf("permissive anonymous after relaxing = %d", got)
	}
}

func TestReloadNarrowsTheNamespaceCeiling(t *testing.T) {
	f := newFixture(t)
	wide := f.jwtConfig()
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: wide}, nil)
	tok := f.jwt("tesseract.read", nil)
	const other = "/v1/memory/current?namespace=app/other/memory/x&key=k"
	if rec := s.do("GET", other, tok, nil); rec.Code == 403 || rec.Code == 401 {
		t.Fatalf("setup: wide ceiling refused: %d %s", rec.Code, rec.Body.String())
	}
	narrow := wide
	narrow.NamespaceGlobs = []string{"app/allowed/*"}
	if err := s.rt.Reload(config.AuthConfig{Mode: "enforce", JWT: narrow}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	rec := s.do("GET", other, tok, nil)
	if rec.Code != 403 || errCode(t, rec) != "namespace_not_permitted" {
		t.Fatalf("narrowed ceiling not applied: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFailedReloadChangesNothing(t *testing.T) {
	f := newFixture(t)
	good := f.jwtConfig()
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: good}, nil)
	tok := f.jwt("tesseract.read", nil)
	check := func(label string) {
		t.Helper()
		wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 200, label+": existing token")
		if got := anonymousStatus(s); got != 401 {
			t.Fatalf("%s: anonymous = %d, want 401", label, got)
		}
		if s.rt.Mode() != "enforce" {
			t.Fatalf("%s: mode %s", label, s.rt.Mode())
		}
	}
	noGlobs := good
	noGlobs.NamespaceGlobs = nil
	missingKey := good
	missingKey.BootstrapKeyFile = filepath.Join(t.TempDir(), "absent.pem")
	for name, cfg := range map[string]config.AuthConfig{
		"jwt without namespace_globs":  {Mode: "enforce", JWT: noGlobs},
		"unreadable bootstrap key":     {Mode: "enforce", JWT: missingKey},
		"deny-list without a verifier": {Mode: "enforce", DenyList: config.AuthDenyListConfig{URL: "http://127.0.0.1:1/auth/denylist"}},
		"literal deny-list bearer":     {Mode: "enforce", JWT: good, DenyList: config.AuthDenyListConfig{URL: "http://127.0.0.1:1/d", BearerRef: "literal-secret"}},
		"unknown mode":                 {Mode: "enforcing", JWT: good},
	} {
		err := s.rt.Reload(cfg)
		if err == nil {
			t.Fatalf("%s: reload accepted", name)
		}
		if strings.Contains(err.Error(), "literal-secret") {
			t.Fatalf("%s: error leaks the bearer: %v", name, err)
		}
		check(name)
	}
}

func TestReloadCannotRelaxANonLoopbackListener(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, func(o *Options) {
		o.Addr, o.Loopback = "0.0.0.0:8089", false
	})
	tok := f.jwt("tesseract.read", nil)
	err := s.rt.Reload(config.AuthConfig{Mode: "off", JWT: f.jwtConfig()})
	if err == nil {
		t.Fatal("a reload relaxed auth on a non-loopback listener")
	}
	if s.rt.Mode() != "enforce" || anonymousStatus(s) != 401 {
		t.Fatalf("a refused reload changed the mode: %s", s.rt.Mode())
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 200, "existing token after the refused reload")
}

func TestReloadReplacesTheDenyListAndStopsTheOldLoops(t *testing.T) {
	f := newFixture(t)
	var hitsA, hitsB atomic.Int32
	deny := func(n *atomic.Int32, jti string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			_, _ = fmt.Fprintf(w, `{"jti":[%q],"sub":[],"sid":[]}`, jti)
		}))
	}
	a, b := deny(&hitsA, "j-revoked-a"), deny(&hitsB, "j-revoked-b")
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	cfg := func(url string) config.AuthConfig {
		return config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig(), DenyList: config.AuthDenyListConfig{URL: url}}
	}
	s := f.serve(cfg(a.URL), nil)
	tokA := f.jwt("tesseract.read", map[string]any{"jti": "j-revoked-a"})
	tokB := f.jwt("tesseract.read", map[string]any{"jti": "j-revoked-b"})
	waitFor(t, func() bool { return s.do("GET", "/v1/namespaces/list", tokA, nil).Code == 401 }, "deny-list A applied")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tokB, nil), 200, "jti listed only by B before the reload")

	if err := s.rt.Reload(cfg(b.URL)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	waitFor(t, func() bool { return s.do("GET", "/v1/namespaces/list", tokB, nil).Code == 401 }, "deny-list B applied")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tokA, nil), 200, "the old deny-list no longer applies")
	if hitsB.Load() == 0 {
		t.Fatal("the new deny-list was never polled")
	}

	// Each generation's refresh and poll loops end with it: repeated reloads
	// must not accumulate goroutines.
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		if err := s.rt.Reload(cfg(a.URL)); err != nil {
			t.Fatalf("Reload %d: %v", i, err)
		}
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before+8 }, "reload loops to stop")
}

func TestWatchAppliesAConfigEditAndIgnoresABadOne(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("auth:\n  mode: permissive\n")
	s := f.serve(config.AuthConfig{Mode: "permissive"}, quietStderr) // the reloader and the would-deny summary both log
	r := s.rt.Reloader(path, 10*time.Millisecond, nil)
	r.NoSignal = true
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { stop(); <-done }() // the watcher ends before the test does
	time.Sleep(60 * time.Millisecond)

	write("auth:\n  mode: enforce\n")
	waitFor(t, func() bool { return anonymousStatus(s) == 401 }, "the edit to enforce to take effect")

	write("auth: [this is not valid\n")
	time.Sleep(150 * time.Millisecond)
	if s.rt.Mode() != "enforce" || anonymousStatus(s) != 401 {
		t.Fatalf("a malformed config changed the mode to %s", s.rt.Mode())
	}
	write("auth:\n  mode: enforce\n  jwt:\n    issuer: tether-test\n    bootstrap_key_file: " + f.bootstrapFile() + "\n")
	time.Sleep(150 * time.Millisecond)
	if s.rt.Mode() != "enforce" {
		t.Fatalf("an auth section without namespace_globs changed the mode to %s", s.rt.Mode())
	}
	write("auth:\n  mode: permissive\n")
	waitFor(t, func() bool { return anonymousStatus(s) == 200 }, "a valid edit back to permissive")
}

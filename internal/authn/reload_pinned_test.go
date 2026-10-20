package authn

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
)

// Review of #86: a request is judged by ONE auth generation from verification
// to the namespace ceiling, however long it is paused and however many reloads
// land meanwhile. And a reload never forgets a revocation.

func authCfg(f *fixture, globs ...string) config.AuthConfig {
	jc := f.jwtConfig()
	jc.NamespaceGlobs = globs
	return config.AuthConfig{Mode: "enforce", JWT: jc}
}

// pausedClaims admits a request with tok, pauses it inside the handler, runs
// during (typically a Reload), then lets the handler translate the principal
// to claims and returns the ceiling it got.
func pausedClaims(t *testing.T, s served, tok string, during func()) (admitted bool, globs []string) {
	t.Helper()
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := s.rt.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admitted = true
		p, ok := auth.FromContext(r.Context())
		if !ok || !p.Verified {
			t.Error("admitted request carries no verified principal")
			return
		}
		close(paused)
		<-release
		c, has := s.rt.ClaimsFor(r.Context(), p)
		if !has {
			t.Error("no claims for a verified principal")
		}
		globs = c.NamespaceGlobs
	}))
	go func() {
		defer close(done)
		req := httptest.NewRequest("GET", "/v1/namespaces/list", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the request was never admitted")
	}
	during()
	close(release)
	<-done
	return admitted, globs
}

func TestPausedRequestKeepsItsGenerationAcrossReloads(t *testing.T) {
	old := newFixture(t) // key A
	nxt := newFixture(t) // key B
	// Old key + old ceiling; the reload swaps in a different key and user/*.
	s := old.serve(authCfg(old, "app/old/*"), nil)
	tokA := old.jwt("tesseract.read", nil)
	admitted, globs := pausedClaims(t, s, tokA, func() {
		if err := s.rt.Reload(authCfg(nxt, "user/*")); err != nil {
			t.Fatalf("Reload: %v", err)
		}
	})
	if !admitted || !reflect.DeepEqual(globs, []string{"app/old/*"}) {
		t.Fatalf("a request verified by the old key was given %v after a reload (admitted=%v); want [app/old/*]", globs, admitted)
	}
	// The next request sees only the new generation: the old key is gone.
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tokA, nil), 401, "old key after the reload")

	// Reverse: a request admitted by the NEW key keeps the NEW ceiling when the
	// configuration flips back underneath it.
	tokB := nxt.jwt("tesseract.read", nil)
	admitted, globs = pausedClaims(t, s, tokB, func() {
		if err := s.rt.Reload(authCfg(old, "app/old/*")); err != nil {
			t.Fatalf("Reload back: %v", err)
		}
	})
	if !admitted || !reflect.DeepEqual(globs, []string{"user/*"}) {
		t.Fatalf("a request verified by the new key was given %v after the config flipped back (admitted=%v); want [user/*]", globs, admitted)
	}
}

func TestPausedMCPCallKeepsItsModeAcrossReloads(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ from, to string }{{"enforce", "permissive"}, {"permissive", "enforce"}} {
		t.Run(tc.from+" to "+tc.to, func(t *testing.T) {
			s := f.serve(config.AuthConfig{Mode: tc.from}, nil)
			sess := s.rt.NewMCPSession(nil)
			ctx, _ := sess.Authorize(context.Background(), "memory_recall")
			if err := s.rt.Reload(config.AuthConfig{Mode: tc.to}); err != nil {
				t.Fatalf("Reload: %v", err)
			}
			// The namespace decision for a call that began under `from` uses
			// `from`: enforce refuses, permissive lets it through (and logs).
			want := tc.from == "permissive"
			if got := sess.NamespaceDecision(ctx, false); got != want {
				t.Fatalf("a call begun under %s was decided under %s (allowed=%v, want %v)", tc.from, tc.to, got, want)
			}
		})
	}
}

// --- a reload never forgets a revocation ---

type denyServer struct {
	srv    *httptest.Server
	status atomic.Int32
	hits   atomic.Int32
	jti    string
}

func newDenyServer(t *testing.T, jti string) *denyServer {
	d := &denyServer{jti: jti}
	d.status.Store(200)
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		d.hits.Add(1)
		if code := int(d.status.Load()); code != 200 {
			http.Error(w, "down", code)
			return
		}
		_, _ = w.Write([]byte(`{"jti":["` + d.jti + `"],"sub":[],"sid":[]}`))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func denyCfg(f *fixture, url string) config.AuthConfig {
	c := authCfg(f, "*")
	c.DenyList = config.AuthDenyListConfig{URL: url}
	return c
}

func TestReloadDuringADenyListOutageKeepsKnownRevocations(t *testing.T) {
	f := newFixture(t)
	d := newDenyServer(t, "j-revoked")
	cfg := denyCfg(f, d.srv.URL)
	s := f.serve(cfg, quietStderr) // the poller logs outages from its own goroutine
	revoked := f.jwt("tesseract.read", map[string]any{"jti": "j-revoked"})
	fine := f.jwt("tesseract.read", map[string]any{"jti": "j-fine"})
	waitFor(t, func() bool { return s.do("GET", "/v1/namespaces/list", revoked, nil).Code == 401 }, "the revocation to be learned")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", fine, nil), 200, "an unrevoked token")

	d.status.Store(503) // the issuer's deny-list goes down
	for i := 0; i < 3; i++ {
		if err := s.rt.Reload(cfg); err != nil { // an identical config reload (SIGHUP, an edit)
			t.Fatalf("identical reload %d during the outage: %v", i, err)
		}
		wantStatus(t, s.do("GET", "/v1/namespaces/list", revoked, nil), 401, "revoked token after an identical reload during an outage")
		wantStatus(t, s.do("GET", "/v1/namespaces/list", fine, nil), 200, "unrevoked token after the reload")
	}
}

func TestReloadToANewDenyListEndpointNeedsAValidatedFirstFetch(t *testing.T) {
	f := newFixture(t)
	oldD := newDenyServer(t, "j-revoked")
	s := f.serve(denyCfg(f, oldD.srv.URL), quietStderr)
	revoked := f.jwt("tesseract.read", map[string]any{"jti": "j-revoked"})
	waitFor(t, func() bool { return s.do("GET", "/v1/namespaces/list", revoked, nil).Code == 401 }, "the revocation to be learned")

	// A new endpoint that is down: nothing is published, the old list stays.
	down := newDenyServer(t, "j-other")
	down.status.Store(503)
	err := s.rt.Reload(denyCfg(f, down.srv.URL))
	if err == nil {
		t.Fatal("a reload to an unreachable deny-list endpoint was accepted")
	}
	if got := err.Error(); containsAny(got, down.srv.URL, "127.0.0.1") {
		t.Fatalf("the error carries the endpoint URL: %v", err)
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", revoked, nil), 401, "old revocation after the refused reload")

	// A new endpoint that answers: published, with its own list.
	up := newDenyServer(t, "j-other")
	if err := s.rt.Reload(denyCfg(f, up.srv.URL)); err != nil {
		t.Fatalf("reload to a working endpoint: %v", err)
	}
	other := f.jwt("tesseract.read", map[string]any{"jti": "j-other"})
	wantStatus(t, s.do("GET", "/v1/namespaces/list", other, nil), 401, "a jti the new endpoint revokes")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", revoked, nil), 200, "a jti only the replaced endpoint revoked")
}

func TestReloadNeedsAVerifierThatCanVerify(t *testing.T) {
	f := newFixture(t)
	s := f.serve(authCfg(f, "*"), nil)
	tok := f.jwt("tesseract.read", nil)
	// JWKS URL only, unreachable, no bootstrap key: the new generation could not
	// verify anything, so it is refused and the running one is kept.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	jc := config.AuthJWTConfig{Issuer: testIssuer, JWKSURL: dead.URL + "/.well-known/jwks.json", NamespaceGlobs: []string{"*"}}
	err := s.rt.Reload(config.AuthConfig{Mode: "enforce", JWT: jc})
	if err == nil {
		t.Fatal("a reload to a verifier with no keys was accepted")
	}
	if containsAny(err.Error(), dead.URL) {
		t.Fatalf("the error carries the JWKS URL: %v", err)
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 200, "running generation after the refused reload")

	// With a bootstrap key the same unreachable JWKS URL is tolerated.
	jc.BootstrapKeyFile = f.bootstrapFile()
	if err := s.rt.Reload(config.AuthConfig{Mode: "enforce", JWT: jc}); err != nil {
		t.Fatalf("reload with a bootstrap key: %v", err)
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 200, "bootstrap-key generation")
}

// quietStderr gives the runtime a writer safe for concurrent use: the fixture's
// bytes.Buffer is not, and the deny-list poller logs from its own goroutine.
func quietStderr(o *Options) { o.Stderr = io.Discard }

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

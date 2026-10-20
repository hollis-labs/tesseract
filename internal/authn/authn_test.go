package authn

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/authscope"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/credentials"
	"github.com/hollis-labs/tesseract/internal/credentials/credtest"
	"github.com/hollis-labs/tesseract/internal/knowledge"
	"github.com/hollis-labs/tesseract/internal/memory"
)

const (
	testIssuer = "tether-test"
	testKid    = "k1"
)

type fixture struct {
	t     *testing.T
	store *contextstore.Store
	priv  ed25519.PrivateKey
	jwks  []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": testKid, "use": "sig", "x": base64.RawURLEncoding.EncodeToString(pub),
	}}})
	return &fixture{t: t, store: s, priv: priv, jwks: jwks}
}

// token mints a managed store token and returns its secret value.
func (f *fixture) token(label string, scopes, globs []string) string {
	f.t.Helper()
	tok, _, err := f.store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{
		Label: label, ClientID: label, Scopes: scopes, NamespaceGlobs: globs,
	})
	if err != nil {
		f.t.Fatalf("create token: %v", err)
	}
	return tok
}

// jwt signs a Tether-style token for audience "tesseract".
func (f *fixture) jwt(scope string, over map[string]any) string {
	f.t.Helper()
	c := map[string]any{
		"iss": testIssuer, "sub": "msg://agent/a1", "aud": "tesseract", "scope": scope,
		"exp": time.Now().Add(10 * time.Minute).Unix(), "iat": time.Now().Unix(), "jti": "j-" + scope, "kind": "agent", "sid": "s1",
	}
	for k, v := range over {
		if v == nil { // a nil override removes the claim
			delete(c, k)
			continue
		}
		c[k] = v
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	in := enc(map[string]string{"alg": "EdDSA", "kid": testKid, "typ": "JWT"}) + "." + enc(c)
	return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.priv, []byte(in)))
}

// bootstrapFile writes the JWKS as a bootstrap key file (no network needed).
func (f *fixture) bootstrapFile() string {
	p := filepath.Join(f.t.TempDir(), "issuer.jwks.json")
	if err := os.WriteFile(p, f.jwks, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) jwtConfig() config.AuthJWTConfig {
	return config.AuthJWTConfig{Issuer: testIssuer, BootstrapKeyFile: f.bootstrapFile(), NamespaceGlobs: []string{"*"}}
}

type served struct {
	rt  *Runtime
	srv *contextapi.Server
	out *bytes.Buffer
}

// serve builds a Runtime exactly as cmd/tesseract does and fronts it with the real API server.
func (f *fixture) serve(cfg config.AuthConfig, mod func(*Options)) served {
	f.t.Helper()
	out := &bytes.Buffer{}
	o := Options{
		Config: cfg, Addr: "127.0.0.1:8089", Loopback: true, Store: f.store,
		Scopes: contextapi.RouteScopeTable(), Stderr: out, Surface: "http",
		SummaryInterval: time.Hour,
	}
	if mod != nil {
		mod(&o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.t.Cleanup(cancel)
	rt, err := Build(ctx, o)
	if err != nil {
		f.t.Fatalf("Build: %v", err)
	}
	srv := contextapi.NewServer(f.store, nil)
	ms := memory.NewStore(f.store.DB(), nil, "", 0, memory.NoopQueue{})
	srv.MemoryStore = ms
	srv.KnowledgeStore = knowledge.New(ms)
	srv.Auth = rt
	return served{rt: rt, srv: srv, out: out}
}

func (s served) do(method, path, bearer string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("User-Agent", "authn-test/1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.srv.ServeHTTP(rec, req)
	return rec
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: status %d, want %d; body=%s", what, rec.Code, want, rec.Body.String())
	}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Code
}

var defaultScopes = []string{"write", "promote.request", "promote.approve", "promote.apply", "packet", "repair", "namespace.register"}

// --- modes ---

func TestEnforceRejectsAnonymousAndKeepsErrorEnvelope(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce"}, nil)
	rec := s.do("GET", "/v1/namespaces/list", "", nil)
	wantStatus(t, rec, 401, "anonymous in enforce")
	if errCode(t, rec) != "auth_required" {
		t.Fatalf("legacy error envelope changed: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Tesseract-Auth"); got != "auth=enforce" {
		t.Fatalf("X-Tesseract-Auth = %q", got)
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "not-a-token", nil), 401, "invalid token in enforce")
}

func TestPermissiveAdmitsAnonymousAndRecordsWhoIsStillAnonymous(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "permissive"}, nil)
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "", nil), 200, "anonymous in permissive")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "garbage", nil), 200, "invalid bearer in permissive")
	if rec := s.do("GET", "/v1/recall", "", nil); rec.Code == 401 || rec.Code == 403 {
		t.Fatalf("anonymous recall in permissive must reach the handler: %d %s", rec.Code, rec.Body.String())
	}
	// a valid caller is not reported
	good := f.token("good-caller", nil, nil)
	wantStatus(t, s.do("GET", "/v1/namespaces/list", good, nil), 200, "valid token in permissive")

	rows, _ := s.rt.Summary.Snapshot()
	if len(rows) == 0 {
		t.Fatal("summary is empty; permissive must record the callers it would deny")
	}
	var sawMissing, sawInvalid bool
	for _, r := range rows {
		if r.Principal == "tesseract-token/good" || strings.Contains(r.Principal, "good-caller") {
			t.Fatalf("a valid caller must not appear in the would-deny summary: %+v", r)
		}
		if r.Route == "/v1/namespaces/list" && r.Authentication == auth.StateMissing && r.UserAgent == "authn-test/1" && r.Count == 1 {
			sawMissing = true
		}
		if r.Route == "/v1/namespaces/list" && r.Authentication == auth.StateInvalid {
			sawInvalid = true
		}
	}
	if !sawMissing || !sawInvalid {
		t.Fatalf("summary rows missing expected callers: %+v", rows)
	}
	// and it is queryable over the API
	body := s.do("GET", "/v1/auth/summary", "", nil)
	wantStatus(t, body, 200, "summary endpoint")
	if !strings.Contains(body.Body.String(), "/v1/namespaces/list") {
		t.Fatalf("summary endpoint lacks the caller: %s", body.Body.String())
	}
}

func TestOffModeNeverRejectsAndReportsOff(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "off"}, nil)
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "", nil), 200, "anonymous in off")
	who := s.do("GET", "/v1/auth/whoami", "", nil)
	wantStatus(t, who, 200, "whoami")
	var w Whoami
	_ = json.Unmarshal(who.Body.Bytes(), &w)
	if w.Auth != "auth=off" || w.Verified {
		t.Fatalf("whoami in off = %+v", w)
	}
	// honors a token when present
	tok := f.token("known", nil, nil)
	who = s.do("GET", "/v1/auth/whoami", tok, nil)
	_ = json.Unmarshal(who.Body.Bytes(), &w)
	if !w.Verified || w.Display != "known" {
		t.Fatalf("off mode must still attribute a presented token: %+v", w)
	}
}

func TestPublicRoutesStayOpenInEnforce(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce"}, nil)
	wantStatus(t, s.do("GET", "/v1/health/readiness", "", nil), 200, "readiness")
}

// --- existing managed tokens keep their outcomes ---

func TestExistingManagedTokensBehaveTheSameInEveryMode(t *testing.T) {
	f := newFixture(t)
	def := f.token("default", nil, nil)
	hub := f.token("hub-tether", []string{"memory:read", "memory:write", "packet", "promote.request"}, []string{"*"})
	scoped := f.token("scoped", nil, []string{"app/allowed/*"})

	type probe struct {
		name, method, path, bearer string
		body                       any
		want                       int
	}
	probes := []probe{
		{"default token lists namespaces", "GET", "/v1/namespaces/list", def, nil, 200},
		{"hub token lists namespaces", "GET", "/v1/namespaces/list", hub, nil, 200},
		{"hub token reads memory over HTTP", "GET", "/v1/memory/current?namespace=app/x/memory/note&key=k", hub, nil, 404},
		{"hub token has no repair scope", "POST", "/v1/maintenance/compact", hub, map[string]any{}, 403},
		{"default token has repair (reaches handler)", "POST", "/v1/maintenance/compact", def, map[string]any{}, 0},
		{"default token cannot apply settings (no admin)", "POST", "/v1/admin/settings/apply", def, map[string]any{}, 403},
	}
	for _, mode := range []string{"permissive", "enforce"} {
		s := f.serve(config.AuthConfig{Mode: mode}, nil)
		for _, p := range probes {
			rec := s.do(p.method, p.path, p.bearer, p.body)
			if p.want == 0 {
				if rec.Code == 401 || rec.Code == 403 {
					t.Errorf("[%s] %s: auth rejected an existing token: %d %s", mode, p.name, rec.Code, rec.Body.String())
				}
				continue
			}
			if rec.Code != p.want {
				t.Errorf("[%s] %s: status %d, want %d; body=%s", mode, p.name, rec.Code, p.want, rec.Body.String())
			}
		}
	}
	// namespace constraint is a per-token additional check, in every mode
	for _, mode := range []string{"permissive", "enforce"} {
		s := f.serve(config.AuthConfig{Mode: mode}, nil)
		rec := s.do("GET", "/v1/memory/current?namespace=app/other/memory/x&key=k", scoped, nil)
		if rec.Code != 403 || errCode(t, rec) != "namespace_not_permitted" {
			t.Errorf("[%s] namespace constraint not applied: %d %s", mode, rec.Code, rec.Body.String())
		}
	}
}

func TestStaticTokenIsAcceptedWithLegacyClaims(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce"}, func(o *Options) { o.StaticToken = "legacy-shared-secret"; o.Credentialed = true })
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "legacy-shared-secret", nil), 200, "static token")
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "wrong", nil), 401, "wrong static token")
	wantStatus(t, s.do("POST", "/v1/admin/settings/apply", "legacy-shared-secret", map[string]any{}), 403, "static token has no admin")
}

// --- scope mapping and the coarse gate ---

func TestCoarseScopeGateUnderEnforce(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, nil)
	read := f.jwt("tesseract.read", nil)
	write := f.jwt("tesseract.write", map[string]any{"jti": "j-w"})
	other := f.jwt("torque.write", map[string]any{"jti": "j-o"})
	admin := f.jwt("tesseract.admin", map[string]any{"jti": "j-a"})

	wantStatus(t, s.do("GET", "/v1/namespaces/list", read, nil), 200, "read jwt reads")
	rec := s.do("POST", "/v1/memory/write", read, map[string]any{})
	wantStatus(t, rec, 403, "read jwt cannot write")
	if !strings.Contains(rec.Body.String(), authscope.Write) {
		t.Fatalf("403 should name the missing scope: %s", rec.Body.String())
	}
	if rec.Code == 200 || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("unexpected envelope: %s", rec.Body.String())
	}
	// write jwt passes the guard (the handler then validates the empty body)
	if got := s.do("POST", "/v1/memory/write", write, map[string]any{}); got.Code == 401 || errCode(t, got) == "insufficient_scope" || errCode(t, got) == "namespace_not_permitted" {
		t.Fatalf("write jwt rejected by auth: %d %s", got.Code, got.Body.String())
	}
	wantStatus(t, s.do("GET", "/v1/namespaces/list", other, nil), 403, "other app's scope is not tesseract's")
	wantStatus(t, s.do("GET", "/v1/auth/tokens/list", read, nil), 403, "token lifecycle needs admin")
	// An admin JWT passes the guard; the handler then still demands a managed
	// administrator credential (credential rotation), which a JWT is not.
	adminList := s.do("GET", "/v1/auth/tokens/list", admin, nil)
	wantStatus(t, adminList, 403, "admin jwt reaches the handler, which requires a managed administrator credential")
	if errCode(t, adminList) != "admin_auth_required" {
		t.Fatalf("admin jwt should be refused by the handler, not the guard: %s", adminList.Body.String())
	}
	// default managed token (no admin) can no longer mint or list tokens
	def := f.token("default", nil, nil)
	wantStatus(t, s.do("GET", "/v1/auth/tokens/list", def, nil), 403, "default managed token is not an admin")
}

func TestJWTWildcardAndFineLayerExpansion(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, nil)
	all := f.jwt("tesseract.*", map[string]any{"jti": "j-all"})
	allList := s.do("GET", "/v1/auth/tokens/list", all, nil)
	wantStatus(t, allList, 403, "tesseract.* is admin at the guard; the handler requires a managed administrator credential")
	if errCode(t, allList) != "admin_auth_required" {
		t.Fatalf("tesseract.* should pass the guard and be refused by the handler: %s", allList.Body.String())
	}
	// a read-only JWT must not pass the fine layer for a legacy write scope either
	rd := f.jwt("tesseract.read", nil)
	wantStatus(t, s.do("POST", "/v1/context/write", rd, map[string]any{}), 403, "read jwt cannot context/write")
}

// --- JWT: namespace constraint, bad tokens, revocation ---

func TestJWTNamespaceConstraintFromConfig(t *testing.T) {
	f := newFixture(t)
	jc := f.jwtConfig()
	jc.NamespaceGlobs = []string{"app/allowed/*"}
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: jc}, nil)
	tok := f.jwt("tesseract.read", nil)
	rec := s.do("GET", "/v1/memory/current?namespace=app/other/memory/x&key=k", tok, nil)
	if rec.Code != 403 || errCode(t, rec) != "namespace_not_permitted" {
		t.Fatalf("jwt namespace constraint not applied: %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.do("GET", "/v1/memory/current?namespace=app/allowed/memory/x&key=k", tok, nil); rec.Code == 403 || rec.Code == 401 {
		t.Fatalf("allowed namespace rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestJWTRejectsWrongIssuerAudienceExpiryAndAlg(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, nil)
	cases := map[string]string{
		"wrong issuer":   f.jwt("tesseract.read", map[string]any{"iss": "someone-else"}),
		"wrong audience": f.jwt("tesseract.read", map[string]any{"aud": "torque"}),
		"expired":        f.jwt("tesseract.read", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
	}
	for name, tok := range cases {
		wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 401, name)
	}
	forged := f.jwt("tesseract.read", nil)
	parts := strings.Split(forged, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 64))
	wantStatus(t, s.do("GET", "/v1/namespaces/list", strings.Join(parts, "."), nil), 401, "bad signature")
	// a JWT-shaped string must never fall through to the opaque store verifier
	wantStatus(t, s.do("GET", "/v1/namespaces/list", "eyJhbGciOiJub25lIn0.e30.", nil), 401, "alg none")
}

func TestJWKSFetchedFromIssuerURLAndDenyListWithBearerRef(t *testing.T) {
	f := newFixture(t)
	var gotBearer atomic.Value
	var denied atomic.Bool
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/jwks.json":
			_, _ = w.Write(f.jwks)
		case "/auth/denylist":
			gotBearer.Store(r.Header.Get("Authorization"))
			doc := auth.DenyListDoc{}
			if denied.Load() {
				doc.JTI = []string{"j-revoked"}
			}
			_ = json.NewEncoder(w).Encode(doc)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.Close)

	res := credtest.New(t)
	res.Set("file://tesseract/denylist-bearer", "bearer-value-for-denylist")
	s := f.serve(config.AuthConfig{
		Mode:     "enforce",
		JWT:      config.AuthJWTConfig{Issuer: testIssuer, JWKSURL: issuer.URL + "/.well-known/jwks.json", NamespaceGlobs: []string{"*"}},
		DenyList: config.AuthDenyListConfig{URL: issuer.URL + "/auth/denylist", BearerRef: "file://tesseract/denylist-bearer"},
	}, func(o *Options) { o.Resolver = res })

	tok := f.jwt("tesseract.read", map[string]any{"jti": "j-revoked"})
	wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 200, "jwt verified via JWKS fetched over HTTP")
	waitFor(t, func() bool { v, _ := gotBearer.Load().(string); return v == "Bearer bearer-value-for-denylist" }, "deny-list poll with the go-secrets bearer")

	denied.Store(true)
	// the poller runs every 30s; drive one poll directly through a fresh runtime is not possible,
	// so assert on what the poller already proved (bearer) and revoke via a second runtime below.
	f2 := f.serve(config.AuthConfig{
		Mode:     "enforce",
		JWT:      config.AuthJWTConfig{Issuer: testIssuer, JWKSURL: issuer.URL + "/.well-known/jwks.json", NamespaceGlobs: []string{"*"}},
		DenyList: config.AuthDenyListConfig{URL: issuer.URL + "/auth/denylist", BearerRef: "file://tesseract/denylist-bearer"},
	}, func(o *Options) { o.Resolver = res })
	waitFor(t, func() bool {
		return f2.do("GET", "/v1/namespaces/list", tok, nil).Code == 401
	}, "revoked jti rejected after the deny-list loads")
}

// --- fail closed on configuration ---

func TestJWTNamespaceGlobsRequired(t *testing.T) {
	f := newFixture(t)
	for name, globs := range map[string][]string{"unset": nil, "empty": {}, "blank": {"", "  "}} {
		jc := f.jwtConfig()
		jc.NamespaceGlobs = globs
		_, err := Build(context.Background(), Options{Config: config.AuthConfig{JWT: jc}, Loopback: true, Addr: "127.0.0.1:1", Store: f.store, Scopes: contextapi.RouteScopeTable()})
		if err == nil || !strings.Contains(err.Error(), "auth.jwt.namespace_globs") {
			t.Errorf("%s globs: want startup refusal naming auth.jwt.namespace_globs, got %v", name, err)
		}
	}
}

// The hub ceiling must keep an issuer-minted JWT out of user/* while leaving
// the ceiling's own namespaces reachable.
func TestJWTHubCeilingExcludesUserNamespaces(t *testing.T) {
	f := newFixture(t)
	jc := f.jwtConfig()
	jc.NamespaceGlobs = []string{"project/*", "session/*", "app/*", "org/*", "system/*"}
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: jc}, nil)
	tok := f.jwt("tesseract.read", nil)
	rec := s.do("GET", "/v1/memory/current?namespace=user/chris/memory/x&key=k", tok, nil)
	if rec.Code != 403 || errCode(t, rec) != "namespace_not_permitted" {
		t.Fatalf("user/* must be unreachable for a JWT under the hub ceiling: %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.do("GET", "/v1/memory/current?namespace=project/x/memory/y&key=k", tok, nil); rec.Code == 403 || rec.Code == 401 {
		t.Fatalf("project/* rejected under the hub ceiling: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBuildFailsClosedOnBadVerifierConfig(t *testing.T) {
	f := newFixture(t)
	build := func(cfg config.AuthConfig, res credentials.Resolver) error {
		_, err := Build(context.Background(), Options{Config: cfg, Loopback: true, Addr: "127.0.0.1:1", Store: f.store, Scopes: contextapi.RouteScopeTable(), Resolver: res})
		return err
	}
	if err := build(config.AuthConfig{JWT: config.AuthJWTConfig{Issuer: testIssuer}}, nil); err == nil {
		t.Error("jwt issuer without jwks_url or bootstrap key must refuse to start")
	}
	if err := build(config.AuthConfig{JWT: config.AuthJWTConfig{Issuer: testIssuer, BootstrapKeyFile: "/nonexistent/key.pem"}}, nil); err == nil {
		t.Error("missing bootstrap key file must refuse to start")
	}
	if err := build(config.AuthConfig{DenyList: config.AuthDenyListConfig{URL: "http://x/denylist"}}, nil); err == nil {
		t.Error("deny-list without a jwt verifier must refuse to start")
	}
	jc := f.jwtConfig()
	err := build(config.AuthConfig{JWT: jc, DenyList: config.AuthDenyListConfig{URL: "http://x/d", BearerRef: "literal-secret-value"}}, nil)
	if err == nil || strings.Contains(err.Error(), "literal-secret-value") {
		t.Errorf("a literal bearer must be refused without echoing it: %v", err)
	}
	err = build(config.AuthConfig{JWT: jc, DenyList: config.AuthDenyListConfig{URL: "http://x/d", BearerRef: "file://tesseract/missing"}}, credtest.New(t))
	if err == nil {
		t.Error("an unresolvable bearer reference must refuse to start")
	}
	if err := build(config.AuthConfig{Mode: "sometimes"}, nil); err == nil {
		t.Error("an invalid mode must refuse to start")
	}
}

// --- bind guard ---

func TestBindGuardRefusesNonEnforceOnNonLoopback(t *testing.T) {
	f := newFixture(t)
	for _, mode := range []string{"off", "permissive"} {
		_, err := Build(context.Background(), Options{
			Config: config.AuthConfig{Mode: mode}, Addr: "0.0.0.0:8089", Loopback: false,
			Store: f.store, Scopes: contextapi.RouteScopeTable(),
		})
		if err == nil {
			t.Errorf("mode %s on 0.0.0.0 must be refused", mode)
		}
	}
	rt, err := Build(context.Background(), Options{
		Config: config.AuthConfig{}, Addr: "0.0.0.0:8089", Loopback: false,
		Store: f.store, Scopes: contextapi.RouteScopeTable(),
	})
	if err != nil || rt.Mode != auth.ModeEnforce {
		t.Fatalf("non-loopback default must be enforce, got %v / %v", rt, err)
	}
	// a tailnet bind is not loopback, so it defaults to enforce too
	rt, err = Build(context.Background(), Options{Addr: "100.74.49.44:8089", Loopback: false, Store: f.store, Scopes: contextapi.RouteScopeTable()})
	if err != nil || rt.Mode != auth.ModeEnforce {
		t.Fatalf("tailnet default must be enforce: %v %v", rt, err)
	}
}

// --- unmapped route fails closed ---

func TestUnmappedRouteFailsClosedInEnforceButNotPermissive(t *testing.T) {
	f := newFixture(t)
	tok := f.token("anyone", nil, nil)
	mk := func(mode string) served {
		return f.serve(config.AuthConfig{Mode: mode}, func(o *Options) { o.Scopes = auth.NewScopeTable() })
	}
	rec := mk("enforce").do("GET", "/v1/namespaces/list", tok, nil)
	if rec.Code != 403 || errCode(t, rec) != "route_not_mapped" {
		t.Fatalf("unmapped route in enforce: %d %s", rec.Code, rec.Body.String())
	}
	wantStatus(t, mk("permissive").do("GET", "/v1/namespaces/list", tok, nil), 200, "unmapped route in permissive")
}

// --- flipping loopback permissive -> enforce is config alone ---

func TestLoopbackFlipIsConfigOnly(t *testing.T) {
	f := newFixture(t)
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TESSERACT", "")
	stages := []struct {
		name string
		cfg  config.AuthConfig
		env  string
		want auth.Mode
		anon int
	}{
		{"code default on bare loopback", config.AuthConfig{}, "", auth.ModePermissive, 200},
		{"config auth.mode: enforce", config.AuthConfig{Mode: "enforce"}, "", auth.ModeEnforce, 401},
		{"config auth.mode: off", config.AuthConfig{Mode: "off"}, "", auth.ModeOff, 200},
		{"env beats config", config.AuthConfig{Mode: "permissive"}, "enforce", auth.ModeEnforce, 401},
		{"observe alias", config.AuthConfig{Mode: "observe"}, "", auth.ModePermissive, 200},
	}
	for _, st := range stages {
		t.Run(st.name, func(t *testing.T) {
			t.Setenv("HOLLIS_AUTH_TESSERACT", st.env)
			s := f.serve(st.cfg, nil)
			if s.rt.Mode != st.want {
				t.Fatalf("mode %s, want %s", s.rt.Mode, st.want)
			}
			wantStatus(t, s.do("GET", "/v1/namespaces/list", "", nil), st.anon, "anonymous request")
		})
	}
}

func TestStartupLineReportsModeAndSource(t *testing.T) {
	f := newFixture(t)
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TESSERACT", "")
	s := f.serve(config.AuthConfig{}, nil)
	for _, want := range []string{"auth=permissive", "source=default", "managed-token-store", "NOT rejected"} {
		if !strings.Contains(s.out.String(), want) {
			t.Errorf("startup output lacks %q:\n%s", want, s.out.String())
		}
	}
	s = f.serve(config.AuthConfig{Mode: "enforce"}, nil)
	if !strings.Contains(s.out.String(), "auth=enforce") || !strings.Contains(s.out.String(), "source=config") || strings.Contains(s.out.String(), "NOT rejected") {
		t.Errorf("enforce startup output wrong:\n%s", s.out.String())
	}
}

// --- helpers ---

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// --- dev service tokens (security direction v1) ---

// svcJWT is a dev service token as Tether issues it: kind=service, no
// session, and an exp about ten years out (go-auth rejects a token without
// exp, so "no expiry" is a distant exp), revocable by jti.
func (f *fixture) svcJWT(scope, jti string) string {
	f.t.Helper()
	return f.jwt(scope, map[string]any{
		"sub": "msg://service/local/agent-os", "kind": "service", "sid": "", "jti": jti,
		"exp": time.Now().Add(10 * 365 * 24 * time.Hour).Unix(),
	})
}

func TestServiceJWTAdmittedAndScopedInEveryMode(t *testing.T) {
	for _, mode := range []string{"off", "permissive", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			s := f.serve(config.AuthConfig{Mode: mode, JWT: f.jwtConfig()}, nil)
			read := f.svcJWT("tesseract.read", "svc-read")
			rec := s.do("GET", "/v1/namespaces/list", read, nil)
			wantStatus(t, rec, 200, "service token reads")
			var list map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) == 0 {
				t.Fatalf("namespaces list is not a JSON object with content: %v %q", err, rec.Body.String())
			}
			if mode == "enforce" {
				w := s.do("POST", "/v1/context/write", read, map[string]any{})
				wantStatus(t, w, 403, "read-only service token cannot write")
				if errCode(t, w) != "insufficient_scope" {
					t.Fatalf("read-only service token refused for the wrong reason: %s", w.Body.String())
				}
			}
		})
	}
}

func TestServiceJWTIsConfinedByNamespaceGlobs(t *testing.T) {
	f := newFixture(t)
	jc := f.jwtConfig()
	jc.NamespaceGlobs = []string{"app/allowed/*"}
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: jc}, nil)
	tok := f.svcJWT("tesseract.read", "svc-ns")
	rec := s.do("GET", "/v1/memory/current?namespace=app/other/memory/x&key=k", tok, nil)
	if rec.Code != 403 || errCode(t, rec) != "namespace_not_permitted" {
		t.Fatalf("service token escaped the namespace ceiling: %d %s", rec.Code, rec.Body.String())
	}
	rec = s.do("GET", "/v1/memory/current?namespace=app/allowed/memory/x&key=k", tok, nil)
	// Permitted by the ceiling and the scope; the key simply does not exist.
	// Anything else (a 500, a 403) is a failure, not "not rejected".
	if rec.Code != 404 || errCode(t, rec) != "not_found" {
		t.Fatalf("allowed namespace, absent key: want 404 not_found, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestServiceJWTWithoutExpIsRefused(t *testing.T) {
	f := newFixture(t)
	s := f.serve(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, nil)
	for name, tok := range map[string]string{
		"exp is zero":   f.jwt("tesseract.read", map[string]any{"kind": "service", "sid": "", "exp": 0}),
		"exp is absent": f.jwt("tesseract.read", map[string]any{"kind": "service", "sid": "", "exp": nil}),
	} {
		wantStatus(t, s.do("GET", "/v1/namespaces/list", tok, nil), 401, "a token with "+name+" is never valid")
	}
}

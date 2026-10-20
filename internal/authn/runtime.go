package authn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/authscope"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextapi"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/credentials"
)

// DefaultAudience is the "aud" a Tether-issued token must carry for Tesseract.
const DefaultAudience = "tesseract"

// Options builds a Runtime.
type Options struct {
	Config config.AuthConfig
	// Addr is the listen address; empty for stdio MCP (no listener).
	Addr string
	// Loopback, Credentialed, ConfigFailed feed DefaultMode.
	Loopback, Credentialed, ConfigFailed bool

	Store *contextstore.Store
	// StaticToken is the resolved legacy --static-token value ("" = none).
	StaticToken string
	// Scopes is the route table (serve) or tool table (mcp).
	Scopes *auth.ScopeTable
	// Resolver resolves go-secrets references (the deny-list bearer).
	Resolver credentials.Resolver
	// Stderr receives the startup line and the periodic would-deny summary.
	Stderr io.Writer
	// SummaryInterval overrides the 5 minute summary cadence (tests).
	SummaryInterval time.Duration
	// Surface labels the startup line: "http" or "mcp".
	Surface string
}

// Runtime is the assembled guard plus everything the handlers need from it.
//
// The verifier chain, the JWT namespace ceiling and the deny-list belong to a
// runtimeState that Reload replaces as a whole; the live mode is the Guard's.
type Runtime struct {
	Summary *Summary

	store *StoreVerifier
	opts  Options
	base  context.Context // process lifetime: parent of every state's loops

	state   atomic.Pointer[runtimeState]
	reload  sync.Mutex // serializes Reload
	summary sync.Once  // starts the would-deny summary the first time mode != enforce

	surface string
	stderr  io.Writer
}

// runtimeState is one generation of reloadable auth configuration: the mode,
// the verifier chain, the JWT namespace ceiling and the guard that applies
// them, together. It is immutable once published and a request takes it once,
// on entry (Middleware, MCPSession.Authorize), then carries it in its context,
// so the verifier, the mode and the ceiling that judge a request always belong
// to the same generation. cancel stops the JWKS refresh and deny-list loops
// that belong to it.
type runtimeState struct {
	guard   *auth.Guard
	verify  auth.Verifier // routed: JWT or opaque
	jwtGlob []string
	jwtOn   bool
	denyOn  bool
	mode    auth.Mode
	source  string
	cancel  context.CancelFunc
}

// Build resolves the mode, validates the bind, builds the verifiers and the
// guard, and starts the JWKS refresher and deny-list poller when configured.
// Anything it cannot construct is an error: a verifier that was asked for and
// cannot be built must stop startup, not silently fall back to fewer checks.
func Build(ctx context.Context, o Options) (*Runtime, error) {
	if o.Store == nil {
		return nil, errors.New("authn: a store is required for the managed-token verifier")
	}
	rt := &Runtime{
		Summary: NewSummary(), store: &StoreVerifier{Store: o.Store}, opts: o, base: ctx,
		surface: o.Surface, stderr: o.Stderr,
	}
	st, err := rt.buildState(o.Config, o.ConfigFailed)
	if err != nil {
		return nil, err
	}
	rt.state.Store(st)

	rt.announce()
	rt.startSummaryIfNeeded()
	return rt, nil
}

// buildState resolves the mode from cfg and builds one generation of verifiers.
// It starts background loops (JWKS refresh, deny-list poll) under a child of
// the process context; on error nothing is left running. It never touches the
// live Guard.
func (rt *Runtime) buildState(cfg config.AuthConfig, configFailed bool) (*runtimeState, error) {
	o := rt.opts
	mode, source, err := ResolveMode(ModeInput{
		Config: cfg, Loopback: o.Loopback, Credentialed: o.Credentialed, ConfigFailed: configFailed,
	})
	if err != nil {
		return nil, err
	}
	if o.Addr != "" {
		if err := auth.ValidateBind(o.Addr, mode); err != nil {
			return nil, fmt.Errorf("%w (set HOLLIS_AUTH_TESSERACT=enforce or config auth.mode: enforce)", err)
		}
	}

	ctx, cancel := context.WithCancel(rt.base)
	st := &runtimeState{mode: mode, source: source, cancel: cancel}
	fail := func(err error) (*runtimeState, error) {
		cancel()
		return nil, err
	}

	opaque := []auth.Verifier{rt.store}
	if strings.TrimSpace(o.StaticToken) != "" {
		opaque = append(opaque, auth.NewAPIKeyVerifier(auth.APIKey{
			Hash:    auth.HashKey(o.StaticToken),
			ID:      StaticTokenID,
			Display: "static-token",
			Scopes:  authscope.FromLegacy(contextapi.StaticTokenClaims().Scopes),
		}))
	}

	var jv auth.Verifier
	if jwtConfigured(cfg.JWT) {
		// A JWT carries no namespace claim, so these globs are the only thing
		// confining an issuer-minted token. There is deliberately no default:
		// a "*" fallback would hand every aud=tesseract JWT the user/* scopes.
		globs := cleanGlobs(cfg.JWT.NamespaceGlobs)
		if len(globs) == 0 {
			return fail(errors.New("auth.jwt.namespace_globs is required when auth.jwt is configured (a JWT carries no namespace claim); " +
				"set it explicitly, e.g. [project/*, session/*, app/*, org/*, system/*] to keep issuer-minted tokens out of user/* (see docs/auth.md)"))
		}
		dl := auth.NewDenyList()
		v, err := auth.NewJWTVerifier(auth.JWTConfig{
			Issuer:           strings.TrimSpace(cfg.JWT.Issuer),
			Audience:         firstNonEmpty(cfg.JWT.Audience, DefaultAudience),
			JWKSURL:          strings.TrimSpace(cfg.JWT.JWKSURL),
			BootstrapKeyFile: strings.TrimSpace(cfg.JWT.BootstrapKeyFile),
			DenyList:         dl,
		})
		if err != nil {
			return fail(fmt.Errorf("auth.jwt: %w", err))
		}
		jv = v
		st.jwtOn = true
		st.jwtGlob = globs
		if u := strings.TrimSpace(cfg.DenyList.URL); u != "" {
			poller := &auth.DenyListPoller{URL: u, List: dl, Logger: slog.New(slog.NewTextHandler(discardIfNil(o.Stderr), &slog.HandlerOptions{Level: slog.LevelWarn}))}
			if ref := strings.TrimSpace(cfg.DenyList.BearerRef); ref != "" {
				if !credentials.IsRef(ref) {
					return fail(errors.New("auth.deny_list.bearer_ref must be a go-secrets reference (file://, op://, keychain://, helper://), not a literal"))
				}
				bearer, err := credentials.Resolve(ctx, o.Resolver, ref)
				if err != nil {
					return fail(fmt.Errorf("auth.deny_list.bearer_ref: %w", err))
				}
				poller.Header = http.Header{"Authorization": []string{"Bearer " + bearer}}
			}
			st.denyOn = true
			// Everything above can fail; only now start the loops.
			go v.Run(ctx)
			go poller.Run(ctx)
		} else {
			go v.Run(ctx)
		}
	} else if strings.TrimSpace(cfg.DenyList.URL) != "" {
		return fail(errors.New("auth.deny_list.url is set but no JWT verifier is configured (auth.jwt.issuer + jwks_url)"))
	}

	st.verify = routed{jwt: jv, opaque: auth.Chain(opaque...)}
	g, err := auth.New(auth.Config{
		Mode: mode, Verifier: st.verify, Scopes: o.Scopes, Addr: o.Addr,
		Logger: slog.New(slog.NewTextHandler(discardIfNil(o.Stderr), &slog.HandlerOptions{Level: slog.LevelWarn})),
		Audit:  rt.Summary.Audit(),
		OnRequest: func(r *http.Request, _ auth.Principal, ev auth.Event) {
			rt.Summary.RecordRequest(r, contextapi.RoutePattern(r.Method, r.URL.Path), ev)
		},
	})
	if err != nil {
		return fail(err)
	}
	st.guard = g
	return st, nil
}

// snapshotKey carries the generation a request was admitted under.
type snapshotKey struct{}

func withSnapshot(ctx context.Context, st *runtimeState) context.Context {
	return context.WithValue(ctx, snapshotKey{}, st)
}

// snapshot is the generation ctx was admitted under, or the current one for a
// caller with no request (a CLI path, a test).
func (rt *Runtime) snapshot(ctx context.Context) *runtimeState {
	if st, ok := ctx.Value(snapshotKey{}).(*runtimeState); ok && st != nil {
		return st
	}
	return rt.state.Load()
}

// startSummaryIfNeeded starts the periodic would-deny summary once, the first
// time the mode is not enforce (at startup, or after a reload that relaxes it).
func (rt *Runtime) startSummaryIfNeeded() {
	if rt.state.Load().mode == auth.ModeEnforce {
		return
	}
	rt.summary.Do(func() { go rt.Summary.Run(rt.base, rt.stderr, rt.opts.SummaryInterval) })
}

// Mode is the live mode. Source says where it came from (default, config, env).
func (rt *Runtime) Mode() auth.Mode { return rt.state.Load().mode }

// Source is where the live mode came from: SourceDefault, SourceConfig or SourceEnv.
func (rt *Runtime) Source() string { return rt.state.Load().source }

// Status is the "auth=<mode>" string for health, whoami and the startup line.
func (rt *Runtime) Status() string { return rt.state.Load().guard.Status() }

// Describe is a one-line operator description (mode, where it came from,
// which verifiers are live). Contains no secret.
func (rt *Runtime) Describe() string {
	st := rt.state.Load()
	v := []string{"managed-token-store"}
	if st.jwtOn {
		v = append(v, "jwt/jwks")
	}
	if st.denyOn {
		v = append(v, "deny-list")
	}
	return fmt.Sprintf("%s (source=%s, surface=%s, verifiers=%s)", rt.Status(), st.source, rt.surface, strings.Join(v, "+"))
}

func (rt *Runtime) announce() {
	if rt.stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(rt.stderr, "  Auth: %s\n", rt.Describe())
	if mode := rt.state.Load().mode; mode != auth.ModeEnforce {
		_, _ = fmt.Fprintf(rt.stderr, "  !! auth is %s: requests without a valid bearer are NOT rejected. Would-deny summaries are logged; flip with config auth.mode: enforce (docs/auth.md).\n", mode)
	}
}

// Verify resolves a bearer through the same verifiers the guard uses.
func (rt *Runtime) Verify(ctx context.Context, token string) (auth.Principal, error) {
	p, err := rt.snapshot(ctx).verify.Verify(ctx, token)
	if err == nil {
		p.Verified = true
	}
	return p, err
}

// ClaimsFor translates a verified principal into the claims Tesseract's fine
// layer (legacy scope names, namespace globs) enforces. ok is false for an
// unverified principal, which carries no claims.
//
//   - managed token: exactly the stored claims, read from the store this request;
//   - static token: contextapi.StaticTokenClaims (default scopes, "*" globs);
//   - JWT: canonical scopes expanded by authscope.ToLegacy, namespace globs
//     from config auth.jwt.namespace_globs (required when JWT is configured, no
//     default); a JWT carries none.
func (rt *Runtime) ClaimsFor(ctx context.Context, p auth.Principal) (contextstore.AuthToken, bool) {
	if !p.Verified {
		return contextstore.AuthToken{}, false
	}
	if c, ok := rt.store.claimsFor(p); ok {
		return c, true
	}
	if p.ID == StaticTokenID {
		return contextapi.StaticTokenClaims(), true
	}
	c := contextstore.AuthToken{
		TokenID:        firstNonEmpty(p.TokenID, p.ID),
		Label:          firstNonEmpty(p.Display, p.ID),
		ClientID:       p.ID,
		Scopes:         authscope.ToLegacy(p.Scopes),
		NamespaceGlobs: append([]string(nil), rt.snapshot(ctx).jwtGlob...),
	}
	if !p.ExpiresAt.IsZero() {
		c.ExpiresAt = p.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return c, true
}

// Whoami describes the caller for GET /v1/auth/whoami. No token material.
type Whoami struct {
	Auth      string   `json:"auth"`
	Mode      string   `json:"mode"`
	Source    string   `json:"mode_source"`
	Verified  bool     `json:"verified"`
	Principal string   `json:"principal,omitempty"`
	Display   string   `json:"display,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	Globs     []string `json:"namespace_globs,omitempty"`
}

// WhoamiFor builds the whoami body for the principal stamped on a request.
func (rt *Runtime) WhoamiFor(ctx context.Context, p auth.Principal, ok bool) Whoami {
	st := rt.snapshot(ctx)
	w := Whoami{Auth: st.guard.Status(), Mode: string(st.mode), Source: st.source}
	if !ok {
		return w
	}
	w.Verified, w.Principal, w.Display, w.Kind, w.Scopes = p.Verified, p.ID, p.Display, p.Kind, p.Scopes
	if c, has := rt.ClaimsFor(ctx, p); has {
		w.Globs = c.NamespaceGlobs
	}
	return w
}

func jwtConfigured(c config.AuthJWTConfig) bool {
	return strings.TrimSpace(c.Issuer) != "" || strings.TrimSpace(c.JWKSURL) != "" || strings.TrimSpace(c.BootstrapKeyFile) != ""
}

func discardIfNil(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// Middleware implements contextapi.Authenticator.
func (rt *Runtime) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := rt.state.Load() // one generation for the whole request
		st.guard.Middleware(next).ServeHTTP(w, r.WithContext(withSnapshot(r.Context(), st)))
	})
}

// WhoamiBody implements contextapi.Authenticator.
func (rt *Runtime) WhoamiBody(ctx context.Context, p auth.Principal, ok bool) any {
	return rt.WhoamiFor(ctx, p, ok)
}

// SummaryBody implements contextapi.Authenticator.
func (rt *Runtime) SummaryBody() any {
	rows, dropped := rt.Summary.Snapshot()
	return map[string]any{
		"auth":    rt.Status(),
		"mode":    string(rt.Mode()),
		"source":  rt.Source(),
		"callers": rows,
		"dropped": dropped,
		"note":    "callers that enforce would reject; empty and meaningless in enforce mode",
	}
}

// cleanGlobs trims entries and drops blanks, so a list of empty strings counts
// as unset.
func cleanGlobs(in []string) []string {
	var out []string
	for _, g := range in {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

package authn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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
type Runtime struct {
	Guard   *auth.Guard
	Mode    auth.Mode
	Source  string // SourceDefault | SourceConfig | SourceEnv
	Summary *Summary

	store   *StoreVerifier
	verify  auth.Verifier
	jwtGlob []string
	jwtOn   bool
	denyOn  bool
	surface string
	stderr  io.Writer
}

// Build resolves the mode, validates the bind, builds the verifiers and the
// guard, and starts the JWKS refresher and deny-list poller when configured.
// Anything it cannot construct is an error: a verifier that was asked for and
// cannot be built must stop startup, not silently fall back to fewer checks.
func Build(ctx context.Context, o Options) (*Runtime, error) {
	mode, source, err := ResolveMode(ModeInput{
		Config: o.Config, Loopback: o.Loopback, Credentialed: o.Credentialed, ConfigFailed: o.ConfigFailed,
	})
	if err != nil {
		return nil, err
	}
	if o.Addr != "" {
		if err := auth.ValidateBind(o.Addr, mode); err != nil {
			return nil, fmt.Errorf("%w (set HOLLIS_AUTH_TESSERACT=enforce or config auth.mode: enforce)", err)
		}
	}
	if o.Store == nil {
		return nil, errors.New("authn: a store is required for the managed-token verifier")
	}

	rt := &Runtime{
		Mode: mode, Source: source, Summary: NewSummary(),
		store:   &StoreVerifier{Store: o.Store},
		surface: o.Surface, stderr: o.Stderr,
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
	if jwtConfigured(o.Config.JWT) {
		dl := auth.NewDenyList()
		v, err := auth.NewJWTVerifier(auth.JWTConfig{
			Issuer:           strings.TrimSpace(o.Config.JWT.Issuer),
			Audience:         firstNonEmpty(o.Config.JWT.Audience, DefaultAudience),
			JWKSURL:          strings.TrimSpace(o.Config.JWT.JWKSURL),
			BootstrapKeyFile: strings.TrimSpace(o.Config.JWT.BootstrapKeyFile),
			DenyList:         dl,
		})
		if err != nil {
			return nil, fmt.Errorf("auth.jwt: %w", err)
		}
		jv = v
		rt.jwtOn = true
		rt.jwtGlob = append([]string(nil), o.Config.JWT.NamespaceGlobs...)
		if len(rt.jwtGlob) == 0 {
			rt.jwtGlob = []string{"*"}
		}
		go v.Run(ctx)
		if u := strings.TrimSpace(o.Config.DenyList.URL); u != "" {
			poller := &auth.DenyListPoller{URL: u, List: dl, Logger: slog.New(slog.NewTextHandler(discardIfNil(o.Stderr), &slog.HandlerOptions{Level: slog.LevelWarn}))}
			if ref := strings.TrimSpace(o.Config.DenyList.BearerRef); ref != "" {
				if !credentials.IsRef(ref) {
					return nil, errors.New("auth.deny_list.bearer_ref must be a go-secrets reference (file://, op://, keychain://, helper://), not a literal")
				}
				bearer, err := credentials.Resolve(ctx, o.Resolver, ref)
				if err != nil {
					return nil, fmt.Errorf("auth.deny_list.bearer_ref: %w", err)
				}
				poller.Header = http.Header{"Authorization": []string{"Bearer " + bearer}}
			}
			rt.denyOn = true
			go poller.Run(ctx)
		}
	} else if strings.TrimSpace(o.Config.DenyList.URL) != "" {
		return nil, errors.New("auth.deny_list.url is set but no JWT verifier is configured (auth.jwt.issuer + jwks_url)")
	}

	rt.verify = routed{jwt: jv, opaque: auth.Chain(opaque...)}
	logger := slog.New(slog.NewTextHandler(discardIfNil(o.Stderr), &slog.HandlerOptions{Level: slog.LevelWarn}))
	g, err := auth.New(auth.Config{
		Mode: mode, Verifier: rt.verify, Scopes: o.Scopes, Logger: logger,
		Audit: rt.Summary.Audit(),
		OnRequest: func(r *http.Request, _ auth.Principal, ev auth.Event) {
			rt.Summary.RecordRequest(r, contextapi.RoutePattern(r.Method, r.URL.Path), ev)
		},
	})
	if err != nil {
		return nil, err
	}
	rt.Guard = g

	rt.announce()
	if mode != auth.ModeEnforce {
		go rt.Summary.Run(ctx, o.Stderr, o.SummaryInterval)
	}
	return rt, nil
}

// Status is the "auth=<mode>" string for health, whoami and the startup line.
func (rt *Runtime) Status() string { return rt.Guard.Status() }

// Describe is a one-line operator description (mode, where it came from,
// which verifiers are live). Contains no secret.
func (rt *Runtime) Describe() string {
	v := []string{"managed-token-store"}
	if rt.jwtOn {
		v = append(v, "jwt/jwks")
	}
	if rt.denyOn {
		v = append(v, "deny-list")
	}
	return fmt.Sprintf("%s (source=%s, surface=%s, verifiers=%s)", rt.Status(), rt.Source, rt.surface, strings.Join(v, "+"))
}

func (rt *Runtime) announce() {
	if rt.stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(rt.stderr, "  Auth: %s\n", rt.Describe())
	if rt.Mode != auth.ModeEnforce {
		_, _ = fmt.Fprintf(rt.stderr, "  !! auth is %s: requests without a valid bearer are NOT rejected. Would-deny summaries are logged; flip with config auth.mode: enforce (docs/auth.md).\n", rt.Mode)
	}
}

// Verify resolves a bearer through the same verifiers the guard uses.
func (rt *Runtime) Verify(ctx context.Context, token string) (auth.Principal, error) {
	p, err := rt.verify.Verify(ctx, token)
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
//     from config auth.jwt.namespace_globs (default "*"); a JWT carries none.
func (rt *Runtime) ClaimsFor(p auth.Principal) (contextstore.AuthToken, bool) {
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
		NamespaceGlobs: append([]string(nil), rt.jwtGlob...),
	}
	if len(c.NamespaceGlobs) == 0 {
		c.NamespaceGlobs = []string{"*"}
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
func (rt *Runtime) WhoamiFor(p auth.Principal, ok bool) Whoami {
	w := Whoami{Auth: rt.Status(), Mode: string(rt.Mode), Source: rt.Source}
	if !ok {
		return w
	}
	w.Verified, w.Principal, w.Display, w.Kind, w.Scopes = p.Verified, p.ID, p.Display, p.Kind, p.Scopes
	if c, has := rt.ClaimsFor(p); has {
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
func (rt *Runtime) Middleware(next http.Handler) http.Handler { return rt.Guard.Middleware(next) }

// WhoamiBody implements contextapi.Authenticator.
func (rt *Runtime) WhoamiBody(p auth.Principal, ok bool) any { return rt.WhoamiFor(p, ok) }

// SummaryBody implements contextapi.Authenticator.
func (rt *Runtime) SummaryBody() any {
	rows, dropped := rt.Summary.Snapshot()
	return map[string]any{
		"auth":    rt.Status(),
		"mode":    string(rt.Mode),
		"source":  rt.Source,
		"callers": rows,
		"dropped": dropped,
		"note":    "callers that enforce would reject; empty and meaningless in enforce mode",
	}
}

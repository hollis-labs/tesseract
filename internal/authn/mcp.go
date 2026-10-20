package authn

import (
	"context"
	"fmt"
	"strings"
	"sync"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// MCPSession is the stdio MCP side of the guard. There is no HTTP request to
// carry a header, so the bearer comes from a TokenSource that is consulted on
// EVERY tool call. That is what keeps `--token-file` working when a launcher
// rewrites the file in place (a short-lived Tether-issued JWT is refreshed
// before expiry), and what makes revocation of a managed token take effect on
// the next call rather than at process restart.
type MCPSession struct {
	rt     *Runtime
	source TokenSource
}

// TokenSource returns the bearer to present now, or "" for none.
type TokenSource func() (string, error)

// NewMCPSession binds a runtime to a token source. A nil source means the
// process was started without a token.
func (rt *Runtime) NewMCPSession(source TokenSource) *MCPSession {
	return &MCPSession{rt: rt, source: source}
}

type mcpToolKey struct{}
type mcpNamespaceAudit struct {
	tool string
	once sync.Once
}

// Authorize implements mcpadapter.Authorizer.
func (m *MCPSession) Authorize(ctx context.Context, tool string) (context.Context, error) {
	st := m.rt.state.Load() // one generation for this tool call
	ctx = withSnapshot(ctx, st)
	ctx = context.WithValue(ctx, mcpToolKey{}, &mcpNamespaceAudit{tool: tool})
	if p, ok := m.principal(ctx); ok {
		ctx = auth.WithPrincipal(ctx, p)
	}
	return ctx, st.guard.AuthorizeTool(ctx, tool)
}

// ClaimsFromContext implements mcpadapter.Authorizer.
func (m *MCPSession) ClaimsFromContext(ctx context.Context) (contextstore.AuthToken, bool) {
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Verified {
		return contextstore.AuthToken{}, false
	}
	return m.rt.ClaimsFor(ctx, p)
}

// principal verifies the current token. An unreadable source, an empty token,
// an invalid or expired token and an unavailable verifier all yield "no
// principal": the guard then reports missing and decides per mode. (A bad
// token is never promoted to anonymous access: enforce refuses, and the
// handlers' own scope checks still refuse mutations without claims.)
func (m *MCPSession) principal(ctx context.Context) (auth.Principal, bool) {
	if m.source == nil {
		return auth.Principal{}, false
	}
	tok, err := m.source()
	tok = strings.TrimSpace(tok)
	if err != nil || tok == "" {
		return auth.Principal{}, false
	}
	p, err := m.rt.Verify(ctx, tok)
	if err != nil {
		return auth.Principal{}, false
	}
	return p, true
}

// NamespaceDecision applies the same mode as the coarse tool guard, without
// logging namespace names, record identities, payloads, or bearer material.
func (m *MCPSession) NamespaceDecision(ctx context.Context, allowed bool) bool {
	if allowed {
		return true
	}
	if m.rt.snapshot(ctx).mode == auth.ModeEnforce {
		return false
	}
	audit, _ := ctx.Value(mcpToolKey{}).(*mcpNamespaceAudit)
	if audit == nil {
		audit = &mcpNamespaceAudit{}
	}
	audit.once.Do(func() {
		p, verified := auth.FromContext(ctx)
		state := auth.StateMissing
		if verified && p.Verified {
			state = auth.StateVerified
		}
		m.rt.Summary.Audit()(auth.Event{Tool: audit.tool, Authentication: state, PrincipalID: p.ID, Reason: "namespace_not_permitted", WouldDeny: true})
		if m.rt.stderr != nil {
			_, _ = fmt.Fprintf(m.rt.stderr, "auth: would-deny namespace_not_permitted tool=%s\n", audit.tool)
		}
	})
	return true
}

package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/mcpadapter"
)

// mcpHarness is the stdio surface as cmd/tesseract wires it: a real Runtime,
// an MCPSession over a swappable token source, and the full tool registry.
type mcpHarness struct {
	rt  *Runtime
	srv *gomcpserver.Server

	mu  sync.Mutex
	tok string
}

func (f *fixture) mcp(cfg config.AuthConfig, initialToken string, mod func(*Options)) *mcpHarness {
	f.t.Helper()
	h := &mcpHarness{tok: initialToken}
	o := Options{Config: cfg, Loopback: true, Store: f.store, Scopes: mcpadapter.ToolScopeTable(), Stderr: &bytes.Buffer{}, Surface: "mcp"}
	if mod != nil {
		mod(&o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.t.Cleanup(cancel)
	rt, err := Build(ctx, o)
	if err != nil {
		f.t.Fatalf("Build: %v", err)
	}
	h.rt = rt
	a := mcpadapter.New(f.store, initialToken)
	a.Auth = rt.NewMCPSession(func() (string, error) { h.mu.Lock(); defer h.mu.Unlock(); return h.tok, nil })
	h.srv = gomcpserver.NewServer("t", "0")
	a.RegisterAllTools(h.srv)
	return h
}

func (h *mcpHarness) setToken(t string) { h.mu.Lock(); h.tok = t; h.mu.Unlock() }

// call returns the error code in the tool result ("" when it is not an error).
func (h *mcpHarness) call(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	res, err := h.srv.CallTool(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("%s: transport error %v", tool, err)
	}
	b, _ := json.Marshal(res)
	var m struct {
		StructuredContent struct {
			Code string `json:"code"`
		} `json:"structuredContent"`
		Code string `json:"code"`
	}
	_ = json.Unmarshal(b, &m)
	if m.Code != "" {
		return m.Code
	}
	if m.StructuredContent.Code != "" {
		return m.StructuredContent.Code
	}
	if strings.Contains(string(b), `"code":"`) {
		i := strings.Index(string(b), `"code":"`) + len(`"code":"`)
		return strings.SplitN(string(b)[i:], `"`, 2)[0]
	}
	return ""
}

func TestMCPEnforceRequiresAToken(t *testing.T) {
	f := newFixture(t)
	h := f.mcp(config.AuthConfig{Mode: "enforce"}, "", nil)
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "auth_required" {
		t.Fatalf("anonymous read tool in enforce = %q, want auth_required", code)
	}
}

func TestMCPEnforceAcceptsManagedTokenAndKeepsFineScopes(t *testing.T) {
	f := newFixture(t)
	readOnly := f.token("reader", []string{"memory:read"}, nil)
	h := f.mcp(config.AuthConfig{Mode: "enforce"}, readOnly, nil)
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "" {
		t.Fatalf("read tool with a valid token failed: %q", code)
	}
	// coarse gate: tesseract.write missing
	if code := h.call(t, "context_write", map[string]any{}); code != "insufficient_scope" {
		t.Fatalf("write tool with a read-only token = %q", code)
	}
	// a token with write passes the coarse gate; the handler then validates args, not auth
	writer := f.token("writer", defaultScopes, nil)
	h.setToken(writer)
	if code := h.call(t, "context_write", map[string]any{}); code == "insufficient_scope" || code == "auth_required" {
		t.Fatalf("write token rejected by auth: %q", code)
	}
}

func TestMCPTokenSourceIsConsultedPerCallSoRefreshAndRevocationApply(t *testing.T) {
	f := newFixture(t)
	h := f.mcp(config.AuthConfig{Mode: "enforce", JWT: f.jwtConfig()}, "", nil)
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "auth_required" {
		t.Fatalf("no token yet: %q", code)
	}
	// launcher drops a Tether JWT into the token file; same process, next call succeeds
	h.setToken(f.jwt("tesseract.read", nil))
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "" {
		t.Fatalf("freshly written jwt not honored: %q", code)
	}
	if code := h.call(t, "context_write", map[string]any{}); code != "insufficient_scope" {
		t.Fatalf("read jwt must not write: %q", code)
	}
	// a managed token revoked mid-session stops working on the next call
	tok, meta, err := f.store.CreateAuthToken(context.Background(), contextstore.TokenCreateInput{Label: "rev", ClientID: "rev"})
	if err != nil {
		t.Fatal(err)
	}
	h.setToken(tok)
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "" {
		t.Fatalf("valid managed token rejected: %q", code)
	}
	if err := f.store.RevokeAuthTokenByID(context.Background(), meta.TokenID); err != nil {
		t.Fatal(err)
	}
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "auth_required" {
		t.Fatalf("revoked token still honored: %q", code)
	}
}

func TestMCPPermissiveNeverRejectsAtTheGuardAndReportsCallers(t *testing.T) {
	f := newFixture(t)
	h := f.mcp(config.AuthConfig{Mode: "permissive"}, "", nil)
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "" {
		t.Fatalf("anonymous read in permissive must work: %q", code)
	}
	// mutations still need a token exactly as before go-auth (fine layer)
	if code := h.call(t, "context_write", map[string]any{}); code != "auth_required" {
		t.Fatalf("anonymous write in permissive = %q, want auth_required from the handler", code)
	}
	rows, _ := h.rt.Summary.Snapshot()
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Surface == "mcp" && r.Authentication == auth.StateMissing {
			seen[r.Route] = true
		}
	}
	if !seen["tesseract_skills"] || !seen["context_write"] {
		t.Fatalf("summary should list the tools called without a bearer: %+v", rows)
	}
}

func TestMCPUnmappedToolFailsClosedInEnforce(t *testing.T) {
	f := newFixture(t)
	tok := f.token("anyone", nil, nil)
	h := f.mcp(config.AuthConfig{Mode: "enforce"}, tok, func(o *Options) { o.Scopes = auth.NewScopeTable() })
	if code := h.call(t, "tesseract_skills", map[string]any{}); code != "insufficient_scope" {
		t.Fatalf("unmapped tool in enforce = %q", code)
	}
}

func TestMCPDefaultsToPermissiveWithoutAListener(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TESSERACT", "")
	f := newFixture(t)
	h := f.mcp(config.AuthConfig{}, "", nil)
	if h.rt.Mode() != auth.ModePermissive {
		t.Fatalf("stdio MCP default = %s", h.rt.Mode())
	}
	h = f.mcp(config.AuthConfig{}, "", func(o *Options) { o.ConfigFailed = true })
	if h.rt.Mode() != auth.ModeEnforce {
		t.Fatalf("an unreadable config must default to enforce, got %s", h.rt.Mode())
	}
}

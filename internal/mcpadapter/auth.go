package mcpadapter

import (
	"context"
	"errors"

	gomcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tesseract/internal/authscope"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// Authorizer is the go-auth integration for the MCP surface (implemented by
// internal/authn.MCPSession). A nil Adapter.Auth keeps the pre-go-auth
// behavior: Adapter.Token validated against the store by checkScope.
type Authorizer interface {
	// Authorize resolves the caller for one tool call and applies the guard's
	// mode and the coarse tool-to-scope decision. It returns a context stamped
	// with the verified principal (if any). In enforce mode a refusal is
	// auth.ErrUnauthenticated or an error wrapping auth.ErrForbidden; in
	// permissive and off modes it returns nil and logs would-deny.
	Authorize(ctx context.Context, tool string) (context.Context, error)
	// ClaimsFromContext returns the legacy claims of the verified caller
	// stamped by Authorize, for the fine-grained scope and namespace checks.
	ClaimsFromContext(ctx context.Context) (contextstore.AuthToken, bool)
}

// toolScopes is the go-auth tool-to-scope table: the COARSE decision. It is
// deny-by-default: a tool absent from this map is not registered with the
// guard and is refused in enforce mode. TestEveryRegisteredToolHasAScope fails
// the build before a new tool can be forgotten. The fine decision (checkScope
// on legacy scope names, namespace globs) is unchanged inside the handlers.
//
// Reads are tesseract.read, which every managed token holds. Tools that
// mutate anything, including activation counters (tesseract_touch) and stored
// embeddings (context_embed), are tesseract.write.
var toolScopes = map[string]string{
	// reads
	"context_audit_list":     authscope.Read,
	"context_estimate":       authscope.Read,
	"context_pack":           authscope.Read,
	"context_plan":           authscope.Read,
	"context_promotion_list": authscope.Read,
	"context_rag_query":      authscope.Read,
	"context_registry_list":  authscope.Read,
	"context_search":         authscope.Read,
	"context_typed_view":     authscope.Read,
	"context_view":           authscope.Read,
	"event_list":             authscope.Read,
	"tesseract_get":          authscope.Read,
	"tesseract_get_revision": authscope.Read,
	"tesseract_history":      authscope.Read,
	"tesseract_recall":       authscope.Read,
	"tesseract_ref_resolve":  authscope.Read,
	"tesseract_runtime_get":  authscope.Read,
	"tesseract_skills":       authscope.Read,
	// writes
	"context_embed":         authscope.Write,
	"context_ingest":        authscope.Write,
	"context_session_write": authscope.Write,
	"context_status_set":    authscope.Write,
	"context_typed_write":   authscope.Write,
	"context_write":         authscope.Write,
	"event_write":           authscope.Write,
	"knowledge_write":       authscope.Write,
	"memory_promote":        authscope.Write,
	"memory_write":          authscope.Write,
	"tesseract_deprecate":   authscope.Write,
	"tesseract_touch":       authscope.Write,
	"workspace_delete":      authscope.Write,
	"workspace_write":       authscope.Write,
	// promotion
	"context_promote":   authscope.Promote,
	"workspace_promote": authscope.Promote,
	// maintenance
	"context_namespace_register": authscope.Maintain,
}

// ToolScopeTable builds the guard's tool table.
func ToolScopeTable() *auth.ScopeTable {
	t := auth.NewScopeTable()
	for name, scope := range toolScopes {
		t.Tool(name, scope)
	}
	return t
}

// withAuth wraps one tool handler so the guard decides before anything else
// runs. Resolution is per call (not per process) because the token source may
// be a file a launcher refreshes in place and a managed token may be revoked.
func (a *Adapter) withAuth(name string, next gomcpserver.ToolHandler) gomcpserver.ToolHandler {
	return func(ctx context.Context, args map[string]any) (any, error) {
		if a.Auth == nil {
			return next(ctx, args)
		}
		ctx, err := a.Auth.Authorize(ctx, name)
		switch {
		case err == nil:
			return next(ctx, args)
		case errors.Is(err, auth.ErrForbidden):
			return toolError(codeInsufficientScope, "token does not hold the scope this tool needs: "+err.Error()), nil
		default:
			return toolError(codeAuthRequired, "authentication required: present a valid token (tesseract mcp --token-file); "+err.Error()), nil
		}
	}
}

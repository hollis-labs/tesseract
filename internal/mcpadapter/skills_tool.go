package mcpadapter

import (
	"context"
	"errors"

	"github.com/hollis-labs/vanta-conduit/internal/mcpadapter/skills"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerSkillsTool registers the vanta_skills progressive-discovery
// meta-tool. No capability token required — this is read-only orientation
// served from an embedded filesystem.
func (a *Adapter) registerSkillsTool(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("vanta_skills",
		mcp.WithDescription(
			"Vanta's self-documenting skill index. Call with no args for the catalog; "+
				"call with `name` to read a specific skill in full. Progressive discovery — "+
				"skills only load when requested. Start with name=`start-here` for orientation.",
		),
		mcp.WithString("name",
			mcp.Description("Skill name (e.g. start-here, namespaces, memory). Omit to get the skill index.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), a.handleVantaSkills)
}

func (a *Adapter) handleVantaSkills(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	if name == "" {
		metas, err := skills.List()
		if err != nil {
			return toolError("internal_error", err.Error()), nil
		}
		return toolJSON(metas), nil
	}
	body, err := skills.Get(name)
	if err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			return toolError("skill_not_found", err.Error()), nil
		}
		return toolError("internal_error", err.Error()), nil
	}
	return mcp.NewToolResultText(body), nil
}

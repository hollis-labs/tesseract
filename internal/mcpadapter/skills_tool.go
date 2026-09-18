package mcpadapter

import (
	"context"
	"errors"

	"github.com/hollis-labs/tesseract/internal/mcpadapter/skills"
)

// registerSkillsTool registers the tesseract_skills progressive-discovery
// meta-tool. No capability token required — this is read-only orientation
// served from an embedded filesystem.
func (a *Adapter) registerSkillsTool(s *toolRegistrar) {
	a.addTool(s, gomcpTool("tesseract_skills",
		"Tesseract's self-documenting skill index. Call with no args for the catalog; "+
			"call with `name` to read a specific skill in full. Progressive discovery — "+
			"skills only load when requested. Start with name=`start-here` for orientation.",
		inputSchema(
			strProp("name", "Skill name (e.g. start-here, namespaces, memory). Omit to get the skill index.", false),
		),
		toolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		a.handleTesseractSkills,
	))
}

func (a *Adapter) handleTesseractSkills(_ context.Context, req map[string]any) (any, error) {
	name := argString(req, "name", "")
	if name == "" {
		metas, err := skills.List()
		if err != nil {
			return toolError(codeInternalError, err.Error()), nil
		}
		return toolJSON(metas), nil
	}
	body, err := skills.Get(name)
	if err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			return toolError(codeSkillNotFound, err.Error()), nil
		}
		return toolError(codeInternalError, err.Error()), nil
	}
	return body, nil
}

package contextapi

import (
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

// PlannerConfig holds server-side caps for context plan validation.
// NOTE: The MCP tool names (context_broker_plan, context_broker_fetch) and
// HTTP route (/v1/broker/plan) retain "broker" for backward compatibility.
// Internally this is the context query planner, not the universal ContextBroker.
type PlannerConfig struct {
	// MaxItemsCap is the server-enforced maximum for plan budget max_items. Default 200.
	MaxItemsCap int
	// MaxTokensCap is the server-enforced maximum for plan budget max_tokens_estimate. Default 32000.
	MaxTokensCap int
	// ForbiddenNamespacePatterns is a list of glob patterns that are never allowed in plans.
	ForbiddenNamespacePatterns []string
}

func (c PlannerConfig) maxItems() int {
	if c.MaxItemsCap <= 0 {
		return 200
	}
	return c.MaxItemsCap
}

func (c PlannerConfig) maxTokens() int {
	if c.MaxTokensCap <= 0 {
		return 32000
	}
	return c.MaxTokensCap
}

func (c PlannerConfig) forbidden() []string {
	if len(c.ForbiddenNamespacePatterns) == 0 {
		return []string{"system/*", "internal/*"}
	}
	return c.ForbiddenNamespacePatterns
}

// PlannerConstraints is the optional constraints block in a context plan request.
type PlannerConstraints struct {
	Namespaces        []string `json:"namespaces,omitempty"`
	MaxItems          int      `json:"max_items,omitempty"`
	MaxTokensEstimate int      `json:"max_tokens_estimate,omitempty"`
}

// ContextPlanRequest is the body for POST /v1/broker/plan (and /v1/context/plan alias).
type ContextPlanRequest struct {
	TaskSummary string             `json:"task_summary,omitempty"`
	Intent      string             `json:"intent"` // resume_task|boot_project|review_session|custom
	Constraints PlannerConstraints `json:"constraints,omitempty"`
}

// ContextPlanResponse is the response for POST /v1/broker/plan (and /v1/context/plan alias).
type ContextPlanResponse struct {
	Plan      PacketRequest `json:"plan"`
	Rationale string        `json:"rationale"`
	Warnings  []string      `json:"warnings"`
}

// stopwords is a small set of common English words excluded from keyword extraction.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "in": true, "is": true, "it": true,
	"its": true, "of": true, "on": true, "or": true, "the": true,
	"this": true, "that": true, "to": true, "was": true, "with": true,
	"we": true, "i": true, "my": true, "me": true, "our": true,
	"will": true, "not": true, "but": true, "into": true, "just": true,
	"task": true, "work": true, "previous": true, "new": true,
	"using": true, "use": true, "via": true, "which": true, "all": true,
}

// extractKeywords returns up to top-N unique, lowercase non-stopword tokens from text.
func extractKeywords(text string, n int) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9') && r != '-' && r != '_'
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) < 3 || stopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= n {
			break
		}
	}
	return out
}

func (s *Server) handleContextPlan(w http.ResponseWriter, r *http.Request) {
	var req ContextPlanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	intent := strings.TrimSpace(strings.ToLower(req.Intent))
	if intent == "" {
		intent = "custom"
	}

	// Determine caller's permitted namespace globs from token claims.
	// When no managed auth, permit all.
	permittedGlobs := []string{"*"}
	if claims, ok := getTokenClaims(r); ok {
		if len(claims.NamespaceGlobs) > 0 {
			permittedGlobs = claims.NamespaceGlobs
		}
	}

	var warnings []string
	var namespaces []string
	var rationale string

	// Determine constraint overrides.
	maxItems := req.Constraints.MaxItems
	if maxItems <= 0 {
		maxItems = 50
	}
	maxTokens := req.Constraints.MaxTokensEstimate
	if maxTokens <= 0 {
		maxTokens = 4000
	}

	switch intent {
	case "resume_task":
		keywords := extractKeywords(req.TaskSummary, 3)
		for _, kw := range keywords {
			namespaces = append(namespaces, "user/memory/"+kw+"*")
		}
		namespaces = append(namespaces, "user/pins/*")
		if len(keywords) > 0 {
			rationale = fmt.Sprintf("resume_task: targeting namespace patterns derived from task_summary keywords [%s] + user/pins/*",
				strings.Join(keywords, ", "))
		} else {
			rationale = "resume_task: no keywords extracted from task_summary; using user/memory/* + user/pins/*"
			namespaces = append(namespaces, "user/memory/*")
		}

	case "boot_project":
		namespaces = []string{"user/memory/*", "user/pins/*"}
		if maxItems < 100 {
			maxItems = 100
		}
		rationale = "boot_project: full user/memory/* + user/pins/* for project boot context"

	case "review_session":
		namespaces = []string{"user/cache/*", "user/pins/*"}
		if maxItems <= 0 || maxItems > 30 {
			maxItems = 30
		}
		rationale = "review_session: user/cache/* (last 24h) + user/pins/* for session review"

	default: // custom
		if len(req.Constraints.Namespaces) > 0 {
			namespaces = req.Constraints.Namespaces
		} else {
			namespaces = []string{"user/*"}
		}
		rationale = "custom: using provided constraints.namespaces"
	}

	// Validation (TASK-018): strip forbidden namespaces.
	forbidden := s.Planner.forbidden()
	namespaces, warnings = stripForbiddenNamespaces(namespaces, forbidden, warnings)

	// Validation: strip namespaces outside caller's token globs.
	if !hasWildcardGlob(permittedGlobs) {
		namespaces, warnings = stripUnpermittedNamespaces(namespaces, permittedGlobs, warnings)
	}

	if len(namespaces) == 0 {
		writeError(w, http.StatusForbidden, "plan_forbidden", "no permitted namespaces in plan", nil)
		return
	}

	// Validation: clamp budget to server caps.
	serverMaxItems := s.Planner.maxItems()
	serverMaxTokens := s.Planner.maxTokens()
	if maxItems > serverMaxItems {
		warnings = append(warnings, fmt.Sprintf("budget max_items clamped from %d to server cap %d", maxItems, serverMaxItems))
		maxItems = serverMaxItems
	}
	if maxTokens > serverMaxTokens {
		warnings = append(warnings, fmt.Sprintf("budget max_tokens_estimate clamped from %d to server cap %d", maxTokens, serverMaxTokens))
		maxTokens = serverMaxTokens
	}

	plan := PacketRequest{
		Selector: contextstore.Selector{
			Namespaces:    namespaces,
			RevisionScope: "head",
			Order:         []string{"created_desc"},
			Limit:         maxItems,
		},
		Assembly: PacketAssembly{
			IncludePins: intent != "custom" || strings.Contains(strings.Join(namespaces, ","), "pins"),
			Budget: PacketBudget{
				MaxItems:          maxItems,
				MaxTokensEstimate: maxTokens,
			},
			Shape: PacketShape{
				IncludePayload: true,
				PayloadMode:    "full",
			},
			ManifestLevel: "summary",
		},
	}

	resp := ContextPlanResponse{
		Plan:      plan,
		Rationale: rationale,
		Warnings:  warnings,
	}
	if resp.Warnings == nil {
		resp.Warnings = []string{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// stripForbiddenNamespaces removes namespaces matching any forbidden glob pattern.
func stripForbiddenNamespaces(namespaces, forbidden []string, warnings []string) ([]string, []string) {
	var out []string
	for _, ns := range namespaces {
		blocked := false
		for _, f := range forbidden {
			if matched, _ := path.Match(f, ns); matched {
				blocked = true
				break
			}
			// Also check prefix matching for patterns like "system/*"
			prefix := strings.TrimSuffix(f, "*")
			if strings.HasSuffix(f, "*") && strings.HasPrefix(ns, prefix) {
				blocked = true
				break
			}
		}
		if blocked {
			warnings = append(warnings, fmt.Sprintf("namespace %q is forbidden and was stripped from plan", ns))
		} else {
			out = append(out, ns)
		}
	}
	return out, warnings
}

// stripUnpermittedNamespaces removes namespace patterns not covered by token globs.
func stripUnpermittedNamespaces(namespaces, permittedGlobs []string, warnings []string) ([]string, []string) {
	var out []string
	for _, ns := range namespaces {
		permitted := false
		for _, glob := range permittedGlobs {
			if glob == "*" || glob == ns {
				permitted = true
				break
			}
			if matched, _ := path.Match(glob, ns); matched {
				permitted = true
				break
			}
			// Check if namespace pattern is a sub-pattern of glob
			prefix := strings.TrimSuffix(glob, "*")
			nsStem := strings.TrimSuffix(ns, "*")
			if strings.HasSuffix(glob, "*") && strings.HasPrefix(nsStem, prefix) {
				permitted = true
				break
			}
		}
		if !permitted {
			warnings = append(warnings, fmt.Sprintf("namespace %q is outside token's permitted globs and was stripped", ns))
		} else {
			out = append(out, ns)
		}
	}
	return out, warnings
}

func hasWildcardGlob(globs []string) bool {
	for _, g := range globs {
		if g == "*" {
			return true
		}
	}
	return false
}

// sortedKeys returns the keys of a map in sorted order (for deterministic output in tests).
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

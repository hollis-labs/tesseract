package corpusmigration

import (
	"strings"

	"github.com/hollis-labs/tesseract/domains"
)

// Action specifies how a namespace or record is handled during migration.
type Action string

const (
	ActionRename     Action = "rename"
	ActionReclassify Action = "reclassify"
	ActionVerify     Action = "verify"
	ActionDeferred   Action = "deferred"
	ActionClassify   Action = "classify"
	ActionSkip       Action = "skip"
)

// RuleCategory categorizes the mapping rules per the N6 classification survey.
type RuleCategory string

const (
	CategorySessionRename          RuleCategory = "session_rename"
	CategoryProjectMemoryRename    RuleCategory = "project_memory_rename"
	CategoryProjectKnowledgeRename RuleCategory = "project_knowledge_rename"
	CategorySystemVerify           RuleCategory = "system_verify"
	CategorySessionCloseReclassify RuleCategory = "session_close_reclassify"
	CategoryHandoffReclassify      RuleCategory = "handoff_reclassify"
	CategoryBootPromptReclassify   RuleCategory = "boot_prompt_reclassify"
	CategoryAmbiguousHowWeWork     RuleCategory = "ambiguous_how_we_work"
	CategoryAmbiguousPackages      RuleCategory = "ambiguous_packages"
	CategoryAmbiguousPortfolio     RuleCategory = "ambiguous_portfolio"
	CategoryAmbiguousLowConfidence RuleCategory = "ambiguous_low_confidence"
	CategoryEventReasoningRouting  RuleCategory = "event_reasoning_routing"
	CategoryDeferredApp            RuleCategory = "deferred_app"
	CategoryDeferredTether         RuleCategory = "deferred_tether"
	CategoryUnmapped               RuleCategory = "unmapped"
)

// Decision contains the classification outcome for a namespace.
type Decision struct {
	Category     RuleCategory   `json:"category"`
	Action       Action         `json:"action"`
	SourceDomain domains.Domain `json:"source_domain"`
	TargetDomain domains.Domain `json:"target_domain"`
	OldNamespace string         `json:"old_namespace"`
	NewNamespace string         `json:"new_namespace"`
	TargetTags   []string       `json:"target_tags,omitempty"`
	IsAmbiguous  bool           `json:"is_ambiguous,omitempty"`
	Confidence   string         `json:"confidence,omitempty"` // "high" or "low"
	Reason       string         `json:"reason"`
}

// SpecifiedProjectSlugs is the explicit list of project slugs finalized in N6 Phase 1 and 2.
var SpecifiedProjectSlugs = map[string]struct{}{
	"cairn":       {},
	"cerberus":    {},
	"hadron":      {},
	"nanite":      {},
	"tachyon":     {},
	"tangent":     {},
	"torque":      {},
	"worldarcana": {},
	"agent-setup": {},
	"hollis_labs": {},
	"tesseract":   {},
	"design-kit":  {},
}

// HowWeWorkSubpaths is the set of relative subpaths under user/{id}/knowledge/
// mapped to system/knowledge/...
var HowWeWorkSubpaths = map[string]struct{}{
	"runbooks":               {},
	"playbook":               {},
	"playbook/orchestration": {},
	"ops":                    {},
	"agent-ops":              {},
	"orchestration":          {},
	"packages":               {},
}

// LowConfidenceSubpaths is the set of ambiguous subpaths mapped to system/knowledge/...
// with low confidence, flagged for explicit director review.
var LowConfidenceSubpaths = map[string]struct{}{
	"ideas":       {},
	"issues":      {},
	"probe":       {},
	"profile":     {},
	"projects":    {},
	"composition": {},
}

// ClassifyNamespace maps a namespace and domain to a migration Decision.
func ClassifyNamespace(ns string, domain domains.Domain) Decision {
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return Decision{
			Category:     CategoryUnmapped,
			Action:       ActionSkip,
			SourceDomain: domain,
			OldNamespace: ns,
			Reason:       "empty namespace",
		}
	}

	// 1. Deferred app namespaces: app/{id}/...
	if strings.HasPrefix(ns, "app/") || ns == "app" {
		return Decision{
			Category:     CategoryDeferredApp,
			Action:       ActionDeferred,
			SourceDomain: domain,
			TargetDomain: domain,
			OldNamespace: ns,
			NewNamespace: ns,
			Reason:       "app/* namespace deferred to per-app consultation",
		}
	}

	// 2. System namespaces: system/... or system
	if strings.HasPrefix(ns, "system/") || ns == "system" {
		return Decision{
			Category:     CategorySystemVerify,
			Action:       ActionVerify,
			SourceDomain: domain,
			TargetDomain: domain,
			OldNamespace: ns,
			NewNamespace: ns,
			Confidence:   "high",
			Reason:       "system namespace already correctly scoped",
		}
	}

	parts := strings.Split(ns, "/")

	// Check user/* namespace hierarchy
	if len(parts) >= 2 && parts[0] == "user" {
		// 3. Session-scoped: user/{id}/session/{sid}/... -> session/{sid}/...
		if len(parts) >= 4 && parts[2] == "session" {
			newNS := "session/" + strings.Join(parts[3:], "/")
			return Decision{
				Category:     CategorySessionRename,
				Action:       ActionRename,
				SourceDomain: domain,
				TargetDomain: domain,
				OldNamespace: ns,
				NewNamespace: newNS,
				Confidence:   "high",
				Reason:       "mechanical session-scoped rename (strip user/{id} prefix)",
			}
		}

		// 4. Project memory: user/{id}/project/{pid}/memory/... -> project/{pid}/memory/...
		if len(parts) >= 4 && parts[2] == "project" {
			newNS := "project/" + strings.Join(parts[3:], "/")
			return Decision{
				Category:     CategoryProjectMemoryRename,
				Action:       ActionRename,
				SourceDomain: domain,
				TargetDomain: domain,
				OldNamespace: ns,
				NewNamespace: newNS,
				Confidence:   "high",
				Reason:       "mechanical project memory rename (strip user/{id} prefix)",
			}
		}

		// Event namespace rules: user/{id}/event/reasoning
		if len(parts) >= 4 && parts[2] == "event" && parts[3] == "reasoning" {
			return Decision{
				Category:     CategoryEventReasoningRouting,
				Action:       ActionClassify,
				SourceDomain: domains.Event,
				TargetDomain: domains.Event,
				OldNamespace: ns,
				Confidence:   "high",
				Reason:       "directly-authored event reasoning log routed per-record via classifier",
			}
		}

		// Knowledge namespace rules: user/{id}/knowledge/...
		if len(parts) >= 4 && parts[2] == "knowledge" {
			subpath := strings.Join(parts[3:], "/")
			seg3 := parts[3]

			// 5. Deferred Tether knowledge: any namespace with segment "tether" under user/{id}/knowledge/
			isTether := false
			for _, p := range parts[3:] {
				if p == "tether" {
					isTether = true
					break
				}
			}
			if isTether {
				return Decision{
					Category:     CategoryDeferredTether,
					Action:       ActionDeferred,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Knowledge,
					OldNamespace: ns,
					NewNamespace: ns,
					Reason:       "tether knowledge namespace deferred to per-app consultation",
				}
			}

			// 6. Reclassify: session-close -> event domain (project/{project}/event/reasoning)
			if seg3 == "session-close" {
				targetProject := "default"
				if len(parts) >= 5 && parts[4] != "" {
					targetProject = parts[4]
				}
				newNS := "project/" + targetProject + "/event/reasoning"
				if len(parts) == 4 {
					newNS = "system/event/reasoning"
				}
				return Decision{
					Category:     CategorySessionCloseReclassify,
					Action:       ActionReclassify,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Event,
					OldNamespace: ns,
					NewNamespace: newNS,
					TargetTags:   []string{"session-close"},
					Confidence:   "high",
					Reason:       "reclassify session-close knowledge to event domain",
				}
			}

			// 7. Reclassify: handoff -> workspace domain (project/{project}/workspace/handoff)
			if seg3 == "handoff" {
				targetProject := "default"
				if len(parts) >= 5 && parts[4] != "" {
					targetProject = parts[4]
				}
				newNS := "project/" + targetProject + "/workspace/handoff"
				if len(parts) == 4 {
					newNS = "system/workspace/handoff"
				}
				return Decision{
					Category:     CategoryHandoffReclassify,
					Action:       ActionReclassify,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Domain("workspace"),
					OldNamespace: ns,
					NewNamespace: newNS,
					TargetTags:   []string{"handoff"},
					Confidence:   "high",
					Reason:       "reclassify handoff knowledge to workspace domain",
				}
			}

			// 8. Reclassify: boot-prompt -> workspace domain (project/{project}/workspace/boot-prompt)
			if seg3 == "boot-prompt" {
				targetProject := "default"
				if len(parts) >= 5 && parts[4] != "" {
					targetProject = parts[4]
				}
				newNS := "project/" + targetProject + "/workspace/boot-prompt"
				if len(parts) == 4 {
					newNS = "system/workspace/boot-prompt"
				}
				return Decision{
					Category:     CategoryBootPromptReclassify,
					Action:       ActionReclassify,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Domain("workspace"),
					OldNamespace: ns,
					NewNamespace: newNS,
					TargetTags:   []string{"boot-prompt"},
					Confidence:   "high",
					Reason:       "reclassify boot-prompt knowledge to workspace domain",
				}
			}

			// 9. Project-scoped knowledge: user/{id}/knowledge/{project-slug}/{kind}
			if _, ok := SpecifiedProjectSlugs[seg3]; ok {
				rest := ""
				if len(parts) > 4 {
					rest = "/" + strings.Join(parts[4:], "/")
				}
				newNS := "project/" + seg3 + "/knowledge" + rest
				return Decision{
					Category:     CategoryProjectKnowledgeRename,
					Action:       ActionRename,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Knowledge,
					OldNamespace: ns,
					NewNamespace: newNS,
					Confidence:   "high",
					Reason:       "project-scoped knowledge rename to project/{slug}/knowledge/{kind}",
				}
			}

			// 10. Ambiguous - How We Work -> system/knowledge/...
			if _, ok := HowWeWorkSubpaths[subpath]; ok {
				newNS := "system/knowledge/" + subpath
				return Decision{
					Category:     CategoryAmbiguousHowWeWork,
					Action:       ActionRename,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Knowledge,
					OldNamespace: ns,
					NewNamespace: newNS,
					IsAmbiguous:  true,
					Confidence:   "high",
					Reason:       "how-we-work knowledge reclassified to system/knowledge/...",
				}
			}

			// 11. Ambiguous - Packages: laravel/* and tools/* -> system/knowledge/packages/...
			if strings.HasPrefix(subpath, "laravel/") || strings.HasPrefix(subpath, "tools/") {
				newNS := "system/knowledge/packages/" + subpath
				return Decision{
					Category:     CategoryAmbiguousPackages,
					Action:       ActionRename,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Knowledge,
					OldNamespace: ns,
					NewNamespace: newNS,
					IsAmbiguous:  true,
					Confidence:   "high",
					Reason:       "package/tool reference knowledge mapped to system/knowledge/packages/...",
				}
			}

			// 12. Ambiguous - Portfolio -> project/hollis-labs-portfolio/knowledge/...
			if seg3 == "portfolio" {
				rel := strings.TrimPrefix(subpath, "portfolio")
				rel = strings.TrimPrefix(rel, "/")
				newNS := "project/hollis-labs-portfolio/knowledge"
				if rel != "" {
					newNS += "/" + rel
				}
				return Decision{
					Category:     CategoryAmbiguousPortfolio,
					Action:       ActionRename,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Knowledge,
					OldNamespace: ns,
					NewNamespace: newNS,
					IsAmbiguous:  true,
					Confidence:   "high",
					Reason:       "portfolio knowledge mapped to project/hollis-labs-portfolio/knowledge/... (PRJ-20260419-0001)",
				}
			}

			// 13. Ambiguous - Low Confidence -> system/knowledge/...
			if _, ok := LowConfidenceSubpaths[subpath]; ok {
				newNS := "system/knowledge/" + subpath
				return Decision{
					Category:     CategoryAmbiguousLowConfidence,
					Action:       ActionRename,
					SourceDomain: domains.Knowledge,
					TargetDomain: domains.Knowledge,
					OldNamespace: ns,
					NewNamespace: newNS,
					IsAmbiguous:  true,
					Confidence:   "low",
					Reason:       "low-confidence default to system/knowledge/... (flagged for director review)",
				}
			}
		}
	}

	// 14. Unmapped default
	return Decision{
		Category:     CategoryUnmapped,
		Action:       ActionSkip,
		SourceDomain: domain,
		OldNamespace: ns,
		Reason:       "no matching migration rule, skipped and reported",
	}
}

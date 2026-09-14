package corpusmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SignalTier indicates which signal tier resolved a classification or if there was a conflict.
type SignalTier string

const (
	Tier1ExplicitProjectTag SignalTier = "tier1_explicit_project_tag"
	Tier2BareProjectTag     SignalTier = "tier2_bare_project_tag"
	Tier3KeyPrefix          SignalTier = "tier3_key_prefix"
	Tier4SystemDefault      SignalTier = "tier4_system_default"
	TierConflict            SignalTier = "tier_conflict"
)

// CanonicalPortfolioProjects is the set of canonical project slugs fetched from Torque's PRJ-* list + design-kit.
var CanonicalPortfolioProjects = map[string]string{
	"agent-ops":             "agent-ops",
	"agent-stability-arc":   "agent-stability-arc",
	"airmedcred":            "airmedcred",
	"cairn":                 "cairn",
	"cerberus":              "cerberus",
	"chrispian":             "chrispian",
	"frag":                  "frag",
	"fragments-engine":      "fragments-engine",
	"glyph":                 "glyph",
	"hadron":                "hadron",
	"hollis-labs-libs":      "hollis-labs-libs",
	"hollis-labs-tools":     "hollis-labs-tools",
	"hollis-labs-portfolio": "hollis-labs-portfolio",
	"hollislabs-com":        "hollislabs-com",
	"ion":                   "ion",
	"loom":                  "loom",
	"nanite":                "nanite",
	"nil":                   "nil",
	"suds":                  "suds",
	"sigil":                 "sigil",
	"stack-explorer":        "stack-explorer",
	"tachyon":               "tachyon",
	"tangent":               "tangent",
	"tesseract":             "tesseract",
	"tether":                "tether",
	"tether-taxi":           "tether-taxi",
	"torque":                "torque",
	"worldarcana":           "worldarcana",
	"agent-setup":           "agent-setup",
	"fe-clipper":            "fe-clipper",
	"design-kit":            "design-kit",
}

// KnownProjectAliases maps common spelling variants, underscores, and historical aliases
// to their canonical project slugs.
var KnownProjectAliases = map[string]string{
	"suds-v2":               "suds",
	"suds_v2":               "suds",
	"world-arcana":          "worldarcana",
	"world_arcana":          "worldarcana",
	"hollislabs-web":        "hollislabs-com",
	"hollislabs_web":        "hollislabs-com",
	"hollislabs.com":        "hollislabs-com",
	"hollislabs":            "hollislabs-com",
	"tether-launcher":       "tether-taxi",
	"tether_launcher":       "tether-taxi",
	"hollis-labs-portfolio": "hollis-labs-portfolio",
	"hollis_labs_portfolio": "hollis-labs-portfolio",
	"hollislabs-portfolio":  "hollis-labs-portfolio",
	"hollis-labs":           "hollis-labs",
	"hollis_labs":           "hollis-labs",
}

// NormalizeProjectSlug normalizes underscores to hyphens, lowercases, trims, and applies canonical aliases.
func NormalizeProjectSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	if alias, ok := KnownProjectAliases[s]; ok {
		return alias
	}
	return s
}

// BacklogRecordInput represents one record to classify.
type BacklogRecordInput struct {
	MemoryID  string   `json:"memory_id"`
	Namespace string   `json:"namespace"`
	Domain    string   `json:"domain"`
	MemoryKey string   `json:"memory_key"`
	Summary   string   `json:"summary"`
	Tags      []string `json:"tags"`
}

// ClassificationResult represents the classification outcome for a record.
type ClassificationResult struct {
	MemoryID          string     `json:"memory_id"`
	Namespace         string     `json:"namespace"`
	MemoryKey         string     `json:"memory_key"`
	Summary           string     `json:"summary"`
	SummarySnippet    string     `json:"summary_snippet"` // first ~150 chars of summary
	Tags              []string   `json:"tags"`
	TargetDomain      string     `json:"target_domain"`
	TargetNamespace   string     `json:"target_namespace"`
	MatchedProject    string     `json:"matched_project,omitempty"`
	SignalTier        SignalTier `json:"signal_tier"`
	CandidateProjects []string   `json:"candidate_projects,omitempty"` // populated on conflict or multiple matches
	ConflictReason    string     `json:"conflict_reason,omitempty"`
}

// ClassificationReport aggregates the results of running the classifier.
type ClassificationReport struct {
	TotalRecords     int                    `json:"total_records"`
	PerProjectCounts map[string]int         `json:"per_project_counts"`
	PerTierCounts    map[SignalTier]int     `json:"per_tier_counts"`
	SystemDefaulted  []ClassificationResult `json:"system_defaulted"`
	Conflicts        []ClassificationResult `json:"conflicts"`
	Classified       []ClassificationResult `json:"classified,omitempty"`
}

// ClassifyRecord classifies a single backlog record according to the 4-tier signal precedence.
func ClassifyRecord(rec BacklogRecordInput) ClassificationResult {
	snippet := rec.Summary
	if len(snippet) > 150 {
		snippet = strings.TrimSpace(snippet[:150]) + "..."
	}

	tier1Projects := make(map[string]struct{})
	tier2Projects := make(map[string]struct{})
	tier3Projects := make(map[string]struct{})

	// 1. Tier 1: Explicit project:{slug} tags
	for _, rawTag := range rec.Tags {
		t := strings.ToLower(strings.TrimSpace(rawTag))
		if strings.HasPrefix(t, "project:") {
			rawSlug := strings.TrimPrefix(t, "project:")
			slug := NormalizeProjectSlug(rawSlug)
			if slug != "" {
				tier1Projects[slug] = struct{}{}
			}
		}
	}

	// 2. Tier 2: Bare {slug} tag matching a known portfolio project
	for _, rawTag := range rec.Tags {
		t := strings.ToLower(strings.TrimSpace(rawTag))
		if strings.HasPrefix(t, "project:") || strings.Contains(t, ":") {
			continue
		}
		slug := NormalizeProjectSlug(t)
		if canonical, ok := CanonicalPortfolioProjects[slug]; ok {
			tier2Projects[canonical] = struct{}{}
		}
	}

	// 3. Tier 3: memory_key prefix matching a known project slug ({slug}_ or {slug}.)
	if rec.MemoryKey != "" {
		lowerKey := strings.ToLower(rec.MemoryKey)
		// Try matching against known portfolio projects (longest slug first to avoid prefix collisions)
		var knownSlugs []string
		for slug := range CanonicalPortfolioProjects {
			knownSlugs = append(knownSlugs, slug)
		}
		sort.Slice(knownSlugs, func(i, j int) bool {
			return len(knownSlugs[i]) > len(knownSlugs[j])
		})

		for _, slug := range knownSlugs {
			// Check underscore join: e.g. "nanite_wave4_..." or "go_envelopes_..."
			underscoreJoinHyphen := slug + "_"
			underscoreJoinUnderscore := strings.ReplaceAll(slug, "-", "_") + "_"
			// Check dot join: e.g. "hadron.workflow_engine..."
			dotJoin := slug + "."
			dotJoinUnderscore := strings.ReplaceAll(slug, "-", "_") + "."

			if strings.HasPrefix(lowerKey, underscoreJoinHyphen) ||
				strings.HasPrefix(lowerKey, underscoreJoinUnderscore) ||
				strings.HasPrefix(lowerKey, dotJoin) ||
				strings.HasPrefix(lowerKey, dotJoinUnderscore) {
				tier3Projects[slug] = struct{}{}
				break // matched longest matching slug
			}
		}
	}

	// Gather all unique matched projects across tiers 1-3
	allCandidatesMap := make(map[string]struct{})
	for p := range tier1Projects {
		allCandidatesMap[p] = struct{}{}
	}
	for p := range tier2Projects {
		allCandidatesMap[p] = struct{}{}
	}
	for p := range tier3Projects {
		allCandidatesMap[p] = struct{}{}
	}

	var allCandidates []string
	for p := range allCandidatesMap {
		allCandidates = append(allCandidates, p)
	}
	sort.Strings(allCandidates)

	res := ClassificationResult{
		MemoryID:          rec.MemoryID,
		Namespace:         rec.Namespace,
		MemoryKey:         rec.MemoryKey,
		Summary:           rec.Summary,
		SummarySnippet:    snippet,
		Tags:              rec.Tags,
		CandidateProjects: allCandidates,
	}

	// Conflict detection: more than one distinct project matched across tiers 1-3
	if len(allCandidates) > 1 {
		res.SignalTier = TierConflict
		res.ConflictReason = fmt.Sprintf("multiple distinct candidate projects matched across signals: %s", strings.Join(allCandidates, ", "))
		res.TargetDomain = rec.Domain
		res.TargetNamespace = "(conflict - needs review)"
		return res
	}

	// Single project matched
	if len(allCandidates) == 1 {
		matched := allCandidates[0]
		res.MatchedProject = matched

		// Determine winning tier by precedence (first match wins)
		if _, ok := tier1Projects[matched]; ok {
			res.SignalTier = Tier1ExplicitProjectTag
		} else if _, ok := tier2Projects[matched]; ok {
			res.SignalTier = Tier2BareProjectTag
		} else {
			res.SignalTier = Tier3KeyPrefix
		}

		res.TargetDomain = rec.Domain
		res.TargetNamespace = deriveTargetNamespace(rec.Namespace, rec.Domain, matched)
		return res
	}

	// Tier 4: No match -> system default
	res.SignalTier = Tier4SystemDefault
	res.TargetDomain = rec.Domain
	res.TargetNamespace = deriveTargetNamespace(rec.Namespace, rec.Domain, "system")
	return res
}

func deriveTargetNamespace(oldNS, domain, projectOrSystem string) string {
	parts := strings.Split(oldNS, "/")

	// If projectOrSystem is "system"
	if projectOrSystem == "system" {
		if strings.HasSuffix(oldNS, "/event/reasoning") || oldNS == "user/chrispian/event/reasoning" {
			return "system/event/reasoning"
		}
		// e.g. user/chrispian/memory/{type} -> system/memory/{type}
		if len(parts) >= 4 && parts[2] == "memory" {
			memType := parts[3]
			return "system/memory/" + memType
		}
		return "system/" + domain
	}

	// Target project
	if strings.HasSuffix(oldNS, "/event/reasoning") || oldNS == "user/chrispian/event/reasoning" {
		return "project/" + projectOrSystem + "/event/reasoning"
	}
	if len(parts) >= 4 && parts[2] == "memory" {
		memType := parts[3]
		return "project/" + projectOrSystem + "/memory/" + memType
	}
	return "project/" + projectOrSystem + "/" + domain
}

// ClassifyBacklog reads all records from user/chrispian/memory/* and user/chrispian/event/reasoning
// and returns the complete classification report without writing anything to the database.
func ClassifyBacklog(ctx context.Context, db *sql.DB) (*ClassificationReport, error) {
	query := `
SELECT 
    s.memory_id,
    s.namespace,
    s.domain,
    COALESCE(s.memory_key, ''),
    COALESCE(r.payload_summary, ''),
    COALESCE(r.tags, '[]')
FROM memory_state s
LEFT JOIN memory_revisions r ON r.revision_id = s.current_revision
WHERE (s.namespace LIKE 'user/chrispian/memory/%' OR s.namespace = 'user/chrispian/event/reasoning')
  AND s.current_revision IS NOT NULL AND (r.status IS NULL OR r.status <> 'deprecated')
ORDER BY s.namespace, s.memory_key, s.memory_id`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query backlog records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	report := &ClassificationReport{
		PerProjectCounts: make(map[string]int),
		PerTierCounts:    make(map[SignalTier]int),
	}

	for rows.Next() {
		var rec BacklogRecordInput
		var tagsJSON string
		if scanErr := rows.Scan(&rec.MemoryID, &rec.Namespace, &rec.Domain, &rec.MemoryKey, &rec.Summary, &tagsJSON); scanErr != nil {
			return nil, fmt.Errorf("scan backlog record: %w", scanErr)
		}
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &rec.Tags)
		}

		res := ClassifyRecord(rec)
		report.TotalRecords++
		report.PerTierCounts[res.SignalTier]++
		switch res.SignalTier {
		case TierConflict:
			report.Conflicts = append(report.Conflicts, res)
			report.PerProjectCounts["(conflict)"]++
		case Tier4SystemDefault:
			report.SystemDefaulted = append(report.SystemDefaulted, res)
			report.PerProjectCounts["system"]++
		default:
			report.PerProjectCounts[res.MatchedProject]++
		}

		report.Classified = append(report.Classified, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate backlog records: %w", err)
	}

	return report, nil
}

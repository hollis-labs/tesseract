package typeregistry

// The shipped vocabularies.
//
// These are the defaults a fresh install runs on and the baseline types.yaml
// revises. They are in Go, and that is not a contradiction of
// [[config_is_policy_code_is_engine]]: the engine ships a working policy, the
// operator owns changing it. What moved to config is the ability to change
// these without a release — not the requirement to restate them in a file
// before the service will start.

// DefaultVocabularies returns the shipped vocabularies.
func DefaultVocabularies() []Vocabulary {
	return []Vocabulary{
		defaultContextRecordTypes(),
		defaultMemoryTypes(),
		defaultKnowledgeFacetKinds(),
		defaultEventTypes(),
	}
}

// defaultEventTypes is the {type} segment of an event namespace
// (CW-20260909-0035).
//
// The segment names the STREAM, not a taxonomy of what happened. The test a
// value has to pass is "do I routinely read this partition whole, or exclude
// it whole" — and exactly one distinction passes it: the two producers. Asking
// for the journal must not return agent reasoning, and at the volumes an event
// log reaches the reasoning stream would drown the journal in any mixed read.
// Finer classification is what tags and the registry's other axes are for;
// this is the partition.
//
// Deliberately two values, not three. `friction` is the obvious candidate —
// the 56 process_friction notes in user/chrispian/memory/notes are an early
// instance of this shape — and it is left out because nothing writes it yet.
// Shipping a vocabulary entry no producer can fill is the `playbook` mistake
// recorded in [[tesseract_three_domains_equal_importance]]: an agent reads the
// vocabulary, believes the partition is populated, queries, and cannot tell
// "none exists" from "not implemented". The vocabulary is config-driven, so
// adding it the day the friction skill writes events is an edit to types.yaml
// rather than a release.
func defaultEventTypes() Vocabulary {
	return Vocabulary{
		VocabularyID: VocabEventType,
		Closed:       true,
		Types: []Type{
			{TypeID: "journal"},
			{TypeID: "reasoning"},
		},
	}
}

// defaultMemoryTypes is the {type} segment of a memory namespace.
//
// Was memory.DefaultTypeAllowlist, a Go slice whose own comment called it
// "config-driven" when it was not — CW-20260909-0027, closed by this move.
// The list is locked by decision in CW-20260519-0029 (sprint SP-20260518-0012,
// "Memory namespace shallow + faceted"); `notes` is the deliberate catch-all
// for memories that carry no stronger type. Revise it here or in types.yaml,
// not by widening the parser.
func defaultMemoryTypes() Vocabulary {
	return Vocabulary{
		VocabularyID: VocabMemoryType,
		Closed:       true,
		Types: []Type{
			{TypeID: "decisions"},
			{TypeID: "feedback"},
			{TypeID: "followups"},
			{TypeID: "learnings"},
			{TypeID: "limitations"},
			{TypeID: "notes"},
			{TypeID: "outcomes"},
			{TypeID: "references"},
		},
	}
}

// defaultKnowledgeFacetKinds is the knowledge domain's facet_kind vocabulary.
//
// Was a closed Go map in internal/memory/kinds.go. Closed:true is now a
// DECLARATION rather than a consequence of the container someone picked, which
// is the substance of [[tesseract_vocabularies_fold_into_registry]]. The
// enforcement point did not move: memory.WriteRevision still rejects a kind
// outside this set, at the persistence boundary, naming the allowed values.
//
// The set is the taxonomy locked 2026-05-14 (nine kinds), plus `mcp_server` and
// `investigation` promoted 2026-08-25 because a shipped producer emits each
// systematically, plus `wiki_page` added here.
//
// `wiki_page` is a compiled OKF page — a compiler, a template, a provenance
// chain and a link graph. Not `doc` (an external documentation reference) and
// not `note` (a generic agent note); filing it as either discards the signal
// that it is compiled output, the argument that earned `investigation` its
// place. It was added on approval rather than on first emission, against the
// usual rule, because Loom is BUILT AND BLOCKED waiting for it: the rule exists
// to stop a vocabulary filling with entries nothing writes, and a stalled
// compiler is the opposite case. See [[tesseract_wiki_page_kind_approved]].
//
// Three kinds are canonical but unpopulated — `playbook`, `learning` and
// `handoff`. They stay writable on purpose: a vocabulary naming only what
// already exists could never be written into, which is the readable-but-
// unwritable trap the 2026-08-25 normalization existed to remove.
//
// Naming rule: snake_case. Adding a kind is a governed change — the vocabulary
// and the [[kinds_taxonomy]] record are revised as one change.
func defaultKnowledgeFacetKinds() Vocabulary {
	return Vocabulary{
		VocabularyID: VocabKnowledgeFacetKind,
		Closed:       true,
		Types: []Type{
			{TypeID: "doc"},
			{TypeID: "handoff"},
			{TypeID: "investigation"},
			{TypeID: "learning"},
			{TypeID: "mcp_server"},
			{TypeID: "note"},
			{TypeID: "package"},
			{TypeID: "playbook"},
			{TypeID: "pointer"},
			{TypeID: "project_canonical"},
			{TypeID: "session_close"},
			{TypeID: "wiki_page"},
		},
	}
}

// defaultContextRecordTypes is the context store's record_type vocabulary —
// the MVP core types, carried over from contexttypes.DefaultTypes.
//
// Two fields did NOT come across, and both were live here:
//
//   - retrieval_rank_bias, which every type declared and the typed-view
//     ranking multiplied in. Typed views now rank on status weight alone.
//   - promotion_rules, which only decision/adr declared, as
//     "draft->reviewed:requires_human_approval". That guard was already
//     vacuous — every caller of the promote path defaults actor to "user"
//     when the field is absent, so it fired only for a caller that
//     volunteered a non-user actor.
//
// See [[tesseract_type_declaration_field_set]]. The context store retires in
// CW-20260909-0037 and takes this vocabulary with it.
//
// OpenPrefixes keeps the `custom/` escape hatch this surface has always had.
func defaultContextRecordTypes() Vocabulary {
	return Vocabulary{
		VocabularyID: VocabContextRecordType,
		Closed:       true,
		OpenPrefixes: []string{"custom/"},
		Types: []Type{
			// Strategy
			{TypeID: "strategy/goal", AllowedStatuses: allStatuses()},
			{TypeID: "strategy/constraints", AllowedStatuses: allStatuses()},
			{TypeID: "strategy/roadmap", AllowedStatuses: allStatuses()},
			{TypeID: "system/map", AllowedStatuses: allStatuses()},
			// Execution
			{TypeID: "task/spec", AllowedStatuses: allStatuses(), RequiredFields: []string{"title"}},
			{TypeID: "runbook", AllowedStatuses: allStatuses()},
			{TypeID: "contract/api", AllowedStatuses: allStatuses()},
			{TypeID: "contract/data", AllowedStatuses: allStatuses()},
			// Knowledge
			{TypeID: "decision/adr", AllowedStatuses: allStatuses()},
			{TypeID: "brief/summary", AllowedStatuses: allStatuses(), DefaultTTL: "2160h"},    // 90 days
			{TypeID: "note/volatile", AllowedStatuses: []string{"draft"}, DefaultTTL: "336h"}, // 14 days
			// Governance
			{TypeID: "principles", AllowedStatuses: allStatuses()},
			// Session
			{TypeID: "session/snapshot", AllowedStatuses: allStatuses(), DefaultTTL: "720h", RequiredFields: []string{"summary"}}, // 30 days
			// Configuration
			{TypeID: "config/service", AllowedStatuses: allStatuses()},
			// Identity
			{TypeID: "project/identity", AllowedStatuses: allStatuses(), RequiredFields: []string{"name"}},
		},
	}
}

func allStatuses() []string {
	return []string{"draft", "reviewed", "canonical", "deprecated"}
}

// DefaultViews returns the MVP view presets over context records.
func DefaultViews() []ViewDef {
	return []ViewDef{
		{
			ViewID:   "task_exec",
			Types:    []string{"task/spec", "contract/api", "contract/data", "decision/adr", "runbook", "system/map"},
			MaxItems: 50,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.8,
				"draft":      0.5,
				"deprecated": 0.1,
			},
		},
		{
			ViewID:   "strategy",
			Types:    []string{"strategy/goal", "strategy/constraints", "strategy/roadmap", "decision/adr", "system/map"},
			MaxItems: 30,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.8,
				"draft":      0.5,
				"deprecated": 0.1,
			},
		},
		{
			ViewID:   "agent_boot",
			Types:    []string{"system/map", "principles", "strategy/constraints", "contract/api", "contract/data"},
			MaxItems: 20,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.9,
				"draft":      0.4,
				"deprecated": 0.0,
			},
		},
		{
			ViewID:   "briefing",
			Types:    []string{"brief/summary", "decision/adr", "system/map", "strategy/goal"},
			MaxItems: 25,
			RankWeights: map[string]float64{
				"canonical":  1.0,
				"reviewed":   0.8,
				"draft":      0.5,
				"deprecated": 0.1,
			},
		},
	}
}

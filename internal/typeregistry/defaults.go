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
// Deliberately two values, not three. `friction` was the obvious candidate and
// was SETTLED AGAINST on 2026-09-11 (CW-20260910-0059), on the stream test
// above rather than on the absence of a producer:
//
//   - Friction is read whole — aggregate review is its stated purpose — but
//     nothing needs it excluded whole, which is the half that earns a path
//     segment.
//   - The partition axis here is the PRODUCER. Friction is agent-written
//     commentary about process: a subject, not a producer. Admitting it makes
//     {type} a mixed axis, where two values answer "who wrote this" and one
//     answers "what is it about".
//   - The volume argument that earns {type} runs the other way. A segment beats
//     a tag filter when the thing being filtered out is one to two orders of
//     magnitude larger than the thing wanted — which is true of reasoning
//     against journal, and false of friction against anything. Friction is a
//     rounding error inside the reasoning stream, so the tag filter is the
//     cheap shape, not the expensive one.
//
// `/process-friction` therefore writes user/{id}/event/reasoning tagged
// `friction`, which is what this comment already says finer classification is
// for. Count the corpus rather than trusting a number here — the figure this
// comment used to carry was ten short after a day:
//
//	SELECT COUNT(*) FROM memory_state s
//	  JOIN memory_revisions r ON r.revision_id = s.current_revision
//	 WHERE s.namespace = 'user/chrispian/memory/notes'
//	   AND r.tags LIKE '%process_friction%';
//
// Those historical notes do NOT migrate: domain is stamped at creation and
// resolveOrCreateMemory refuses a change, so friction written before the switch
// stays in memory permanently ([[domain_is_immutable_no_migration_path]]).
//
// What would reopen this: friction volume rising to where excluding it from a
// reasoning read is a routine need rather than a hypothetical one. That is
// measurable, not a matter of taste — compare the tagged subset against the
// stream that holds it.
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
//
// `references` was retired 2026-09-10 (CW-20260910-0067). It meant "pointers to
// where information lives", which is content you go TO and therefore knowledge —
// the `pointer` and `doc` kinds already carry it. The corpus agreed: of the ten
// entries filed under it, roughly half were reference material that belongs in
// knowledge and the rest were dated findings that belong under `outcomes`,
// `limitations` or `learnings`. Not one of them needed the type to exist. The
// name also collided with the `references` link relation, so retiring it leaves
// exactly one meaning of the word in Tesseract. See
// docs/knowledge-memory-boundary.md for the corpus test.
//
// Retiring a type does NOT make its rows unreadable: the vocabulary is checked
// on the write path only (memory.ParseNamespace, reached from
// memory.Store.WriteRevision), and recall filters namespaces as strings. The
// ten existing entries stay readable by recall and by tesseract_get; what stops
// is writing a NEW revision under that namespace.
//
// `todos` was added 2026-09-10 (CW-20260909-0036) and is the first type in any
// vocabulary that declares hot_fields — the first STRUCTURED OBJECT, in other
// words, rather than a classification of prose. Its lifecycle lives in the
// revision's consumer_state bag; its content lives where every other type's
// does, title in payload.summary and notes in payload.body.
//
// Todos belong here and tasks do not, per [[task_is_not_todo]]: a Torque task
// is FSM-governed tracked work with dispatch, budgets and dependencies, and a
// todo is a flat list item with light state that is often ephemeral. They share
// an English word and nothing that matters. Tesseract does not mirror Torque's
// entities or their lifecycle, and nothing here should ever grow a transition
// rule that starts it.
//
// The field set is NIL's store/models.go `Item`, which is a working model
// rather than a guess ([[nil_personal_task_note_management_direction]]): kind,
// section, pinned, completed, archived, priority, due_at, threshold_at,
// recurrence_rule, inbox and external_ref in consumer_state.
//
// No required_fields, deliberately. The mechanism works — see
// memory.validateConsumerStateFor — and declaring one here before NIL has
// migrated would refuse exactly the rows the migration exists to move.
// Requiring a field is a decision to take once a consumer is writing, not one
// to ship ahead of it.
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
			{TypeID: "todos", HotFields: todoHotFields()},
		},
	}
}

// todoHotFields is the `todos` type's declared hot fields — the consumer_state
// keys that carry an index.
//
// Four, not eleven. Per [[tesseract_registry_index_ddl_constrained]] the
// accepted trade-off is that adding a type is free while indexing one costs a
// release, on the reading that "a type earns an index after it has query load,
// not on day one." These four are the ones a working list cannot be drawn
// without:
//
//	kind          todo | note | scratch — NIL's list is filtered by it constantly
//	section       now | soon | anytime — the partition the UI is built around
//	completed     open vs done, the predicate every view applies
//	external_ref  the idempotency correlation, looked up by exact value on
//	              every re-push; unindexed that is a full scan per write
//
// `archived`, `inbox`, `pinned` and `priority` are filterable without being
// indexed — the declaration governs which fields carry an index, not which
// ones a caller may name.
//
// `due_at` is deliberately absent even though it is the obvious fifth. Recall
// filters consumer_state by SET MEMBERSHIP only; there is no range predicate,
// so `due before tomorrow` cannot be expressed and an index on it would serve
// no query anyone can write. That is the `playbook` mistake in
// [[tesseract_three_domains_equal_importance]] wearing a different hat —
// shipping a declaration nothing can use reads, to whoever finds it, exactly
// like a capability. It earns its index the day ranges land.
//
// These four names are also the migration's contract:
// TestDeclaredHotFieldsAreMaterializedByMigration binds this list to the four
// index statements in internal/contextstore migration 19, so neither can move
// without the other. This comment does not spell those statements out, and
// cannot: TestRegistryLoaderContainsNoDDL scans this package for the words of a
// schema statement, on the reading that a registry which GENERATES DDL has
// already taken the decision gate G2 refused even if another package runs it.
func todoHotFields() []string {
	return []string{"completed", "external_ref", "kind", "section"}
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
// systematically, plus `wiki_page` and `boot_prompt` added here, minus
// `learning` retired 2026-09-10.
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
// `boot_prompt` is a rendered briefing an agent authored for Chrispian to hand
// to another agent — prose, not slots, addressed by id and never searched for.
// Added on Chrispian's direction 2026-09-10 (CW-20260910-0068). It is NOT the
// agent's identity and NOT its materialized boot directory: an agent is a
// profile plus scope plus args ([[local_agent_object_model]]), and the boot dir
// under agent-workspaces/boot/<project>/ is session state that rots in hours
// ([[feedback_boot_prompt_layout]]). Both of those are regenerable from config
// and stay on the filesystem. This kind holds the half that is not — the
// judgment about what the next agent must be told, which is why the corrected
// premise in this task's own boot prompt could not have been re-derived.
//
// No MCP tool was added with it. `tesseract_get` already takes domain +
// namespace + key across every domain, so a `tesseract_boot` would be a second
// way to do a thing that works; the skill carries the semantic mapping instead.
// Worth knowing at the call site: `tesseract_get` reinforces activation under
// `memory` but not under `knowledge`, so re-reading a popular boot prompt does
// not climb it up recall rankings. That is the wanted behavior here, not a
// gap — this kind is addressed, never discovered.
//
// `learning` was retired by CW-20260910-0067. It duplicated the `learnings`
// memory type across a domain boundary that cannot be crossed afterwards — one
// letter apart, and an agent that guessed wrong could never move the record.
// Under the boundary in docs/knowledge-memory-boundary.md a distilled lesson is
// memory: you do not know it exists until recall surfaces it while you are
// working nearby. `learnings` is live (38 revisions, still being written);
// `learning` held two, both written the same hour of 2026-09-05 and both in
// substance investigation dossiers.
//
// Removing it leaves those two carrying a value the vocabulary no longer names.
// That is a read-side cost only — the vocabulary is checked on the write path
// (knowledgePolicy.ValidateFacets) and recall's facet_kinds filter is plain
// SQL, so `facet_kinds: ["learning"]` still returns them. Re-filing them as
// `investigation` is a corpus edit, filed rather than done here.
//
// Two kinds are canonical and unpopulated: `wiki_page`, waiting on its first
// Loom emission, and `boot_prompt`, added the day it was specified. Both stay
// writable on purpose — a vocabulary naming only what already exists could
// never be written into, which is the readable-but-unwritable trap the
// 2026-08-25 normalization existed to remove. The other ten all have corpus
// entries as of 2026-09-10; `playbook`, `learning` and `handoff` were described
// as unpopulated here and in facets-and-kinds.md long after they had been
// seeded, which is what made three healthy kinds look mis-filed. So do not read
// this paragraph as current: count them (`tesseract_recall` with `facet_kinds`
// and `estimate_only`) before you rely on a number written in a comment.
//
// Naming rule: snake_case. Adding a kind is a governed change — the vocabulary
// and the [[kinds_taxonomy]] record are revised as one change.
func defaultKnowledgeFacetKinds() Vocabulary {
	return Vocabulary{
		VocabularyID: VocabKnowledgeFacetKind,
		Closed:       true,
		Types: []Type{
			{TypeID: "boot_prompt"},
			{TypeID: "doc"},
			{TypeID: "handoff"},
			{TypeID: "investigation"},
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

# Agent-facing prose — audit

**Criterion.** Agent-facing normative prose is *guidance with its reason
attached*, not rules. Per `agent_guidance_over_rules` (canonical, 2026-09-10):
an agent that finds something shaped like a rule stops reasoning and starts
enforcing. Two questions, per clause:

1. **Does it state its reason, or only its rule?** A rule only covers the cases
   it enumerates. A reason lets an agent tell whether the case in front of it is
   the one the guidance was written for.
2. **Does it say where it stops applying?** Prose that only pushes produces
   agents that over-apply it.

**Scope.** Tesseract's own agent-facing content: MCP tool descriptions in
`internal/mcpadapter/*.go`, the twelve skills in `internal/mcpadapter/skills/`,
`internal/mcpadapter/toolvocab.go`, `AGENTS.md`, and the agent-facing half of
`docs/`. **Not** the wider sweep of skills, `AGENTS.md` files, templates and
hooks — that is CW-20260910-0062 on agent-setup, which owns those.

**Status 2026-09-10.** Seven factual defects found; six fixed here, one left
open because the honest fix is a code change rather than a prose change. On
style, the corpus is **substantially more conformant than the task assumed** —
question 1 passes nearly everywhere, question 2 fails in a specific and
predictable place. Two style changes applied as worked examples; the rest is
proposed, not applied.

**Confidence is marked** where I am unsure, rather than dropping the finding.

---

## The discriminators that survived contact

Three, and the first is what stops this audit from generating thirty false
positives.

### 1. The criterion applies to normative clauses, not to descriptions

`context_search`'s whole description is *"Semantic search across records using
embeddings. Returns ranked results by cosine similarity. Requires a configured
embedding provider."* Asking "does it state its reason?" of that sentence is a
category error — it is not telling an agent what to do, so there is no rule to
attach a reason to. It is thin, which is a different complaint with a different
remedy.

**The pre-filter: is this clause telling an agent what to do or not do?** If
not, the two questions do not apply. Roughly half the MCP tool surface — most of
the `context_*` family — is descriptive, and passing it through the criterion
unfiltered would have produced a long list of findings that are all the same
non-finding.

### 2. Question 1 mostly passes; question 2 mostly fails — and that is structural

This is the sharpest result in the audit, and it was not the expected one.

**Q1 is visible while you write.** You notice that you have written a bare
imperative, because the sentence feels short. Tesseract's authors clearly did
notice: the reason is attached almost everywhere it matters.

**Q2 is only visible when you imagine the reader over-applying you.** Nothing
about writing *"Prefer this BEFORE filesystem or web exploration"* prompts the
author to ask what happens when recall comes back empty, or when the recalled
record disagrees with what the user just said. So Q2 needs a checklist and Q1
does not.

Concretely: of the clauses that survived the pre-filter, Q1 fails in **two**
places. Q2 fails or is silent in **most** of the strong ones — including the
single highest-traffic sentence on the surface.

### 3. Form beats content across section boundaries — so the unit is the clause

`facets-and-kinds.md` already carried the governance bar **in guidance form,
with its reason and its exception**, in the section that introduces it. Forty
lines later the same bar was restated as a bare imperative — *"**Earn it.** A
kind is worth adding when…"* — with no reason and no exception.

That is exactly the failure `agent_guidance_over_rules` records: *"`kinds_taxonomy`
rev 6 already contained the diagnosis… and the failure recurred anyway, because
the warning was prose and the governance rule was a rule. Content in one section
does not defeat form in another."*

**So a per-document verdict is worthless.** The document that produced the wrong
default twice in twenty-four hours would have passed one.

---

## Factual defects — these outrank everything else

A style verdict on prose that is wrong is wasted. These were separated out and
fixed first.

| # | Defect | Where | Status | Confidence |
|---|---|---|---|---|
| F1 | Domain choice is one-way and nothing said so | `start-here.md`, `revisions.md` | **Fixed** | **High** |
| F2 | "memory and knowledge write audit is in flight" — it is live | `start-here.md` | **Fixed** | **High** |
| F3 | "audit emission for memory deprecations is in flight" — it is live | `revisions.md` | **Fixed** | **High** |
| F4 | "memory and knowledge revisions share one table" — Event is a third | `crossdomain_read_tools.go` ×2, `revisions.md` | **Fixed** | **High** |
| F5 | `memory.deprecate` fires for knowledge and event revisions too | `audit.md` | **Fixed in prose; code defect open** | **High** |
| F6 | Audit helper list and event-type list both incomplete | `audit.md` | **Fixed** | **High** |
| F7 | `event_*` missing from the tool inventory; touch guidance narrower than the tool | `docs/AGENT-SETUP.md`, `docs/CONTEXT-FOR-PROJECTS.md` | **Fixed** | **High** |

### F1 — the domain choice is one-way, and no agent-facing prose said so

`internal/memory/write.go:345` refuses a domain change outright, and
`memory_state.domain` is set once at creation. So **a record's domain is
immutable for its whole lineage.** The promotion workflow is the obvious escape
and it is the wrong shape — it moves records across *namespaces*, not domains.
Rewriting under a new identity works and discards the supersede chain, the
`created_at` history and the lineage edges.

Nothing in `start-here.md`, the domain skills, or any tool description said the
choice was one-way. An agent picking a domain had no way to know the cost of
picking wrong. Recorded in `domain_is_immutable_no_migration_path`; now stated
in `start-here.md` beside the three-way fork, and in `revisions.md` under "What
NOT to expect".

Written **as guidance, deliberately**: the constraint is real, but the useful
posture is one moment of thought at write time, not paralysis. `notes` and
`note` are honest catch-alls, and a record in a defensible domain is better left
where it is than migrated at the cost of its lineage.

### F5 — `memory.deprecate` is domain-blind, and this one is a code defect

`Store.Deprecate` (`internal/memory/promote.go:196`) emits its audit event with
`string(domains.Memory)` hard-coded, whatever domain the revision actually
belongs to. So deprecating a **knowledge** or **event** revision writes a
`memory.deprecate` row. There is no `knowledge.deprecate` or `event.deprecate`
to filter on, and an agent reconstructing a knowledge retraction from the log
finds nothing under the name it would reach for.

`state.Domain` is loaded two lines above the emit and is simply not used. This is
the same shape as the defect CW-20260909-0033 removed elsewhere — the comment on
`internal/memory/write.go:302` says so in as many words: *"a domain without an arm
produced an audit row naming the wrong domain — a log that lies is worse than one
that is missing."*

**`audit.md` now describes the actual behaviour**, because a doc that describes
what the code does is always correct. **The code fix is proposed, not applied**:
it changes what lands in the audit table, which is a behaviour change and not a
prose sweep's call to make. The fix is one argument —

```go
_ = s.auditSink.EmitRevision(ctx, string(state.Domain), auditOpDeprecate, ...)
```

— and `EmitRevision` already composes `{domain}.{op}` freely, so no constant
needs adding. Filed as a follow-up.

### F6 — two more gaps between the log and what happened

`audit.md` already documented one place where MCP and HTTP emit differently
(`status_deprecate`). Two more sit in the same blind spot and were undocumented:

- **`context_namespace_register` over MCP emits nothing.** It calls
  `UpsertNamespacePolicy` directly, which has no emit. The HTTP peer emits
  `namespace.register`. Confidence: **high** — verified at
  `internal/mcpadapter/tools.go:1255` against `internal/contextapi/server.go:804`.
- **`namespace.register` arrives unasked.** The first memory, knowledge or event
  write into a namespace with no policy row auto-registers it and emits the
  event with `source: "inferred"`. An agent reading its own audit trail will see
  registrations it did not request. Confidence: **high** —
  `internal/memory/write.go:57` → `ensureNamespaceRegistered`.

The helper list also omitted `EmitRevision` — the one that replaced the six
per-domain memory/knowledge helpers — along with `EmitNamespaceRegister` and
`EmitNamespaceUpdate`. All three added.

---

## Per-surface verdicts

Verdicts are per clause, not per document, for the reason in discriminator 3.
"Pass" against Q2 includes clauses that are invariants, where bounding would be
wrong — see [What stays a rule](#what-stays-a-rule).

### MCP tool descriptions

| Surface | Q1: reason? | Q2: where it stops? | Note |
|---|---|---|---|
| `tesseract_recall` | **Pass** | **FAIL → fixed** | The one Q2 gap that matters. See below. |
| `tesseract_touch` | **Pass** | **Pass** | The in-repo exemplar. Names the failure mode *and* bounds itself. |
| `event_write` | **Pass** | **Pass** | "would a trace already have this?" is both reason and bound. |
| `event_list` | **Pass** | **Pass** | "No total, deliberately" — states the cost it is avoiding. |
| `memory_write`, `knowledge_write`, `memory_promote` | **Pass** | Silent | "Read this first" carries its reason (*"faster than finding that out one validation_error at a time"*). No permission to skip on a second write in the same session; low cost. |
| `tesseract_get`, `tesseract_history`, `tesseract_get_revision`, `tesseract_deprecate` | **Pass** | n/a | Mechanical. Reasons given where behaviour would surprise. |
| `context_namespace_register` | **Pass** | n/a | *"not a per-call mistake, it changes who may write there afterwards"* — reason attached to a consequential act. |
| `context_write`, `context_typed_write`, `context_status_set`, `context_ingest` | **Pass** | n/a | Normative clauses carry their consequence. |
| `context_embed`, `context_search`, `context_estimate`, `context_rag_query`, `context_typed_view`, `context_registry_list`, `context_promotion_list`, `context_audit_list`, `context_view`, `context_plan`, `context_pack`, `context_session_write`, `tesseract_skills` | n/a — descriptive | n/a | Thin, not rule-shaped. Filtered out by discriminator 1. See [Thin, not wrong](#thin-not-wrong). |

**`tesseract_recall` was the single clearest Q2 failure in the corpus, and it
sat on the highest-traffic tool.** The description said:

> **Prefer this BEFORE filesystem or web exploration.**

A push with no bound at all. `search-first` — the reference implementation named
in the task — bounds precisely this sentence, in two directions Tesseract's did
not: *"Sparse or low-confidence results → fall back to filesystem/web as normal"*
and *"It is not a ruling on today's instruction… Don't lead with 'this
contradicts ADR X, are you sure?'"*

Two failure modes were reachable from the unbounded version: an agent treating an
empty recall as evidence of absence, and an agent treating a `canonical` record as
outranking a live instruction. **Fixed** — `search-first`'s "What a hit is evidence
of" transplanted as a bullet, both directions stated. This is style change **A**.

### Skills corpus

| Skill | Q1 | Q2 | Note |
|---|---|---|---|
| `knowledge.md` | **Pass** | **Pass** | The best doc in the corpus. Reason (*"the pointer is the half that rots"*), failure named (*"an entry whose body is a stub and whose pointer is dead carries nothing — that is the failure mode this guidance exists to prevent"*), and the bound (*"Reach for a real `file:` pointer when the external thing genuinely is the artifact"*). |
| `memory.md` | **Pass** | **Pass** | *"This is the default shape of a turn"* is rule-shaped, and the very next heading is **"Why the third step exists at all."** Bounded by *"Under-reporting is fine."* |
| `event.md` | **Pass** | **Pass** | The not-telemetry test states its own negative case. |
| `recall-and-ranking.md` | **Pass** | **Pass** | Dense and reasoned throughout; few normative claims to bound. |
| `promotion.md` | **Pass** | n/a | *"## Why this exists — User sovereignty / Audit trail / Deferred decisions."* An invariant **with** its reason is still an invariant, and now an agent knows why not to route around it. This is the right treatment of a rule. |
| `start-here.md` | **Pass** | **Pass** | **"## Invariants (don't fight these)"** — the section header *is* the bound. It tells an agent which five clauses are rules, and by omission that the rest are not. The most economical instance of the discriminator in the repo. |
| `facets-and-kinds.md` | **Split — one pass, one FAIL → fixed** | Split | See below. |
| `namespaces.md` | **Pass** | n/a | "## Two authority rules" — correctly rules. The tier table's `Purpose` column gives no reason for the ownership assignment; low value, low risk. Confidence: **medium** that this is worth changing. |
| `revisions.md`, `audit.md`, `views.md`, `context-packet.md` | n/a — mechanical | n/a | `revisions.md`'s "What NOT to expect" and `views.md`'s "What views don't do" are good negative space. |

**`facets-and-kinds.md` is the live instance.** Its "Adding a kind" section
already reads as guidance with the reason and the exception attached:

> *the rule exists to stop a vocabulary filling with entries nothing writes, and
> a stalled producer is the opposite case*

That is `kinds_taxonomy` rev 7's restatement, already landed. **Confirmed
conformant — reported as a real finding, not a non-answer.**

Forty lines later, the same bar was restated as a bare imperative with neither:

> - **Earn it.** A kind is worth adding when something emits it systematically
>   and filing those records under an existing kind would discard information.

This is the clause an agent scanning "Naming rules for a proposed kind" actually
hits, and it is rule-shaped. Given that this exact bar produced the wrong default
twice in twenty-four hours — `wiki_page` needing an operator override, `friction`
producing a circular deadlock — leaving a bare restatement in the same document
is the documented failure mode sitting in the documented location. **Fixed** —
the bullet now carries the reason and names the stalled-producer case as the
exception it is. This is style change **B**.

### `toolvocab.go` — not agent-facing, and an exemplar anyway

Its normative prose addresses a developer editing the verb table, not an agent
using the store, so it is outside the criterion's target. It is worth naming
because it does the Q2 thing better than anything else in the tree — a guard that
states where its own authority ends:

> **WHAT THIS STRUCTURE CANNOT DO FOR YOU** — Deriving the conformance test from
> this table proves every registered name matches THE TABLE. It does not prove
> the table is RIGHT. A wrong verb here produces a surface that is consistently
> wrong and passes every check.

No change needed.

### `AGENTS.md`

Conformant. Every rule carries its reason — *"The Makefile's `HERMETIC_ENV`
unsets…; a bare run inherits them"*; *"`internal/webui/dist/` is committed, so
`make build` needs only Go."*

And one clause bounds a guard's authority explicitly, which is the property the
task went looking for:

> That guard is blind to a rename that changes both the domain and the operation
> segment at once, so land the doc updates in the same commit.

The invariants section (append-only writes, transactional head advancement,
promotion) is correctly rule-shaped. **No change proposed.**

### `docs/`

The weakest agent-facing prose in the repo, and the reason is worth stating: it
was written for a human operator and then partly repurposed as agent
instructions.

**`docs/CONTEXT-FOR-PROJECTS.md`'s prompt block is the highest-leverage prose in
`docs/` and was the least conformant.** It is a fenced block a project pastes
verbatim into its own agent prompt, and it was a wall of bare imperatives with
the reasons stripped: *"do not bypass the human approval/apply stages"*, *"Do not
turn Tesseract into a task tracker"*, *"call `tesseract_touch` only for
summary-only results"*.

The last one was also **factually narrower than the tool**: `tesseract_touch`
covers projected hits *and* an intentional second reinforcement on a hydrated
hit. A pasted rule that contradicts the tool description is worse than a vague
one.

**Fixed** as part of F7 — the recall-discipline section now carries the
self-reinforcement reason, the under-reporting bound, and a compressed version of
"what a hit is evidence of". It is the one place in `docs/` where the fix was not
optional, because the text is *designed* to be copied out of reach of the skills
that would otherwise supply the reasoning.

`docs/AGENT-SETUP.md`'s "Recommended session workflow" is five bare imperatives
(*"do not silently write around the ownership boundary"*). **Proposed, not
applied** — see below.

---

## Already conformant

Per the task's instruction to confirm rather than assume, and to report this as a
real finding: **four in-repo instances of the target property**, none of which
needed changing.

1. **`tesseract_touch`** — states the failure mode (*"popular-because-returned
   beats actually-useful within a few cycles"*), has explicit negative space
   (*"Don't use this for: everything you recalled, everything you skimmed…"*),
   and bounds itself (*"Under-reporting is fine; over-reporting is worse than
   silence"*).
2. **`knowledge.md`'s body-vs-pointer section** — names the failure the guidance
   exists to prevent, and states the case where the opposite is right.
3. **`AGENTS.md`'s `surfaceCatalog` paragraph** — states where its own guard goes
   blind.
4. **`toolvocab.go`'s "what this structure cannot do for you"** — same, for a
   conformance test.

`start-here.md`'s **"Invariants (don't fight these)"** header deserves separate
mention: it draws `agent_guidance_over_rules`'s own discriminator, in the prose,
in four words, and it predates the decision record.

**Rewriting any of these would be a regression with no test to catch it.**

---

## Thin, not wrong

Twelve `context_*` descriptions and `tesseract_skills` are one to three sentences
plus a skill pointer. They do not fail the criterion — they are descriptive, not
normative.

They are nonetheless the weakest part of the surface for a different reason: an
agent choosing between `context_view`, `context_pack` and `context_plan` gets
little help from *"Assemble a budget-bounded bundle of context records."* That is
a **completeness** gap, not a rule-shape gap, and it wants a different fix
(worked examples, or a decision table) from the one this task is about.

**Recorded and deliberately not acted on.** Confidence that this is real:
**high**. Confidence that it is worth fixing soon: **low** — the `context_*`
family is used mostly by framework tooling rather than by agents choosing
between tools, and `start-here.md` already carries the routing table that
answers the question.

---

## What stays a rule

Not everything softens. The test applied: **would a well-reasoned exception be a
bug, or a good call?** If a bug, it is a rule.

Kept as rules, with the reason each was kept:

| Clause | Where | Why it stays |
|---|---|---|
| Append-only; every write is a new revision | `start-here.md`, `revisions.md` | An agent reasoning its way to an in-place edit is a defect. The store's entire claim is that history is canonical. |
| Namespace ownership; apps cannot write `user/*` | `namespaces.md`, `promotion.md` | This is an authorization boundary. An exception is a privilege escalation, not judgment. |
| Recall does not silently include the event log | `recall-and-ranking.md`, `event.md` | Isolation by design. An agent that "helpfully" unions them makes every unqualified recall a log search. |
| Views are selectors, not processors | `views.md`, `start-here.md` | Retrieval that synthesizes is a different product. Determinism is the contract. |
| Closed vocabularies are enforced at the write path | `memory.md`, `knowledge.md`, `facets-and-kinds.md` | The *contents* of a vocabulary are guidance (see F/style change B); that a vocabulary is **enforced** is not. |
| The MCP/HTTP shape differences | `start-here.md` and each domain skill | Not normative at all — these are facts about two wire formats. |
| `make test`, not bare `go test ./...` | `AGENTS.md` | A bare run can open and mutate the user's real store. The reason is stated; the rule stays. |

Note the split inside the vocabulary row. *"`kind` is closed and the write path
rejects anything else"* is an invariant. *"A kind is worth adding when…"* is a
governance judgment, and it is the half that produced two wrong defaults. Same
document, same subject, different answers — which is discriminator 3 again.

---

## Applied, and proposed

### Applied

**Six factual fixes** (F1–F4, F6, F7) and **two style changes**, each small,
additive, and independently revertible:

- **A.** `tesseract_recall` gains a "what a hit is evidence of, and where that
  stops" bullet — `search-first`'s section transplanted. Applied because it is
  the single Q2 gap on the highest-traffic surface and the task named this exact
  section as the thing to emulate.
- **B.** `facets-and-kinds.md`'s "Earn it" bullet gains its reason and its
  exception. Applied because the bar it restates produced two wrong defaults in
  twenty-four hours, and the bare restatement is the documented failure mode
  sitting in the documented place.

`make test` green; `go build ./...` clean. `TestShippedProseNamesOnlyRegisteredTools`
and `TestToolDescriptionsNameOnlyRegisteredTools` both pass — this file is under
`docs/`, so it is scanned by the first.

### Proposed, not applied

Deliberately left as proposals. A large mechanical diff across agent-facing prose
is exactly the shape of change that is hard to review and easy to regress.

1. **The `Store.Deprecate` audit domain fix** (F5). One argument. Changes what
   lands in the audit table, so it is a behaviour change and wants its own
   commit. **Recommended.**
2. **`docs/AGENT-SETUP.md`'s "Recommended session workflow."** Five bare
   imperatives. The reasons all exist elsewhere — promotion's is in
   `promotion.md`'s "Why this exists", touch's is in `memory.md` — so this is a
   transplant, not new thinking. **Recommended**, low risk.
3. **`memory_write` / `knowledge_write` / `memory_promote`'s "Read this first."**
   Add a bound: an agent that has already loaded the skill this session does not
   need to reload it. Cheap, and it removes a small friction that currently has
   no stated exit. Confidence this matters: **low**.
4. **`namespaces.md`'s tier table.** The `Purpose` column says what each tier is
   for, not why ownership is fixed to it. Confidence this is worth changing:
   **medium**. It may be that the table is reference material rather than
   guidance, in which case it is correct as it stands.

### Not proposed

**Rewriting the twelve thin `context_*` descriptions to match a template.**
Consistency is worth something; uniformity is not, and these are not failing the
criterion under audit.

---

## Open questions

- **Is there a house style to extract, or only embodied practice?** The four
  exemplars above are consistent enough that the pattern is real:
  *state the failure the guidance prevents → state the case where the opposite
  is right → give the negative space explicitly.* That belongs in agent-setup
  (CW-20260910-0058), not here. Flagged rather than scoped up.
- **Does anything test for this?** No, and probably nothing should. Q1 might be
  weakly greppable (a normative clause with no "because", "so that", or "the
  failure this prevents"); Q2 is not mechanizable at all. The existing prose
  guards test *accuracy* — which is the half that is testable, and the same
  discriminator agent-setup's `agent-facing-tests-audit.md` landed on. Confidence
  that a style test would do more harm than good: **medium-high**.

---

## Related

`agent_guidance_over_rules`, `evaluating_similarity_and_disuse`,
`kinds_taxonomy` (rev 7), `domain_is_immutable_no_migration_path`,
`config_is_policy_code_is_engine`. Task CW-20260910-0047; siblings
CW-20260910-0062, CW-20260910-0058, CW-20260910-0061.

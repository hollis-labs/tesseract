# Handoff and boot prompt — the two authored packets

Two artifacts under one shape: **an agent-authored packet, written for another
agent, retrieved deliberately by id.** Not discovered by recall — addressed.
Both sit on the knowledge side of the boundary in
`docs/knowledge-memory-boundary.md`, which states the rule; this file does not
restate it.

Task CW-20260910-0068. Follows CW-20260910-0067 (`1a9f7bd`).

**The rules themselves live in the skills**, `capture-handoff` and
`boot-prompt`, and the kind table in
`internal/mcpadapter/skills/facets-and-kinds.md`. This file records the corpus
measurements those rules were decided against, so the next session can check the
reasoning rather than re-derive it.

---

## What was measured

Census 2026-09-10 against the live store
(`~/.local/share/tesseract/workspaces/default/main.db`), current revisions only
(`memory_state` joined to `current_revision`), cross-checked through the running
daemon with `tesseract_recall`.

The task that produced this work had already been corrected once for quoting
shipped prose instead of counting rows. Two of its surviving claims did not
survive a count either. Both are below.

### `handoff` — 3 entries, and all three conform

| namespace | key | writer | written |
|---|---|---|---|
| `user/chrispian/knowledge/nanite` | `nanite_gate_integrity_handoff_20260825` | claude | 2026-08-26 |
| `user/chrispian/knowledge/tangent` | `tangent.handoff-desktop-shell-2026-09-05` | claude | 2026-09-05 |
| `user/chrispian/knowledge/composition` | `composition_vnext_handoff_2026_09_10` | claude-code | 2026-09-10 |

Every one is a session writing to its successor at a context boundary, which is
exactly the scope Chrispian's standing rule already gives the word
(`authority_tesseract_code_chrispian_not_files`, 2026-09-03: *"Handoffs are for
session transition after compaction only. One agent passing to the next. Not a
project record, not a status page."*). The rule was not restated for this task —
it was tested against the corpus and held, so the skill carries it rather than a
competing definition.

Two of the three also warn their own reader off the state they carry —
*"RE-DERIVE EVERYTHING BELOW. Numbers here were true at `5b5da6b5` and several
moved"* — which is the file-state lesson applied inside a handoff. That
discipline is now in the skill.

### Correction 1: the `memory/notes` route is one record, not a route

The task described a second live route, *"`memory/notes` tagged `handoff`"*.
Thirteen memory records carry the tag. **Twelve of them use `handoff` as a
subject-matter topic, not as an artifact type:**

- `fe_render_handoff_loaded_event` — a `handoff_loaded` SSE event Glass-4 emits
- `hadron_workflow_engine_orchestrator_handoff` — a decision about orchestrator
  execution handoff
- `decisions_nanite_chat_dispatch_side_channel_v1` — tagged `executor-handoff`
- eight more from the Glass-4 compaction work, where "handoff" names the
  *feature* being built

Exactly **one** is the artifact: `nanite_alignment_handoff_20260910`
(`memory/notes`, tagged `handoff`, `session-state`).

So there is no third route to close, there is a single misfiling. Its
disposition is to stay where it is: a record's domain is stamped at creation
(`domain_is_immutable_no_migration_path`), it is readable through recall and
`tesseract_get`, and rewriting it under a new identity to fix a filing error
would discard its lineage to buy nothing.

**The finding that changes the advice:** the tag `handoff` is not a
discriminator — it is right about the subject twelve times out of thirteen. No
skill should tell an agent to find handoffs by searching that tag. Find them by
namespace and kind.

### Correction 2: the namespace convention did not emerge from handoff

The task read the three handoffs above as three independent writers converging
on `user/chrispian/knowledge/{project}/…` and asked that the convention be
ratified rather than redesigned. Against the whole corpus that reads the wrong
signal. Sorting every populated knowledge kind by namespace shape:

| kind | has a skill? | namespace shape | entries |
|---|---|---|---|
| `session_close` | yes — `end-of-session` | `knowledge/session-close/{project}` — **kind first** | 49 across 21 projects |
| `investigation` | yes — `capture-investigation` | `knowledge/{project}/investigations` — **kind last** | 11 of 14 |
| `handoff` | **no** | `knowledge/{project}` — **bare** | 3 |
| `playbook` | **no** | `knowledge/{topic}` — **bare** | 3 |
| `note` | no | mixed, mostly `knowledge/nanite/architecture` | 26 |

The pattern is not project-major versus kind-major. It is: **a kind with a skill
gets a segment naming the kind; a kind without one does not.** The three bare
handoff placements are what agents do when nothing tells them anything — the
absence of a convention, not a convention. `knowledge/composition` is not even a
project; it is a work stream, which is what a fallback bucket looks like.

Adding the skill is what this task does, so ratifying the bare shape would mean
ratifying the absence of the thing being added.

---

## Decisions

### Namespace: `user/chrispian/knowledge/{kind}/{project}`

`user/chrispian/knowledge/handoff/{project}` and
`user/chrispian/knowledge/boot-prompt/{project}`. Kind segment hyphenated, as
`session-close` already is.

This departs from what the task expected, on three measurements:

1. **The majority skill-driven pattern is kind-first, 49 writes to 11.**
   `session_close` is not a marginal cohort — it is the most-written kind in the
   store, across 21 project namespaces, and it puts the kind first.
2. **`handoff` and `session_close` are written at the same moment, by the same
   wrap-up flow, and are the pair most easily confused.** Sibling paths —
   `knowledge/handoff/torque` beside `knowledge/session-close/torque` — put that
   choice in front of the agent at write time, in the one place it cannot be
   skipped. Inverting the two shapes for two artifacts written minutes apart is
   the confusing option.
3. **Grouping a project's knowledge under one prefix buys nothing at read time.**
   This is the load-bearing check and it is easy to assume the other way:
   **knowledge namespaces are exact-match only.** `buildNamespaceClause`
   (`internal/memory/recall_namespace.go:41`) resolves prefixes only for
   namespaces ending in the `memory` or `event` segment, and
   `scopedPrefix`'s own comment says knowledge is excluded deliberately, because
   its namespaces have free depth and reading `user/x/knowledge/foo` as a prefix
   would reinterpret a real namespace. There is no wildcard read for knowledge.
   So `knowledge/{project}/handoffs` cannot be swept with
   `knowledge/{project}/*`, and the project-major shape's apparent advantage
   does not exist.

The three existing entries stay where they are. Moving them is a cross-namespace
promotion, which is a corpus edit; filed, not done here, consistent with how
0067 handled the same shape.

### Key style: snake_case, `a-z 0-9 _`, the memory rule

`<slug>_<YYYY_MM_DD>`, matching what `capture-investigation` already prescribes.
Project and task id are **tags, never key segments**, as `capture-followup`
already says.

Knowledge keys are validated by `knowledgePolicy.ValidateKey`, which accepts
anything (`internal/memory/domainpolicy.go:202`). The declared reason is
specific: *"Knowledge keys carry slugs from external sources — hyphens, slashes,
mixed case."* Both of these kinds are authored, not imported; they write
`pointer_scheme: nil` because there is no external source, so the reason the
field is free-form does not apply to them. Adopting memory's stricter rule means
one key rule to remember across the whole store, and a key that is never one
memory would have rejected.

This settles what would not settle on its own — the three existing handoff keys
are in three different styles.

### The three routes, reconciled

**Knowledge `kind=handoff` survives**, at the namespace above. It is where the
real instances are, the boundary rule predicts knowledge for it, and
`authority_tesseract_code_chrispian_not_files` sends reasoning to Tesseract.

**The memory route is one misfiled record**, not a route. It stays readable
where it is; the skill stops the next one. See Correction 1.

**The inbox stops being where agents write handoffs.** `~/dev/chrispian/inbox/`
holds 301 files, of which roughly twenty are boot prompts or handoffs by name.
The cost is documented rather than hypothetical: a partly-settled architecture
document sat there — outside both Tesseract and `workspaces/drafts/` — so a
cross-project planning session found no decision, reconstructed a direction from
library state, and filed two Torque tasks and dispatched two agents on it before
Chrispian caught it.

The bound matters here, because the inbox is Chrispian's own drop zone and this
does not touch it: **an inbox file is an input a human placed, not an output an
agent produced.** Reading one is fine and is how this task was briefed. What
changes is that an agent writing a handoff writes it to Tesseract, and an agent
*briefed* from an inbox file captures what is durable in it. Migrating the
existing 301 files is CW-20260910-0022 and is out of scope.

### `boot_prompt`: the kind, and no tool

Added to `knowledge.facet_kind`, with the `kinds_taxonomy` record revised as one
change. **No MCP tool was built.** `tesseract_get` already takes `domain` +
`namespace` + `key` across every domain, so a `tesseract_boot` would be a second
way to do a thing that works; the skill carries the semantic mapping from "boot
the 0068 prompt" to the call.

`tesseract_get` reinforces activation under `memory` but **not** under
`knowledge`. That is the wanted behaviour here rather than a gap: a boot prompt
handed out weekly should not climb recall rankings, because it is addressed by
id and never searched for.

**Its boundary is against two things that stay on the filesystem**, both of them
already recorded:

- **The agent** — profile + scope + args, materialized into a boot dir
  (`local_agent_object_model`). That is identity, it is config at rest, and it
  is reused at every boot.
- **The boot directory** — `agent-workspaces/boot/<project>/boot-prompt.md`,
  session state that rots fast (`feedback_boot_prompt_layout`).

The test that separates them: **could you regenerate it from config?** The
profile *is* config; the boot dir is materialized from profile plus scope. An
authored boot prompt is neither — it holds the judgment of an agent that
understood the problem, written for one that does not yet. The corrected premise
in this task's own boot prompt is the example: no amount of config would have
produced it.

---

## Filed, not done

- Move the three existing `handoff` entries to `knowledge/handoff/{project}`
  (cross-namespace promotion, a corpus edit).
- `nanite_alignment_handoff_20260910` stays in `memory/notes` permanently; the
  domain is stamped and rewriting it would discard lineage for a filing fix.
- The 301 inbox files — CW-20260910-0022.

## Related

`docs/knowledge-memory-boundary.md` (CW-20260910-0067),
`docs/agent-facing-prose-audit.md` (CW-20260910-0047),
`authority_tesseract_code_chrispian_not_files`, `local_agent_object_model`,
`feedback_boot_prompt_layout`, `portfolio_shape_emerges_from_usage`,
`domain_is_immutable_no_migration_path`, `kinds_taxonomy`.
Skills `capture-handoff` and `boot-prompt` ship from `agent-setup`.

# The knowledge/memory boundary — the rule, and the corpus test behind it

**Status 2026-09-10.** The rule shipped until today was false. It has been
replaced, and the replacement was tested against every populated value in both
vocabularies before it was written down. This file records that test.

**The rule itself is not stated here.** It is stated once, in
`internal/mcpadapter/skills/start-here.md` under "Memory or knowledge — the
canonical statement", and every other surface points at it. This file quotes it
once, below, as the thing under test — restating it anywhere else is how the
defect this task fixed got made.

Task CW-20260910-0067. Supersedes part of `docs/agent-facing-prose-audit.md`
(CW-20260910-0047, landed `e882d26`), which swept several of these same files
hours earlier and left the boundary clause alone.

---

## The defect

`knowledge_write`'s MCP tool description — the string most agents read, ahead of
any skill — said:

> *"Don't use this for: agent-authored content with no external source — use
> `memory_write`."*

`skills/knowledge.md`, `skills/start-here.md`, `docs/MCP_TOOLS.md`,
`internal/knowledge/store.go` and `domains/domains.go` all carried a version of
it.

It was false for **three populated knowledge kinds**, all of them agent-authored
with nothing outside Tesseract to point at:

| Kind | Entries | External source? | Agent-authored? |
|---|---|---|---|
| `session_close` | 49 | no | yes |
| `project_canonical` | 15 | no | yes |
| `investigation` | 14 | no — `/capture-investigation` writes it | yes |

That is 78 of the corpus's 166 knowledge entries — **47%** — sitting outside a
rule the write tool stated as a boundary. It survived because nobody ran it
against what was being written.

**The second-order cost is the one that mattered.** A false rule makes conformant
records look mis-filed. A review on 2026-09-10 came close to migrating `handoff`,
`learning` and `playbook` to the memory domain on the authority of this sentence
— a migration that is not reversible, since a record's domain is stamped at
creation ([[domain_is_immutable_no_migration_path]]).

### A second, independent staleness in the same place

`skills/facets-and-kinds.md` and `internal/typeregistry/defaults.go` both said:

> *"`playbook`, `learning`, and `handoff` are canonical and writable but
> currently **unpopulated** — no entries exist yet."*

Also false. Measured against the live store on 2026-09-10: `playbook` 3,
`handoff` 3, `learning` 2. All were seeded between 2026-09-05 and 2026-09-10.
`kinds_taxonomy` rev 7 had already corrected this on 2026-09-09 — *"Corpus check
2026-09-09: `playbook` 2, `learning` 2, `handoff` 2 — all since seeded"* — and
the shipped prose was never updated to match.

The two defects compounded: a false rule said those kinds did not belong, and a
false count said nothing was using them. Either alone is survivable. Together
they read as a finding.

**Only `wiki_page` is genuinely unpopulated**, waiting on its first Loom
emission, exactly as intended.

---

## The rule under test

Chrispian's ruling: the rule is too narrow, not the kinds. Sharpened in the same
session into a form a record can actually be tested against.

> **Knowledge is content you go *to*.** Addressed by key, read whole, expected
> to stay true.
>
> **Memory is content that comes *to you*.** Surfaced by recall when you are
> working nearby, dated, superseded rather than edited.

`derived_from` drops out. Both domains are overwhelmingly agent-authored; what separates
them is how the content gets found again.

**The operational form used for this test**, because "you go to it" is not
directly checkable against a stored row: *could you have named it before you went
looking?* Knowledge you can name — you know the project has a canonical, and you
go get it. Memory you cannot — you did not know that decision record existed, and
recall put it in front of you while you were working on something else.

That form is also the bound. The over-application to watch for is an agent
reasoning *"I might look this up later, so it is knowledge"* — which sends
everything to knowledge, because nearly all memory is looked up eventually.
Being lookable-up does not separate the two. Being **nameable in advance** does.

---

## Method

Census taken 2026-09-10 against the live store
(`~/.local/share/tesseract/workspaces/default/main.db`, schema 18, both binaries
redeployed at `e882d26` the same morning). Current revisions only
(`memory_state` joined to `current_revision`), which is what recall returns by
default. Counts spot-checked against the running daemon through
`tesseract_recall estimate_only` — knowledge kinds and memory namespaces both
agreed exactly.

Every populated knowledge kind and every memory type was checked. For each, the
question was: **does the rule predict where these records actually live?** A
misprediction is a finding against the rule, not against the records — records
were not reclassified to make the rule look good, which is precisely how the
previous rule survived as long as it did.

---

## Result: knowledge kinds

166 entries, 11 populated kinds. **10 of 11 predicted correctly.**

| Kind | Entries | Could you name it before looking? | Predicted | Actual | ✓ |
|---|---|---|---|---|---|
| `session_close` | 49 | Yes — "the last session close for torque". You go to the project's session-close namespace and take the newest. | knowledge | knowledge | ✓ |
| `doc` | 43 | Yes — you know which document you want. | knowledge | knowledge | ✓ |
| `note` | 26 | Yes — filed under a project's own namespace (`knowledge/nanite/architecture/…`); you go to a project's notes. | knowledge | knowledge | ✓ |
| `project_canonical` | 15 | Yes — this is the definitional case. | knowledge | knowledge | ✓ |
| `investigation` | 14 | Yes — you go to the dossier for the thing you are investigating. | knowledge | knowledge | ✓ |
| `mcp_server` | 7 | Yes — `mcp.server.cerberus`, addressed by name. | knowledge | knowledge | ✓ |
| `playbook` | 3 | Yes — you go to the runbook for the procedure you are running. | knowledge | knowledge | ✓ |
| `handoff` | 3 | Yes — you know a handoff exists when you pick up the work. | knowledge | knowledge | ✓ |
| `pointer` | 2 | Yes — `portfolio.index` is the definitional go-to. | knowledge | knowledge | ✓ |
| `package` | 2 | Yes — you know the library's name. | knowledge | knowledge | ✓ |
| `learning` | 2 | **No** — `tangent.test-blindspots-2026-09` is not something you would have known to ask for. | **memory** | knowledge | ✗ |

`wiki_page` — 0 entries, not testable. By construction a compiled page is
addressed by title and gone to, so the rule predicts knowledge; the first
emission will confirm or refute that.

### The one misprediction, taken seriously

`learning` is the only kind the rule gets wrong, and it is the kind that
duplicates a memory type. Two readings:

- **(a) The rule is wrong.** Against it: the rule predicts 10 of 11 knowledge
  kinds and 7 of 8 memory types, and both misses land on the two values that are
  cross-domain duplicates. A rule that fails only where the vocabulary is
  ambiguous is being told something about the vocabulary.
- **(b) The vocabulary is wrong.** `learning` (knowledge) and `learnings`
  (memory) are the same English word one letter apart, on either side of a
  choice that cannot be undone.

Landed on (b), and the resolution is below. Stated plainly so it is not hidden:
**the two existing `learning` records stay in the knowledge domain and the rule
says they should have been memory.** That is a real residual, not a rounding
error. Their bodies are findings-plus-evidence — closer to `investigation` than
to anything else — so re-filing them within the knowledge domain would cost
nothing but is a corpus edit, filed rather than done here.

---

## Result: memory types

1,551 entries, 8 populated types. **7 of 8 predicted correctly.**

| Type | Entries | Could you name it before looking? | Predicted | Actual | ✓ |
|---|---|---|---|---|---|
| `notes` | 587 | No — the catch-all. | memory | memory | ✓ |
| `decisions` | 491 | No. See below — this is the case that looks like an exception and is not. | memory | memory | ✓ |
| `followups` | 273 | No — `capture-followup` says so in as many words: it *"surfaces on recall when someone works nearby"*. | memory | memory | ✓ |
| `limitations` | 129 | No — you meet a limitation by walking into it. | memory | memory | ✓ |
| `learnings` | 27 | No — `lsof_cwd_checks_need_a_positive_control` finds you, not the reverse. | memory | memory | ✓ |
| `feedback` | 18 | No — surfaced when the pattern recurs. | memory | memory | ✓ |
| `outcomes` | 16 | No — dated statements about what was true after some work. | memory | memory | ✓ |
| `references` | 10 | **Mixed** — about half are reference material you would name (`proxima_routing_rules`, `pm_escalation_rubric`, `weekly_review_templates_user_and_portfolio`); the rest are dated findings. | **split** | memory | ✗ |

`todos` joined the vocabulary after this audit (CW-20260909-0036) and is
deliberately not in the table. The counts above are a measurement, and it has
none — but the deeper reason is that the "could you name it before looking?"
test does not apply to it. That test sorts PROSE by how it is retrieved. A todo
is a structured object: it is read by asking for a list, filtered on the
`consumer_state` fields it carries, and it lives under memory because it is
per-user working state, not because it passed this test. It is a different axis,
and reading a row for it here would suggest otherwise.

### `decisions` is the stress test, and it passes

491 records, cited by key throughout this repo's own prose —
`[[kinds_taxonomy]]`, `[[agent_guidance_over_rules]]`,
`[[config_is_policy_code_is_engine]]`. Under a careless reading of "content you
go to", all 491 are knowledge. This is the single most likely way the new rule
gets misapplied, and it is why the bound is stated as loudly as the rule.

The citation is **downstream of a recall**, not a substitute for one. Someone met
that decision while working nearby, then wrote the link. A record you can cite
precisely *afterwards* is not thereby a record you would have gone looking for.
And the lifecycle matches memory exactly: `kinds_taxonomy` is at revision 7, each
revision superseding rather than editing the last.

### `references` is the second misprediction, and it is the same shape

The type's stated meaning — *"pointers to where information lives"* — describes
content you go to, so the rule sends the type as a whole to knowledge. Its actual
contents split:

- **Reference material you would name:** `proxima_routing_rules`,
  `proxima_pattern_library`, `proxima_operator_working_style`,
  `pm_escalation_rubric`, `pm_project_pm_contacts`,
  `weekly_review_templates_user_and_portfolio` → knowledge (`pointer` / `doc`).
- **Dated findings you would not:** `critical_services_rollout_complete`
  (→ `outcomes`), `legacy_adoption_dotdir_gap` (→ `limitations`),
  `portfolio_go_library_ci_setup_gotchas` and
  `harness_hook_capabilities_measured` (→ `learnings` or `notes`).

**Not one of the ten needs the type to exist.** That is the finding, and it
decides the duplicate below.

---

## The two duplicates, resolved

Both are the same shape: one vocabulary entry in each domain, meaning the same
thing, either side of a choice that is stamped at creation and cannot be undone.
That irreversibility is what makes a near-identical name pair expensive rather
than untidy.

### `learning` (knowledge) vs `learnings` (memory) — `learnings` survives

**`learning` is retired from `knowledge.facet_kind`.**

- The rule says a distilled lesson is memory: you do not know it exists until
  recall surfaces it while you are working nearby.
- `learnings` is live — 38 revisions, most recent 2026-09-09. `learning` holds
  two, both written within two minutes of each other on 2026-09-05, nothing
  since, no producer.
- **What the retired value means now:** write it to `.../memory/learnings` with
  `memory_write`. If the thing you have is a dossier — findings plus the evidence
  behind them, written for someone to go read — that was always `investigation`.

### `references` (memory) vs `pointer` / `doc` (knowledge) — the knowledge kinds survive

**`references` is retired from `memory.type`.**

- A pointer to where information lives is content you go to. `pointer` (a bare
  reference) and `doc` (a documentation reference) already carry it.
- The corpus agrees: nothing filed under `references` requires it, and its
  contents divide cleanly between knowledge and three other memory types.
- Dormant — last written 2026-08-24, 12 revisions across 10 entries.
- **A third collision settles it.** `references` is also one of the two
  `memory_links` relation names (a `[[wikilink]]` parsed out of a payload). The
  word named three things. It now names one.
- **What the retired value means now:** the pointer half goes to
  `knowledge_write` as `pointer` or `doc`; the dated-finding half goes to
  `outcomes`, `limitations`, `learnings` or `notes`.

### What retiring a value does and does not do

Checked in code rather than assumed:

- **Writes stop.** `memory.Store.WriteRevision` validates the namespace `{type}`
  through `memory.ParseNamespace` (`internal/memory/write.go:412`) and the
  knowledge `kind` through `knowledgePolicy.ValidateFacets`
  (`internal/memory/domainpolicy.go:207`).
- **Reads do not.** `RecallPage` requires namespaces to be non-empty and
  otherwise treats them as strings (`internal/memory/recall.go:319`); the
  `facet_kinds` filter is plain SQL. So the 10 `references` entries and the 2
  `learning` entries stay reachable by `tesseract_recall`, `tesseract_get` and
  `tesseract_history`. What they lose is the ability to take a **new revision**
  in place.
- **One coupling had to be fixed with it.** `internal/memory/migrate.go`'s
  `typeNormalize` routed the legacy key prefixes `reference` / `references` to
  the `references` namespace, and `migrate-namespaces` applies with raw `UPDATE`
  statements rather than through the write path — so it does not inherit the
  vocabulary check. Left alone, a fresh legacy import would have landed rows in a
  namespace nobody could write a second revision to: readable but unwritable,
  the exact trap the 2026-08-25 kind normalization existed to remove. Both
  prefixes now point at `notes`.

---

## A third collision, reported and not resolved

`note` (knowledge, 26 entries) and `notes` (memory, 587) are the same pair shape
— singular kind, plural type — and this task did not scope them.

They are **not** resolved here, because unlike the other two the rule does not
mispredict either side. Knowledge `note` records are architectural facts filed
under a project's own namespace (`knowledge/nanite/architecture/…`); you go to a
project's notes. Memory `notes` is the deliberate catch-all for memories with no
stronger type; it finds you. Both placements are correct.

What remains is the name. Both are the designated fallback in their own
vocabulary, which means an agent that is unsure reaches for a near-identical word
in each domain — and picks irreversibly. Worth a decision; not worth taking one
inside a task that did not scope it. Filed.

---

## What changed

**Source of the rule** — `internal/mcpadapter/knowledge_tools.go`. The false
clause is gone. The boundary now lives in one `const domainBoundaryLine` used by
both `knowledge_write` and `memory_write`, so the two write tools cannot drift
from each other.

**Canonical statement** — `skills/start-here.md`, one section, with its reason
(`derived_from` does not decide this) and its limit ("I might look this up later" is not
the test). `skills/knowledge.md`, `skills/memory.md`,
`skills/facets-and-kinds.md`, `skills/namespaces.md` and `skills/event.md` carry
the short form and point at it.

**Vocabulary** — `internal/typeregistry/defaults.go`: `learning` out of
`knowledge.facet_kind` (11 kinds), `references` out of `memory.type` (7 types),
each with its reasoning in the declaration. `kinds_taxonomy` revised as part of
the same change.

**One class of defect closed structurally.** `memory_write`'s `namespace`
description restated the eight memory types as a literal, and would have gone on
advertising `references` after the vocabulary dropped it. It now renders from the
vocabulary through `memory.TypeList()`, which is what `knowledge_write`'s `kind`
has done since the registry move. A description can no longer name a value the
write path rejects.

## What moved, 2026-09-11 (CW-20260910-0078)

The first two bullets under "Filed, not done" are done. Twelve entries were
re-filed and every original is deprecated rather than deleted. This section
records what the move cost, because the rule above is unchanged by it — the
corpus was brought to the rule, not the reverse.

All writes landed between 14:05:09Z and 14:27:59Z on 2026-09-11.

**A count in the task description was loose, and is corrected here.** The
stranded `references` set is **12 rows across 10 distinct entries**, not 12
entries: `portfolio_go_library_ci_setup_gotchas` carried three revisions. Twelve
*entries* is right only when the two `learning` records are included, which is
how the original framing reached the number.

### The cheap half: same domain, nothing lost

`tangent.test-blindspots-2026-09` and `tangent.unverified-strengths-claims-2026-09`
now carry `kind: investigation` — same namespace, same key, each superseding its
own previous revision. The supersede chain and the original `created_at` both
survive; the old revisions are the previous link in a chain rather than orphans.
Canonical `learning` entries in the corpus: **0**.

### The ten `references` entries

| Entry | Re-filed to | Crossed a domain |
|---|---|---|
| `proxima_routing_rules` | `knowledge/agent-ops`, `doc` | yes |
| `proxima_pattern_library` | `knowledge/agent-ops`, `doc` | yes |
| `proxima_operator_working_style` | `knowledge/agent-ops`, `doc` | yes |
| `pm_escalation_rubric` | `knowledge/agent-ops`, `doc` | yes |
| `weekly_review_templates_user_and_portfolio` | `knowledge/portfolio`, `doc` | yes |
| `critical_services_rollout_complete` | `memory/outcomes` | no |
| `legacy_adoption_dotdir_gap` | `memory/limitations` | no |
| `portfolio_go_library_ci_setup_gotchas` | `memory/learnings` | no |
| `harness_hook_capabilities_measured` | `memory/learnings` | no |
| `pm_project_pm_contacts` | `memory/notes`, still `draft` | no |

Canonical entries left in `user/chrispian/memory/references`: **0**. All twelve
rows there are deprecated and every one is still readable.

### One classification changed on contact with the record

The task listed `pm_project_pm_contacts` as knowledge. Reading it, that does not
hold: it is a target shape for a PM-of-PMs ritual that never materialized — "You
are the ONLY project PM on the substrate. No peers yet", five of six URNs `TBD`.
It is a plan for a directory, not a directory, so there is nothing in it to look
up and nobody could name it in advance. It stays in memory as `notes`, the
catch-all for a parked intention, and keeps `status: draft`.

Three things fell out of that call, and they generalize:

- **Crossing into knowledge silently promotes a draft.** `knowledgeWriteRequest`
  has no status field; `memoryWriteRequest` does. A `draft` that crosses comes
  out `canonical`, which is a change of meaning nobody asked for.
- **Mis-filing is not symmetric**, per `fallback_belongs_to_memory`. A record
  mis-filed into memory still reaches someone through recall; one mis-filed into
  knowledge is write-only, because retrieval there needs a name nobody has.
- So a record that is genuinely ambiguous belongs in memory, and a never-
  materialized stub is not ambiguous at all.

Chrispian approved the *method* — re-create and deprecate — not each
classification line by line, so this refinement is recorded rather than silent.

### What the crossing cost, and what the old revisions cost to reach

For the five that crossed into knowledge, and for the four that changed memory
type, the new entry is a **new record with today's `created_at`**. Lost: the
supersede chain, the original `created_at`, and any lineage edges. This is not
avoidable — `WriteRevision` rejects a `supersedes` whose revision belongs to a
different memory (`internal/memory/write.go:212-227`), and a record's domain is
stamped at creation (`domain_is_immutable_no_migration_path`).

Every re-filed entry therefore carries a **provenance line** in its body naming
the old namespace, the original `created_at`, and the deprecated revision id.

Nothing was destroyed. The originals are readable at

```
GET /v1/memory/history?namespace=user/chrispian/memory/references&memory_key=<key>
```

or `tesseract_history` on the same namespace and key.

### The `[[wikilink]]` mitigation does not work for a same-key re-file

Worth recording, because it is the obvious thing to reach for and it fails
silently. `TxResolver.Resolve` (`internal/memorylinks/links.go:233-239`) resolves
a link target by `memory_key`, ordering `(namespace = ?) DESC` so a local match
wins. A re-file that keeps its key and links back to that key therefore resolves
to **itself** — a self-edge, not a trail to the deprecated original.

The bound matters: this breaks only for **same-key** re-files, which is all ten
of these. A re-file that genuinely changes its key can still link back, because
the old key remains a distinct `memory_state` row and is the only claimant. The
provenance line above is the substitute used here, and it has the side benefit of
being greppable, which an edge is not.

## Filed, not done

- Decide `note` / `notes`.

## Related

`kinds_taxonomy`, `agent_guidance_over_rules`,
`domain_is_immutable_no_migration_path`, `auditing_prose_for_guidance_form`,
`config_is_policy_code_is_engine`, and the `wiki_page` approval record
(`tesseract_` + `wiki_page_kind_approved`, in `user/chrispian/memory/decisions`).
Task CW-20260910-0067; reads on `docs/agent-facing-prose-audit.md`
(CW-20260910-0047, `e882d26`); blocks CW-20260910-0068.

---
name: facets-and-kinds
description: Facet vocabulary, the closed set of knowledge kinds, and how to request an addition.
scope_hint: none
related: [knowledge, memory]
---

# Facets and kinds

Namespaces, revisions, and audit are structurally rigid. `source` and `pointer` are conventional. `kind` is a **closed, validated vocabulary** — see below.

## Facets

Every memory and knowledge revision carries a small facet structure. Current facets:

- `kind` — what this record is. Closed vocabulary, validated on write.
- `source` — where it came from (e.g., `filesystem`, `obsidian`, `nil`, `web`, `manual`). Conventional, not validated.
- `pointer` — a structured `{scheme, locator, resolved_at}` triple. Scheme `nil` is the first-class way to say the entry has no external source, which is the common case.

Memory-domain revisions leave facets zero-valued; memory categorizes by `tags` and key prefix.

## The `kind` vocabulary

`knowledge_write` accepts exactly these twelve values and rejects anything else, naming the allowed set in the error. Canonical kinds are **snake_case**.

| Kind | Use for |
|---|---|
| `session_close` | Session closure record. |
| `project_canonical` | Single-source-of-truth for a project — paths, configs, roadmap. |
| `doc` | A documentation reference. |
| `package` | Library or package reference. |
| `mcp_server` | An MCP server: its tools, transport, and configuration. |
| `investigation` | A dossier from a completed investigation — findings and the evidence behind them. |
| `pointer` | A bare reference with minimal body. |
| `note` | Generic agent-authored note. The fallback when nothing else fits. |
| `playbook` | Codified process or runbook. |
| `handoff` | What the next session needs in order to continue this work. Written **to** a successor, at a context boundary. Not `session_close` — see below. |
| `boot_prompt` | A rendered briefing authored for someone to hand to another agent. Prose, not slots. |
| `wiki_page` | A compiled wiki page — compiler output with a template, a provenance chain and a link graph. Not `doc` (a documentation reference) and not `note` (a generic note). |

**Populated as of 2026-09-10:** all of them except `wiki_page`, waiting on its first Loom emission, and `boot_prompt`, added the day it was specified. Both are writable so that the first one can be written. `playbook`, `learning` and `handoff` were described here as unpopulated for months after they had been seeded — that stale sentence is what made three healthy kinds look mis-filed, and nearly got them moved. If you need to know whether a kind has entries, count them: `tesseract_recall` with `facet_kinds` and `estimate_only`.

**`learning` was retired 2026-09-10** (CW-20260910-0067). It duplicated the `learnings` memory type one letter apart, across a domain boundary a record cannot be moved back over. A distilled lesson is memory — you do not know it exists until recall surfaces it while you are working nearby. Write it with `memory_write` to `.../memory/learnings`. Two entries still carry the old kind; `facet_kinds: ["learning"]` still returns them, because the vocabulary is enforced on the write path and recall's filter is not.

### `handoff` vs `session_close` — the line that gets exercised most

`session_close` is the most-written kind in the store (49 entries as of 2026-09-10) and `handoff` is its nearest neighbour, so this is the choice you will actually face.

- **`session_close` answers "what happened?"** It is written *about* the session that is ending, keyed by session id, and it is written **whether or not anyone picks the work up**.
- **`handoff` answers "what do you need to know to continue?"** It is written *to* a successor, about a thread of work rather than a session, and if nobody is picking the work up there is nothing to hand off.

The test is one question: **would this record be identical if no one continued the work?** If yes, it is a `session_close`.

**Where this stops — and this is the failure to guard against.** *Most session output is not a handoff.* A session that finished what it started has something to close, not something to hand off. The over-application is an agent filing every note it leaves for a successor as a handoff, which is nearly all of them; write one when the work is genuinely mid-thread and the next agent would otherwise re-derive the frame — Chrispian's standing rule scopes it to session transition after compaction, one agent passing to the next ([[authority_tesseract_code_chrispian_not_files]]). It is **not** a project record and **not** a status page.

A handoff that carries state — SHAs, task statuses, install status, counts — will lie about it within hours. Name the thing that is always current instead of copying its value: Torque for task state, `git log` for the tree, the tool's own `--check` for an install. The two best handoffs in the corpus both say this to their own reader in their opening lines.

For ephemeral content use `kind=note` with a short `ttl_seconds`. Scratch is a TTL modifier, not a kind.

Task-tracking entities — bug, task, todo, plan, sprint, issue, epic — belong to Torque, not to this vocabulary. Cross-reference them from a knowledge entry by ID or tag.

## Adding a kind

The vocabulary is centrally governed: request an addition rather than introducing one locally. A new kind lands when a producer emits it systematically and the case is written down — that is how `mcp_server` and `investigation` earned theirs. `wiki_page` is the one exception so far, added while its producer was built but blocked waiting for it; the rule exists to stop a vocabulary filling with entries nothing writes, and a stalled producer is the opposite case.

Adding one is a single change to the vocabulary declaration and to the taxonomy record together. The declaration lives in the type registry (`knowledge.facet_kind`), which ships a default set and reads an operator's `types.yaml` over it — so an addition is a config change rather than a release. What did not change is that the write path enforces it: `knowledge_write` still rejects anything the vocabulary does not name.

Until a kind is in the vocabulary, `knowledge_write` will reject it. File the request; use `note` with descriptive tags meanwhile.

Naming rules for a proposed kind:

- **snake_case.** Multi-word kinds join with `_` — `mcp_server`, `session_close`, `project_canonical`. This is what the vocabulary validates against, so a hyphenated or spaced value is rejected.
- **Stay short**, and singular unless the thing is inherently plural.
- **Stay stable.** The value is a public API once adopted; changing it is a migration, not an edit.
- **Do not collide with a memory `{type}` or an event `{type}`.** They are separate vocabularies on separate axes, so nothing mechanically stops `learning` next to `learnings` — and that pair shipped, one letter apart, until it was retired in 2026-09-10. The cost is specific rather than aesthetic: the two sit either side of a domain choice that is stamped at creation and cannot be undone, so an agent that guesses wrong between near-identical names has no way back. Check `tesseract_skills namespaces` for the current memory and event type lists before proposing a kind.
- **Earn it — and know what the bar is for.** A kind is worth adding when something emits it systematically and filing those records under an existing kind would discard information. The bar exists to stop the vocabulary filling with values nothing writes; it is not a waiting period, and it is not a reason to withhold a value a shipped producer is blocked on. A producer that is built and stalled waiting for the kind is the opposite of the case the bar was aimed at — that is how `wiki_page` landed. When you hit that case, say so and ask; do not read the bar as a refusal you have to enforce.

## Filtering by facet

`tesseract_recall` accepts `facet_kinds` and `facet_sources` as JSON-array filters. Use these to narrow a cross-domain search:

```json
{"query": "embedding provider", "facet_kinds": ["package"], "limit": 10}
```

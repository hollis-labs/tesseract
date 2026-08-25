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
- `pointer` — a structured `{scheme, locator, resolved_at}` triple for knowledge entries referencing external content.

Memory-domain revisions leave facets zero-valued; memory categorizes by `tags` and key prefix.

## The `kind` vocabulary

`knowledge_write` accepts exactly these eleven values and rejects anything else, naming the allowed set in the error. Canonical kinds are **snake_case**.

| Kind | Use for |
|---|---|
| `session_close` | Session closure record. |
| `project_canonical` | Single-source-of-truth for a project — paths, configs, roadmap. |
| `doc` | External documentation reference. |
| `package` | Library or package reference. |
| `mcp_server` | An MCP server: its tools, transport, and configuration. |
| `investigation` | A dossier from a completed investigation — findings and the evidence behind them. |
| `pointer` | A bare external reference with minimal body. |
| `note` | Generic agent-authored note. The fallback when nothing else fits. |
| `playbook` | Codified process or runbook. |
| `learning` | Reusable insight or pattern extracted from work. |
| `handoff` | Agent-to-agent or session-to-session packet. |

`playbook`, `learning`, and `handoff` are canonical and writable but currently **unpopulated** — no entries exist yet. They are in the vocabulary so that the first one can be written; treat them as available, not as evidence of an established pattern.

For ephemeral content use `kind=note` with a short `ttl_seconds`. Scratch is a TTL modifier, not a kind.

Task-tracking entities — bug, task, todo, plan, sprint, issue, epic — belong to Torque, not to this vocabulary. Cross-reference them from a knowledge entry by ID or tag.

## Adding a kind

The vocabulary is centrally governed: request an addition rather than introducing one locally. A new kind lands when a producer emits it systematically and the case is written down — that is how `mcp_server` and `investigation` earned theirs. Adding one is a single change to the vocabulary in the code and to the taxonomy record together.

Until a kind is in the vocabulary, `knowledge_write` will reject it. File the request; use `note` with descriptive tags meanwhile.

Naming rules for a proposed kind:

- **snake_case.** Multi-word kinds join with `_` — `mcp_server`, `session_close`, `project_canonical`. This is what the vocabulary validates against, so a hyphenated or spaced value is rejected.
- **Stay short**, and singular unless the thing is inherently plural.
- **Stay stable.** The value is a public API once adopted; changing it is a migration, not an edit.
- **Earn it.** A kind is worth adding when something emits it systematically and filing those records under an existing kind would discard information.

## Filtering by facet

`tesseract_lookup` accepts `facet_kinds` and `facet_sources` as JSON-array filters. Use these to narrow a cross-domain search:

```json
{"query": "embedding provider", "facet_kinds": ["package"], "limit": 10}
```

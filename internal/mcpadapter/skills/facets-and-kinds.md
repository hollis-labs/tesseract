---
name: facets-and-kinds
description: Facet vocabulary, the kind convention, and how consumers extend it.
scope_hint: none
related: [knowledge, memory]
---

# Facets and kinds

Vanta is deliberately soft on content taxonomy. Structural invariants (namespaces, revisions, audit) are rigid; how you tag content is up to you.

## Facets

Every memory and knowledge revision carries a small, open-valued facet structure. Current facets:

- `kind` — what this record is (e.g., `package`, `doc`, `note`, `pointer`).
- `source` — where it came from (e.g., `filesystem`, `obsidian`, `nil`, `web`, `manual`).
- `pointer` — a structured `{scheme, locator, resolved_at}` triple for knowledge entries referencing external content.

## The `kind` convention

`kind` is a free-form string, but a few well-known values are shipped today:

- `package` — a software package or library (knowledge domain).
- `doc` — documentation (knowledge domain).
- `note` — an agent or user note (memory or knowledge).
- `pointer` — a bare external reference with minimal body.

Consumers can and should introduce new `kind` values as needed (e.g., `playbook`, `adr`, `todo`). Nothing in Vanta validates the `kind` string — it is a coordination convention.

## Filtering by facet

`conduit_lookup` accepts `facet_kinds` and `facet_sources` as JSON-array filters. Use these to narrow a cross-domain search:

```json
{"query": "embedding provider", "facet_kinds": ["package"], "limit": 10}
```

## Extension rules

- Stay short. Multi-word kinds get awkward; use `-` separators if needed (e.g., `decision-record`).
- Stay stable. Don't churn the value after adoption; treat it like a public API.
- Document your conventions elsewhere. Consumer repos own the "what does `kind=adr` mean" docs.

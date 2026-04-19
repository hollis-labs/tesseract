---
name: context-packet
description: Boot workflows — broker plan/fetch, packet assembly, budget tuning.
scope_hint: none
related: [namespaces, views]
---

# Context packet

A **packet** is a budget-bounded, ordered set of records assembled for an agent — typically at boot or task resume. Pins come first; then namespace-matched records; then a manifest recording what was included and why.

The three relevant tools — `context_broker_plan`, `context_broker_fetch`, and `context_packet` — work together. The broker names are retained for MCP compatibility; internally they are the context *query planner*, not the universal ContextBroker.

## One-shot: `context_broker_fetch`

The simplest path. Combines planning and fetching in a single call:

```json
{
  "intent": "boot_project",
  "summary": "Vanta Conduit backend — batch 1 parity work",
  "budget_items": 50,
  "budget_tokens": 4000,
  "payload_mode": "full"
}
```

`intent` is one of `resume_task | boot_project | review_session | custom` (default `custom`).

- `resume_task` — extracts up to 3 keywords from `summary`, globs as `user/memory/<kw>*`, plus `user/pins/*`.
- `boot_project` — `user/memory/*` + `user/pins/*`; bumps item budget to at least 100.
- `review_session` — `user/cache/*` + `user/pins/*`.
- `custom` — `user/*` catch-all when no explicit constraints are provided.

## Two-phase: `context_broker_plan` + `context_packet`

When you want to inspect or edit the plan before executing:

1. `context_broker_plan` — returns `{plan: {namespaces, include_pins, budget}, rationale}`. No fetch.
2. `context_packet` — accepts `namespaces` (comma-separated glob string), `include_pins` (default `true`), and executes with your chosen parameters.

Reach for two-phase when you want to override globs, tighten budgets, or inspect the rationale before committing to a fetch.

## Budgets

Two tool surfaces, two parameter names — same semantics:

- `context_broker_plan` / `context_broker_fetch` → `budget_items` (default 50), `budget_tokens` (default 4000).
- `context_packet` → `max_items` (default 50), `max_tokens_estimate` (default 8000).

Records are included in selector order until either budget is hit. The manifest's `truncated` / `truncation_reason` fields identify the binding constraint (`budget.max_items` or `budget.max_tokens_estimate`), so you can tune the next call.

## Payload modes

- `full` (default) — include the full payload JSON.
- `head_only` — payloads larger than 512 bytes are truncated to a 512-byte head; smaller payloads pass through unchanged.

Use `head_only` to survey a large surface cheaply; follow up with `full` on a narrower namespace set.

## Pins

User pins live at `user/pins/*` (see `vanta_skills namespaces`). With `include_pins: true` (the default), pin records are loaded first and counted against the budget before any namespace-matched candidates. The manifest reports `pins_included`.

## Manifest

Every packet returns a `manifest` alongside `items`:

- `request_id`, `pins_included`, `items_total`, `items_returned`.
- `bytes_returned`, `tokens_estimate` — what was actually returned.
- `truncated`, `truncation_reason` — whether/why a budget bound stopped inclusion.
- `sources` — per-namespace-prefix count of included records.

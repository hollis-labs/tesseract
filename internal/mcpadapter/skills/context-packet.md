---
name: context-packet
description: Boot workflows — broker planning and execution, packet assembly, budget tuning.
scope_hint: none
related: [namespaces, views]
---

# Context packet

A **packet** is a budget-bounded, ordered set of records assembled for an agent — typically at boot or task resume. Pins come first; then namespace-matched records; then a manifest recording what was included and why.

Two tools cover this, each with an arm selector:

- `context_broker` — `execute` selects between returning a plan and running it. The broker name is retained for continuity; internally this is the context *query planner*, not the universal ContextBroker.
- `context_pack` — `shape` selects between ranking a named view (`list`, the default) and assembling namespace globs into a packet (`packet`).

## One-shot: `context_broker` with `execute: true`

The simplest path. Plans and fetches in a single call:

```json
{
  "execute": true,
  "intent": "boot_project",
  "summary": "Tesseract backend — batch 1 parity work",
  "budget_items": 50,
  "budget_tokens": 4000
}
```

`intent` is one of `resume_task | boot_project | review_session | custom` (default `custom`).

- `resume_task` — extracts up to 3 keywords from `summary`, globs as `user/memory/<kw>*`, plus `user/pins/*`.
- `boot_project` — `user/memory/*` + `user/pins/*`; bumps item budget to at least 100.
- `review_session` — `user/cache/*` + `user/pins/*`.
- `custom` — `user/*` catch-all when no explicit constraints are provided.

## Two-phase: plan, then assemble

When you want to inspect or edit the plan before executing:

1. `context_broker` with `execute` omitted — returns `{plan: {namespaces, include_pins, budget}, rationale}`. Reads no records.
2. `context_pack` with `shape: "packet"` — accepts `namespaces` (comma-separated glob string), `include_pins` (default `true`), and executes with your chosen parameters.

Reach for two-phase when you want to override globs, tighten budgets, or inspect the rationale before committing to a fetch.

## Budgets

Two tool surfaces, two parameter names — same semantics:

- `context_broker` → `budget_items` (default 50), `budget_tokens` (default 4000).
- `context_pack` with `shape: "packet"` → `max_items` (default 50), `max_tokens_estimate` (default 8000).

Records are included in selector order until either budget is hit. The manifest's `truncated` / `truncation_reason` fields identify the binding constraint (`budget.max_items` or `budget.max_tokens_estimate`), so you can tune the next call.

## Capping payloads

`payload_max_bytes` caps how much of each record's payload comes back. Omit it, or pass 0, for no cap.

When the cap binds, the item carries **no `payload` key at all**. Instead it carries:

- `payload_head` — a JSON string holding the first N bytes,
- `payload_truncated: true`,
- `payload_bytes` — the full payload's size.

So an absent `payload` under a cap means **withheld**, never empty. Use a small cap to survey a large surface cheaply, then re-read a narrower namespace set with no cap.

`payload_mode` is **not** a projection knob on these two tools: they accept only `payload_mode: "full"`, and any other value is a `validation_error`. The `keys | summary | full` projection lives on the recall and lookup tools, where it means one thing.

## Pins

User pins live at `user/pins/*` (see `tesseract_skills namespaces`). With `include_pins: true` (the default), pin records are loaded first and counted against the budget before any namespace-matched candidates. The manifest reports `pins_included`.

## Manifest

Every packet returns a `manifest` alongside `items`:

- `request_id`, `pins_included`, `items_total`, `items_returned`.
- `bytes_returned`, `tokens_estimate` — what was actually returned.
- `truncated`, `truncation_reason` — whether/why a budget bound stopped inclusion.
- `sources` — per-namespace-prefix count of included records.

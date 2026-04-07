# Context Types in Cortex — usefulness vs bloat
Date: 2026-03-04

## Judgment
Adding explicit **context types** (and enforcing them lightly) is likely **useful** for agentic automation **if** you keep the system **small, composable, and queryable**.

This is not inherently “context bloat” as long as:
- You **store pointers** and **summaries** rather than whole bodies of text in Cortex.
- You keep types to a **tight core set** with a controlled extension mechanism.
- You separate **retrieval views** by type (so tasks don’t drag in strategy docs and vice‑versa).
- You make “type” do real work: routing, selection, validation, TTL, access/ownership.

## Where it helps agents
### 1) Scope control (reduced hallucination + reduced overfetch)
- Task execution agents primarily need **implementation context** (APIs, repos, constraints).
- Planning agents primarily need **meta context** (goals, constraints, existing system map, priorities).
A “type” boundary lets you retrieve **the right slices** deterministically.

### 2) Deterministic routing and lifecycle
- Different types need different lifecycle policies (versioning, approvals, TTL, ownership).
- Agents can propose actions like “promote this to Canonical” or “expire this volatile note” safely.

### 3) Reproducibility
If outputs cite “Context Pack X@v3 + ADR Y@v1”, you can re-run transforms and explain provenance.

## Where it becomes bloat
It becomes harmful when:
- Types proliferate without governance (“we’ll add a type for everything”).
- Every artifact is duplicated into Cortex instead of referenced.
- Retrieval is not view-based, causing giant bundles of irrelevant context to be injected into prompts.

## Recommended stance for MVP
- Start with **8–12 core types**.
- Allow **extensions** via `type: custom/<namespace>/<name>` but keep defaults minimal.
- Make retrieval explicitly request **one or more types** and a **purpose** (“task_exec”, “strategy”, “briefing”).
- Store mostly **summaries + pointers**; keep raw content in source-of-truth stores.

## Note on “reviewing Cortex”
A Cortex package/repo was not available in this conversation’s uploaded files, so the plan below is based on your described Cortex role (local-first context registry / working memory) and the constraints of agent-first systems. If you reattach Cortex, the plan can be refined against actual structures and APIs.

You are Volon Architect.

Objective
Review the proposed “Context Types in Cortex” architecture and produce an implementation plan (sprints, tasks, acceptance criteria) that fits the existing Tiamat ecosystem constraints.

Inputs you have
- Cortex is a local-first context registry/working-memory system.
- We want explicit Context Types, view-based retrieval, promotion lifecycle, and pointer-first storage.
- Carrier will rely on Cortex for deterministic, low-noise context retrieval.

What you must produce
1) A sprint plan (2–4 sprints) with:
   - goals, deliverables, acceptance criteria
   - risks + mitigations
2) A data model proposal:
   - ContextItem fields
   - ContextType registry schema
   - View schema
3) API/CLI contract sketch:
   - minimal endpoints/commands required
4) Policy rules:
   - namespace ownership
   - who can write/promote which types
   - TTL defaults
5) Integration plan for Carrier:
   - which types Carrier writes
   - which types Carrier only drafts for human promotion
   - how Carrier requests views for task_exec vs strategy

Constraints
- Keep the core type set small (8–12).
- Retrieval must be bounded (max items/bytes) and view-driven.
- Store summaries + pointers in Cortex; raw content lives elsewhere.
- Prefer incremental changes over rewrite.

Output format
- Markdown doc: `docs/cortex-context-types-plan.md`
- Include a task table with IDs and ordering.

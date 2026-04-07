# Updated Architecture Summary (Pivot)

Date: 2026-02-25

## What changed
- Scope moved from personal-cache-centric to generalized context registry + working memory.
- `user/*` is explicitly first-class inside the same namespace model.
- Storage model clarified around append-only records + heads for all namespaces.
- Views are now explicit deterministic selectors for retrieval.
- PCC relationship clarified: complementary, not replaced.

## Why
The broader model supports multiple tools/agents consistently while preserving strong ownership and deterministic behavior.

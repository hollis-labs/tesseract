# ADR-0002: Generalized Context Registry Pivot

Date: 2026-02-25
Status: accepted

## Context
Initial framing emphasized a personal cache model. Updated requirements broaden the product to a general context registry and working-memory system supporting multiple local clients and integration paths (CLI/API/MCP).

## Decision
1. Reframe product scope from personal-cache-first to general context registry + working memory.
2. Keep personal cache as first-class `user/*` namespace under the same model.
3. Standardize on append-only records + head pointers for all namespaces.
4. Define retrieval through deterministic views/selectors.
5. Treat PCC as complementary orchestration context, not a replacement for service data storage.

## Consequences
Positive:
- One coherent model for user and app contexts.
- Better compatibility across CLI, HTTP API, and future MCP adapter.
- Deterministic selectors improve repeatability for automation.

Trade-offs:
- Selector model must be specified carefully to avoid hidden processing behavior.
- Broader scope increases initial interface surface area.

Follow-up:
- Update API/CLI specs for multi-client usage and deterministic selectors.
- Publish dedicated Views specification and examples.

## Related docs
- `docs/SCOPE.md`
- `docs/ARCHITECTURE.md`
- `docs/SPECS/STORAGE.md`
- `docs/SPECS/API.md`
- `docs/SPECS/CLI.md`

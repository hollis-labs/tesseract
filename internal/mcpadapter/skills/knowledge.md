---
name: knowledge
description: The pointer-first knowledge domain - kind/source/pointer facets, when to use knowledge vs. memory.
scope_hint: memory:read
related: [facets-and-kinds, memory]
---

# Knowledge domain

Knowledge is for **pointer-first references to external content**: packages, documents, notes that live somewhere else. The reference is the primary artifact; the summary and body are aids for discovery and embedding.

## When to use knowledge

- Recording that a library exists and what it does.
- Cataloging a document's location and summary.
- Capturing a reference to external content you'll want to find later via search or similarity.

## When NOT to use knowledge

- **Agent-authored content with no external source.** Use memory (`memory_write`).
- **Generic records.** Use `context_write`.

## Namespace rule

A knowledge namespace MUST have shape `{user|app}/{id}/knowledge[/...]`. The third segment must literally be the word `knowledge`. Examples:

- Valid: `user/chrispian/knowledge`, `user/chrispian/knowledge/framework`, `app/ingester/knowledge/obsidian/work`.
- Invalid: `user/alice/memory/knowledge` (`knowledge` not 3rd segment), `knowledge/user/alice` (missing `user/` or `app/` prefix), `org/acme/knowledge` (wrong first segment).

## Required fields on knowledge_write

From the `knowledge_write` MCP declaration:

- `namespace` (required; must satisfy the shape above)
- `kind` (required) - facet: `package`, `doc`, `note`, `pointer`, ...
- `source` (required) - facet: `filesystem`, `obsidian`, `nil`, `web`, `manual`, ...
- `pointer_scheme` (required) - `file`, `http`, `https`, `obsidian`, `nil`, ...
- `pointer_locator` (required) - scheme-specific address.
- `summary` (required) - short summary; feeds embeddings.
- `author_agent_id` (required)
- `session_id` (required)

Optional: `key` (logical slug), `pointer_resolved_at` (RFC3339; defaults to now), `body`, `author_version`, `tags`, `ttl_seconds`, `confidence` (defaults to `0.9`), `supersedes`.

Every knowledge write is stamped `Domain=knowledge`, `status=canonical`, `trigger=manual`, `origin=reference` - these are fixed at the write path, not caller-controlled.

## Example

```json
{
  "namespace": "user/chrispian/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "filesystem",
  "pointer_scheme": "file",
  "pointer_locator": "/Users/chrispian/Projects-apps/framework/libs/go-providers",
  "summary": "go-providers: multi-provider AI adapter (Anthropic, OpenAI, Ollama, ...)",
  "body": "Exports provider.Embedder and provider.Completer. Replace-directive in consumer go.mod.",
  "author_agent_id": "indexer",
  "session_id": "indexer:2026-04-15"
}
```

## Reading

- `knowledge_get` returns the current revision by `(namespace, key)`. Returns `not_found` if the entry exists but is not knowledge-domain (cross-domain reads are filtered).
- `knowledge_history` returns the full chain, newest first; non-knowledge revisions are filtered out.
- For cross-domain search (memory + knowledge), use `tesseract_lookup` with `facet_kinds` / `facet_sources` filters. See `tesseract_skills facets-and-kinds`.

## Confidence default

Knowledge `confidence` defaults to `0.9` when omitted. Memory requires an explicit value. Override per-call if you want a lower confidence.

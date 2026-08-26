---
name: views
description: The selectors-not-processors model - namespace globs, revision scope, deterministic ordering.
scope_hint: none
related: [namespaces, recall-and-ranking]
---

# Views

Views are **selectors**, not processors. They return records that match criteria in a deterministic order. They do not synthesize, merge, summarize, or rank-for-relevance - that's the recall pipeline's job.

## Selector shape

The shared `contextstore.Selector` struct:

```json
{
  "namespaces": ["user/chrispian/memory/*", "app/test/session/*"],
  "keys": ["task-001", "goal"],
  "revision_scope": "head",
  "order": ["namespace", "key", "revision"],
  "limit": 50,
  "tags_any": ["priority", "pinned"],
  "types": ["state"],
  "statuses": ["canonical"]
}
```

Fields:

- `namespaces` - glob patterns. `views_evaluate` takes an array; `context_view` takes a comma-separated string.
- `keys` - optional explicit key filter list (capped by `maxSelectorKeys`).
- `revision_scope` - `head` (default; current revision per namespace/key) or `all` (full history). Case-insensitive; anything other than `all` normalizes to `head`.
- `order` - stable sort keys. Default when omitted: `["namespace", "key", "revision"]`. Allowed: `namespace`, `key`, `revision`, `created_asc`, `created_desc`.
- `limit` - `DefaultSelectLimit` when 0; max 500.
- `tags_any` - records with at least one matching tag (OR semantics).
- `types` - filter by `record_type`.
- `statuses` - filter by status.

Unknown selector fields are rejected as `validation_error`.

## Two surfaces

- `context_view` - simplified MCP tool for agent queries. Takes `namespaces` (comma-separated), `revision_scope`, `limit`. Returns summary records (payload omitted from list view).
- `views_evaluate` - full selector via JSON. Matches the HTTP `POST /v1/views/evaluate` envelope exactly. Takes `selector` (JSON object), `include_payload` (default false), `limit` (overrides selector.limit). Returns `items` + `evaluation_meta` (`sort_keys`, `matched_count`, `truncated`, `normalized_scope`).

Memory-domain recall uses a different revision-scope vocabulary (`current` / `timeline`) - see `tesseract_skills recall-and-ranking`. Views speak the context-store vocabulary (`head` / `all`).

## Ordering

When `order` is omitted, results sort by `(namespace, key, revision)` as a stable tiebreak. Don't rely on insertion order; always reason against the declared order or the canonical fallback.

## What views don't do

- No ranking (use `tesseract_recall`).
- No payload transforms.
- No cross-namespace joins beyond the glob set.
- No synthesis or summarization.

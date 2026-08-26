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

- `namespaces` - glob patterns. As a JSON selector this is an array; `context_view` also accepts the same globs as a comma-separated string, in either `selector` or `namespaces`.
- `keys` - optional explicit key filter list (capped by `maxSelectorKeys`).
- `revision_scope` - `head` (default; current revision per namespace/key) or `all` (full history). Case-insensitive; anything other than `all` normalizes to `head`.
- `order` - stable sort keys. Default when omitted: `["namespace", "key", "revision"]`. Allowed: `namespace`, `key`, `revision`, `created_asc`, `created_desc`.
- `limit` - `DefaultSelectLimit` when 0; max 500.
- `tags_any` - records with at least one matching tag (OR semantics).
- `types` - filter by `record_type`.
- `statuses` - filter by status.

Unknown selector fields are rejected as `validation_error`.

## Two arms of `context_view`

`include_meta` selects which arm answers. It is not a display knob — the two arms query differently and answer differently.

- **`include_meta` omitted** - the simplified agent query. Takes `namespaces` (comma-separated) or `selector` in its glob form, plus `revision_scope` and `limit`. Returns summary records in the shared budget envelope; payloads are never included. Results are filtered by the capability token's namespace globs when a token is configured.
- **`include_meta: true`** - the full selector. Matches the HTTP `POST /v1/views/evaluate` envelope exactly. Takes `selector` (JSON object or globs), `include_payload` (default false), `limit` (overrides selector.limit). Returns `items` + `evaluation_meta` (`sort_keys`, `matched_count`, `truncated`, `normalized_scope`). Like its HTTP peer, this arm does **not** filter by the token's namespace globs.

Knobs the chosen arm cannot honor are rejected rather than ignored: `include_payload` without `include_meta` is a `validation_error`, as is a JSON selector carrying `keys`, `order`, `limit`, `tags_any`, `types` or `statuses` on the default arm.

Memory-domain recall uses a different revision-scope vocabulary (`current` / `timeline`) - see `tesseract_skills recall-and-ranking`. Views speak the context-store vocabulary (`head` / `all`).

## Ordering

When `order` is omitted, results sort by `(namespace, key, revision)` as a stable tiebreak. Don't rely on insertion order; always reason against the declared order or the canonical fallback.

## What views don't do

- No ranking (use `tesseract_recall`).
- No payload transforms.
- No cross-namespace joins beyond the glob set.
- No synthesis or summarization.

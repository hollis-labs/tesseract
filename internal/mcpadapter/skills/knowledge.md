---
name: knowledge
description: The knowledge domain - write the body as the artifact, the pointer as a convenience. kind/source/pointer facets, pointer health, when to use knowledge vs. memory.
scope_hint: memory:read
related: [facets-and-kinds, memory]
---

# Knowledge domain

Knowledge is for **references to external content**: packages, documents, notes that live somewhere else.

## Write the body as the artifact; the pointer is a convenience

**The body is the durable half of a knowledge entry. The pointer is the half that rots.**

A path or URL is a claim about a machine at a moment. Files move, repos are reorganized, directory trees are retired, hosts go away. When that happens the entry does not announce it — an agent following the pointer just fails, with no way to have known in advance. The summary and body, by contrast, are inside Tesseract and last as long as the store does.

So write the entry as though the pointer will stop working, because eventually it will:

- Put **what you actually learned** in `body` — the API shape, the decision, the gotcha, the thing you would have to re-derive. Not "see the file".
- Treat `pointer_locator` as a **convenience** for getting back to the original, not as the content.
- An entry whose body is a stub and whose pointer is dead carries nothing. That is the failure mode this guidance exists to prevent.

### `pointer_scheme: nil` is a first-class pattern

When the content has no external source — the knowledge is what you wrote, and there is nothing to link to — use `pointer_scheme: "nil"`. This is **a normal, correct way to write knowledge**, not a fallback for when you cannot find a path.

Use it whenever the body is the artifact. It is honest, it never rots, and it reads as `not_applicable` in pointer health rather than sitting in the pile of entries nobody has verified.

Reach for a real `file:` or `https:` pointer when the external thing genuinely is the artifact (a package you do not control, a spec, a repo) — and still write a body worth reading on its own.

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
- `kind` (required) - facet, **closed vocabulary**: `doc`, `handoff`, `investigation`, `learning`, `mcp_server`, `note`, `package`, `playbook`, `pointer`, `project_canonical`, `session_close`. Anything else is rejected with an error naming the allowed set. See `tesseract_skills facets-and-kinds` for what each means and how to request an addition.
- `source` (required) - facet, conventional (not validated): `filesystem`, `obsidian`, `nil`, `web`, `manual`, ...
- `pointer_scheme` (required) - `file`, `http`, `https`, `obsidian`, `nil`, ...
- `pointer_locator` (required) - scheme-specific address.
- `summary` (required) - short summary; feeds embeddings.
- `author_agent_id` (required)
- `session_id` (required)

Optional: `key` (logical slug), `pointer_resolved_at` (RFC3339; defaults to now), `body`, `author_version`, `tags`, `ttl_seconds`, `confidence` (defaults to `0.9`), `supersedes`.

`pointer_resolved_at` is **your assertion at write time**, not a verification — nothing checks the pointer on the write path, by design, because a pointer that is unreachable now may be reachable in an hour. Whether a pointer actually resolves is answered by pointer health, below.

Every knowledge write is stamped `Domain=knowledge`, `status=canonical`, `trigger=manual`, `origin=reference` - these are fixed at the write path, not caller-controlled.

## Example

The body carries what you learned; the entry stays useful with no external source to point at.

```json
{
  "namespace": "user/chrispian/knowledge/framework",
  "key": "framework.go-providers",
  "kind": "package",
  "source": "manual",
  "pointer_scheme": "nil",
  "pointer_locator": "framework/go-providers",
  "summary": "go-providers: multi-provider AI adapter (Anthropic, OpenAI, Ollama, ...)",
  "body": "Exports provider.Embedder and provider.Completer, both single-method interfaces. Consumers wire it with a replace-directive in go.mod rather than a version tag. Embedder returns a struct, not a bare slice, so a caller reads .Embedding.",
  "author_agent_id": "indexer",
  "session_id": "indexer:2026-04-15"
}
```

`pointer_locator` is still required when the scheme is `nil` — give it a stable identifier for the thing (a slug, a module path), not a filesystem path. It is a label, and nothing tries to resolve it.

When the external artifact genuinely is the point, use a real scheme — and **verify the path exists before you write it**. A pointer is only ever as good as the moment it was written:

```json
{
  "pointer_scheme": "https",
  "pointer_locator": "https://github.com/hollis-labs/tesseract",
  "source": "web"
}
```

## Reading

- `knowledge_get` returns the current revision by `(namespace, key)`. Returns `not_found` if the entry exists but is not knowledge-domain (cross-domain reads are filtered).
- `knowledge_history` returns the full chain, newest first; non-knowledge revisions are filtered out.
- For cross-domain search (memory + knowledge), use `tesseract_lookup` with `facet_kinds` / `facet_sources` filters. See `tesseract_skills facets-and-kinds`.

## Pointer health

Every knowledge result from `tesseract_lookup` (and `memory_recall`, when pointed at a knowledge namespace) carries a `pointer_health` object under `payload_mode` `summary` and `full`. It is **not** carried under `keys`, which stays identity-only.

`pointer_health.status` is one of:

| status | means |
|---|---|
| `resolved` | a resolver reached the target and saw it |
| `unresolvable` | a resolver got a **definitive negative** — the file is not there, or an origin answered 404/410 |
| `unverifiable` | the resolver **could not tell**: a timeout, a 403, a rate limit, or a scheme nothing in this build can check. **Not evidence the pointer is dead.** |
| `unchecked` | the pointer names something external and **nobody has looked yet** |
| `not_applicable` | scheme `nil` — the record declares it has no external source |

Two readings that matter:

- **The field being absent means the revision has no pointer facet at all.** It never means "healthy".
- **`unchecked` is not `resolved`.** A pointer nobody has verified is exactly that, and it is the largest group on a store where the verification job has not swept everything.

`pointer_health.last_resolved_at` is the discriminator for `unverifiable`: a pointer that resolved yesterday and times out today is a blip, while one that has never resolved is a different problem. `pointer_health.detail` names the specific reason (`http_403`, `timeout`, `unsupported_scheme:conduit`, `not_found`).

### Finding suspect entries by query

Pass `pointer_health` to `tesseract_lookup` as a JSON array of statuses. The filter is applied in SQL **before** `limit`, so this enumerates the set rather than sampling whatever ranked well:

```json
{
  "namespaces": ["user/chrispian/knowledge"],
  "pointer_health": ["unresolvable"],
  "limit": 200
}
```

Use `["unresolvable"]` for entries whose pointer is confirmed gone — those need their body checked, or a rewrite to `nil`. Use `["unchecked"]` to see what has never been swept.

### Recording verification

Verification is a **CLI operation**, never part of a write. `contextd verify-pointers` resolves pointers and appends what it observed to a verification log keyed by revision; it is dry-run by default and opens the store read-only unless `--apply` is passed. Network schemes are opt-in via `--schemes`.

The authored revision is never rewritten by this: `pointer_resolved_at` stays the author's write-time assertion, and the observation log is the authority for what actually resolved.

## Confidence default

Knowledge `confidence` defaults to `0.9` when omitted. Memory requires an explicit value. Override per-call if you want a lower confidence.

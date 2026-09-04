---
name: start-here
description: Orientation for agents new to Tesseract — the three domains, invariants, and how to use tesseract_skills.
scope_hint: none
related: [namespaces, memory, knowledge]
---

# Tesseract — start here

Tesseract is a local-first, append-only context and memory service. You reach it through the `mcp__tesseract__*` tool family. Everything you write is revisioned, auditable, and namespace-owned.

## The three domains

- **Memory** — agent-authored observations, preferences, session notes. Recall by activation, chronological order, semantic similarity, or hybrid relevance. Start with `tesseract_skills memory`.
- **Knowledge** — pointer-first references to external content (packages, docs, notes). Every knowledge write carries `kind`/`source`/`pointer` facets. Start with `tesseract_skills knowledge`.
- **Context** — generic revisioned records for app-scoped state (session workspaces, typed payloads, packets). Used heavily by framework tooling; agents typically reach for memory or knowledge instead.

Search across memory + knowledge with `tesseract_recall` — the unified query surface.

## Invariants (don't fight these)

- **Append-only.** Every write creates a new revision. Nothing is mutated in place.
- **Namespace-owned.** `user/*` is user-owned (write-protected except via promotion). `app/*` is app-owned. See `tesseract_skills namespaces`.
- **Deterministic.** Identical selectors against identical state return identical results.
- **Audited.** Context writes and promotions are logged today; memory and knowledge write audit is in flight (see `tesseract_skills audit`). Use `tesseract_skills audit` to query.
- **Views are selectors, not processors.** Retrieval does not synthesize, merge, or infer.

## How to use `tesseract_skills`

- `tesseract_skills` with no args — returns this index.
- `tesseract_skills` with `name=<skill-name>` — returns the full body of a single skill.

Skills are progressive: the index is small; bodies only load when requested.

## Where request shapes live

Every write tool's description opens by naming the skill that carries its request shape. **Read that skill before your first write of a kind.** The shapes are not guessable: several fields are closed vocabularies enforced at the write path, and the two surfaces below do not take the same JSON.

| What you want to write | Tool | Shape lives in |
|---|---|---|
| An agent observation, preference, or session note | `memory_write` | `tesseract_skills memory` |
| A reference to content that lives outside Tesseract | `knowledge_write` | `tesseract_skills knowledge` |
| A plain revisioned record | `context_write` | below, on this page |
| A record with a registered type and lifecycle status | `context_typed_write` | below, on this page |
| Many records at once, or one long document | `context_ingest` | below, on this page |
| A session snapshot for the next boot to read | `context_session_write` | `tesseract_skills context-packet` |
| A record across the app → user ownership boundary | `context_promote` | `tesseract_skills promotion` |
| A lifecycle status change, in place | `context_status_set` | `tesseract_skills promotion` |
| A new revision that replaces an existing one | `memory_write` / `knowledge_write` with `supersedes` | `tesseract_skills revisions` |

## Two surfaces, and they do not take the same JSON

Everything here is reachable over MCP and over HTTP. Where a skill shows both, the differences are real rather than cosmetic:

- **MCP passes structured arguments as JSON-encoded strings.** `payload`, `items` and `tags` are declared as strings on the MCP surface; over HTTP they are real JSON values.
- **MCP flattens nested objects into prefixed scalars.** `pointer_scheme` / `pointer_locator` and `author_agent_id` / `author_version` on MCP are `pointer: {scheme, locator}` and `author: {agent_id, agent_version}` over HTTP.
- **The `POST /v1/context/*` routes reject unknown fields**, so a field copied across from the MCP shape is a `validation_error` rather than a quietly ignored key.

Every `curl` in these skills assumes two variables:

```bash
TESSERACT_URL=http://127.0.0.1:8080   # whatever `tesseract serve --addr` bound
TESSERACT_TOKEN=<capability token>    # omit the header entirely under --auth-mode none
```

## A first write, on both surfaces

`context_write` is the smallest complete write. Over MCP — note that `payload` is a JSON-encoded **string**, not an object:

```json
{
  "namespace": "app/my-agent/session/task-001",
  "key": "status",
  "payload": "{\"phase\":\"implementing\",\"blocked_on\":null}",
  "actor": "app:my-agent"
}
```

The same write over HTTP, where `payload` is a real object and the envelope also carries `client_id` (the policy engine evaluates it alongside `actor`):

```bash
curl -sS -X POST "$TESSERACT_URL/v1/context/write" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "my-agent",
    "actor": "app:my-agent",
    "namespace": "app/my-agent/session/task-001",
    "key": "status",
    "payload": {"phase": "implementing", "blocked_on": null}
  }'
```

Both answer with the new record's identity — `record_id`, `revision`, `namespace`, `key`. Neither mutates anything: the previous revision is still there, and `tesseract_history` under `domain="context"` will show both.

Where the write may land is decided by the namespace, not by this call — see `tesseract_skills namespaces`. Over MCP the capability token's namespace globs are checked first, and a target outside them is a `namespace_not_permitted` error.

## A typed write

`context_typed_write` is the same write plus a registered `record_type` and a lifecycle `status`. The type registry rejects rather than relaxes: an unregistered type, a status the type disallows, and a payload missing a field the type requires are all `validation_error`. `task/spec` requires `title`, so:

```json
{
  "namespace": "app/my-agent/specs",
  "key": "task-001",
  "payload": "{\"title\":\"Unify the token budget contract\",\"owner\":\"backend\"}",
  "record_type": "task/spec",
  "status": "draft",
  "actor": "app:my-agent"
}
```

```bash
curl -sS -X POST "$TESSERACT_URL/v1/context/typed-write" \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "my-agent",
    "actor": "app:my-agent",
    "namespace": "app/my-agent/specs",
    "key": "task-001",
    "payload": {"title": "Unify the token budget contract", "owner": "backend"},
    "record_type": "task/spec",
    "status": "draft"
  }'
```

Ask the deployment which types it has rather than assuming this list: `context_registry_list` with `kind: "types"`, or `GET /v1/context/types`.

## Many records at once

`context_ingest` under `mode: "bulk"` takes a JSON-encoded array of exactly the object above, up to 100 per call. `stop_on_error` is `false` by default, so a malformed entry is reported in that item's `results` slot and the rest of the batch still lands:

```json
{
  "mode": "bulk",
  "items": "[{\"namespace\":\"app/my-agent/specs\",\"key\":\"task-001\",\"payload\":{\"title\":\"First\"},\"record_type\":\"task/spec\"},{\"namespace\":\"app/my-agent/specs\",\"key\":\"task-002\",\"payload\":{\"title\":\"Second\"},\"record_type\":\"task/spec\"}]",
  "embed": false
}
```

Note the nesting: `items` is a string holding an array whose elements carry `payload` as a real object. The HTTP peer `POST /v1/context/bulk-ingest` takes `items` as an actual array.

## Common next steps

- Writing an agent memory? → `tesseract_skills memory`
- Recording a reference to external content? → `tesseract_skills knowledge`
- Looking something up? → use `tesseract_recall` directly, then **close the loop**: hydrate chosen hits with `tesseract_get_revision`, and after reasoning pass projected hits that shaped the turn to `tesseract_touch`. Recall itself does not reinforce; deliberate gets reinforce once, while touch reports use that happened without a fetch (or adds an intentional second signal). `tesseract_skills recall-and-ranking` for ranking modes.
- Working across user/app namespace boundaries? → `tesseract_skills promotion`.
- Booting into a project? → `tesseract_skills context-packet`.

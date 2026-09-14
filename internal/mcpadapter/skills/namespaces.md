---
name: namespaces
description: Canonical namespace patterns, scope-type-rooted paths, ownership, and authority rules.
scope_hint: none
related: [start-here, workspace, memory, knowledge, event, promotion]
---

# Namespaces

Tesseract organizes every record under a **namespace** — a path-like string encoding scope type, scope ID, and domain. Scope type sits at the root; ownership lives in the namespace registry rather than in the path. The namespace is authoritative: it determines write permissions, read visibility, and promotion requirements.

## Scope types — the root segment

The first segment names the **scope type** and the second its ID. `system` is a singleton and takes no ID segment.

| Scope | Means |
|---|---|
| `user/{id}/` | content the human authors or explicitly directs — and nothing else |
| `project/{slug}/` | work *about* a thing we are building |
| `app/{id}/` | an application using Tesseract as its own datastore for its own internal data |
| `org/{slug}/` | organisation-level context |
| `session/{sid}/` | session-scoped ephemeral work |
| `system/` | the agent OS's own content — guidelines, SOPs, templates, glossary. **Singleton — no ID segment** |

`app/` is never used for writing *about* an application: `app/tether` is Tether storing Tether's data; our engineering work on Tether is `project/tether`.

**Prefix sweeps with `/*`**: `{scope}/{id}/*` or `{scope}/*` sweeps at any tier — `project/*` (all projects), `project/tether/*` (one project across all domains), `project/tether/knowledge/*` (all knowledge under tether).

## Five domains

Every namespace path addresses one of Tesseract's five domains:

```
{scope}/{id}/workspace/...             # mutable replaceable working state (free depth)
{scope}/{id}/knowledge/...             # durable reference content (free depth)
{scope}/{id}/memory/{type}             # append-only typed memories (fixed depth)
{scope}/{id}/event/{type}              # append-only narrative log (fixed depth)
system/workspace/...                   # singleton system scope (no id segment)
system/knowledge/...
system/memory/{type}
system/event/{type}
```

Pre-migration legacy shapes (`user/{id}/project/{pid}/memory/{type}`) continue to parse for compatibility while the migration runs.

## Workspace domain — mutable, free depth

The workspace domain (`workspace_write`, `workspace_delete`, and `tesseract_recall` with `domains=["workspace"]`) holds replaceable working material owned by a project or system:

```text
project/{project_id}/workspace/{purpose}/...
session/{session_id}/workspace/{purpose}/...
system/workspace/{purpose}/...
```

- **Free depth** after `workspace`.
- **Instance vs. pattern:** drafts, scratchpad analysis, dispatch prompts, session-temp plans, handoffs, and boot prompt instances belong here.
- **Overwrite-in-place:** items have a stable `item_id` and a `version_token`; workspace retains no revision history.
- **Excluded from default recall** and carries no embeddings.

## Knowledge domain — durable, hierarchical, free depth

Knowledge (`knowledge_write`, `tesseract_get`) is content addressed by key that a later session will look up by name:

```text
project/{project_id}/knowledge/...
system/knowledge/...
user/{user_id}/knowledge/...       # human-authored or explicitly directed only
app/{app_id}/knowledge/...
```

- **Fixed-depth scope head** (`{scope}/{id}/knowledge/` or `system/knowledge/`) with **free depth after it**.
- Classification lives in the `kind` facet (`doc`, `investigation`, `playbook`, `package`, `project_canonical`, etc.), not in the namespace path.
- A bare `/knowledge` namespace is an exact namespace, **NOT a prefix request**. To sweep knowledge, use the explicit prefix: `project/tether/knowledge/*`.

## Memory domain — typed, fixed depth

The memory domain (`memory_write`, `tesseract_recall`) uses a **fixed-depth typed namespace**. The `{type}` segment is the only structural level; finer classification uses tags:

```text
project/{project_id}/memory/{type}
system/memory/{type}
user/{user_id}/memory/{type}       # human-authored or explicitly directed only
session/{session_id}/memory/{type}
```

**Allowed types** (closed vocabulary):
- `decisions` — design calls with rationale and rejected alternatives
- `feedback` — guidance about how to approach work
- `followups` — work deliberately deferred with context
- `learnings` — distilled understanding from a session
- `limitations` — known constraints or preserved tech debt
- `notes` — catch-all default bucket when no stronger type fits
- `outcomes` — what happened / what was true after the work
- `todos` — flat list items with light state in `consumer_state`

Recall accepts prefix form `project/{pid}/memory` or `project/{pid}/memory/*` to match all types under that project.

## Event domain — typed narrative log

The event domain (`event_write`, `event_list`) uses memory's fixed-depth grammar with `event` in the domain-segment position:

```text
project/{project_id}/event/{type}
session/{session_id}/event/{type}  # an agent's per-session thinking
system/event/{type}
user/{user_id}/event/{type}        # human personal journal
```

**Allowed types** (closed vocabulary):
- `reasoning` — an agent's narrative log of what it did and why
- `journal` — human personal log stream

**No dates in the path.** `created_at` is indexed and `event_list` filters on `since`/`until`. Time is an attribute, not a partition.

## Authority rules and CanWrite enforcement

- **`user/*` is protected.** Only `actor=user` may write directly. Non-user actors attempting to write to `user/*` are refused with a teaching error:
  `writes to protected namespace "user/..." require actor=user. User scope is reserved for content the human author explicitly directs — almost never the right answer for an agent. What to use instead: for agent-authored project content, use project/{slug}; for how-we-work guidelines, use system.`
- **`app/<id>/*`** requires `actor=app:<id>` with matching token `client_id=<id>`.
- **Agents default to `project/{slug}`** for codebase work and **`system`** for operating procedures.
- When omitted on write tools, `actor` defaults to `"agent"`, causing unasserted writes to `user/*` to fail closed.

## Common mistakes

- **Prescribing `user/{id}/*` for agent-authored content.** Agent work belongs under `project/{slug}/` (or `system/`).
- **Writing perishable drafts to files.** Drafts, scratchpads, and handoffs default to `project/{slug}/workspace/...`. File-based drafts are reserved for artifacts that are inherently files.
- **Omitting the `{type}` segment on memory or event writes.** A bare `project/{slug}/memory` parses for prefix recall, but is REJECTED on `memory_write`.
- **Using `app/` to write *about* an application.** `app/tether` is Tether's private datastore; engineering work about Tether is `project/tether`.
- **Treating `/knowledge` as a prefix.** A bare `/knowledge` matches only that exact namespace. To sweep, always append `/*` (`project/tether/knowledge/*`).
- **Putting dates, categories, or ticket IDs in namespace paths.** Categories and tickets are tags; timestamps are metadata. Paths carry scope and domain.

---
name: workspace
description: Mutable project-owned scratch — create retry receipts, optimistic edits, tombstones, current reads, typed recall, and touch by item_id.
scope_hint: memory:read, memory:write
related: [start-here, namespaces, recall-and-ranking]
---

# Workspace

Workspace holds replaceable working state owned by one project. It is the one
Tesseract domain that updates content in place. Every live item has a stable
`item_id` and a current `version_token`; it has no immutable `revision_id`,
`memory_id`, or retained content history.

Use a namespace shaped like `{scope}/{id}/workspace/{anything}/...`, normally
`project/<project-id>/workspace/<purpose>`. A `workstream_id`, when useful, is
an item attribute rather than another namespace tier.

## Create and retry safely

Create through `workspace_write` with `namespace`. `summary`,
`author_agent_id`, and `session_id` are required. `key` is optional. A keyless
create also requires `idempotency_key`; keyed creates may provide one.

```text
workspace_write namespace="project/example/workspace/scratch" \
  idempotency_key="session-42-draft-1" \
  workstream_id="ws-release-notes-42" \
  summary="Current draft" body="Working content" \
  consumer_state='{"phase":"draft"}' \
  author_agent_id="assistant" session_id="session-42"
```

The first successful call returns:

```json
{"status":"created","item_id":"01HITEM...","version_token":"01HTOKEN..."}
```

An exact retry returns the original identity without reading or reinforcing the
item:

```json
{"status":"replayed","item_id":"01HITEM...","availability":"live"}
```

The replay omits `version_token`, because the item may have changed after the
first response was lost. Read current state before editing. Reusing the same
retry key and namespace with different create arguments returns
`idempotency_conflict`. A retry after deletion returns the same `item_id` with
`availability:"deleted"`; it never recreates the item.

## Read and edit current state

Prefer `tesseract_get item_id=<item-id>`. It works for keyed and keyless items,
returns the workspace item directly, and records one use. Existing key lookup
also works:

```text
tesseract_get domain="workspace" \
  namespace="project/example/workspace/scratch" key="release-notes"
```

Content remains nested under `payload`; concurrency identity stays at the item
level:

```json
{
  "item_id":"01HITEM...",
  "domain":"workspace",
  "version_token":"01HTOKEN...",
  "namespace":"project/example/workspace/scratch",
  "workstream_id":"ws-release-notes-42",
  "key":"release-notes",
  "payload":{"summary":"Current draft","body":"Working content"}
}
```

Edit through `workspace_write` with `item_id` and the current `version_token`.
Only supplied fields change. `clear_fields` removes optional `key`, `body`,
`data`, `tags`, `consumer_state`, or `workstream_id`. A stale token returns
`version_conflict`; a rename to an occupied live key returns `key_conflict`.
An edit returns a fresh token.

## Delete and tombstones

`workspace_delete item_id=<id> version_token=<current-token>` erases content
and retains only identity, namespace, and deletion time. Current reads then
return `deleted`; an ID that never existed returns `not_found`. Repeating the
delete returns the same successful deleted receipt. Recreating the former key
creates a new item identity.

Workspace deliberately has no history. `tesseract_history` for a workspace
item returns `history_unavailable`.

## Recall and use

Workspace is never part of unqualified recall. Opt it in explicitly:

```text
tesseract_recall \
  namespaces='["project/example/workspace/scratch"]' \
  domains='["workspace"]' query="release" \
  ranking="relevance" search_mode="lexical"
```

Workspace supports lexical relevance, activation, and chronological ordering.
It has no embeddings, so semantic/similarity modes are rejected. It has no
timeline, revision status, confidence, knowledge facets, pointers, or link graph;
filters for those revision-only fields are rejected when workspace is included.
Tags, `workstream_id`, `state_filters`, and `since`/`until` apply before the
result limit.

Workspace returns the current association as top-level `workstream_id`. Its
`provenance.write_context` is the latest successful authored edit's bounded
receiver receipt. A later unstamped edit removes an older receipt so current
state never presents stale transport context. Create retries return the original
receipt and never rewrite association or provenance from the retrying request.

Recall returns a typed alternative: a revision result has `revision`, while a
workspace result has `item`. Projected workspace results retain `item_id`, so
hydrate one with `tesseract_get item_id=<id>`. Recall itself never records use.
After reasoning, report projected workspace hits that mattered with:

```text
tesseract_touch item_ids='["01HITEM..."]'
```

Pass exactly one of `item_ids` or `revision_ids`. Deleted workspace identities
are listed under `deleted`, unknown IDs under `not_found`, and event item IDs
under `not_reinforced`.

## Promote reviewed workspace content

`workspace_promote` copies the live workspace `summary`, `body`, and `data`
into a revisioned memory, knowledge, or event item. The workspace item remains
unchanged. Run all three explicit stages:

```text
workspace_promote stage=request source_item_id=<workspace-id> \
  source_version_token=<token> actor=agent:reviewer \
  target_domain=memory target_namespace=project/example/memory/notes \
  target_key=reviewed.note target_author_agent_id=agent:reviewer \
  target_session_id=session-1 target_trigger=promotion \
  target_derived_from=project

workspace_promote stage=request source_item_id=<workspace-id> \
  source_version_token=<token> actor=agent:reviewer \
  target_domain=knowledge target_namespace=project/tesseract/knowledge/contracts \
  target_key=workspace-promotion target_author_agent_id=agent:reviewer \
  target_session_id=session-1 target_kind=doc target_source=manual \
  target_pointer_scheme=nil target_pointer_locator=workspace-promotion

workspace_promote stage=request source_item_id=<workspace-id> \
  source_version_token=<token> actor=agent:reviewer \
  target_domain=event target_namespace=project/tesseract/event/reasoning \
  target_author_agent_id=agent:reviewer target_session_id=session-1 \
  target_trigger=promotion target_derived_from=observation

workspace_promote stage=approve request_id=<request-id> actor=user
workspace_promote stage=apply request_id=<request-id> actor=agent:reviewer
```

HTTP uses the same fields with destination fields nested under `target`, at
`POST /v1/workspace/promote/request`, `/approve`, and `/apply`. A request for an
existing revisioned item supplies `target_item_id` and
`expected_target_revision_id` and omits target domain, namespace, and key.
Apply verifies the reviewed source version and destination preconditions in
the same transaction as the new revision and its receipt. Repeating a committed
apply returns that receipt without writing another revision.
The receipt links `source_item_id` and `source_version_token` to the exact
`target_item_id` and `target_revision_id`; it never contains source content.

# Tesseract architecture

Tesseract is one local revision service with three information domains and
multiple adapters. The implementation favors explicit authority, deterministic
selection, and append-only history over hidden mutation or inference.

## Domains

| Domain | Purpose | Storage authority |
|---|---|---|
| Context | General JSON records, types, views, packets, and application state | Tesseract record revisions and deterministic head |
| Memory | Durable observations, decisions, outcomes, and other recallable experience | Tesseract memory revisions plus lifecycle/activation state |
| Knowledge | Durable summaries and pointers to external material | Tesseract owns the revision and pointer metadata; the external resource remains authoritative for its own content |

The domains share policy, audit, recall, and revision concepts, but their write
shapes are intentionally distinct. MCP and HTTP can also use different request
shapes where their protocol ergonomics differ; the public specs name those
differences.

## On-disk model

The resolved XDG layout contains:

- a main SQLite database for metadata, context indexes, memory/knowledge
  revisions, namespace policy, audit, auth-token hashes, embeddings, and
  derived indexes
- a `records/` tree containing append-only context payload files
- a separate `queue.db` for background embedding jobs
- `config.yaml` in the config root

Each context write creates a new payload file and database revision, then moves
the deterministic `(namespace, key)` head. Memory and knowledge bodies are
immutable revision rows. Supersession/deprecation is represented as lifecycle
state and new facts rather than editing old content.

The queue and embeddings are operational/derived state. `queue.db` is separate
and is deliberately excluded from the authoritative store backup.

## Service boundaries

```text
Go library / CLI / HTTP + web UI / MCP stdio
                    |
           validation and policy
                    |
       context store + memory/knowledge store
             |                    |
     SQLite + records/       queue.db workers
             |
    optional provider adapters
```

The adapters call shared storage and policy code, with parity tests guarding
the fields that are promised to match. Authentication is adapter-specific:
HTTP token modes protect every non-public route. MCP capability tokens gate
mutations, grant memory/knowledge read capabilities, and supply namespace
globs to tools that filter by them, but several context/audit reads remain
available without a token; MCP is not a complete tenant-isolation boundary.

## Authority and namespace policy

`app/<id>/*` is the normal writer-owned area for an application or agent.
Protected `user/*` updates from an app use a three-stage promotion protocol:
request, approve, and apply. Capability scopes and namespace globs are checked
at mutation boundaries. Audit rows link the promotion records and resulting
revision.

Namespace registrations carry owner metadata and optional policy. Writes remain
append-only even when an item changes status or becomes the current head.

## Retrieval

Deterministic views and context packets filter and order indexed records without
calling a model. Memory/knowledge recall can use:

- local lexical/BM25 ranking
- OpenAI-backed query embeddings and local cosine ranking
- a hybrid of the two

Synthesis is a separate explicit HTTP operation. It selects sources first, then
sends the question and selected source summaries/bodies to OpenAI or Anthropic.
Provider output never silently replaces stored source revisions.

## Operations and observability

The daemon exposes readiness, optional metrics, structured request logging, and
OpenTelemetry instrumentation. Namespace/key metadata can enter traces, and
full request logging includes raw query strings; operators choose and protect
those outputs.

Backup v2 uses a whole-database `VACUUM INTO` snapshot plus the context payload
tree and a checksummed manifest. Restore validates and stages the replacement,
migrates older schemas before the swap, and uses a journaled same-filesystem
rename sequence. See [Operations](OPERATIONS.md).

## Invariants

- revisions are immutable and heads are explicit projections
- identical deterministic selectors over the same store state have stable order
- protected namespace mutations require the relevant policy/capability path
- provider-backed work is explicit and disclosed
- backup omissions must be declared and rebuildable; unknown omissions fail
  verification
- a newer store schema is never opened or restored by an older build that does
  not support it

# Operations and support

This guide describes the operational boundary of the current Tesseract public
preview: supported build paths, authentication, data egress, backup, restore,
and upgrade safety.

## Support matrix

| Area | Public-preview status |
|---|---|
| Distribution | Source only; no supported prebuilt binary, package-manager formula, container image, or desktop installer |
| Toolchain | Go 1.26.6; Node.js 20.19–20.x or 22.12+ with npm is needed only to rebuild `frontend/` |
| macOS | Current development and qualification environment |
| Linux | Source-build target; verify the release on the intended distribution and filesystem before production use |
| Windows | Source may build, but POSIX `0700`/`0600` protection is not enforceable; do not treat it as security-equivalent to the POSIX deployment |
| Storage | Local SQLite plus record files; local filesystems are the supported operating model |
| Interfaces | Go library, CLI, HTTP, embedded web UI, and MCP stdio |
| Providers | OpenAI embeddings; OpenAI or Anthropic synthesis; all are optional |

The repository's current `make test` and `make validate` results define the
qualified commit. There is not yet a promise of release artifacts for every OS
or architecture.

## Paths and isolation

Run `tesseract path` to see the effective layout without creating it. Tesseract
uses the XDG data, state, cache, and config roots and recognizes the specific
`TESSERACT_DB_PATH` and `TESSERACT_WORKSPACE` overrides. There is no
`TESSERACT_HOME` setting.

For a disposable test instance, set all four XDG roots and remove direct path
overrides before any stateful command:

```bash
export XDG_DATA_HOME="$PWD/.tmp/tesseract/data"
export XDG_STATE_HOME="$PWD/.tmp/tesseract/state"
export XDG_CACHE_HOME="$PWD/.tmp/tesseract/cache"
export XDG_CONFIG_HOME="$PWD/.tmp/tesseract/config"
unset TESSERACT_DB_PATH TESSERACT_WORKSPACE
tesseract path
```

Check that every reported path is beneath the intended disposable root before
running writes, migrations, backup restore, or tests.

On POSIX platforms, Tesseract-owned directories and files are tightened to
`0700` and `0600`. A config file you create by hand is not rewritten merely by
loading it and therefore keeps its original mode. Protect it explicitly:

```bash
chmod 600 /path/from/tesseract-path/config.yaml
```

Tesseract does not change the ownership policy of an operator-supplied plugin
directory, backup destination parent, or restore source.

## Type vocabularies (`types.yaml`)

Tesseract validates three vocabularies on write, and an operator owns all
three. They are declared in the `types-file` reported by `tesseract path`,
usually `~/.config/tesseract/types.yaml`:

| Vocabulary | Governs | Enforced at |
|---|---|---|
| `knowledge.facet_kind` | the `kind` facet on every knowledge write | the persistence boundary |
| `memory.type` | the `{type}` segment of a memory namespace | the namespace parser |
| `context.record_type` | context record types | the context write path |

The file is **optional**. With no file, Tesseract runs its shipped
vocabularies, which is what most installs want. Start from
[`examples/types.yaml`](../examples/types.yaml).

Three properties are worth knowing before you write one:

- **A vocabulary you name replaces the shipped one — it is not merged.** That
  is what lets you remove a value rather than only add one, so a partial list
  narrows the vocabulary. A vocabulary you do not name keeps its default.
- **A malformed file stops the daemon starting.** This is deliberately harsher
  than most `config.yaml` errors, which warn and fall back to defaults. Invalid
  workspace retention configuration is also fatal because silently changing a
  deletion policy is unsafe. A bad `types.yaml` would mean enforcing a
  vocabulary you did not declare. Fix or delete the file and start again.

  Malformed means more than unparseable. An unknown key is refused, so `close:`
  for `closed:` is caught rather than quietly leaving a vocabulary open. So is
  a duplicate `vocabulary_id` or `view_id`, and a `default_ttl` that is not a
  Go duration — `24hr` would otherwise load clean and silently mean *no
  expiry*. The load is atomic: one bad entry changes nothing.
- **Removing a value makes existing rows readable but not rewritable.** Add
  before you migrate, remove after.

`hot_fields` on a type is a declaration and nothing more. Declaring one does
not create an index — indexes are materialized by a reviewed migration, so two
config files can never produce two schemas from one binary and a restore stays
deterministic.

## Listener and authentication modes

The default `tesseract serve` listener is `127.0.0.1:8089` without auth. A
non-loopback listener requires one of:

- `--managed-auth`, backed by active tokens stored in Tesseract
- `--static-token <token>`, one shared bearer token
- `--allow-unauthenticated-remote`, an explicit unsafe override

Managed and static auth are mutually exclusive. In either authenticated mode,
all HTTP API routes require `Authorization: Bearer <token>` except readiness
and enabled metrics. Reads, namespace listings, recall, audit, and admin
introspection are protected too.

Create a managed token before starting managed mode:

```bash
tesseract context token create \
  --name operator \
  --client-id user:operator \
  --scopes write,promote.request,promote.approve,promote.apply,packet,repair,namespace.register \
  --namespaces '*' \
  --ttl 24h

tesseract serve --managed-auth --addr 0.0.0.0:8089
```

Token plaintext is displayed once. Store it in a secret manager or protected
environment, not in source control. Create a separate token with the `admin`
scope when an operator must preview/apply runtime settings or create/restore
configuration backups. Static auth cannot grant `admin`.

Tesseract has no TLS listener. Use TLS termination, a VPN, or an SSH tunnel for
remote access, and ensure an upstream proxy does not log `Authorization`
headers. Do not put bearer tokens in URLs.

## Outbound connections and data egress

Core lexical and deterministic operations are local. The following configured
features can make outbound requests:

| Feature | Destination | Data sent | When |
|---|---|---|---|
| Embeddings | OpenAI | Record text, including memory/knowledge summary and body or extracted context payload; semantic search and recall query text | When an OpenAI embedder is configured and a write is embedded, backfill runs, or semantic retrieval embeds a query |
| Synthesis | OpenAI or Anthropic | The caller's question plus selected source identity, summary, and body | `POST /v1/synthesis/ask` with a configured synthesis provider |
| Pricing metadata | models.dev | Catalog-refresh HTTP request; no Tesseract record payload is intentionally included | At daemon startup and refresh while synthesis cost support is configured |
| Pointer verification | The URL in a knowledge pointer | HTTP(S) request to the pointer locator | Only when `verify-pointers` is run with `http` or `https` in `--schemes`; network schemes are opt-in |
| Telemetry export | Operator-configured OpenTelemetry endpoint | Trace/service data; memory read/write spans include namespace and key metadata, and HTTP propagation includes route context | When an OpenTelemetry exporter is configured in the process environment |

Provider credentials come from `OPENAI_API_KEY` and `ANTHROPIC_API_KEY`.
Removing credentials disables the matching provider path; lexical recall and
the core store continue to work.

HTTP request logging is off by default. `--request-logs` emits structured
method, path, status, latency, and request ID fields. Query strings are replaced
with `[REDACTED]` by default. `--request-log-mode full` includes the raw query
string, which may contain namespaces, keys, search terms, cursors, or accidental
secrets. Protect and retain full logs accordingly.

## Workspace retention

Workspace activation starts at `1.0`, has a 14-day half-life and a `0.05`
floor. A deliberate content read, accepted edit, or explicit item touch first
applies all decay owed since the stored baseline and then closes 10 percent of
the remaining distance to the `2.0` ceiling. Search, recall, reference
resolution, list operations, previews, and create/promotion receipt replay do
not count as use. Retention never applies to memory, knowledge, event, or
context records.

Purge and automatic maintenance are disabled by default. The full explicit
configuration is:

```yaml
workspace:
  retention:
    purge_enabled: false
    minimum_idle: 720h
    batch_size: 100
    # automatic_interval: 24h
```

`minimum_idle` cannot be shorter than 720 hours (30 days). Eligibility also
requires effective activation to have reached the floor, so this is not a
30-day TTL. `automatic_interval` starts a purge loop only in `tesseract serve`,
and is rejected unless `purge_enabled` is true. Library `Open`, MCP children,
and one-shot CLI commands never start it.

A namespace policy may lengthen the idle minimum or disable purge:

```json
{
  "workspace_retention": {
    "minimum_idle": "1440h",
    "purge_enabled": false
  }
}
```

The existing top-level namespace-policy `retention` string is unrelated and is
never treated as workspace TTL. Unknown or malformed `workspace_retention`
members are rejected. Missing namespace policy inherits the operator settings;
a policy read failure aborts the report/apply.

Use the operator command against a reviewed database path. It reports only by
default and includes no payload, key, token, author, or provenance fields:

```bash
tesseract workspace-retention --db /path/to/disposable/main.db --json
tesseract workspace-retention --db /path/to/disposable/main.db \
  --cursor 01NEXTITEM --limit 100 --json
```

`truncated` and `next_cursor` state whether the scan page is complete. An apply
requires `purge_enabled: true` in the loaded config and rechecks live use,
effective activation, and current namespace policy in the deletion transaction:

```bash
tesseract workspace-retention --db /path/to/disposable/main.db \
  --apply --item-id 01REVIEWEDITEM --json
```

JSON apply output is one document with `report` and `apply` members.
Without `--item-id`, `--apply` intentionally operates on eligible items in the
current bounded `--cursor`/`--limit` report page. Any positional or otherwise
unparsed argument is rejected before opening the database, including tokens
after `--`; use repeated `--item-id` flags to constrain the apply to reviewed
identities. If a later per-item transaction fails, the command exits nonzero
but still prints the completed prefix in the `apply` member (or text apply
section), because those earlier item transactions are already committed.

Before any live activation, review every report page and namespace override,
stop all writers, export and verify a v2 backup, and retain an offline copy of
the resolved layout for rollback. Confirm the backup contains the main database
and config evidence, restore it into a disposable layout, and verify workspace
current reads, tombstones, creation retries, and promotion receipt replay.
Only then set `purge_enabled`; set `automatic_interval` in a separate reviewed
step after a manual apply is understood. A rollback stops every process,
restores the verified pre-activation layout, restores the reviewed config
separately, and starts one daemon. Opening a live store with a new binary can
perform schema setup, so even a dry-run command is not permission to skip the
normal migration review.

## Backup format

`tesseract context backup export` writes format v2 as a new or empty directory:

```text
backup/
  manifest.json
  main.db
  records/
  config.yaml       # only when --config points to an existing file
```

`main.db` is a consistent whole-database snapshot created with SQLite
`VACUUM INTO`, including uncheckpointed WAL content and tables added by future
schema migrations. The manifest records the format and schema versions, table
inventory, file sizes, SHA-256 checksums, record count, and declared omissions.
The payload tree is copied after the database snapshot so concurrent appends
can produce only unreferenced extra source files, not a restored database that
points at a missing payload.

Verification checks the manifest, rejects unlisted or missing files and unsafe
paths, runs SQLite integrity checks, confirms the schema version, and verifies
that every indexed context payload is present with the expected checksum.

The separate `queue.db` is excluded. It is operational background-job state,
not part of the authoritative store snapshot. A store backup therefore is not
a complete byte-for-byte workspace capture when queued jobs matter.

Example:

```bash
tesseract path
tesseract context backup export \
  --out /secure/new/tesseract-backup \
  --config /path/from/tesseract-path/config.yaml
tesseract context backup verify --in /secure/new/tesseract-backup
```

The backup includes authentication token hashes and may include provider
configuration. Store it with the same or stronger controls as the live store.

## Restore runbook

Restore is replacement, not merge. Data present only in the destination is
removed from the restored authoritative store.

1. Stop every Tesseract daemon, MCP stdio process, and embedded application
   using the destination store. Restore requires exclusive operational access.
2. Confirm the destination with `tesseract path`.
3. Verify the backup with `tesseract context backup verify --in <path>`.
4. Preserve a separate pre-restore backup of the destination if it matters.
5. Run `tesseract context backup restore --in <path>`.
6. Start exactly one Tesseract process and check `tesseract context health`,
   representative head/history/recall results, and provider/queue status.
7. Reconcile the separately managed `queue.db`; it was neither restored nor
   replaced by the store backup.

Before swapping live paths, restore validates and stages the replacement beside
the current database and record tree. It migrates an older v2 schema in staging
and refuses a backup whose schema is newer than the running binary supports.
The swap is journaled and uses same-filesystem renames; an interrupted swap is
completed on the next store open.

An included `config.yaml` is archived evidence only. Store restore does not
install it into the live config path. Review and copy configuration separately
with owner-only permissions.

Legacy v1 JSON backups remain readable but contain only the subset the old
format recorded. Restoring v1 cannot recover memory/knowledge revisions or
other tables that v1 never captured. Treat it as recovery of v1-era context,
not as a merge into a current store.

## Upgrade runbook

1. Read [`CHANGELOG.md`](../CHANGELOG.md) and release-specific migration notes.
2. Record `tesseract --version` and `tesseract path`.
3. Stop all writers and preserve both a verified v2 backup and, when downgrade
   recovery matters, an offline copy of the complete resolved layout.
4. Build the new source revision with its required Go version.
5. Start one process. Opening the store applies supported forward schema
   migrations.
6. Check health, representative records and histories, semantic/lexical recall,
   the audit trail, and configured providers before restoring normal clients.

Never run two versions against the same live layout during an upgrade.

## Downgrade and rollback

There is no general in-place downgrade migration. An older binary refuses a
database whose schema is newer than it supports. If an upgrade did not change
the schema, a code rollback may work, but verify the release notes first.

For a schema-changing rollback, stop all processes and restore the offline
pre-upgrade layout captured with the older version. Preserve the newer layout
separately for investigation. Do not ask an older binary to open or restore a
newer store and do not combine files from the two layouts.

## Known limitations

- pre-1.0 APIs and tool contracts may change between minor versions
- no built-in TLS, at-rest encryption, encrypted backups, or cloud sync
- local MCP tokens do not provide complete read tenancy; several context/audit
  reads remain available without a token
- no supported multi-host writer or distributed-consensus mode
- source distribution only; release packaging is not yet a compatibility promise
- semantic recall depends on an external OpenAI embedding service
- synthesis depends on an external OpenAI or Anthropic service
- `queue.db` is not part of store backup/restore

For vulnerability reporting and deployment hardening, see
[`SECURITY.md`](../SECURITY.md).

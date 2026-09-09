# Command-line interface

Status: implemented public-preview contract.

The binary is `tesseract`. Run `tesseract --help` for the top-level commands
and `tesseract context --help` for the context command catalog. Help, version,
path, and command-specific help are store-free: asking a question about the
binary does not create the XDG data layout.

## Command model

There are no process-wide `--client-id` or `--output` flags. Flags belong to
the command that consumes them. Context operations are always below
`tesseract context`; for example, `tesseract put` is not a command.

Top-level commands are:

| Command | Purpose |
|---|---|
| `serve` | Run the HTTP API and embedded web UI. |
| `mcp` | Run the MCP adapter over stdio. |
| `context` | Read, write, retrieve, and maintain local records. |
| `path` | Print the resolved data/config/cache/state layout without creating it. |
| `plugin` | List, install, enable, disable, and remove plugins. |
| `backfill-embeddings` | Embed stored records that have no vector. |
| `migrate-namespaces` | One-shot rewrite of legacy namespaces. |
| `migrate-knowledge-kinds` | One-shot normalization of knowledge facet kinds. |
| `verify-pointers` | Resolve knowledge pointers and record their health. |
| `help`, `version` | Inspect the binary. |

This reference covers the complete `context` surface. Four operational
top-level commands have their own `--help` output.

## Context command catalog

All 26 live context commands are listed here in dispatch order.

| Command | Purpose | Subcommands |
|---|---|---|
| `namespace` | Register a namespace or show its policy. | `register`, `show` |
| `put` | Append a generic record revision. | — |
| `get` | Read the head of a namespace/key. | — |
| `history` | List revision history. | — |
| `view` | Select records with a selector. | — |
| `promote` | Run the explicit cross-namespace promotion workflow. | `request`, `list`, `approve`, `apply`, `accept` |
| `doctor` | Scan the store for consistency issues. | — |
| `repair-heads` | Rebuild head pointers from revisions. | — |
| `audit` | Query the audit log. | — |
| `token` | Manage store-backed capability tokens. | `create`, `issue`, `rotate`, `revoke`, `list`, `show` |
| `backup` | Export, restore, or verify a snapshot. | `export`, `restore`, `verify` |
| `health` | Report store health and readiness. | — |
| `bootstrap` | Seed default namespaces and report readiness. | — |
| `compact` | Prune old revisions and audit events. | — |
| `contract` | List or run contract suites. | `list`, `run` |
| `maintenance` | Retention trim and namespace compaction. | `trim`, `compact` |
| `packet` | Assemble a selector-based context packet. | — |
| `broker` | Plan or execute intent-based context retrieval. | `plan`, `fetch` |
| `typed-put` | Write a typed record with status, TTL, and pointers. | — |
| `status-promote` | Advance or select a typed record status. | — |
| `status-deprecate` | Mark a typed record deprecated. | — |
| `typed-view` | Render a registered typed view. | — |
| `types` | List registered record types. | — |
| `views` | List registered typed views. | — |
| `ttl-cleanup` | Delete records whose TTL has expired. | — |
| `context-pack` | Rank a registered view under item/token budgets. | — |

## Record and namespace commands

| Invocation | Flags and operands |
|---|---|
| `context namespace register` | `--namespace`, `--owner-type user|app`, `--owner-id` |
| `context namespace show` | `--namespace` |
| `context namespace list` | `-prefix`, or `-match` with `-match-mode prefix\|contains\|glob`; `-owner-type`, `-owner-id`; `-sort namespace\|owner\|updated_at`, `-dir asc\|desc`; `-limit` (0 = every match, unpaged), `-cursor`; `-output json\|table` |
| `context put` | `--client-id`, `--actor`, `--namespace`, `--key`, and exactly one of `--json` or `--file` |
| `context get` | `--namespace`, `--key`, `--output json|table` (default `json`) |
| `context history` | `--namespace`, `--key`, `--limit`, `--output json|table` (default `json`) |
| `context view` | Exactly one of `--selector` or `--selector-file`; optional `--include-payload`, `--limit`, `--output json|table` |

`put` validates actor/namespace policy before appending. `view` accepts the
selector schema documented in [VIEWS.md](VIEWS.md). CLI history does not expose
an HTTP-style cursor.

Example:

```bash
tesseract context namespace register \
  --namespace app/editor/session \
  --owner-type app \
  --owner-id editor

tesseract context put \
  --client-id editor \
  --actor app:editor \
  --namespace app/editor/session \
  --key goal \
  --json '{"text":"ship the public preview"}'

tesseract context view \
  --selector '{"namespaces":["app/editor/*"],"revision_scope":"head","order":["namespace","key","revision"]}' \
  --include-payload \
  --output json
```

## Promotion commands

The old one-shot promotion flow is not a CLI command. Promotion is a stored,
audited request → approve → apply workflow:

| Invocation | Flags and operands |
|---|---|
| `context promote request` | `--source-namespace`, `--source-key`, `--target-namespace`, `--target-key` (required); `--actor` (default `cli`), `--client-id` (default `cli`), `--reason`, `--summary` |
| `context promote list` | `--status pending|approved|applied|all` (default `pending`) |
| `context promote approve <request-id>` | `--actor` (default `user`), `--notes` |
| `context promote apply <request-id>` | `--actor` (default `user`) |
| `context promote accept <request-id>` | Convenience approve + apply; `--actor` (default `user`), `--notes` |

`request` always captures the current source head. It prints the generated
request ID for the later commands.

```bash
tesseract context promote request \
  --client-id editor \
  --actor app:editor \
  --source-namespace app/editor/session \
  --source-key summary \
  --target-namespace user/alex/memory/notes \
  --target-key summary \
  --reason 'reviewed session result'

tesseract context promote approve req-... --notes reviewed
tesseract context promote apply req-...
```

Audit rows use `promote.request`, `promote.approve`, and `promote`, with the
request, approval, and written record IDs linked in stage metadata.

## Diagnostics and audit

| Invocation | Flags |
|---|---|
| `context doctor` | `--output json|table` (default `json`) |
| `context repair-heads` | `--output json|table` (default `json`) |
| `context audit` | `--limit` (default `50`), `--cursor`, `--namespace`, `--event-type`, `--output json|table` |
| `context health` | `--output json|table`, `--summary` |
| `context bootstrap` | `--default-app` (default `default`), `--output json|table` |

`doctor` is read-only. `repair-heads` changes head pointers. `bootstrap` is
idempotent and creates the default namespace policies when absent.

## Managed tokens

| Invocation | Flags and operands |
|---|---|
| `context token create` | `--name` (required), `--client-id`, `--scopes`, `--namespaces`, optional `--ttl` or `--expires`, `--output json|table` (default `table`) |
| `context token issue` | Legacy/default-scope creation: `--label`, `--ttl`, `--output json|table` |
| `context token rotate` | `--token` (raw current token), optional `--label`, `--ttl`, `--output json|table` |
| `context token revoke <token-id>` | Preferred ID operand; legacy raw-token form is `--token`; no output flag |
| `context token list` | `--limit` (default `50`), `--show-revoked`, `--output json|table` (default `table`) |
| `context token show <token-id>` | `--output json|table` (default `table`) |

`token create` is the full-fidelity command. Comma-separate scopes and namespace
globs. `--expires` accepts RFC3339 or `YYYY-MM-DD`; `--ttl` accepts a Go
duration. If both are supplied, `--expires` takes precedence. The raw secret is
shown only when created or rotated.

```bash
tesseract context token create \
  --name preview-agent \
  --client-id app:preview-agent \
  --scopes write,memory:read,memory:write,promote.request \
  --namespaces 'app/preview-agent/*,user/alex/memory/*' \
  --ttl 720h
```

Managed-token HTTP defaults use `write`, `promote.request`, `promote.approve`,
`promote.apply`, `packet`, `repair`, and `namespace.register` when no scopes
are specified. MCP namespace registration instead checks `namespace.admin`.
The `admin` scope must always be requested explicitly.

## Backup commands

| Invocation | Flags |
|---|---|
| `context backup export` | `--out` (required new or empty directory), optional `--config` |
| `context backup verify` | `--in` (required v2 directory or legacy v1 snapshot file) |
| `context backup restore` | `--in` (required v2 directory or legacy v1 snapshot file) |

Format v2 is a directory containing a manifest, whole-database snapshot, and
payload tree. Stop every Tesseract process that can access the target store
before restore. Restore replaces the target state; it is not a merge.

## Retention and maintenance

| Invocation | Flags |
|---|---|
| `context compact` | `--keep-revisions` (default `1`), `--keep-audit` (default `1000`), `--output json|table` |
| `context maintenance trim` | `--namespace` SQL-LIKE pattern (default `user/cache/%`), `--retention` (default `72h`), `--dry-run` |
| `context maintenance compact` | `--namespace` SQL-LIKE pattern (required), `--max-revisions` (default `1`), `--dry-run` |
| `context ttl-cleanup` | No flags; deletes expired records. |

Use the maintenance subcommands' `--dry-run` before destructive retention work.
The broader `context compact` command has no dry-run mode.

## Contract suites

| Invocation | Flags |
|---|---|
| `context contract list` | `--output json|table` |
| `context contract run` | `--suite <name|all>` (default `all`), `--execute`, `--output json|table` |

Without `--execute`, `contract run` reports the command(s) it would invoke.

## Packet and broker budgets

The canonical item and assembly-token flags are the same across packet and
broker commands:

```text
--max-items 50
--max-tokens-estimate 8000
```

The defaults are 50 items and an 8,000-token estimate. For one release, the
following old spellings remain warning aliases:

| Command | Canonical | Deprecated alias |
|---|---|---|
| `context packet` | `--max-items` | `--budget-items` |
| `context packet` | `--max-tokens-estimate` | `--budget-tokens` |
| `context broker plan|fetch` | `--max-items` | `--budget-items` |
| `context broker plan|fetch` | `--max-tokens-estimate` | `--budget-tokens` |
| `context context-pack` | `--max-items` | `--limit` |
| `context context-pack` | `--max-tokens-estimate` | `--max-tokens` |

Supplying both the canonical and deprecated spelling for one dimension is an
error. Generated follow-up commands always use the canonical flags.

### `context packet`

Repeat `--namespace` for each pattern. Other flags are `--budget-bytes`,
`--since`, `--until`, `--no-pins`, `--payload-max-bytes`,
`--manifest summary|full`, and `--output human|json|manifest-only`.
`--payload-mode` accepts only `full`; the retired `head_only` behavior is now
`--payload-max-bytes 512`.

```bash
tesseract context packet \
  --namespace 'user/alex/memory/*' \
  --namespace 'user/pins/*' \
  --max-items 50 \
  --max-tokens-estimate 8000 \
  --output json
```

### `context broker plan|fetch`

Both take `--intent resume_task|boot_project|review_session|custom`,
`--summary`, the canonical budgets, and `--output human|json`. `plan` returns a
deterministic plan; `fetch` executes it against the local store.

### `context context-pack`

This distinct command ranks records from a registered view. It requires
`--view`; optional flags are `--namespace` and the canonical budgets.

## Typed records

| Invocation | Flags |
|---|---|
| `context typed-put` | `--namespace`, `--key`, `--payload`, `--type`, `--status draft|reviewed|canonical|deprecated`, `--ttl`, `--pointers`, `--actor` |
| `context status-promote` | `--namespace`, `--key`, optional `--to`, `--actor` |
| `context status-deprecate` | `--namespace`, `--key`, `--actor` |
| `context typed-view` | `--view`, optional `--namespace`, `--limit`, `--output json|table` |
| `context types` | No flags. |
| `context views` | No flags. |

Omitting `--to` advances one valid status step. Typed writes validate the type,
status, required payload fields, and transition rules against the registry.

## Exit and output behavior

- Successful commands return exit status 0; command parsing and operational
  errors normally return 1 and write `error: ...` to stderr.
- JSON output is one JSON value followed by a newline. Table/human output is
  intended for operators, not stable machine parsing.
- Reads and views do not retry or change record ordering. Commands explicitly
  named `repair`, `restore`, `compact`, `trim`, `cleanup`, `put`, `promote`, or
  `bootstrap` can change the local store.

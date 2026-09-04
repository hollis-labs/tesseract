# Security policy

## Supported versions

Tesseract is pre-1.0 software. Security fixes are made on the current supported
development line and the newest tagged public-preview release, when one exists.
Older preview releases may not receive backports. Upgrade guidance and schema
compatibility notes live in [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

## Report a vulnerability

Do not include an exploit, token, database, backup, provider credential, or
other sensitive material in a public issue.

Use GitHub's private vulnerability-reporting flow when the repository's
Security tab offers it. If it is unavailable, contact a repository maintainer
privately through a contact channel published on the Hollis Labs organization
or maintainer profile. Include:

- the affected commit or version and operating system
- the deployment mode and whether the listener was remote
- reproduction steps and the security impact
- whether credentials or user data may have been exposed
- a safe way to contact you about coordination

Public, non-sensitive hardening suggestions can use the security issue
template. Maintainers will acknowledge a private report, investigate it, and
coordinate disclosure; response times are best effort during the preview.

## Deployment boundary

The safe default is a single-user machine with `tesseract serve` bound to
`127.0.0.1:8089`. That listener is unauthenticated because only local clients
can reach it. Tesseract refuses an unauthenticated non-loopback address unless
the operator explicitly passes `--allow-unauthenticated-remote`.

Remote deployments must use `--managed-auth` or `--static-token`. In either
mode all HTTP routes require a bearer token except:

- `GET /v1/health/readiness`
- `GET /v1/metrics`, when metrics were enabled

Tesseract does not provide TLS. A bearer token sent over plaintext HTTP can be
stolen by anyone able to observe the connection. Put a remote listener behind
TLS, a trusted TLS-terminating reverse proxy, a VPN, or an SSH tunnel. Restrict
the listener and proxy with host firewall rules as well.

The static token has the normal write, promotion, packet, repair, and namespace
registration capabilities across all namespaces, but intentionally lacks the
`admin` scope. Runtime-settings and config-backup mutations require managed
auth with a managed token explicitly granted `admin`.

For MCP, `--token` authorizes mutations, grants the `memory:read` capability
when present, and supplies namespace globs to the tools that filter by them.
Several context and audit reads remain available without a token, so this is
not a complete tenant-isolation boundary. Run the stdio process only for a
client you trust with the context store.

## Data at rest

Tesseract has no built-in at-rest encryption. The SQLite databases, context
record files, configuration, and backups may contain sensitive content.

On POSIX platforms, paths Tesseract creates for itself are forced to `0700`
for directories and `0600` for files. Important limits:

- a manually created `config.yaml` is only read, so its existing mode is
  preserved until Tesseract rewrites it; set `chmod 600` yourself
- the v2 backup directory itself is owner-only, but its operator-selected
  parent, restore sources, and plugin directories are not recursively
  re-permissioned
- Windows does not provide equivalent POSIX owner-only mode enforcement

Backups contain the full store, including authentication token verifiers, and
must be protected like the live data. Plaintext bearer tokens are shown only
when created; do not commit them to MCP configuration, shell history, issue
reports, or logs.

## External data processors

Provider-backed functionality is opt-in through configuration and credentials,
but it is not local-only once enabled. OpenAI embeddings receive record text
or search queries. OpenAI or Anthropic synthesis receives the question and the
selected source summaries and bodies. Other optional network activity includes
models.dev pricing refreshes, HTTP(S) pointer verification, and an OpenTelemetry
exporter configured in the process environment.

The full disclosure table, including logging behavior, is in
[`docs/OPERATIONS.md`](docs/OPERATIONS.md#outbound-connections-and-data-egress).

## Current security limitations

- no built-in TLS
- no at-rest encryption or encrypted backup format
- no multi-tenant read isolation for a local MCP process
- no cloud synchronization or cross-host consistency protocol
- bearer-token authorization rather than an external identity provider
- pre-1.0 contracts and migration guarantees

These are deployment constraints, not hidden roadmap promises. Operate within
them or place Tesseract behind controls that provide the missing boundary.

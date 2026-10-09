# Rotate an upstream service credential

This procedure covers a managed Tesseract credential used by a Tether
upstream. Rotation preserves the existing service identity and grants.
It does not rotate the Tether operator token or grant the service new access.

Source implementation and isolated fixtures do not perform an operator
cutover. The live cutover is coordinated separately after both Tesseract and Hadron
issuers are installed.

## Preserve the credential family

For Tesseract, `principal_id` is the original managed credential's opaque
`token_id`, which roots one credential family. It is not a new service
identity. Equal `ClientID` values do not join independently issued families.
Issue and revoke name a `credential_id` belonging to that family and the
current `expected_generation`. The issuer copies the original exact ClientID,
label, scopes and namespace globs, including empty arrays, without applying
creation defaults. Rotation accepts no replacement identity or grants.

Each successful family mutation increments generation once. The idempotency
key binds the authenticated actor and complete normalized request. An exact
retry is checked before stale-generation refusal and returns current metadata
plus the original `operation_generation`, without another mutation. A changed
request or actor using that key conflicts. Authentication is checked again
on every retry; a replay receipt is not authority.

The default overlap is 900 seconds; an explicit overlap may be from 0 through
86400 seconds. Issuance caps every already-active family credential at the
earlier of its current expiry and the overlap deadline. It never extends an
earlier expiry, revives an expired/revoked credential or replaces the family.
Zero overlap makes old credentials invalid immediately. Optional TTL must
be positive and at most 31536000 seconds; omitted TTL means no expiry for the
new credential. CLI durations must resolve to integral seconds.

## Administrative authority

HTTP mutations require a freshly validated managed bearer with explicit
`admin` scope. Anonymous loopback, static-token and limited managed callers
cannot issue or revoke, including through legacy create/revoke routes. A
caller-provided actor string cannot authorize the operation. The CLI reads
its administrator credential from `--admin-token-file`; it does not accept
that credential's raw value in argv.

The first issue response uses a confidential authenticated local or TLS
connection and `Cache-Control: no-store`. CLI secret delivery is separate
from metadata: an exclusive owner-only `0600` file or a verified non-TTY
pipe. Existing files, symlinks, terminals and unsupported sinks refuse.
No new raw-value MCP tool exists.

## Choose the consumer transport

A Tether stdio upstream uses `token_file`, with the path supplied to the
upstream as `--token-file`. Tether validates and reads the credential file
at each spawn, including reconnects. Existing processes keep their loaded
credential; replacing the file alone does not prove consumer cutover.

A daemon-owned HTTP/SSE upstream uses `proxy_service_token_file`, not the
stdio-only `token_file` field. That owner loads its credential once. The
new credential takes effect when the upstream owner is recreated; there
is no automatic reload from changing the file. Do not combine this field
with a raw catalog `token`.

Use an absolute path to an owner-only regular file with mode `0600`, beneath
a protected root excluded from worker reads. Keep credential values out
of arguments, catalog content, environment dumps, logs and error messages.
The service principal pin must continue to name the same identity after
credential rotation. A matching path or healthy daemon does not prove
that the consumer authenticates with the replacement credential.

## Request and retry metadata

An issue request names `principal_id`, `credential_id`,
`expected_generation`, and `idempotency_key`, with optional integer
`overlap_seconds` and `ttl_seconds`. A revoke request names the same four
required fields and accepts no overlap or TTL. Unknown or duplicate fields
and fractional/out-of-range numbers refuse before mutation. Supply metadata
only; no predecessor credential value belongs in either request.

First-issue metadata identifies the new credential separately from its
one-time value. Lists retain every family credential and its current
active/expired/revoked status, expiry and last-used observations, without
values or digests. Last-used observations do not advance generation.

For an exact retry, resubmit the original normalized request and key,
including its originally submitted generation. Updating that generation
is a changed request, not the same retry. The returned current `generation`
may differ from the original `operation_generation` after other family
changes. `replayed: true` and `secret_available: false` do not authorize
repeating the secret delivery or overwriting its original file.

## Cutover order

1. Read metadata for the current service identity and credential IDs. Record
   its current generation and effective grants without copying the value.
2. Issue a new credential for that identity using the supported rotation
   operation, an expected generation, an idempotency key, an explicit overlap
   window and any chosen TTL. Deliver the new value once to a private sink.
3. Switch only the intended consumer's credential file and recreate or
   reconnect its owner through the coordinated operator procedure. Verify
   actual authenticated operations and unchanged identity/grants during
   overlap. Keep the old credential available until cutover is verified.
4. Revoke the old credential by its ID using the current expected generation.
   Confirm old authentication refuses and the replacement still works.
5. Retain the value-free issuance/revocation audit and cutover outcome.

Do not use legacy raw `context token rotate --token`: its revoke-first
replacement cannot provide this overlap procedure. The legacy library
`RotateAuthToken` likewise refuses before effects; it has no compatibility
fallback. Migrate callers to the supported administrator-authorized,
expected-generation issue/revoke operations. Never substitute a full-access
newly created token for the existing limited service identity.

## Lost delivery and interrupted cutover

An exact idempotent retry returns operation metadata, not the original raw
credential. The issuer cannot replay a value it stores only as a digest.
If private delivery fails or its outcome is uncertain, inspect metadata
without exposing values. Revoke the outstanding issued credential by ID if
its delivery was lost or uncertain, then perform a new authorized issuance
with a new idempotency key and fresh generation. Do not revoke the working old credential merely because issuance committed.
Do not interpret metadata-only retry success as possession of the new value.

Generation conflicts require fresh metadata before a new decision. TTL and
overlap deadlines bound how long cutover and rollback remain possible;
expiry never establishes that a consumer has switched.

## CLI commands

These commands name paths and nonsecret operation metadata only. Replace
the illustrative credential IDs and generations with the metadata returned
by your installed issuer. Do not paste a bearer value into a command.

```sh
tesseract context token list \
  --principal-id ROOT_CREDENTIAL_ID \
  --admin-token-file /absolute/private/administrator.token

tesseract context token rotate \
  --principal-id ROOT_CREDENTIAL_ID \
  --credential-id CURRENT_CREDENTIAL_ID \
  --expected-generation 1 \
  --idempotency-key UNIQUE_ISSUE_KEY \
  --admin-token-file /absolute/private/administrator.token \
  --overlap 15m --ttl 24h \
  --secret-file /absolute/private/new-service.token

tesseract context token revoke \
  --principal-id ROOT_CREDENTIAL_ID \
  --credential-id OLD_CREDENTIAL_ID \
  --expected-generation 2 \
  --idempotency-key UNIQUE_REVOKE_KEY \
  --admin-token-file /absolute/private/administrator.token
```

Use a new secret output path. File delivery writes only the credential to
that `0600` file and emits JSON metadata on stdout. `--secret-stdout` instead
requires a pipe: only the credential goes to that stream and JSON metadata
goes to stderr. Never combine the two delivery flags. Do not redirect the
secret stream to an ordinary terminal or logging wrapper.

An exact issue retry uses the identical original issue arguments. It emits
metadata only and does not open or overwrite the original secret sink, even
if the first delivery failed. A new authorized issuance is a new request
with a new key and current generation, not an exact retry.

## HTTP administration

The supported routes are:

| Operation | Route |
|---|---|
| Issue replacement | `POST /v1/auth/tokens/rotate` |
| Revoke one credential | `POST /v1/auth/tokens/revoke` |
| List retained family credentials | `GET /v1/auth/tokens/list?principal_id=ROOT_CREDENTIAL_ID` |

Use a managed administrator bearer loaded from a private file by an
authorized client. Do not put an expanded bearer in a curl argument or
request logging. Issuance requires an actual loopback peer or TLS; a
forwarded header cannot make an insecure remote connection private.

The successful issue response is flattened operation metadata, with a
`token` field on first delivery only. Exact retry omits `token` and sets
`secret_available: false`. Issue/revoke responses use `Cache-Control:
no-store`. List returns `principal_id`, current `generation` and retained
`credentials`; it contains neither bearer values nor their digests.

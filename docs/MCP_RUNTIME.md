# MCP executable observation

Call `tesseract_runtime_get` with `{}` to inspect the running MCP process and
its replacement candidate. The tool returns schema version 1, `mode:
"observation-only"`, the process PID, display version, observation time,
correlated owner metadata when available, and an `observation` object:

| State | Meaning |
|---|---|
| `same` | Verified running and candidate identities match. |
| `different` | A comparable replacement has different verified content. This includes a rollback; it does not mean a newer version. |
| `unknown` | Running identity, owner/selector evidence, candidate verification, or a stable inspection is unavailable. The `reason` explains which boundary failed. |

Every response has `exit_permitted: false` and `reservation: "none"`. Calling
this tool cannot exit a process, consume a retry, restart a worker, alter queue
ownership, or authorize migration. CW-20260912-0028 remains a separate gated
admission/drain change. There is no polling watcher; inspection happens only
when this tool is called.

## Owner and selector evidence

A mux with the PR47 runtime contract passes `hollis-labs.dev/mcp-runtime` in
upstream initialize capabilities. Tesseract accepts the version-1 observation
contract only from `agent-mux-proxy`, with the current child's PID, its captured
parent PID, and a well-formed owner instance ID. This correlates local process
metadata; it does not authenticate the sender or verify the owner's executable.
Unknown fields, arguments, environment values and asserted exit permission are
not retained or returned. Display version is metadata, never image proof.

Only an absolute launch selector with `relaunch_lookup: "selector"` is inspected.
Symlinks are resolved afresh on each call. A proxy's pre-spawn `resolved_path`
is never substituted for a PATH or relative selector: the next lookup belongs
to the proxy's environment and working directory. Old proxies, direct hosts,
missing or incompatible contracts, and ambiguous selectors report `unknown`.
A wrapper that cannot establish the same Go product/platform relation also
reports `unknown`. The first initialize fixes the accepted context for that
adapter; later initialize calls cannot retarget it.

The tool reports this MCP process, so it has no HTTP peer: a daemon would be a
different process with a different launch owner. Library users that register
adapter tools on another server without its initialize hook receive `unknown`
for missing launch context.

## Image identity and supported platforms

The product acquires its running identity once when the MCP adapter is created.
The shared `go-mcp/staleness` package compares identities and handles candidate
snapshots. Neither path hashing nor version/build labels stand in for a running
image binding.

- **macOS with cgo:** public Security framework dynamic validation compares the
  running code's kernel-bound CodeDirectory with the retained static code object.
  Static validation then checks its signed content before the CDHash is retained
  as `darwin-cdhash`. A pathname replaced before the first acquisition fails
  closed. After acquisition, replacing the pathname cannot change the retained
  running identity. Candidate snapshots undergo static signature validation.
  This scheme identifies validated signed code; it is not a whole-file SHA-256
  identity, notarization check, or publisher trust decision.
- **Linux:** `sha256` covers the executing image opened through `/proc/self/exe`,
  including an unlinked/replaced original pathname. The candidate snapshot uses
  the same full-image digest. Unavailable procfs/image access yields `unknown`.
- **Other platforms and macOS with cgo disabled:** builds remain supported;
  verified image identity is unavailable and the result stays `unknown`.

Candidate Go build information supplies product and GOOS/GOARCH compatibility,
not content identity. Unreadable, unsigned/unverifiable, incompatible or
unrecognized images fail closed. Universal images without an unambiguous Go
build-information view are not supported by this first consumer.

## Inspection limits

Each observation resolves a regular executable with at least one execute bit,
limited to 256 MiB. It creates a 0700 temporary directory and a 0600 snapshot,
verifies that snapshot without executing it, then rechecks source bytes,
selector resolution and file metadata. Concurrent replacement, in-place writes
or symlink retargeting detected during inspection yield `unknown`. Temporary
snapshots are removed before return. An overlapping inspection returns
`unknown` with `inspection-in-progress` instead of queuing another snapshot.

Copying and hashing honor cancellation. Security framework calls disable
network access, but a native validation call already in progress cannot be
interrupted by Go context cancellation. File access on a stalled filesystem can
also block. This is bounded local inspection, not a hard wall-clock guarantee.

The result is a point-in-time observation. It does not reserve a future exec or
prove effective ACL access, dynamic-library availability, sandbox admission or
other execution policy. A later replacement can invalidate it immediately.
Owner recovery admission and safe draining must be established separately.

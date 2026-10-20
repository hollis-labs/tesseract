# Authentication (go-auth)

Tesseract's HTTP API and MCP surface sit behind the shared `go-auth` guard
(module `github.com/hollis-labs/libs/security/auth`, a local `replace` until the
monorepo import). One contract for every Hollis app: `Authorization: Bearer
<token>`, scope names `tesseract.<verb>`, one mode switch. This replaces the old
posture of an anonymous loopback listener (DEC-103 D1 is superseded).

## Modes

| mode | behavior |
|---|---|
| `enforce` | No or invalid bearer: `401`. Verifier down: `503`. Missing scope: `403`. A route or tool with no scope mapping: `403 route_not_mapped` (deny by default). |
| `permissive` | Never rejects at the guard. A valid token is verified and attributed. Everything `enforce` would reject is counted in the **would-deny summary**. (`observe` is an alias.) |
| `off` | Never rejects. A valid token is still verified and attributed; otherwise the request is stamped `operator(off-mode)` and counted. The dev switch. |

Code defaults, when nothing is configured:

- **non-loopback listener: `enforce`**. `off` and `permissive` are refused there
  by the bind guard (`go-auth` `ValidateBind`) unless the address is loopback or
  tailnet, and a tailnet address still defaults to `enforce`;
- **`--managed-auth` or `--static-token` given: `enforce`**, even on loopback.
  Naming a token mode is an explicit request for authentication, and this keeps a
  loopback listener that enforces today enforcing;
- **`config.yaml` failed to load: `enforce`** (an unreadable file must never
  relax auth);
- **bare loopback listener, and stdio `tesseract mcp`: `permissive`**.

Precedence, highest first: `HOLLIS_AUTH_TESSERACT`, `HOLLIS_AUTH`, `auth.mode` in
`config.yaml`, then the code default. An invalid value anywhere stops startup.

Every start prints the mode and where it came from:

```
  Auth: auth=permissive (source=default, surface=http, verifiers=managed-token-store)
```

`auth=<mode>` is also on every API response as `X-Tesseract-Auth`, in
`GET /v1/admin/setup` and `/v1/admin/settings` (`auth.gate`), and in
`GET /v1/auth/whoami`.

## Staging the flip (config only, no rebuild)

Edit `config.yaml` (`tesseract path` shows the directory) and restart. Nothing
else changes.

```yaml
auth:
  mode: off          # dev switch: nothing is rejected
```

```yaml
auth:
  mode: permissive   # find callers that still send no bearer
```

```yaml
auth:
  mode: enforce      # reject
```

An environment override wins over the file, which makes rollback a one-line
change on the unit: `HOLLIS_AUTH_TESSERACT=permissive`.

### Finding callers before flipping

While not enforcing, Tesseract logs a would-deny block to stderr every 5
minutes if anything changed, and once more at shutdown:

```
auth: would-deny summary (2 distinct callers):
auth:   14x http GET /v1/recall auth=missing who=(no bearer) needs=tesseract.read ua=Go-http-client/1.1
auth:   3x mcp tesseract_recall auth=missing who=(no bearer) needs=tesseract.read
```

The same table is live at `GET /v1/auth/summary` (needs `tesseract.admin`, or
nothing while permissive on loopback). Rows carry the route pattern or tool
name, the authentication state (`missing`, `invalid`, `off-mode`), the required
scope, the principal when there is one, and a sanitized User-Agent. Never a
bearer, query string or body. Flip to `enforce` once the table stays empty for
the callers you care about.

## Verifiers

1. **Managed token store** (always on). The tokens from `tesseract context token
   create` and `serve --managed-auth`: same hashes, scopes, namespace globs,
   expiry and revocation, no re-minting. This is the static hashed-key backend.
   Checked against the store on every request and every MCP tool call, so
   revocation is immediate.
2. **Legacy `--static-token`** (when given): one shared secret with the default
   scope set and `*` namespaces (never `admin`), as before. Prefer a go-secrets
   reference.
3. **Tether JWT/JWKS** (when `auth.jwt` is set). EdDSA tokens verified offline
   against the issuer's JWKS; a JWT-shaped bearer (`eyJ...` with three segments)
   is routed only to this verifier, anything else only to the opaque verifiers.

```yaml
auth:
  jwt:
    issuer: tether                    # Tether's identity.issuer (the "iss" claim)
    audience: tesseract               # default; one app per token
    jwks_url: http://HUB:PORT/.well-known/jwks.json
    bootstrap_key_file: /path/issuer.pub.pem   # optional; public key, used until the first JWKS fetch
    namespace_globs: ["project/*", "session/*", "app/*", "org/*", "system/*"]   # REQUIRED; the hub ceiling
  deny_list:
    url: http://HUB:PORT/auth/denylist
    bearer_ref: file://tesseract/denylist-bearer   # go-secrets reference; a literal is refused
```

Store the deny-list bearer (a Tether credential holding `tether.maintain`) with
`secrets set file://tesseract/denylist-bearer`. It is read at startup and again on every reload. A
JWT verifier or deny-list that is configured but cannot be built (missing
bootstrap file, unresolvable reference, deny-list without a JWT verifier)
stops startup. Nothing falls back to fewer checks. The deny-list is polled
every 30 seconds and a failed poll keeps the last list.

A JWT carries no namespace claim, so `auth.jwt.namespace_globs` is the
namespace constraint for every issuer-minted token. It is **required** whenever
`auth.jwt` is configured: unset or empty stops startup with an error naming the
key. There is no `*` default, because that would let any Tether-minted token
with `aud: tesseract` read and write `user/*`.

On the hub the ceiling is `["project/*", "session/*", "app/*", "org/*", "system/*"]`:
no `user/*`. User namespaces stay reachable only through managed tokens that carry
explicit user globs. Writing `["*"]` is still possible, but it is a deliberate,
visible choice, not a default. Managed and static tokens are unchanged.

### Dev service tokens

A service token is a Tether JWT with `kind=service`, `aud=tesseract`, scopes such as `tesseract.read tesseract.write` and an `exp` about ten years away. The verifier requires `exp`, so "no expiry" is a distant one; the token needs no refresher. It is held to the same `auth.jwt.namespace_globs` ceiling as every other issuer-minted token, so the ceiling is the only namespace constraint it has (there is no per-token namespace claim yet).

Revoke it by putting its `jti` (or `sub`) on Tether's deny-list, and only if `auth.deny_list.url` is configured (without a deny-list a service token cannot be revoked short of rotating the issuer key). Revocation takes effect within one poll interval (30 seconds) after a successful poll. If the issuer or its deny-list is unreachable, the last list stays in force (stale), so a revoked token keeps being accepted for as long as the outage lasts; for a long-lived service token there is no hard bound on that outage. Today the deny-list endpoint is read with a credential (`bearer_ref` above); a public minimal deny-list is planned (CW-20261019-0044).

### Reloading without a restart

`auth.mode`, `auth.jwt` and `auth.deny_list` reload while `tesseract serve` runs (CW-20261020-0004): edit `config.yaml` (the daemon checks every 2 seconds and applies a change once the file has been stable for two checks) or send `SIGHUP`. A reload rebuilds the JWT verifier (issuer, audience, JWKS URL, bootstrap key, `namespace_globs`) and the deny-list (URL, `bearer_ref`, which is resolved again) and publishes the new generation in one atomic step; the previous generation's JWKS refresh and deny-list loops stop. The startup line is followed by `Auth reloaded: ...` when it lands.

All or nothing: if anything in the new section fails (a JWT without `namespace_globs`, a deny-list without a verifier, a literal `bearer_ref`, an unreadable bootstrap key, an unknown mode, a config file that does not parse or has been deleted) the running configuration is unchanged and the reason is logged. It also cannot relax auth on a non-loopback listener: the bind guard that refuses `off` or `permissive` at startup applies to a reload. `HOLLIS_AUTH_TESSERACT` / `HOLLIS_AUTH` still win over `auth.mode`, and the environment does not change under a running process. The managed-token store, `--static-token` and the listener are not part of the config file and do not reload. `tesseract mcp` (stdio) reads its config once.

The mode, the verifier chain, the namespace ceiling and the guard that applies them are one snapshot: a request takes it once, on entry, and is judged entirely by it (verification, mode decision and ceiling), so it is never judged by a mixed pair. A request already in flight when a reload lands finishes under the generation it started with; the next request sees the new one.

A reload never forgets a revocation. If the deny-list URL is unchanged, the new generation keeps the list the previous one had learned (an edit or `SIGHUP` during an outage of the issuer's deny-list cannot un-revoke a token). If the URL is new or changed there is nothing known to keep, so the reload requires a validated first fetch from it and is refused (the running configuration kept) if that fails. Likewise a reload to a JWKS URL with no bootstrap key requires the first key fetch to succeed. These error messages never contain the endpoint URL.

**Dev posture, and the release constraint.** In dev, an edit to `config.yaml` or a `SIGHUP` may relax auth on a loopback listener (for example `enforce` to `permissive`, or removing the JWT verifier): the config file is the owner's, and the change is logged (`Auth reloaded: ...`). That is a dev decision, not a release one. A release posture has to constrain it: at minimum no runtime relaxation without a fresh, attributable approval, and the non-loopback guard above stays. The release-posture note owns the exact rule; until it lands this is the dev default, and a build must not treat "reload can relax auth" as a supported release behavior.

## Scopes

Canonical scopes, deliberately few so a minted token is readable:

| scope | meaning |
|---|---|
| `tesseract.read` | recall, lookup, history, list, packet/pack, whoami |
| `tesseract.write` | context, memory, knowledge, event, workspace writes; status changes; touch; embed |
| `tesseract.promote` | promote request/approve/apply (context and workspace) |
| `tesseract.maintain` | repair, compaction, queue operations, namespace registration |
| `tesseract.admin` | settings apply, config backup/restore, token lifecycle, `/v1/auth/summary` |

`tesseract.*` (or `*`) grants all of them.

**Existing token scopes map onto these** (managed tokens translate legacy to
canonical, JWTs translate canonical to legacy; neither direction invents
authority):

| legacy token scope | canonical |
|---|---|
| `memory:read`, `packet` | `tesseract.read` |
| `write`, `memory:write` | `tesseract.write` |
| `promote.request`, `promote.approve`, `promote.apply` | `tesseract.promote` |
| `repair`, `namespace.register`, `namespace.admin` | `tesseract.maintain` |
| `admin` | `tesseract.admin` |

Every valid managed token also holds `tesseract.read`: context-domain reads have
never needed a scope, only a valid token. A default token (no `--scopes`) is
therefore read + write + promote + maintain, never admin.

### Two layers

The guard makes the **coarse** decision from the route or tool table (one
`tesseract.<verb>` per route, deny by default; unit tests fail if a route or
tool has no entry). Tesseract's handlers keep the **fine** decision they always
made: the legacy scope names (`memory:write`, `promote.apply`, ...) and the
per-token **namespace globs**, which stay an additional per-token constraint.
The fine checks apply to every verified caller in every mode, so `permissive`
and `off` relax authentication only; they never widen what a presented token may
do.

## MCP (`tesseract mcp`, stdio)

The adapter opens the store directly (no HTTP peer), so the HTTP middleware does
not cover it. The guard is wired into every tool call: the bearer is consulted
per call, so `--token-file` keeps working and a launcher that rewrites the file
in place (a short-lived Tether agent token) is honored without a restart; a
rejected re-read keeps the last good value. A managed token revoked mid-session
stops working on the next call. A reference given to `--token-file` or
`TESSERACT_MCP_TOKEN` is resolved once at startup.

Default mode is `permissive`. Reads without a token keep working; mutations
without a token are still refused by the tool itself, as before go-auth. Under
`enforce`, every tool needs a valid token holding the tool's scope. The startup
`Auth:` line and the would-deny blocks go to stderr (stdout is the protocol).

## Operational notes

- `GET /v1/health/readiness` and, with `--metrics`, `GET /v1/metrics` stay
  unauthenticated. The embedded web UI (`/`) is static and unauthenticated; its
  API calls need a bearer under `enforce`. The UI has no bearer entry yet, so
  until it does, a browser behind a proxy that does not attach one will see 401.
  Tracked as a follow-up, not built here.
- `--allow-unauthenticated-remote` only bypasses the startup refusal now. The
  guard defaults to `enforce` on a non-loopback listener and the bind guard
  refuses `off`/`permissive` there, so the flag no longer yields an anonymous
  listener. Use a tailnet bind with `HOLLIS_AUTH_TESSERACT=off` for that.
- Guard rejections keep Tesseract's error envelope (`auth_required`,
  `insufficient_scope`, plus `route_not_mapped` and `auth_unavailable`).
- The error text for a 403 names the missing scope.

## DEC-103

DEC-103's managed-token store and lifecycle (token create/list/revoke, scopes,
namespace globs, TTL) is **reused as the static-key backend** rather than
replaced; nothing about it needed re-issuing. What is dropped is D1, "keep the
HTTP API anonymous on loopback": the listener now defaults to `permissive` with
a would-deny report, and `enforce` is the destination. The separate admin-token
and issuer-folder work in DEC-103 is not built: Tether is the issuer, and the
store is the interim root-credential backend. The token lifecycle routes
(`/v1/auth/tokens/*`) now require `tesseract.admin`; before this change any
valid token could mint, list and revoke tokens. Bootstrap an admin token with
the CLI, which works on the store directly:
`tesseract context token create --name admin --scopes admin ...`.

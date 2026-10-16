# Credentials

Tesseract holds no plaintext credential in its configuration. Every secret it
needs is a go-secrets reference (`<scheme>://<authority>/<path>`) resolved at
the moment of use by the shared `go-secrets` library. Tesseract has no
resolver of its own: `internal/credentials` only names the references and
decides which failures are fatal.

References are not secrets. They may appear in `config.yaml`, in flags and in
service definitions. A literal key placed where a reference belongs is rejected
as an invalid reference, and the error never echoes it.

## Which credentials exist

| Credential | Used by | Default reference | Override |
|---|---|---|---|
| OpenAI API key | embeddings (`embedding.provider: openai`), synthesis (`synthesis.provider: openai`) | `file://tesseract/openai-api-key` | `embedding.api_key_ref`, `synthesis.api_key_ref` in `config.yaml` |
| Anthropic API key | synthesis (`synthesis.provider: anthropic`) | `file://tesseract/anthropic-api-key` | `synthesis.api_key_ref` |
| MCP capability token | `tesseract mcp` | none (optional) | `--token-file`, `TESSERACT_MCP_TOKEN`, `--token`, each accepting a reference |
| Static bearer token (legacy) | `tesseract serve --static-token` | none (optional) | `--static-token` accepts a reference |
| Deny-list bearer (Tether credential holding `tether.maintain`) | JWT revocation poll (`auth.deny_list`) | none (optional) | `auth.deny_list.bearer_ref` in `config.yaml`; a literal is refused. See [`auth.md`](auth.md) |

Managed-auth tokens (`serve --managed-auth`) live as hashes in Tesseract's own
store; the plaintext is shown once at creation and is the caller's to keep.
Store the caller's copy with `secrets set` too.

## Setting a value

On a host without a Secret Service (the fleet's Linux hosts) use the managed
file store. The value is read from stdin only, never argv:

```bash
printf %s "$KEY" | secrets set file://tesseract/openai-api-key
secrets check file://tesseract/openai-api-key     # prints the source, never the value
```

Files are `0600` in `0700` directories under
`$XDG_STATE_HOME/hollis/secrets`. `keychain://` works where a Secret Service or
the macOS keychain exists (macOS hosts: `keychain://tesseract/openai-api-key`
with an explicit `api_key_ref`); on Linux without one it returns `unavailable`
with guidance. `op://vault/item/field` reads through the 1Password CLI.

```yaml
embedding:
  provider: openai
  model: text-embedding-3-large
  api_key_ref: op://Hollis/OpenAI/credential   # optional; default is the file:// reference above
```

## Environment variables are no longer read

`OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are ignored. The environment is not a
silent fallback. A supervisor that already injects the variable must instead
hold a reference (`api_key_ref`) or write the value to the store once with
`secrets set`.

## `tesseract mcp` token precedence

Unchanged, file first: `--token-file`, then `TESSERACT_MCP_TOKEN`, then
`--token`. Each source may be a reference.

- `--token-file <path>` is read through go-secrets `tokenfile`: a regular file,
  mode exactly `0600`, owned by the caller, final component not a symlink. A
  looser file is refused. `<path>` may instead be a reference such as
  `file://tesseract/mcp-token`.
- `TESSERACT_MCP_TOKEN` and `--token` are resolved when they are a reference
  and otherwise taken as the legacy literal. A literal in argv or the
  environment is visible to same-user processes; prefer the file or a reference.
- A named source that cannot be read or resolved is an error. It never falls
  through to a lower-precedence source.

## Failing closed

| Situation | Behavior |
|---|---|
| `api_key_ref` / `--token-file` / `--static-token` reference is missing, denied, unavailable or empty | Startup (or the command) fails with the reference name and go-secrets guidance, never the value |
| No `api_key_ref` and the default `file://` entry is absent | Documented degrade: embeddings fall back to BM25-only recall, synthesis returns 503. Storage and every other surface are unaffected, so a checkout without keys still works |
| Default entry exists but is denied, unreadable or empty | Error, not a degrade |

## Audit table

| Site | Before | Now |
|---|---|---|
| `internal/llm/openai`, `internal/llm/anthropic` | read `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` when the key argument was empty | take the key from the caller; read no environment |
| `cmd/tesseract` embedder and synthesis construction | env presence check | `credentials.ProviderKey` (reference, fail closed when explicit) |
| `cmd/tesseract` `mcp` token | file (any mode), env, flag | `tokenfile.Read` (strict) or reference, env/flag literal or reference |
| `cmd/tesseract` `serve --static-token` | literal | literal or reference |
| `internal/authn` deny-list poller | n/a (new) | `auth.deny_list.bearer_ref` resolved at startup; literal refused; unresolvable reference stops startup. The JWKS bootstrap key is a public key file, not a secret |
| `internal/contextapi` admin provider status | `env_var` / `env_present` from the environment | `key_ref` / `key_present` from go-secrets (`env_var`/`env_present` kept as aliases of the same values for the shipped UI) |
| `tesseract.cerberus.yaml` | `OPENAI_API_KEY: keychain://openai/embed` resolved by `cerberus run-secrets` | env block removed; the process resolves its own reference (macOS: set `embedding.api_key_ref: keychain://openai/embed`) |
| `cmd/smoke` | `llmopenai.New("")` from env | resolves the default reference |
| Tether catalog entry `mcp-servers/tesseract.yaml` (outside this repo) | stdio over `ssh` to `tesseract-mcp-stdio`; no inline credential found | owned by Tether (CW-20261015-0014); the remote wrapper chooses the token source |
| `env.example`, `docs/QUICKSTART.md`, `docs/OPERATIONS.md`, `docs/vector-search.md`, `docs/AGENT-SETUP.md` | `export OPENAI_API_KEY=...` | `secrets set` and references |

## Do not put a key in a `.env` beside the binary

The file is read by tooling, copied into generated service definitions and
printed by anything inspecting the working directory. Use a reference.

## Known limits

Same-user processes can read the managed store and the environment; go-secrets
does not defend against them. There is no caching: each resolution reads the
store (startup, and on each admin status request). Rotating a key requires a
restart for the embedder and synthesis clients, which are built once.

# Tesseract quick start

This guide takes a source checkout to a local daemon, first record, optional
provider setup, and optional MCP client.

## 1. Build from source

The current public preview has a source-only install path and requires Go
1.26.6.

```bash
git clone https://github.com/hollis-labs/tesseract.git
cd tesseract
make build
./tesseract --version
```

The embedded web UI is already committed, so this build does not need Node.
Use `make install` if you want `tesseract` in your Go bin directory.

## 2. Confirm the data paths

```bash
./tesseract path
```

This reports the XDG data, state, cache, and config roots, the active workspace,
the main database, `records/`, `queue.db`, and `config.yaml`. The command does
not create them.

There is no `TESSERACT_HOME` override. For a completely isolated test store,
set all four `XDG_*_HOME` variables and unset `TESSERACT_DB_PATH` and
`TESSERACT_WORKSPACE`; then run `tesseract path` again and inspect every path
before issuing a stateful command. See [Operations](OPERATIONS.md#paths-and-isolation).

## 3. Start locally

```bash
./tesseract serve
```

The default listener is `127.0.0.1:8089` without authentication:

- web UI: `http://127.0.0.1:8089/`
- HTTP API: `http://127.0.0.1:8089/v1/`
- readiness: `http://127.0.0.1:8089/v1/health/readiness`

This is a single-machine development boundary. A non-loopback bind is refused
without managed auth, a static token, or the explicit unsafe override. Do not
expose it remotely yet; configure the [remote boundary](#remote-http-access)
first.

## 4. Write and read a context record

In another terminal:

```bash
./tesseract context put \
  --client-id demo \
  --namespace app/demo/session \
  --key goal \
  --actor app:demo \
  --json '{"phase":"start","objective":"evaluate tesseract"}'

./tesseract context get \
  --namespace app/demo/session \
  --key goal

./tesseract context history \
  --namespace app/demo/session \
  --key goal \
  --limit 10
```

The CLI opens the local store directly; `tesseract serve` is not required for
CLI or MCP stdio commands.

## 5. Optional provider setup

Provider keys are not required for the core store or lexical recall.

Current provider support:

| Feature | Providers | Credential |
|---|---|---|
| Embeddings and semantic recall | OpenAI | `OPENAI_API_KEY` |
| Answer synthesis | OpenAI, Anthropic | `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` |

Copy a sample to the `config-file` reported by `tesseract path`:

```bash
mkdir -p /path/from/tesseract-path
cp examples/config.openai.yaml /path/from/tesseract-path/config.yaml
chmod 600 /path/from/tesseract-path/config.yaml
export OPENAI_API_KEY='...'
```

For Anthropic synthesis plus OpenAI embeddings, use
[`../examples/config.anthropic-openai.yaml`](../examples/config.anthropic-openai.yaml)
and set both keys. Tesseract-created configs are owner-only on POSIX systems;
a file copied or created by hand retains the mode you gave it.

Embedding requests send record text or search queries to OpenAI. Synthesis
sends the question and selected source summaries and bodies to the configured
provider. Review the [complete egress disclosure](OPERATIONS.md#outbound-connections-and-data-egress).

## 6. Optional managed HTTP authentication

Create at least one token before starting `--managed-auth`:

```bash
./tesseract context token create \
  --name http-client \
  --client-id app:http-client \
  --scopes write,promote.request,packet \
  --namespaces 'app/http-client/*' \
  --ttl 24h
```

Copy the plaintext value immediately; it is shown once. Then start the daemon:

```bash
./tesseract serve --managed-auth --addr 0.0.0.0:8089
```

Every non-public API call now needs the token, including reads:

```bash
curl \
  -H "Authorization: Bearer $TESSERACT_TOKEN" \
  'https://tesseract.example/v1/context/head?namespace=app/http-client/session&key=goal'
```

Only readiness and enabled metrics are public in a token mode. Tesseract has no
built-in TLS, so use a TLS-terminating reverse proxy, VPN, or SSH tunnel for any
remote connection. A static token is available for simpler deployments but
cannot receive the `admin` scope. See [`SECURITY.md`](../SECURITY.md).

For settings/config administration, create a separate managed token with the
explicit `admin` scope and protect it more tightly than routine client tokens.

## 7. Optional MCP client

Create a narrowly scoped capability token for the agent:

```bash
./tesseract context token create \
  --name demo-agent \
  --client-id app:demo-agent \
  --scopes write,promote.request,memory:read \
  --namespaces 'app/demo-agent/*' \
  --ttl 8760h
```

Configure the MCP host from [`../examples/mcp.json`](../examples/mcp.json), then
restart the host so it refreshes the tool registry. `tesseract mcp` is the
stdio server; you do not also need `tesseract serve`.

The MCP token gates mutations, grants memory/knowledge reads through
`memory:read`, and supplies namespace globs to the tools that filter by them.
It is not a complete read-isolation boundary: several context/audit reads are
available without a token. Attach only clients you trust with the context
store. Begin discovery with `tesseract_skills`, then request `start-here`. See
[Agent setup](AGENT-SETUP.md).

## 8. Create and verify a backup

The v2 backup is a directory, not a JSON file:

```bash
./tesseract context backup export \
  --out "$PWD/tesseract-backup" \
  --config /path/from/tesseract-path/config.yaml

./tesseract context backup verify --in "$PWD/tesseract-backup"
```

The config is optional and is included only when `--config` is supplied.
`queue.db` is not included. Before restore, stop every daemon/MCP/embedded
process; restore replaces rather than merges. Follow the
[restore runbook](OPERATIONS.md#restore-runbook).

## Next steps

- [Agent and MCP setup](AGENT-SETUP.md)
- [Operations and support](OPERATIONS.md)
- [HTTP API](SPECS/API.md)
- [CLI reference](SPECS/CLI.md)
- [MCP tool catalog](MCP_TOOLS.md)

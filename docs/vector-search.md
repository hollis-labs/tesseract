# Vector search

Tesseract supports semantic ranking by storing embeddings beside its local
record indexes. The current daemon uses OpenAI for embeddings; no external
vector database or SQLite extension is required.

## What stays local

- embedding vectors are stored in the local SQLite store
- candidate filtering and cosine-similarity ranking run locally
- lexical/BM25 recall works without an embedding provider
- deterministic namespace, key, tag, type, and status filters do not require a
  provider

## What leaves the machine

When the OpenAI embedder is configured, Tesseract sends text to OpenAI to
produce a vector:

- memory and knowledge summary/body text when their revisions are embedded
- extracted context record payload text for explicit or auto-embedding paths
- the caller's query for semantic search, semantic recall, or RAG retrieval

The returned vector is stored locally. Do not enable embedding for content that
must not be processed by OpenAI. See the complete
[data egress disclosure](OPERATIONS.md#outbound-connections-and-data-egress).

## Configure embeddings

At the `config-file` reported by `tesseract path`:

```yaml
embedding:
  provider: openai
  model: text-embedding-3-large
```

Then set the credential before starting `tesseract serve` or `tesseract mcp`:

```bash
export OPENAI_API_KEY='...'
```

If the provider is unsupported or the key is absent, Tesseract disables the
embedding runtime and keeps lexical retrieval available. Operations that
explicitly require embeddings return an unavailable/error response rather than
pretending an empty semantic result is success.

## When embeddings are created

Memory and knowledge writes enqueue background embedding work when the runtime
has an embedder. The job queue is stored separately in `queue.db`. Context
records are embedded through explicit MCP embedding tools and the context tool
paths whose schemas state that they auto-embed, such as chunked ingestion and
session snapshots.

To fill missing memory/knowledge vectors after enabling or repairing a provider:

```bash
tesseract backfill-embeddings
```

Operator queue status and backfill routes are also available through the admin
surface. The separate queue database is not part of a store backup; see
[Backup format](OPERATIONS.md#backup-format).

## Retrieval modes

Memory and knowledge recall distinguish lexical, semantic, and hybrid search:

- lexical uses the local full-text index
- semantic embeds the query and ranks stored vectors by cosine similarity
- hybrid combines lexical and semantic rankings

Similarity scores are meaningful only within the response that produced them.
Changing model, query, corpus, filters, or ranking mode changes their meaning.
Use payload projection and response budgets to avoid returning full bodies when
summaries are enough.

The MCP context-domain tools `context_embed`, `context_search`, and
`context_rag_query` expose the lower-level record embedding/search surface.
Consult [MCP tools](MCP_TOOLS.md) or the live tool schema for exact arguments.

## Current limitations

- OpenAI is the only embedding provider wired into the daemon
- semantic operations require network access and send input text to OpenAI
- vectors are model-specific; switching models requires backfill
- ranking is local and does not use a dedicated vector database
- queued embedding work is operational state and is excluded from store backups
- there is no namespace-policy `auto_embed` field; embedding is driven by the
  implemented write/tool paths described above

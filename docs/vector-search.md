# Vector Search

Tesseract provides semantic search over stored records via embeddings. This extends the existing deterministic namespace/key/tag-based retrieval with meaning-based ranking.

## Architecture

Embeddings are stored in a dedicated `embeddings` table alongside the existing `records` table. Each record can have embeddings from multiple models, keyed by `(record_id, model)`. Vectors are stored as packed float32 BLOBs in SQLite.

Similarity search uses brute-force cosine similarity in Go — no external vector database or C extensions required. This is efficient for Tesseract's expected scale (hundreds to low thousands of records).

## Embedding Provider

Tesseract uses a pluggable `Provider` interface for embedding generation:

```go
type Provider interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    Model() string
    Dimensions() int
}
```

The default provider is **Ollama** with the `nomic-embed-text` model (768 dimensions). Requires Ollama running locally at `http://127.0.0.1:11434`.

A mock provider is available for testing (deterministic vectors from SHA-256 hashing).

## MCP Tools

### `context_embed`

Generate and store an embedding for a record.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `record_id` | yes | Record ID to embed |
| `namespace` | yes | Record namespace |
| `key` | yes | Record key |
| `model` | no | Embedding model (default: provider's configured model) |

Idempotent: re-embedding the same record with the same model overwrites the previous vector.

### `context_search`

Semantic search across records using embeddings.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `query` | yes | Search query text |
| `limit` | no | Max results (default 10, max 25) |
| `namespace` | no | Namespace prefix filter |
| `type` | no | Record type filter |
| `tags` | no | Comma-separated tag filter (any match) |
| `threshold` | no | Minimum similarity score (default 0.7) |

Returns ranked results with `record_id`, `namespace`, `key`, and `score` fields.

## Usage Examples

### Embed a record

```json
{
  "tool": "context_embed",
  "arguments": {
    "record_id": "rec_abc123",
    "namespace": "app/notes",
    "key": "meeting-2026-03-08"
  }
}
```

### Search for similar content

```json
{
  "tool": "context_search",
  "arguments": {
    "query": "authentication design decisions",
    "namespace": "app/notes",
    "limit": 5
  }
}
```

## Limitations

- Requires Ollama running locally for embedding generation (or another configured provider)
- Brute-force similarity search — suitable for <50k embedded records
- Embeddings may become stale when records are updated; re-embed to refresh
- No automatic embedding on write (planned for future namespace policy flag)

## Future

- Auto-embed on write via namespace policy `auto_embed: true`
- Cloud provider support (OpenAI, Voyage, Cohere)
- sqlite-vec upgrade path if dataset exceeds ~50k records

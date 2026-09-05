# Credentials

Tesseract needs one credential: an API key for the embedding provider, when
`embedding.provider` selects a hosted one such as `openai`.

## How it is supplied

**Environment only, today.** Tesseract reads `OPENAI_API_KEY` from its process
environment (`internal/llm/openai`) and has no other channel — no config-file
field, no keychain lookup, no secret-reference syntax. Setting the variable is
the whole interface:

```bash
OPENAI_API_KEY=… tesseract serve
```

`ANTHROPIC_API_KEY` behaves the same way for the Anthropic client.

## What happens when it is absent

Nothing fails. Tesseract logs

```
warning: embedding.provider=openai but OPENAI_API_KEY not set —
embedding disabled, falling back to BM25-only recall
```

and continues with lexical recall. Semantic search is degraded; storage,
retrieval and every other surface are unaffected. This is the intended path for
a developer working from a checkout without a key, so running the test suite or
a local instance needs no credential at all.

## Deploying without plaintext

Because the environment is the only channel, keeping a key out of plaintext is
the responsibility of whatever launches Tesseract. Two patterns work today:

- **A supervisor that resolves the secret at start.** The process manager holds
  a reference rather than a literal, resolves it in the moment it execs
  Tesseract, and never writes the resolved value to disk.
- **A wrapper that injects the variable**, such as `op run`, `aws-vault exec`,
  `systemd-creds`, or an equivalent. Tesseract sees an ordinary environment
  variable and needs no knowledge of the store behind it.

Either way, resolution happens outside Tesseract, in the process that starts it.

### Do not put the key in a `.env` beside the binary

It is the pattern this codebase most often inherits from, and it is the one that
leaks: the file is read by tooling, copied into generated service definitions,
and printed by anything inspecting the working directory. If a launcher supports
a secret reference, use it; if not, export the variable in the launching shell
for that invocation.

## Known limitation

Tesseract cannot resolve a secret reference itself. A deployment where no
supervisor can inject the variable — Tesseract embedded in another binary, or a
hosted instance — has only the plaintext-environment channel available. Teaching
Tesseract to accept a reference in place of a literal, resolved at the embedder
client's construction and never logged, is planned; the graceful-degradation
behavior above is what stands in until then.

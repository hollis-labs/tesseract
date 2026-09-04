# Tesseract v0.9 adoption and migration guide

This guide is for applications and agents adopting Tesseract `v0.9.0`, especially consumers upgrading from `v0.8.x`. It covers the supported Go and MCP contracts, the breaking migration, and the operating rules that keep stored context trustworthy.

Use the exact release in production:

```bash
go get github.com/hollis-labs/tesseract@v0.9.0
go install github.com/hollis-labs/tesseract/cmd/tesseract@v0.9.0
```

Do not pin `main` or a pseudo-version for the Nanite adoption. The tag is the contract.

## The three domains

Tesseract has three public information domains. Pick the domain before picking a tool.

| Domain | Use it for | Write surface | Source of truth |
|---|---|---|---|
| `context` | Application state, typed records, views, packets, and session working context | `context_write` or typed/context ingestion tools | Tesseract's append-only context records |
| `memory` | Decisions, feedback, outcomes, observations, and other time-situated agent memory | `memory_write` | Tesseract's append-only memory revisions |
| `knowledge` | Durable summaries of documents, packages, investigations, playbooks, and other referenced material | `knowledge_write` | The stored body is durable; the external pointer identifies its provenance and may become stale |

`playbook` is a knowledge `kind`, not a fourth domain. Tasks, issues, epics, sprints, dependencies, and execution state belong in Torque. Tesseract can preserve the durable reasoning behind that work, but it must not become a second task tracker.

Memory and knowledge share the revision engine but remain distinct policy domains. Context has its own record store. On keyed MCP reads, `domain` is required and acts as a filter: asking for `domain=memory` never returns a knowledge revision just because the namespace and key match.

## Ownership, durability, and append-only history

A write appends an immutable revision. A stable namespace/key identity points at its current revision; history remains available after supersession or deprecation. Use a stable key for an evolving concept and set `supersedes` when explicitly replacing a known revision.

Tesseract owns:

- the SQLite state and revision indexes;
- context record files and their history;
- memory and knowledge revision bodies, metadata, activation, and pointer observations;
- schema migrations and orderly shutdown of its background queue and decay workers.

The consuming app owns:

- honest domain, namespace, origin, trigger, confidence, and status selection;
- the external resource named by a knowledge pointer;
- calling `tesseract_touch` only for recall results that actually informed work;
- coordinating process-manager and data-path changes before upgrading the daemon.

Never edit Tesseract's SQLite database or record tree as an integration API. Back up or move the complete resolved layout while Tesseract is stopped.

## Namespace and facet contracts

Memory writes accept exactly these typed namespace shapes:

```text
user/{user_id}/memory/{type}
user/{user_id}/project/{project_id}/memory/{type}
user/{user_id}/session/{session_id}/memory/{type}
```

The built-in `{type}` values are `decisions`, `feedback`, `followups`, `learnings`, `limitations`, `notes`, `outcomes`, and `references`. A flat `user/{id}/memory` remains useful as a recall prefix spanning the typed children, but it is not a writable memory namespace. Test the write path, not just recall.

Knowledge namespaces have the shape `{user|app}/{id}/knowledge[/...]`. Every knowledge write requires a non-empty `source`, a pointer with both `scheme` and `locator`, and one canonical `kind`:

```text
doc, handoff, investigation, learning, mcp_server, note, package,
playbook, pointer, project_canonical, session_close
```

These rules are enforced at the shared persistence boundary. Therefore the root Go facade, the memory store, HTTP, MCP, promotion, and the knowledge wrapper cannot bypass them. Memory-domain revisions must carry zero knowledge facets. `memory.ErrInvalidInput` is the canonical Go validation sentinel; HTTP and MCP translate it to their validation error shape.

## Embedded Go adoption

The supported root type is `tesseract.Tesseract`, created by `tesseract.Open`. The caller owns its lifetime and must call `Close` after all users stop. `Close` cancels and joins background decay/queue workers before closing their databases.

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/memory"
)

func main() {
	ctx := context.Background()
	root, err := os.MkdirTemp("", "tesseract-adoption-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if removeErr := os.RemoveAll(root); removeErr != nil {
			log.Printf("remove temporary Tesseract root: %v", removeErr)
		}
	}()

	db, err := tesseract.Open(ctx, tesseract.Config{RootDir: root})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close tesseract: %v", closeErr)
		}
	}()

	rev, err := db.WriteMemory(ctx, memory.WriteInput{
		Domain:     memory.DomainMemory,
		Namespace:  "user/demo/memory/decisions",
		MemoryKey:  "release.channel",
		Status:     memory.StatusCanonical,
		Author:     memory.Author{AgentID: "nanite"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "adoption-v0.9",
		Origin:     memory.OriginProject,
		Confidence: 0.95,
		Payload: memory.Payload{
			Summary: "Nanite consumes immutable Tesseract release tags.",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	hits, err := db.RecallMemory(ctx, memory.RecallInput{
		Namespaces: []string{"user/demo/memory"},
		Query:      "which Tesseract version should Nanite consume?",
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeLexical,
		Limit:      5,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s; recalled %d result(s)", rev.RevisionID, len(hits))
}
```

The complete compile-checked version lives at [`../../examples/adoption-go/main.go`](../../examples/adoption-go/main.go).

That example intentionally uses a disposable temporary root. An embedded production
consumer must resolve a stable absolute `RootDir` (or explicit `DBPath` and
`RecordsDir`) under its own application layout. `tesseract.Open` does not resolve
the daemon's XDG layout for you; using a relative root can silently create a
second store when the process working directory changes.

### Go embedder modes

The library accepts any implementation of `github.com/hollis-labs/go-embed-contracts.Embedder` through `tesseract.WithEmbedder`. Set the model passed to that implementation with `tesseract.WithEmbeddingModel`. The embedder instance may be used concurrently by context embeddings, memory embeddings, semantic recall, and an optional queue worker, so implementations must be safe for the concurrency their host permits.

```go
db, err := tesseract.Open(
	ctx,
	tesseract.Config{RootDir: root},
	tesseract.WithEmbedder(embedder),
	tesseract.WithEmbeddingModel("text-embedding-3-large"),
)
```

Without an injected embedder, embedding operations return `tesseract.ErrEmbedderUnavailable`; it is the same sentinel as `memory.ErrEmbedderUnavailable`, so `errors.Is` works through either supported facade. Pure lexical recall remains available. `ErrSimilarityUnavailable` was removed; do not retain an alias in consumer code.

The `tesseract` daemon and MCP runtime currently construct the `openai` embedding provider from `embedding.provider: openai`, `embedding.model`, and `OPENAI_API_KEY`. An unsupported provider or missing credential disables embedding and leaves lexical/BM25 recall available. Explicit embedding-only, semantic-only, and RAG operations return a supported unavailable/error response; they do not report an empty semantic success. The same constructed provider instance is shared with the memory and MCP context tools.

## MCP v0.9 surface

Run the stdio server from the same resolved layout as the daemon:

```json
{
  "mcpServers": {
    "tesseract": {
      "type": "stdio",
      "command": "/absolute/path/to/tesseract",
      "args": ["mcp", "--token", "<capability-token>"]
    }
  }
}
```

Begin discovery with `tesseract_skills` and then `tesseract_skills {"name":"start-here"}`. This progressive help is shipped by the `tesseract mcp` binary. The authoritative catalog is [`../MCP_TOOLS.md`](../MCP_TOOLS.md).

The current domain writes are `context_write`, `memory_write`, and `knowledge_write`. The collapsed reads and revision operations are:

| Current tool | Purpose |
|---|---|
| `tesseract_get` | Current entry at `domain`, `namespace`, and `key` |
| `tesseract_history` | Revision history for `domain`, `namespace`, and `key` |
| `tesseract_recall` | Ranked recall across selected memory/knowledge `domains` |
| `tesseract_get_revision` | Full revision by `revision_id` |
| `tesseract_deprecate` | Deprecate a revision by `revision_id` |
| `tesseract_touch` | Reinforce recalled revisions that actually informed work |

### Retired MCP names and exact replacements

There are no compatibility aliases. Update allowlists and prompts as well as executable calls.

| Retired in v0.9 | Replacement |
|---|---|
| `context_head`, `memory_get`, `knowledge_get` | `tesseract_get` with required `domain`, `namespace`, `key` |
| `context_history`, `memory_history`, `knowledge_history` | `tesseract_history` with required `domain`, `namespace`, `key` |
| `memory_get_revision` | `tesseract_get_revision` |
| `memory_deprecate` | `tesseract_deprecate` |
| `memory_recall`, `tesseract_lookup` | `tesseract_recall`; narrow with `domains` |
| `views_evaluate` | `context_view` with `full_evaluation` |
| `context_packet` | `context_pack` with `shape` |
| `context_broker_plan`, `context_broker_fetch`, `context_broker` | `context_plan` with `execute` selecting plan/fetch |
| `context_bulk_ingest`, `context_chunked_ingest` | `context_ingest` with `mode` |
| `context_status_promote`, `context_status_deprecate` | `context_status_set` with `status` |
| `context_types_list`, `context_views_list`, `context_namespaces_list`, `context_namespace_show` | `context_registry_list` with `kind` and optional `name` |
| `context_promote_request`, `context_promote_approve`, `context_promote_apply` | `context_promote` with required `stage` |
| `context_audit` | `context_audit_list` |
| `context_promote_list` | `context_promotion_list` |
| `context_session_snapshot` | `context_session_write` |

The keyed MCP reads now take `key`; the revision JSON and `memory_write` still use `memory_key`. Retired `to_status` must become `status`, and retired `namespace` on the registry tool must become `name`. The packet surfaces no longer support `head_only`; use `payload_max_bytes`. Packet `payload_mode` is retired except for the explicitly accepted `full` value.

One retired call is not exactly equivalent:
`context_status_promote(to_status="deprecated")` used promotion validation and
emitted a `status_promote` audit event. `context_status_set(status="deprecated")`
uses the deprecation path. Consumers that relied on the promotion audit event or
transition validation must handle that behavior change explicitly.

Recall hit fields also changed from PascalCase to snake_case. Update static
decoders from `{Revision, Score, State}` to `{revision, score, state}` in addition
to adopting the outer recall envelope.

## Recall, projection, budgets, and cursors

The default pattern is recall, choose, hydrate, use, touch:

```text
tesseract_recall(payload_mode=summary)
    -> choose relevant revision_id values
tesseract_get_revision(revision_id)
    -> use the full body; this deliberate read reinforces once
tesseract_touch(revision_ids=[...])
    -> reinforce summary-only hits that shaped the work
```

Do not touch every returned candidate. Recall itself does not reinforce activation because a ranker's guess is not evidence of usefulness. `tesseract_get_revision` is already a deliberate read and reinforces the parent entry; touching that same hit adds a second reinforcement, so do it only when that extra signal is intentional. Touch is essential when a projected summary shaped the work without hydration.

`payload_mode` is `keys`, `summary`, or `full`; the server default is `summary`. Keys and summaries retain `revision_id`, so callers can always hydrate. A missing body under projection means withheld, not empty. Full results include state and cap a single page at 100; keys and summary cap at 500.

Every paged recall response includes a `manifest` with `results_total`, `results_returned`, `bytes_returned`, `tokens_estimate`, `truncated`, `truncation_reason`, and nullable `next_cursor`. `budget_bytes` and `budget_tokens` constrain the serialized results. `estimate_only: true` returns the same manifest and facet counts without serializing `results`.

Cursors are opaque, query-bound continuation tokens. Reuse the returned token only with the same namespaces, ranking, search mode, revision scope, query, and filters. Changing an ordering input makes the cursor invalid instead of silently returning a plausible wrong page. Payload projection and page size may change because they do not reorder the candidate set. The public Go `Store.Recall` supports registered rerankers for an unpaged result set; `RecallPaged` rejects reranking because a page-local reorder cannot produce a truthful continuation cursor. MCP and HTTP recall do not expose reranker arguments.

Recall scores are nullable pointers in Go and optional on the wire. They are comparable only inside one response:

| Ranking | Score meaning |
|---|---|
| `activation` | decayed recency/use strength, weighted by confidence |
| `similarity` | cosine similarity; can be zero or negative; needs an embedder |
| `relevance` + `search_mode=hybrid` | RRF-fused BM25 + cosine relevance |
| `relevance` + `search_mode=lexical` | no score; BM25 order is the signal |
| `relevance` + `search_mode=semantic` | cosine similarity |
| `chronological` | no score; array order and `created_at` carry the ordering |

Use `similarity_min` only for pure similarity or relevance with `search_mode=semantic`; other combinations reject it. `confidence_min` is different: it filters the author's recorded confidence.

### Status and revision scope

`revision_scope=current` is the default; `timeline` includes historical revisions. The default status set is draft, reviewed, and canonical, so deprecated material is excluded unless explicitly requested.

Superseding a revision also marks its predecessor deprecated. In v0.9, an explicit `statuses:["deprecated"]` query under current scope returns only terminal deprecated revisions: deprecated revisions with no incoming `supersedes` edge. This makes deliberate deprecation discoverable for dedup without mixing in ordinary superseded history. Timeline semantics are unchanged and can include both terminal deprecations and superseded predecessors.

Before writing a candidate duplicate, recall the relevant namespace/key/topic. On a match, choose deliberately: supersede an evolving entry, deprecate a terminal one, or use a different key only when the new fact is genuinely distinct.

## MCP agent example

Arguments that carry lists are JSON-encoded strings in the MCP schema.

```json
mcp__tesseract__tesseract_recall {
  "namespaces": "[\"user/chrispian/memory\",\"user/chrispian/knowledge\"]",
  "domains": "[\"memory\",\"knowledge\"]",
  "query": "Tesseract v0.9 Nanite adoption",
  "ranking": "relevance",
  "search_mode": "hybrid",
  "revision_scope": "current",
  "payload_mode": "summary",
  "limit": 10,
  "budget_tokens": 2500
}
```

If `manifest.next_cursor` is non-null, repeat the same call with that value in `cursor`. Hydrate only selected hits:

```json
mcp__tesseract__tesseract_get_revision {
  "revision_id": "01HXYZ..."
}
```

After those revisions actually shape the work:

```json
mcp__tesseract__tesseract_touch {
  "revision_ids": "[\"01HXYZ...\"]"
}
```

## Binary and XDG migration

The executable and process name is `tesseract`; `cmd/contextd` and `go install .../cmd/contextd` no longer exist. Update launchd/systemd definitions, managed-process commands, health scripts, MCP client configuration, and shell aliases before the first upgraded restart.

Run `tesseract path` to resolve the authoritative locations. Defaults follow XDG, including `~/.local/share/tesseract`, `~/.local/state/tesseract`, `~/.cache/tesseract`, and `~/.config/tesseract`. The relevant overrides are all four `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`, and `XDG_CONFIG_HOME` variables, plus `TESSERACT_DB_PATH` and `TESSERACT_WORKSPACE` for their specific paths.

`CONTEXTD_ROOT` is removed and ignored. This is a migration hazard: a process still setting it may open the normal live XDG store instead of the intended isolated legacy root. Before upgrade:

1. Stop every old `contextd` and new `tesseract` process.
2. Run the old and new path commands/configuration in a controlled shell and identify the complete source and destination layouts.
3. Back up the legacy database, records, queue/state, and configuration together.
4. Move or copy them to the new resolved locations without combining live writers. If the old installation used the hand-made `~/.tesseract` layout, move it explicitly; automatic legacy-name adoption does not cover that dot-directory.
5. Remove `CONTEXTD_ROOT`, set the intended XDG/Tesseract-specific overrides, and run `tesseract path` again.
6. Run `tesseract migrate-namespaces` and `tesseract migrate-knowledge-kinds` as plan-first migrations when applicable. For knowledge kinds, execute the plan's emitted apply command with both `--expect-rows` and `--expect-digest`; those bindings prevent an apply against a corpus that changed after review.
7. Start one Tesseract instance, verify current/history/recall on representative records, then update remaining clients.

## Nanite v0.9 upgrade checklist

- [ ] Pin `github.com/hollis-labs/tesseract` and the CLI to exactly `v0.9.0`; remove pseudo-version or branch pins.
- [ ] Replace root `Conduit` naming with `tesseract.Tesseract` and its current `Open`/`Close` lifecycle.
- [ ] Replace `ErrSimilarityUnavailable` with `memory.ErrEmbedderUnavailable` or the identical root sentinel.
- [ ] Remove ranking string casts; use `memory.RankingRelevance` and the exported `memory.SearchMode*` constants.
- [ ] Replace all retired MCP tool IDs and selector argument names from the table above; update tool allowlists and prompt text.
- [ ] Parse recall envelopes and nullable scores; default recall to summary projection and hydrate chosen revisions on demand. For memory/knowledge history, keep bare-array decoding when no paging knob is passed and decode `{results,manifest}` when `limit`, `cursor`, or a budget is passed; context history always uses its context budget envelope.
- [ ] Preserve `manifest.next_cursor`, budgets, and query settings across pages; handle cursor validation errors by restarting the read.
- [ ] Wire recall -> use -> `tesseract_touch` for summary-only hits that affected the result; account for `tesseract_get_revision` already reinforcing hydrated hits.
- [ ] Use typed writable memory namespaces; keep tasks in Torque and reasoning in Tesseract.
- [ ] Ensure memory writes carry no knowledge facets; ensure knowledge writes carry a canonical kind, source, and complete pointer.
- [ ] Treat terminal deprecated recall separately from timeline history when deduplicating.
- [ ] Configure one embedder/model intentionally; test lexical fallback and explicit semantic-unavailable behavior.
- [ ] Rename the process/binary and coordinate XDG data migration before restart; delete `CONTEXTD_ROOT` from runtime configuration.
- [ ] Run Nanite's generated assets/contracts after the dependency update and verify no generated file restores a retired name.

For the complete per-tool schema, HTTP peers, scopes, and current examples, use [`../MCP_TOOLS.md`](../MCP_TOOLS.md). For first-run daemon configuration, use [`../QUICKSTART.md`](../QUICKSTART.md).

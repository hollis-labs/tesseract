# Changelog

All notable changes to Tesseract are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor.

Consumers should watch this file for new MCP tools, HTTP routes, store-method additions, and configuration changes. Each release notes the user-visible MCP tool IDs and `/v1/*` routes that landed.

## [Unreleased]

### Added

- **Event is a third revision domain: the append-only narrative log.** An
  agent's reasoning about what it is doing, and a personal log and journal.
  **Not telemetry** — the distinguishing property is that an event carries
  reasoning in prose, which is exactly what a trace discards.

  New MCP tools: `event_write`, `event_list`. New routes:
  `POST /v1/event/write`, `GET /v1/event/log`. `tesseract_get`,
  `tesseract_history`, `tesseract_get_revision`, `tesseract_deprecate` and
  `tesseract_touch` all accept event revisions. New skill:
  `tesseract_skills event`.

  Namespaces use memory's grammar with `event` in the domain-segment position
  — `user/{id}/event/{type}`, plus the project and session scopes — with a new
  closed `event.type` vocabulary (`journal`, `reasoning`) in the type registry.
  Dates deliberately do not belong in the path: `created_at` is indexed and the
  log read filters on it.

- **A linear read path for the log — `event_list` / `GET /v1/event/log`.**
  Chronological in either direction, with a `since`/`until` window and **keyset
  pagination**: `next_cursor` names the position a page stopped at, so entries
  appended while you page do not shift what you have already seen. Recall's
  offset cursor cannot promise that on a log that grows at the head, and
  `ranking=chronological` materializes every matching row before windowing —
  fine for a curated corpus, not for a log.

  The manifest carries no total, deliberately: counting a log means scanning
  it. `has_more` answers the question a total stands in for.

### Changed

- **`tesseract_recall` no longer searches every domain by default.** An
  unqualified recall covers the curated corpus — memory and knowledge — and
  leaves the event log to be asked for by name via `domains: ["event"]`. A
  reasoning log runs an order of magnitude or two above a corpus of deliberate
  captures, so a default that included it would make every unqualified recall a
  log search. Callers that already pass `domains` are unaffected; a caller that
  wants everything now lists the domains it wants.

- **`ranking=activation` over a domain that opts out of activation is now a
  `validation_error`.** Event's rows are never decayed and never reinforced, so
  their stored activation is the insert default forever — an absence of a
  score, not a low one, and one that sits above almost the whole curated
  corpus. Answering would rank the log first, everywhere, permanently. An
  event-only recall that does not name a ranking resolves to `chronological`
  rather than erroring.

### Fixed

- **`tesseract_touch` reports what it moved, not what it asked to move.**
  `touched` counted the reinforcement requests it issued, which was exact for
  as long as every domain participated in activation. Event is the first that
  does not, so the count is now derived from what the UPDATE actually affected,
  and the response carries a third bucket: `not_reinforced` lists revision IDs
  that resolved to a real entry in a non-participating domain. Every distinct
  ID sent still lands in exactly one of `touched`, `not_found` or
  `not_reinforced`. Deferred from CW-20260910-0021, fixed with the domain that
  introduced the case.

- **The type registry serves all domains, and its vocabularies moved to
  config.** `internal/contexttypes` is now `internal/typeregistry`, and the
  three vocabularies that were spelled three different ways are declared in one
  place: `context.record_type` (was `DefaultTypes`), `memory.type` (was
  `memory.DefaultTypeAllowlist`, a Go slice) and `knowledge.facet_kind` (was a
  closed Go map). Closure is now a declared `closed` property rather than an
  accident of which container someone reached for.

  An optional `types.yaml` beside `config.yaml` — the `types-file` reported by
  `tesseract path` — revises them without a release. A vocabulary the file
  names REPLACES the shipped one rather than merging, so an operator can remove
  a value and not only add one. Unknown keys are an error, and a malformed file
  stops the daemon starting rather than falling back to defaults: enforcing a
  vocabulary nobody declared is worse than not starting. See
  [`examples/types.yaml`](examples/types.yaml) and
  [Type vocabularies](docs/OPERATIONS.md#type-vocabularies-typesyaml).

  **This moves enforcement authority from code to config, deliberately.** A bad
  config edit can now open a vocabulary that was closed, where before it took a
  compile. What did not move is enforcement: `memory.WriteRevision` is still
  the persistence boundary that rejects an off-vocabulary `facet_kind`, and the
  namespace parser still rejects an unknown `{type}`.

  `hot_fields` and `schema_ref` are new declarative fields. Declaring a hot
  field does NOT create an index — the loader has no path to schema, asserted
  by a test, so two config files can never produce two schemas from one binary.
  Nothing reads `schema_ref` yet.

- **`wiki_page` is a canonical knowledge kind**, the twelfth. A compiled wiki
  page is compiler output with a template, a provenance chain and a link graph
  — not `doc` (an external reference) and not `note` (a generic note).

- **Activation participation is a domain property.**
  `DomainPolicy.ParticipatesInActivation() bool` gates both halves of activation
  — the decay sweep and the reinforcement `UPDATE` — in SQL, from one predicate
  built off the policy registry. Memory and knowledge both participate, so the
  decay filter selects exactly the rows the unfiltered sweep did (verified: 1694
  of 1694, 0 excluded); no stored activation value changes as a result of this
  release.

  It is one predicate rather than a `DecaysActivation`/`ReinforcesOnRead` pair
  on purpose: splitting it would make the incoherent combination a supported
  configuration instead of the defect it was. Gating in SQL rather than at the
  call sites is the other half — the defect was a call site holding this choice.

- **`domains.DomainPolicy` and `domains.Domain.Policy()` moved to
  `internal/memory`.** The `domains` package is now identity alone — the
  `Domain` type, its constants, `Valid()` and `All()` — which is what lets
  every layer keep importing it cheaply. The policy interface moved to where
  the types it validates already live, and grew the per-domain rules that used
  to sit in `if`/`switch` sites in the memory write and read paths: namespace
  shape, memory-key vocabulary, and the domain/facet contract.

  This is a library-surface removal for anyone importing the `domains` package
  directly. Nothing under `memory` or the root facade changes: `memory.Domain`,
  `memory.DomainMemory` and `memory.DomainKnowledge` are unaffected, and no
  MCP tool, `/v1` route, CLI command or stored value changes. Validation
  behavior at the write boundary is unchanged — the rules moved, they were not
  rewritten.

  The point is the failure mode. A domain added to the registry without its
  behavior used to fall through a `switch` default and silently take another
  domain's rules; audit rows were the worst of it, since a domain with no arm
  emitted `memory.write` and the log recorded a memory write that never
  happened. Adding a domain now fails loudly in three packages until it is
  fully described.

### Removed

- **`promotion_rules` and `retrieval_rank_bias` are gone from type
  declarations**, and both were live on the context surface:

  - Typed views (`/v1/context/typed-view`, `/v1/views/evaluate`,
    `context_typed_view`, `context_view`, `context view`) now rank on status
    weight alone. The per-type multiplier every context type declared is not
    applied, so ordering within a view can differ.
  - `decision/adr` no longer requires `actor=user` to move `draft -> reviewed`.
    That guard was already vacuous — HTTP, MCP and the CLI all default `actor`
    to `"user"` when the caller omits it, so it fired only for a caller that
    volunteered a non-user actor.
  - `GET /v1/context/types` and `context_registry_list` with `kind=types` no
    longer return `promotion_rules` or `retrieval_rank_bias` on an entry, and a
    `types.yaml` declaring either is refused rather than silently ignored.

### Fixed

- **Knowledge participates in activation. It decayed but never reinforced.**
  `tesseract_get` under `domain=knowledge` and `GET /v1/knowledge/current` now
  reinforce activation, `access_count` and `last_accessed_at`, matching their
  memory twins. Knowledge was swept by the decay job — a domain-blind `SELECT`
  over `memory_state` — while both of its deliberate-read call sites reached for
  the non-reinforcing getter. Decay with no reinforcement is a countdown with no
  input.

  Measured before the fix, across 163 knowledge and 1531 memory `memory_state`
  rows: 76.1% of knowledge sat at the 0.05 floor. The number that matters is not
  the floor mass — memory is 83.5% floored, so knowledge was never uniquely
  damaged — but what held the rest up. Of the 39 knowledge rows above the floor,
  **38 had `access_count` 0 and no `last_accessed_at`**: they ranked purely by
  how recently they were written. `ranking=activation` over knowledge, which is
  recall's default with no query, was a chronological ordering wearing an
  activation label.

  This is a regression, not an omission. Before `62843dd` (2026-05-18)
  reinforcement fired from the recall path, which is domain-blind, so knowledge
  was reinforced — for the wrong reason, since it let the ranker's guesses
  reinforce themselves. That commit correctly removed it and rewired
  reinforcement onto the deliberate-read paths, but only memory's. The surviving
  `access_count` values of 800-1040 across knowledge rows, all frozen at
  `last_accessed_at` 2026-05-24, are what that era left behind.
  `tesseract_get_revision` and `tesseract_touch` were already domain-blind and
  did keep reaching knowledge.

  **Existing rows were deliberately not backfilled.** Reads climb them back at
  `activation + 0.1*(2.0 - activation)`, so one deliberate read lifts a floored
  knowledge row to 0.245 — above 127 of the 163 knowledge rows as measured.
  Recomputing from `access_count` was rejected because those counts were
  accumulated by the search-reinforcement path `62843dd` deleted on purpose;
  restoring from them would re-import the echo chamber it removed.

- **Recall across many namespaces no longer fails to parse.** The namespace
  filter rendered one `OR` level per namespace, and SQLite refuses an
  expression tree deeper than `SQLITE_MAX_EXPR_DEPTH` (1000). A caller
  recalling across every registered namespace — the memory review queue in the
  web UI does exactly that — got `SQL logic error: Expression tree is too large
  (maximum depth 1000)` instead of results, on both the metadata and the BM25
  arm. Exact namespaces now collapse into a single `IN (...)` list and the
  remaining prefix terms are `OR`'d as a balanced tree, so depth grows as
  log2(n). Matching semantics are unchanged, and a single-namespace recall
  still emits the same `r.namespace = ?` it always did.

## [0.10.0] — 2026-09-07

Public-preview hardening: the daemon stops being open by default, backup starts
including the data this product exists to hold, restore stops being able to
destroy a store on the way to replacing it, and everything Tesseract owns on
disk becomes owner-only. The CLI also gains `--help` and `--version`, which it
has never had.

Nothing here is a data migration. Existing stores are tightened in place on the
next open; backups taken by earlier versions remain restorable.

### Breaking changes

- **`tesseract serve` binds `127.0.0.1:8089` by default.** It previously
  defaulted to `:8089`, which binds every interface. If you relied on that,
  pass `--addr` explicitly — and note the bare `:8089` form is itself
  non-loopback.
- **Binding a non-loopback address with no token mode is refused.** Configure
  `--managed-auth` or `--static-token`, or state the risk explicitly with the
  new `--allow-unauthenticated-remote`. The error names all three ways out.
- **With a token mode configured, every route requires a valid token — reads
  included.** Only `GET /v1/health/readiness` and, when enabled, `GET
  /v1/metrics` are public. Previously every GET was unauthenticated even under
  `--managed-auth`, including `/v1/auth/tokens/list`, which returned every
  token's id, client, scopes and namespace globs, and seven `/v1/admin/*` reads
  exposing config, storage paths and queue internals.
- **A static token can no longer reach `/v1/admin/settings/apply` or
  `/v1/admin/config/restore`.** `requireScope` and `requireNamespaceAccess`
  returned true when no claims were present, so the `admin` guards on those two
  routes were no-ops under a static token. A validated static token now carries
  explicit claims — the default scope set a managed token receives, plus `"*"`
  globs — which deliberately excludes `admin`. Those routes now require a
  managed token minted with an explicit `admin` scope.
- **JSON request bodies are rejected when they carry unknown fields.** Eight
  handlers — knowledge write, the six memory routes, lookup and synthesis —
  decoded leniently and silently discarded fields they did not recognize. A
  caller sending the flat MCP shape to `POST /v1/knowledge/write` got a
  validation error about missing pointer facets, naming fields it had not used.
  The rejection now names the offending field and, where one exists, its nested
  equivalent. `filters.*` on recall is affected too: `memory.RecallFilters`
  carries no struct tags, so its children are Go field names.
- **Request bodies are capped at 10 MiB** and reported as a 400 naming the limit.
- **`context_plan` takes `max_items` and `max_tokens_estimate`, not
  `budget_items` and `budget_tokens`, and its token default rises 4000 → 8000.**
  `context_pack` with `shape=list` takes `max_tokens_estimate` rather than
  `max_tokens`. The same knob had three names and two defaults, so a planner
  call with defaults silently received half the budget of a direct pack call.
  The retired names are refused rather than ignored, and each message names its
  replacement. Note `budget_tokens` survives on `tesseract_recall` and
  `tesseract_history` as a genuinely different knob — the response
  serialization ceiling — and is unchanged.
- **HTTP promote events are renamed to the MCP spellings**:
  `promote.request.created` → `promote.request`, `promote.request.approved` →
  `promote.approve`. Apply stays `promote` on all surfaces. No data migration
  ships: no row under either spelling has ever been persisted.
- **MCP's promotion audit events point at the stored request record, and
  promotion audit metadata is unified across MCP and CLI.** MCP's
  `promote.request` event previously recorded its subject as the *source*
  record; it now matches HTTP/CLI and records the stored request record,
  consistent with the event-name unification above. Metadata on
  `promote.request`/`promote.approve`/`promote` gains `request_id` (and
  `approval_id`/`record_id` on approve/apply); the `source: "mcp"`
  discriminator is removed on both MCP and CLI. A consumer reading promotion
  audit rows by subject or by the old discriminator field will see different
  data. No data migration ships: existing rows keep their old subject and
  their old `source: "mcp"` field, so a query for promotion history that
  spans the upgrade boundary sees both the old and the new shape in one
  result set.
- **Backups are directories, not a single JSON file** (format v2). Backups
  written by earlier versions are still restorable; only v2 is written. Restore
  is a replacement, not a merge — restoring a v1 snapshot drops the memory and
  knowledge tables that format could not represent.
- **A missing static asset returns 404 instead of `index.html` with 200.**
  Extension-less paths still fall back to `index.html`, so client-side routes
  are unaffected. A missing `.js` served as HTML surfaced to users as
  `Unexpected token '<'` rather than as the missing file it was.
- **OpenTelemetry configuration and signal names use the Hollis taxonomy.**
  `FE_OTEL_REDACT_PROMPTS` becomes `HOLLIS_OTEL_REDACT_PROMPTS`, and `fe.*`
  span names, attributes, and the `fe.http` tracer become their `hollis.*`
  equivalents. Update external deployment configuration and telemetry queries;
  the old redaction variable is no longer read by the upstream library.

### Added

- **`tesseract --help` and `tesseract --version`.** Neither existed. Bare
  `tesseract` previously initialized telemetry, materialized the whole XDG
  layout, opened the database and ran migrations *before* looking at its
  arguments, then failed with a usage line that named the wrong binary and
  listed 15 of 26 subcommands. Help, version, `path`, `plugin`, an unknown
  command and any `<command> --help` now answer without touching disk.
  Subcommand help prints that subcommand's flags from the same flagset the
  parser uses, and exits 0 — `serve --help` previously printed
  `error: flag: help requested` and exited 1.
- **`--allow-unauthenticated-remote`** on `tesseract serve`, for a deliberate
  unauthenticated network bind.
- **`tesseract context backup verify`** checks every file checksum, refuses a
  backup directory carrying files its manifest does not list, runs an integrity
  check, and confirms each record row resolves to a payload matching its stored
  checksum. **`backup export` gains `--config`** to include `config.yaml`.
- **Backups now contain the whole store.** The previous format captured three of
  thirteen tables and seven of the fifteen `records` columns — the entire typed
  record layer, `record_tags`, `namespace_policies`, `embeddings`, and both
  memory and knowledge tables were absent. A backup of a memory engine did not
  include its memory. The snapshot is now taken with SQLite `VACUUM INTO`, which
  is consistent under concurrent writers and covers tables added in future
  schema versions without anyone remembering to list them.
- **`Store.DeferredEmbeddingStatus`** reports whether deferred embedding is live
  or disabled, so an intentional no-op queue is distinguishable from a broken
  one. They were previously identical at runtime.
- **HTTP server timeouts**: `ReadHeaderTimeout` 10s, `ReadTimeout` 60s,
  `IdleTimeout` 120s, and an explicit 1 MiB `MaxHeaderBytes`. None were set.
  `WriteTimeout` is deliberately omitted — synthesis, recall and lookup make
  synchronous provider calls on the request path.
- **Worked request examples on both surfaces in `tesseract_skills`.** The skill
  corpus contained no HTTP examples at all, while MCP and HTTP genuinely differ
  in shape — so it documented only the flat MCP form and misled HTTP callers.
  Ten mutating tools now open with a pointer to their own skill rather than
  ending with a generic footer.
- **The web UI can hold and apply a bearer token.** A token-entry control lets
  an operator paste a capability token, which is stored for the browser
  session and attached to every API call. Without it, the web UI had no way
  to authenticate at all once a token mode is configured — see the auth
  breaking changes above.

### Changed

- **CLI packet assembly now uses the same budget vocabulary and defaults as
  MCP:** `--max-items 50` and `--max-tokens-estimate 8000` on `context packet`,
  `context broker plan`, `context broker fetch`, and `context-pack`. Generated
  broker commands use the canonical names too. The former `--budget-items` /
  `--budget-tokens` spellings remain warning aliases for one release because
  CLI invocations persist in shell history and scripts; `context-pack` likewise
  warns on its former `--limit` / `--max-tokens` spellings. Passing an old and
  new name for the same knob is rejected rather than made argument-order
  dependent.
- **Everything Tesseract owns on disk is owner-only** — `0700` directories,
  `0600` files, covering the database, record payloads, config, and the config
  backup tree. There was previously no `0600` or `0700` anywhere in production
  code. Existing stores are tightened in place on next open. Paths you name —
  a plugins directory, a backup `--out`, a restore source — are never touched.
- **Restore is failure-atomic.** It validates the complete snapshot before
  touching live state, stages beside the live paths, and swaps atomically. It
  previously deleted the record tree before any database work, so a later
  failure left index rows pointing at files that no longer existed; it also
  cascade-deleted every embedding row through a foreign key without restoring
  them, rebuilt heads in a separate transaction after commit, and left a hybrid
  store when restoring over a non-empty one. An interrupted swap is now
  resolved on the next start rather than left ambiguous.
- **The MCP handshake reports the real build version.** It was a hardcoded
  `"0.9.0"` that had already outlived the release it named. `make build` and
  `make install` now stamp the version.
- **`make build` and `make install` no longer require Node.** They compile the
  committed UI bundle with Go alone; `make build-all` / `make install-all` keep
  the chained frontend build.
- The web UI kit resolves from npm rather than a private git ref, so
  `npm install` works without organization access.
- **The public build floor is Go 1.26.6.** Runtime dependencies move to the
  current published Anthropic, MCP, SQLite, gRPC, OpenTelemetry, `x/net`, and
  `x/text` releases; `govulncheck` reports no reachable vulnerabilities in the
  resulting module graph. OpenAI moves to its current `/v3` module, and YAML
  moves from the abandoned `gopkg.in` path to the maintained YAML-org module.
- **Frontend contributors need Node.js 20.19–20.x or 22.12+.** Vite moves to 8.2.2,
  the React plugin to 6.1.1, TypeScript to 7.0.2, and Tiptap to 3.31.3. The
  abandoned `tiptap-markdown` bridge is replaced by Tiptap's official Markdown
  extension. Both the complete and production-only npm audits report zero
  vulnerabilities, and build-only tooling published through the Sysop
  dependency does not enter the browser bundle.
- `internal/webui` serves the SPA through `github.com/hollis-labs/go-webui`
  instead of a local copy, and an unbuilt bundle serves a placeholder rather
  than panicking at startup.

### Fixed

- **A failed embedding enqueue is no longer silent.** `WriteRevision` committed
  the revision and then discarded the enqueue error, so a revision could be
  durably stored and never embedded with nothing recorded anywhere. It also
  reused the caller's context after commit, so a client disconnect between
  commit and enqueue dropped the job. Post-commit work now runs on a detached
  context and failures are counted and logged with identity only, no payload
  content. Recovery remains `POST /v1/admin/queue/backfill`.
- **The CLI emits audit events for the promote request and approve stages.** It
  emitted none, so two of three stages were absent from the audit trail for
  every CLI-initiated promotion.
- **A restore no longer writes outside the record tree.** Record paths from a
  backup were used unsanitized. Manifest paths and record paths are now
  validated at verify time and again while staging.
- **Backup files are owner-only.** They embed `auth_tokens.token_hash` and were
  written world-readable.
- **Memory key rejections now teach the rule.** Keys remain strictly validated
  rather than normalized — folding `-` to `_` would collide with knowledge rows,
  which bypass key validation, and hyphens are already stripped before the
  lexical index sees them. The error now states the charset and segment rules
  and suggests the valid spelling of what was passed. Reads report the same
  diagnosis: a structurally invalid key previously returned "not found" on read
  and a validation error on write.
- **The static token comparison is constant-time.**
- `docs/SPECS/VIEWS.md` advertised `normalized_selector` and `returned_count` on
  `evaluation_meta`; neither has ever existed. `docs/SPECS/API.md` and
  `docs/SPECS/PROMOTION.md` were corrected against the code as well.
- `internal/contextcli/plugin_cmd.go` discarded the error from creating the
  plugins directory.
- **Auth-token revocation no longer reports success when it cannot confirm
  the token was actually revoked.** `RevokeAuthToken`, `RevokeAuthTokenByID`
  and `TrimAuditEvents` discarded a `RowsAffected` error and returned `nil`
  regardless, so a driver-level failure on the revocation `UPDATE` was
  indistinguishable from a normal revoke. `RotateAuthToken` calls
  `RevokeAuthToken` internally, so the same gap could let a rotation report
  success while the old token stayed live. Found by golangci-lint's `nilerr`
  check during the public-preview qualification pass (`CW-20260904-0083`).
- **A graceful shutdown no longer logs a false telemetry failure and no longer
  drops telemetry recorded near shutdown.** OTel shutdown was passed the same
  context the shutdown signal had already canceled, so exporters got no window
  to flush; every normal `Ctrl-C` exit logged `warning: OTel shutdown failed:
  context canceled` regardless of whether anything was actually wrong.
  Shutdown now runs against a fresh context bounded to 5 seconds.
- **`context_write`'s `record_type` and `context_plan`/`context_broker`'s
  `boot_project` item floor are no longer silently ignored.** MCP's
  `context_write` accepted a `record_type` argument but never passed it to the
  store, so every write landed untyped no matter what was requested — its
  documented default of `state` was fictional; writes with no `record_type`
  now land untyped, matching what the tool actually did. Separately,
  `buildContextPlan` raised `max_items` to at least 100 for `boot_project`
  intents but the raised value was computed and discarded rather than
  returned, so the response and the fetch it drove still used the caller's
  original, smaller budget.

## [0.9.0] — 2026-09-04

The memory, knowledge and context domains get one read surface instead of three.
Recall becomes bounded, projectable and pageable; the lexical arm is reachable
directly; the knowledge `kind` vocabulary closes; and activation gains both the
deliberate-read input it was always missing and a decay job that stops erasing
it within the day.

Consumers pinning `v0.8.x` should read the breaking-changes section in full.
**Ten read tools are retired with no aliases**, and **the default recall response
shape changed** — the second affects every caller whether or not it adopts a
single new knob. The complete consumer runbook is
[`docs/guides/tesseract-adoption-and-v0.9-migration.md`](docs/guides/tesseract-adoption-and-v0.9-migration.md).

### Breaking changes

- **`memory_recall` and `tesseract_lookup` now project results by default.**
  `payload_mode` takes `keys`, `summary`, or `full`, and the default comes from
  config (`read.payload_mode`), which itself defaults to **`summary`** — status,
  tags, confidence and `payload.summary`. Previously every hit serialized in full,
  including `payload.body` and the whole `State` struct; on the reference corpus a
  default `limit=30` recall put 69,996 bytes on the wire, 40,449 of it payload
  text. **Callers needing the prior byte-identical response must pass
  `payload_mode=full`**, which skips projection entirely rather than
  round-tripping through it. `keys` returns identity rows and doubles as the
  browse/enumerate affordance.
  Note both tools are also renamed by the read collapse below — a v0.8.x caller
  of `memory_recall` is affected by that entry as well as this one.
- **Recall results are snake_case.** `memory.RecallResult` carried no JSON tags, so
  both tools served `{"Revision":…,"Score":…,"State":…}` — PascalCase, against
  snake_case everywhere else in the API. Now `{revision, score, state}`.
- **`score` is absent under `ranking=chronological`.** It previously carried a raw
  `CreatedAt.UnixNano()`, which is not a score; ordering there is already carried
  by array order plus `revision.created_at`. The chronological sort now reads the
  timestamp directly instead of smuggling it through `score`.
- **Recall and lookup return a manifest alongside results** — `{results_total,
  results_returned, bytes_returned, tokens_estimate, truncated,
  truncation_reason, next_cursor}`. Every field is emitted unconditionally:
  `truncated:false` is how a caller learns its result set is complete and
  `next_cursor:null` is how it learns there is nothing left, so neither is
  `omitempty`. Memory/knowledge `tesseract_history` preserves its bare-array
  response when no paging knob is passed and returns `{results, manifest}` only
  when the caller passes `limit`, `cursor`, `budget_bytes`, or `budget_tokens`;
  context history always returns its existing context budget envelope and ignores
  the cursor/budget knobs.
- **`payload_mode=full` caps `limit` at 100**, where `keys` and `summary` keep 500.
  The clamp is never silent — it reports `truncation_reason: payload_mode_limit_cap`
  and issues a cursor, so rows past it are reached by paging rather than by raising
  `limit`.
- **Result ordering is now a total order.** `fetchCandidates` issues no `ORDER BY`,
  so tied rows previously came back in whatever order SQLite produced, and an
  offset over a partial order silently skips one row and repeats another.
  `revision_id` now breaks every tie, matching what relevance recall always did.
  Callers depending on the previous (undefined) order of tied rows will see it
  change.
- **`knowledge_write` rejects an off-vocabulary `kind`.** The write path previously
  enforced only `kind != ""`, so the vocabulary drifted. The set is now closed at
  eleven values and the error names all of them, so a caller that guessed wrong
  does not have to read the source. Off-vocabulary rows were normalized first by a
  reviewed plan-then-apply migration, so enforcement cannot reject a value the
  migration left behind.
- **Knowledge facets are now enforced at the shared persistence boundary.** Every
  supported Go, HTTP, MCP, promotion, and wrapper write path rejects memory-domain
  revisions carrying knowledge facets and knowledge-domain revisions missing a
  canonical kind, source, pointer scheme, or pointer locator. Callers that used the
  lower-level memory store or root facade to persist off-contract combinations now
  receive the canonical validation error instead.
- **Go API: `ErrSimilarityUnavailable` is removed.** Use
  `memory.ErrEmbedderUnavailable`; `tesseract.ErrEmbedderUnavailable` is the same
  sentinel for root-package operations, so `errors.Is` works across both facades.
- **Go API: `memory.RecallResult.Score` is `*float64`** (was `float64`), as is
  `synthesisSource.Score`. Cosine similarity is legitimately 0 or negative, and a
  value type with `omitempty` drops those real scores.
- **Ten memory/knowledge/context read tools were retired and replaced by five
  cross-domain ones. No aliases.** `context_head`, `memory_get` and
  `knowledge_get` → **`tesseract_get(domain, namespace, key)`**;
  `context_history`, `memory_history` and `knowledge_history` →
  **`tesseract_history`**; `memory_get_revision` → **`tesseract_get_revision`**;
  `memory_deprecate` → **`tesseract_deprecate`**; `memory_recall` and
  `tesseract_lookup` → **`tesseract_recall(domains[], …)`**. The tool surface
  goes from 43 to 38. `tesseract_recall` is a superset of both tools it replaces:
  no argument was lost, and it gains `domains`, `facet_kinds`, `facet_sources`
  and `pointer_health`.
- **`domain` is required on `tesseract_get` and `tesseract_history`, with no
  default.** Inferring it from the namespace would answer a different question
  silently. An unknown domain is a `validation_error` naming the allowed set; a
  domain in the vocabulary with no store wired is a new `domain_unavailable`,
  kept distinct from `not_found` on purpose.
- **`domain` is a filter, not a hint — and cross-domain reads now refuse.**
  `tesseract_get`/`tesseract_history` with `domain=memory` over a knowledge
  namespace previously returned the knowledge revision **and reinforced it**; the
  same was true of `GET /v1/memory/current` and `GET /v1/memory/history`, which
  are fixed alongside. Those calls now return `not_found`. The prior behavior
  contradicted the contract already stated at `internal/knowledge/store.go:124`
  — *"callers should not see cross-domain reads"* — which one store implemented
  and the other did not. A caller relying on it was relying on a bug the sibling
  arm explicitly disclaimed.
- **The keyed reads take `key` where the retired tools took `memory_key`.** This
  is MCP-side only: the HTTP peers still accept `?memory_key=`, the revision JSON
  still carries `memory_key`, and `memory_write` is unchanged. `knowledge_write`
  and `context_write` already took `key`.
- **Callers of the retired `memory_recall` now receive a `facets` key** in the
  response envelope, which `tesseract_lookup` already returned.
- **Empty `namespace` or `key` is a `validation_error`.** The retired tools
  reached the store and returned `not_found`. This moves toward the HTTP peers,
  which already validated.
- **The budget truncation hint changed text.** `tesseract_history` with
  `domain=context` emits *"Use `tesseract_get` with domain, namespace and key…"*
  where `context_history` emitted *"Use `context_head` with namespace and key…"*.
  It appears only when results are truncated, but it is a response-body change.
- **Authorization on `tesseract_get` depends on the `domain` argument.**
  `domain=context` requires no token, as `context_head` did; `memory` and
  `knowledge` check `memory:read`. The domain is resolved before the scope check
  and the context arm reads physically separate tables, so the boundary is
  unchanged per-arm — but a client that allowlists by *tool name* loses
  granularity, since a policy permitting unauthenticated `context_head` must now
  permit `tesseract_get`.
- **Seventeen context-domain tools were merged into seven behind selector knobs.**
  `context_view`+`views_evaluate` → **`context_view`** (`full_evaluation`);
  `context_pack`+`context_packet` → **`context_pack`** (`shape`);
  `context_broker_plan`+`context_broker_fetch` → **`context_plan`** (`execute`);
  `context_bulk_ingest`+`context_chunked_ingest` → **`context_ingest`** (`mode`);
  `context_status_promote`+`context_status_deprecate` → **`context_status_set`**
  (`status`); `context_types_list`+`context_views_list`+`context_namespaces_list`+
  `context_namespace_show` → **`context_registry_list`** (`kind`, `name`);
  `context_promote_request`/`_approve`/`_apply` → **`context_promote`** (`stage`).
  Every selector routes to different code, and **each arm refuses the other arm's
  knobs** rather than ignoring them.
- **Four more tools were renamed onto one verb vocabulary**: `context_audit` →
  `context_audit_list`, `context_broker` → `context_plan`, `context_promote_list` →
  `context_promotion_list`, `context_session_snapshot` → `context_session_write`.
  Tool names now follow `<prefix>_[subject_]<verb>`, with `tesseract_*` for
  cross-domain tools and `<domain>_*` for domain-specific ones, so a prefix carries
  information rather than decoration. The rule and its two exemptions are published
  in `docs/MCP_TOOLS.md`.
- **`payload_mode` is retired on the packet surfaces; only `full` is still accepted**,
  and `head_only` is replaced by **`payload_max_bytes`**. `head_only` did not
  truncate — it cut the payload mid-JSON, which made the enclosing response fail to
  serialize and returned an **empty result reported as success**. Under a binding
  `payload_max_bytes` an item now carries **no `payload` key at all**; it carries
  `payload_head` (a JSON string), `payload_truncated` and `payload_bytes`. **An
  absent `payload` means capped, never empty.** Applies on MCP, `POST
  /v1/context/packet`, and the CLI's `-payload-mode` / new `-payload-max-bytes`.
- **Retired argument names are now refused rather than ignored.**
  `context_status_set` rejects `to_status`, and `context_registry_list` rejects
  `namespace` (use `name`). Previously these reached the handler and were dropped —
  `to_status="canonical"` silently advanced the status by one step and reported
  success. A silent behavior change is worse than an error, so both are now
  `validation_error` with the replacement named.
- **Several previously-silent argument mistakes are now `validation_error`**: a knob
  belonging to the other arm of a merged tool; `include_payload` without
  `full_evaluation`; `selector` together with `namespaces`; an absent or
  unrecognized `stage`, `kind`, `shape` or `mode`; a negative `payload_max_bytes`.
- **`context_status_promote(to_status="deprecated")` has no exact equivalent.**
  `draft → deprecated` was a legal *promotion* and took the promotion path, emitting
  a `status_promote` audit event. `context_status_set(status="deprecated")` takes the
  deprecation path. Sixteen of the seventeen merged pairs are exactly equivalent;
  this is the one value on the one pair that is not.

#### The daemon binary is renamed — coordinate before upgrading

This is separated because its prerequisite is different in kind from everything
above. The changes above take effect when a client updates its calls; **this one
takes effect when the daemon is restarted, and anything launching it as a managed
process must be updated first.**

- **The binary is `tesseract`.** `cmd/contextd/` → `cmd/tesseract/`, and all nine
  subcommands — `path`, `serve`, `mcp`, `plugin`, `backfill-embeddings`,
  `migrate-namespaces`, `migrate-knowledge-kinds`, `verify-pointers`, `context` —
  are reachable only under the new name. `make build` emits `./tesseract`;
  `scripts/contextd-{smoke,e2e-local}.sh` are renamed; error and migration-plan
  strings print the new name; `examples/mcp.json` names it.
- **`go install …/cmd/contextd@latest` no longer resolves.** Use
  `…/cmd/tesseract@latest`, which installs to `~/go/bin/tesseract`.
- **Process supervisors must be updated before the restart.** Anything invoking
  `contextd serve` or `contextd mcp` — launchd, a managed-process runner, a shell
  alias — breaks at restart, not at upgrade. This is the coordination step, and it
  is the reason this section exists.
- **`CONTEXTD_ROOT` is removed and now silently ignored.** The failure mode is not
  that a variable stops working: callers used it to point at an **isolated
  throwaway root**, and a process still setting it now resolves against the real
  XDG layout instead — the live store. There is no warning. Use the four
  `$XDG_*_HOME` variables, or `TESSERACT_DB_PATH` / `TESSERACT_WORKSPACE`.
- **`make contract-cli-list` and `contract-cli-run` take `CONTRACT_ROOT`**, not the
  retired variable, and their default moved from `.tesseract/tmp/contextd` to
  `.tesseract/tmp/contract`. Same silent-redirect shape as above, on the
  contract-test targets rather than the daemon.
- **`internal/mcpadapter/skills/knowledge.md` changed.** Skills are embedded in the
  binary and served to MCP clients through `tesseract_skills`, so this is
  consumer-visible text rather than an internal comment.
- **`/contextd` is no longer gitignored**, so a stale pre-rename binary at the repo
  root becomes visible to `git status` — and fails the new name guard until it is
  removed. `rm contextd` clears it. The same applies to `.tesseract/tmp/contextd/`.
- **Thirteen documentation files** name the new binary, and the `.mcp.json` samples
  in `CONTEXT-FOR-PROJECTS.md` and `SPECS/MCP.md` no longer pin a root that is now
  ignored.
### Added

- **`tesseract_touch`** — explicit activation reinforcement. An agent reports which
  recalled memories actually shaped its turn, **after reasoning** rather than at
  recall time; those receive the deliberate-read bump. This is a tool rather than a
  flag on recall precisely because of that timing: a `touch: true` knob would
  reinforce the ranker's own guesses, which recall correctly refuses to do. Recall
  does not reinforce a result merely for returning it. Deliberate
  `tesseract_get`/`tesseract_get_revision` calls reinforce once; touch supplies the
  use signal for projected hits that were not fetched, or an intentional second
  reinforcement for a fetched hit. Touch only what genuinely shaped the turn —
  under-reporting is fine, over-reporting is worse than silence, because it teaches
  the ranking that noise is signal. HTTP peer `POST /v1/memory/touch`.
- **`search_mode` on `ranking=relevance`** — `hybrid` (default, unchanged: both arms
  fused by RRF and weighted by the activation-style modifiers), `lexical` (the BM25
  arm alone, in `bm25()` order, modifiers not applied), `semantic` (the cosine arm
  alone). BM25 was fully built and indexed but reachable only through fusion, which
  dilutes exactly the cases an agent most needs to look up exactly — a ticket ID, a
  symbol, a namespace, an error string. Measured on the reference corpus for a
  query appearing in exactly one revision: hybrid ranks it 3rd of 12, lexical
  returns it alone at 1st.
- **`budget_bytes` / `budget_tokens`, `cursor` / `next_cursor`, and `limit` on both
  history tools.** A 500-result recall under `payload_mode=full` serializes to
  1,559,859 bytes on the reference corpus — roughly 390K tokens, which no caller
  can receive. A cursor carries a fingerprint of everything determining the
  ordering, so resuming after a changed ranking, namespace set, `revision_scope`,
  query, filter or reranker is a validation error rather than a plausible-looking
  wrong page.
- **`estimate_only`** — returns the envelope describing a read without the rows, so
  an agent can size a result before spending its context window on it. The numbers
  are an identity rather than a prediction: the query runs and the rows are
  fetched, projected and measured exactly as they would be otherwise, and only
  their serialization is skipped. `results` is omitted rather than emitted empty,
  because an empty array is indistinguishable from "nothing matched" — the question
  a pre-flight is asked.
- **`similarity_min`** — an inclusive floor on cosine similarity between query and
  result. Honored only where cosine is the ordering signal (`ranking=similarity`,
  and `ranking=relevance` with `search_mode=semantic`) and **refused elsewhere
  rather than ignored**, because silently dropping a knob hands back a
  differently-filtered set under the name the caller asked for. Distinct from
  `confidence_min`, which filters on the confidence the memory's author recorded.
- **Pointer verification and a staleness surface for knowledge** — schema 13 adds an
  append-only `pointer_verifications` table with outcomes `resolved`,
  `unresolvable` and `unverifiable`, plus a `verify-pointers` command with
  plan-then-apply semantics.
- **The `migrate-knowledge-kinds` subcommand** — plan-then-apply normalization of off-vocabulary
  knowledge `kind` values, with `--expect-rows` and `--expect-digest` binding the
  apply to the reviewed plan (exit 3 on approval mismatch, exit 2 on an unmapped
  kind).
- **A test guard against tool-name drift** — shipped prose naming an unregistered
  tool now fails `go test ./tests/parity/`. It walks `docs/` and the embedded
  skills recursively; `CHANGELOG.md` is deliberately outside its scope, because
  release notes legitimately name superseded identifiers.
- **The public memory facade now exposes every documented consumer contract** used
  for bounded recall and relevance ranking: `RankingRelevance`, `SearchMode` and
  its vocabulary, paging budgets/manifests/requests/results, payload truncation and
  limit constants, cursor errors, pointer-health result/status types and vocabulary,
  touch results and request cap, and the supported reranker types and constructor.
  External consumers no longer need string casts or internal imports.
- **A versioned adoption and migration guide** covering embedded Go lifecycle,
  MCP replacements, projection/paging, terminal deprecation, XDG cutover, and a
  Nanite-ready `v0.9.0` checklist, with a compile-checked external Go example.

### Fixed

- **Explicit current-scope deprecated recall no longer reports a false empty
  result.** When `statuses` explicitly contains `deprecated`, current scope admits
  terminal deprecated lineage leaves (no incoming `supersedes` edge) while still
  excluding ordinary superseded history. Omitted statuses remain unchanged and
  hide deprecated rows; timeline remains the full status-filtered history. The
  rule is shared by metadata, semantic, lexical, hybrid, MCP, and both HTTP recall
  paths, with an indexed lineage anti-lookup.
- **The production MCP adapter now receives the configured embedding provider and
  model.** It shares the memory subsystem's provider instance, making
  `context_embed`, semantic `context_search`, and `context_rag_query` reachable
  when configured. Missing credentials or an unsupported provider leave lexical
  recall available and produce explicit unavailable errors on semantic-only work.
- **Early server exits now stop background memory workers before closing their
  databases.** The memory subsystem owns a cancellation context and shutdown
  barrier for its queue and decay workers, closing the queue database only after
  both have joined; this removes the intermittent temporary-directory cleanup
  failure under managed-auth startup errors.
- **Serialization failures are no longer reported as empty success or partial
  success.** MCP tool result marshaling returns a structured internal error, and
  HTTP JSON responses buffer serialization before committing a status so failures
  return `500 serialization_failed`. Guards cover direct and composite
  `json.RawMessage` destinations and audited persistence/ID/glob error paths now
  propagate their failures.
- **Memory timestamps now preserve chronological TEXT ordering.** Schema 16
  atomically normalizes every memory-owned timestamp to fixed-width UTC
  nanoseconds, preserving the existing indexes used by history, deprecation,
  recall time filters, TTL expiry, and pointer-health queries. This fixes the
  RFC3339Nano prefix inversion that also made `verify-pointers --apply`
  intermittently reject its own committed observations; that post-apply range
  count now uses the same canonical encoding and a dedicated index. Migration,
  ordering, index-plan, and race/non-race regressions cover the affected paths.

- **`memory_key` is in the lexical index, and outranks a mention of it
  (schema 15).** `memory_revisions_fts` indexed `payload.summary`,
  `payload.body` and tags only, so an exact-key `search_mode=lexical` search
  returned every entry that *cited* the key and never the entry that *was* it.
  Since entries here cite each other by key in `[[wikilink]]` form, that
  inverted every citation lookup; where nobody cited the key it returned
  `results_total: 0`, which reads as "no such record". Four live canonical
  entries were confirmed unreachable by their own keys. `memory_key` is now
  indexed, and carries a `bm25()` column weight of 10 so the key's owner ranks
  above prose hits — measured on the live corpus, that puts the owner first for
  1,368 of 1,383 current keys, against 1,103 unweighted. The weight scales a
  per-column term frequency, so recall for queries that touch no `memory_key`
  is unchanged. Applies to the hybrid arm too, which shares the expression.
  **No reindex step is needed**: schema 15 rebuilds the FTS table from
  `memory_revisions` on first open, covering existing entries.
  `namespace` is deliberately still not indexed — it is already an exact filter
  via the required `namespaces` argument. The `search_mode` description and the
  recall skill claimed `lexical` was the tool for "a namespace"; both now say
  `memory_key` and state what is and is not indexed.
- **The FTS index no longer goes stale when a key or namespace is renamed.**
  Schema 12 shipped INSERT and DELETE triggers only, on the reasoning that
  indexed columns never mutate. That held for the payload columns but not for
  `memory_key` and tags, which `ApplyMigration` rewrites in place during a
  namespace rename — so a renamed entry stayed searchable under its old tags,
  and would have under its old key. A guarded `AFTER UPDATE` trigger
  (`memory_revisions_fts_au`) now reindexes the row, and its `WHEN` clause skips
  the status and embedding writes that touch no indexed column.
- **Activation decay no longer compounds across passes.** The decay job measured
  elapsed time from a baseline it never advanced, so every pass re-decayed an
  already-decayed value over a longer interval. A never-read memory lost two
  thirds of its activation in eight hours against a nominal fourteen-day
  half-life — roughly sixty times the intended rate, which meant no
  activation-ranked recall carried information beyond "read in the last few
  hours." A new `memory_state.last_decayed_at` column (schema 14) records how
  current the stored value is, and every writer of `activation` advances it.
  Reads still stamp `last_accessed_at` only, so the deliberate-read signal stays
  distinct from decay bookkeeping. Existing rows are stamped rather than
  backfilled: the prior levels are unrecoverable, and replaying each row's whole
  lifetime in one pass would finish the erasure instead of undoing it.
- **FTS5 queries beginning or ending with an operator keyword no longer fail the
  whole recall.** `AND memory`, `memory AND` and `NOT NULL constraint` were
  `fts5: syntax error` on the hybrid path; they are now quoted into ordinary terms
  and return rows. Infix operators keep their operator reading and are unchanged —
  verified by a 3,030-query differential sweep.
- **A `similarity_min` boundary claim that described a `>` floor where the code
  implements `>=`**, which survived in a guard's failure message — where it would
  have been read while forming a hypothesis about what broke.
- **Two incorrect claims about activation ranking** in `recall-and-ranking` and the
  `memory` skill, which stated that a heavily-touched memory cannot outrank a
  freshly written one. `ranking=activation` multiplies the stored activation by
  status, origin, confidence and recency weights, so that never held.
- **A data race in the pointer-resolver test stub** that could take down the whole
  `internal/memory` package under `go test`.
- **A configured budget leaking into history's response shape.**
- **A fractional `limit` now rejected the way both HTTP peers already did.**

## [0.8.0] — 2026-08-24

Typed memory namespaces, an admin operations surface, and the XDG on-disk
cutover. Consumers pinning `v0.7.x` should read the breaking-changes section
before bumping: namespace strings, two MCP tool IDs, one HTTP route, and the
default on-disk layout all changed.

### Breaking changes

- **Memory namespaces are now typed and fixed-depth.** `ParseNamespace` accepts
  exactly three shapes — `user/{id}/memory/{type}`,
  `user/{id}/project/{project_id}/memory/{type}`, and
  `user/{id}/session/{session_id}/memory/{type}`. The segment-count rule went
  from `len(parts) < 3 || len(parts) > 5` to `len(parts) != 4 && len(parts) != 6`,
  so the legacy flat `user/{id}/memory` is rejected on write with
  `wrong segment count (want 4 or 6, got N)`. `{type}` is validated against an
  allowlist (`decisions`, `feedback`, `followups`, `learnings`, `limitations`,
  `notes`, `outcomes`, `references`). **Recall is more permissive than write:**
  the flat form is still accepted there as a *prefix*, spanning every typed
  sub-namespace under that scope. Callers that construct namespace strings must
  add a type segment; callers that only recall may not need to change.
- **Two MCP tool IDs and one HTTP route were renamed onto the Tesseract
  vocabulary.** The unified lookup tool is now `tesseract_lookup`, the skills
  meta-tool is `tesseract_skills`, and the lookup route is
  `/v1/tesseract/lookup`. The other 40 tool IDs are unchanged. Clients pinned
  before v0.8.0 must update these three identifiers; the
  [v0.8.0 release notes](https://github.com/hollis-labs/tesseract/releases/tag/v0.8.0)
  carry the prior names.
- **On-disk layout moved to XDG roots** via `go-apppaths`. Data, state, cache and
  config now resolve independently instead of nesting under one base directory.
  `CONTEXTD_ROOT` is retired and kept alive by a **one-release deprecation shim**
  that maps it onto all four `$XDG_*_HOME` vars; it is scheduled for removal in
  the release after this one. Migrate to `$XDG_*_HOME`, or to `TESSERACT_DB_PATH`
  / `TESSERACT_WORKSPACE` for the common cases. Run `contextd path` to see where
  a given environment actually resolves.

### Added

- **Admin operations surface** — 15 new routes: `/v1/admin/setup`,
  `/v1/admin/settings`, `/v1/admin/settings/preview`, `/v1/admin/settings/apply`,
  `/v1/admin/storage`, `/v1/admin/queue`, `/v1/admin/queue/failures`,
  `/v1/admin/queue/retry-failed`, `/v1/admin/queue/backfill`,
  `/v1/admin/namespaces/preview`, `/v1/admin/namespaces/update`,
  `/v1/admin/namespaces/history`, `/v1/admin/config/backup`,
  `/v1/admin/config/backups`, `/v1/admin/config/restore` — with a matching
  frontend admin dashboard.
- **`contextd migrate-namespaces`** — one-shot cutover for the typed-namespace
  grammar. Dry-run by default; `--apply` commits. Flags: `--db`, `--apply`,
  `--json`, `--project-threshold`. Reports collisions and derives project tags
  from the corpus rather than a hardcoded list.
- **`contextd path`** — prints the resolved on-disk layout (go-apppaths roots,
  active workspace, main DB, `config.yaml`, `records/`, `queue.db`). Honors every
  override the daemon sees and never creates directories.
- **Environment overrides** `TESSERACT_DB_PATH`, `TESSERACT_WORKSPACE`,
  `TESSERACT_PLUGINS_DIR`, `TESSERACT_MEMORY_DECAY_INTERVAL`.
- Type-aware promotion and namespace prefix-matching in recall, backing the
  typed grammar above.

### Changed

- **Activation reinforcement moved from search to deliberate reads.** `memory_recall` (every ranking mode, including relevance) no longer bumps `activation`, `access_count`, or `last_accessed_at` — being returned by a search is the ranker's guess, not a signal of importance, and reinforcing on recall created a self-reinforcing echo chamber. Reinforcement now fires only on the deliberate-read paths: `memory_get` / `GET /v1/memory/current` and `memory_get_revision` / `GET /v1/memory/revisions/{id}`. Activation **decay** is unchanged. New store methods `Store.GetCurrentReinforced` and `Store.GetRevisionByIDReinforced` back the reinforcing reads; the plain `GetCurrent` / `GetRevisionByID` stay non-reinforcing for internal callers (promotion, embedding, knowledge lookups). Consumers that depended on "recall reinforces activation" (notably Nanite) should flag this — hot-memory activation trails will now reflect deliberate reads only.

- Frontend app shell adopted the `@hollis-labs/sysop-ui` kit (bumped to v0.6.2).
- Repository prepared for the OSS beta: README rewritten, documentation
  restructured under `docs/`, and stale internal task trees removed.

### Fixed

- **SQLite DSN pragmas were written in the wrong driver's dialect and never took
  effect.** Connection strings used the mattn/go-sqlite3 spelling
  (`_busy_timeout=`, `_fk=`) while the driver is `modernc.org/sqlite`, which
  configures pragmas via `_pragma=name(value)` and silently ignores parameters it
  does not recognize. Measured, the mattn form was equivalent to passing no
  parameters at all: `busy_timeout` was 0 and `foreign_keys` was OFF on every
  connection. So the declared `REFERENCES` clauses on `heads`, `embeddings` and
  `memory_revisions` were inert, and concurrent writers failed immediately with
  `SQLITE_BUSY` instead of waiting. Both pragmas are now applied on every
  connection, and DSN construction is centralized in `internal/sqlitedsn` so the
  dialect is stated once. Enabling foreign keys was checked against a copy of a
  live store first: zero `foreign_key_check` violations.
- `memory_write` and related MCP tools now accept a native JSON array for the
  `tags` argument, not only a JSON-encoded string.

## [0.7.0] — 2026-05-15

### Changed

- Finalized the public Tesseract naming pass. The Go module path is `github.com/hollis-labs/tesseract`, and the default local state root is `~/.tesseract`.
- Pinned `go.mod` dependency references to published versions so the module is consumable from GitHub: `go-otel v0.1.0`, `go-queue v0.1.0`, `go-embed-contracts v0.1.1`, `go-modelsdev v0.2.0`, and the renamed `go-mcp v0.1.0` (previously the dead `mcp-helpers` module path). These were placeholder `v0.0.0` requires resolvable only via local `replace` directives.

## [0.6.0] — 2026-05-09

Path B clean-break migration off `github.com/hollis-labs/go-providers`. All
LLM contract types relocate to dedicated repos; Tesseract ships its own
SDK-backed wrappers for the embedding and synthesis paths.

### Breaking changes

- **Dropped `github.com/hollis-labs/go-providers` dependency entirely.**
  All `provider.*` references migrated to the canonical contract repos:
  - `provider.Embedder` / `provider.EmbeddingResult` → `embedcontracts.{Embedder,EmbeddingResult}`
    (`github.com/hollis-labs/go-embed-contracts`)
  - `provider.ChatRequest` / `provider.ChatMessage` → `llmtypes.{ChatRequest,ChatMessage}`
    (`github.com/hollis-labs/go-llm-types`)
  - `provider.Provider` → `llmcontracts.Provider`
    (`github.com/hollis-labs/go-llm-contracts`)
- **`tesseract.WithEmbedder` parameter type** changed from `provider.Embedder`
  to `embedcontracts.Embedder`. Callers must update imports + types.
- **`Server.SynthesisProvider` type** changed from `provider.Provider` to
  `llmcontracts.Provider`. Callers wiring a custom synthesis provider must
  switch to the new contract.
- **Synthesis providers narrowed to `openai` + `anthropic`.** The legacy
  `gemini` and `mistral` synthesis branches in `cmd/contextd/main.go` were
  removed — they were configured but never used in production. Callers
  needing other vendors should inject a custom `llmcontracts.Provider`
  implementation by mutating `srv.SynthesisProvider` before serve.

### Added

- **`internal/llm/openai`** — openai-go (v1.12.0) backed wrapper that
  implements both `embedcontracts.Embedder` and `llmcontracts.Provider`.
  Powers `createEmbedder` (used by `setupMemorySubsystem` + `backfill`) and
  the `synthesis.provider=openai` branch.
- **`internal/llm/anthropic`** — anthropic-sdk-go (v1.41.0) backed wrapper
  that implements `llmcontracts.Provider`. Powers the
  `synthesis.provider=anthropic` branch.

### Notes

- `Complete` is fully implemented in both wrappers (the synthesis route
  is non-streaming). `StreamChat` returns a "not implemented" error —
  Tesseract's only chat consumer (`/v1/synthesis/ask`) is non-streaming.
  If a future caller needs streaming, fill in `StreamChat` per provider or
  import an SDK-backed wrapper from a sibling module.
- `Capabilities()` returns conservative fixed defaults; per-model tuning
  is left to callers that need it.

### Background

go-providers v0.10.0 narrowed scope to CLI/PTY/subprocess-only; the
embedding contract relocated to `go-embed-contracts`, the LLM contracts to
`go-llm-contracts`, and the data types to `go-llm-types`. Per Path B
(clean break, no transitional aliases), this release migrates all
references in one pass.

Sprint: SP-20260508-0001
Tesseract decision: `decisions.nanite.architecture.go_llm_contracts_split`

## [0.5.3] — 2026-04-26

LLM-backed answer synthesis for the Search & Research surface, plus
operator forms for memory write / promote / deprecate, audit timeline
filters (actor + since/until), and an API naming-convention pass that
consolidates `invalid_request` → `validation_error`.

### Added

- **HTTP route: `POST /v1/synthesis/ask`** — fans curated memory + knowledge
  recall into an LLM completion via go-providers, returns the synthesised
  answer + numbered cited sources + per-call telemetry (provider, model,
  latency, tokens when available, cost via go-modelsdev catalog lookup).
  Returns 503 `synthesis_unavailable` when no provider is configured.
- **Config: `synthesis.*`** in `~/.tesseract/config.yaml` —
  `provider`, `model`, `system_prompt`, `max_tokens`, `temperature`.
  Provider names: `openai`, `anthropic`, `gemini`, `mistral`. API key
  is read from the conventional env var (e.g. `ANTHROPIC_API_KEY`).
- **Dependency:** `go-modelsdev` (replace → `../go-modelsdev`) — used for
  model pricing lookups in the synthesis cost report.
- **Memory write / promote / deprecate UI** (`MemoryWritePage`) — a single
  operator surface with three tabs over the existing `/v1/memory/*` routes.
  Symmetric to the context-domain WriteRecord + Promote pages.
- **Search & Research: Synthesis tab** — third tab alongside Answer / Sources.
  Click "Synthesize" to call the new LLM endpoint; result cached on the
  thread entry so revisiting the question doesn't re-spend tokens. Telemetry
  footer shows provider/model/latency/tokens/cost.

### Changed

- **Audit filter: `actor` + `since` + `until`** added to `/v1/context/audit`.
  Backed by SQL substring (`actor LIKE %?%`) and lexical comparison on the
  TEXT `created_at` column. Frontend AuditPage now uses server-side actor
  filter (was client-side workaround) and gains datetime-local pickers for
  `since` / `until`.
- **Error code consolidation:** all `invalid_request` HTTP error codes
  (33 callsites) renamed to `validation_error` to match the dominant
  pattern (105 callsites). No tests depended on the old literal.

## [0.5.2] — 2026-04-26

Frontend operator surfaces: Memory & Knowledge browser, Search & Research
(v1 curated), Memory / Knowledge detail page, Recall improvements
(facet drill-down, URL state, clickable tags, recent namespaces),
Audit Timeline overhaul (memory/knowledge event types, click-through to
detail page, cursor-paginated load-more, day grouping, client-side actor
filter). Plus the foundation: `GET /v1/namespaces/list` backend endpoint,
knowledge `key` → `memory_key` parameter normalisation, and a tsc baseline
fix that re-enables `make frontend` / `npm run build`.

### Changed

- **Breaking (local-only):** Knowledge HTTP + MCP read endpoints renamed
  their key parameter from `key` to `memory_key`, matching the existing
  memory endpoints so both stores expose a single normalized identifier.
  Affected:
  - `GET /v1/knowledge/current` — query param `key` → `memory_key`
  - `GET /v1/knowledge/history` — query param `key` → `memory_key`
  - MCP tool `knowledge_get` — required arg `key` → `memory_key`
  - MCP tool `knowledge_history` — required arg `key` → `memory_key`
  Error messages updated. No other callers in the portfolio depend on
  the old name; safe to break pre-public-release.
- **Audit & Ops page** — restructured into a timeline. Event-type filter
  now exposes the v0.5.1 memory / knowledge events grouped by domain
  (memory, knowledge, context, promote-MCP, promote-HTTP, maintenance).
  Rows for memory/knowledge events click through to the appropriate
  detail page; context events go to the existing record detail. Cursor
  pagination ("Load older events") + day grouping + client-side actor
  filter.
- **Recall page** — facet domain chips are now clickable to filter the
  result list. Form state mirrored into `#recall?…` URL hash so views
  are bookmarkable / shareable.
- **Search & Research page** — thread, named presets, and recent
  namespaces persisted to localStorage. Tag chips on result cards in
  the Answer tab are clickable (add to filter).
- **Memory & Knowledge browser** — new "Load counts" bulk action
  (concurrent recall for every visible namespace) and inline
  Register-namespace form for fast bootstrap.
- **`npm run build` works again** — fixed pre-existing
  `exactOptionalPropertyTypes` strict-mode errors across PromotePage,
  RecordDetailPage, ViewBuilderPage, WriteRecordPage, BrokerPage,
  AuthTokensPage, PolicyManagerPage, PacketBuilderPage,
  CompareRevisionsPage, KeyHistoryPage, NamespaceDetailPage. The
  canonical `make frontend` target builds cleanly again.

### Added

- **HTTP route: `GET /v1/namespaces/list`** — list all registered
  namespaces with optional `?prefix=` (string-prefix match) and
  `?limit=` (default 200, max 1000). Returns `{items, count, truncated}`.
  Backs the new Memory & Knowledge Browser frontend tree view; mirrors
  the existing MCP `context_namespaces_list` tool.
- **Frontend page: Memory & Knowledge Browser** — tree view of registered
  namespaces grouped by tier prefix (`user/`, `app/`); domain tabs filter
  memory vs knowledge; lazy-load keys per namespace via `/v1/recall`;
  drill-through to the new memory/knowledge detail page.
- **Frontend page: Search & Research (v1 curated)** — question textarea
  over `POST /v1/tesseract/lookup` with status / domain / tag / confidence
  filters. Tabbed result view: **Answer** (cards grouped by domain) and
  **Sources** (raw revisions for citation). Session-local thread of past
  Q&As. v2 LLM-backed synthesis is comment-tracked in source — will use
  the portfolio go-modelsdev library for cost / token / latency
  telemetry.
- **Frontend page: Memory / Knowledge Revision Detail** — single
  component handles both `domain="memory"` and `"knowledge"` via prop;
  identity bar (status, confidence, author, tags, timestamps); four
  tabs (Summary / Payload / History / Raw). Wired as click-through
  destination for Recall, Memory & Knowledge Browser, Search & Research,
  and the Audit Timeline.
- **Frontend page: Recall (improved)** — operator surface over
  `GET /v1/recall` (shipped in 0.5.1). Tag chips clickable (add to
  filter); recent-namespaces dropdown via localStorage; click-through
  routed by `item.domain` to the memory/knowledge detail page (fixed
  the "head not found" regression that hit when the click previously
  went to the context-domain record-detail handler).

## [0.5.1] — 2026-04-26

Audit-pipeline catch-up + recall ergonomics. Memory/knowledge writes now appear in `context_audit`, the audit-emit code path is consolidated through canonical `Store.Emit*` helpers, the daemon degrades gracefully when no embedding key is present, and a new `GET /v1/recall` endpoint gives scripted / agent consumers a query-param surface that mirrors `POST /v1/tesseract/lookup`. Includes the audit fixes from `CW-20260419-0040` (PR #11) plus the audit-helper consolidation from `CW-20260419-0060`.

### Changed

- `contextstore` now exposes canonical `Store.Emit*` helpers for every audit
  event type. Handlers and CLI commands migrated off direct `AuditEvent`
  construction; `RecordAuditEvent` is now unexported. Event-type identifiers
  centralized as constants in `internal/contextstore/audittypes.go`.
  (`CW-20260419-0060`)
- `context_audit` now captures `maintenance.trim`, `maintenance.compact`, and
  `packet` events that were previously failing the store's validation
  silently. This is a visibility improvement — no event-type names changed.
  (`CW-20260419-0060`)

### Fixed

- `memory_write` / `memory_deprecate` / `memory_promote` / `knowledge_write`
  now record audit events. Previously these write paths bypassed the audit
  log entirely, making `context_audit` queries for memory/knowledge
  namespaces silently empty. Emits at the domain layer so all surfaces
  (MCP, HTTP, library-facade) are covered. (`CW-20260419-0040`)
- `context_audit` MCP response now includes the `metadata` field per row
  when non-empty. HTTP `/v1/context/audit` already projected metadata via
  struct serialization. (`CW-20260419-0040`)
- `contextd` no longer instantiates the OpenAI embedder when
  `OPENAI_API_KEY` is unset. Previously the embedder was constructed
  unconditionally and failed at every invocation; now the daemon logs a
  warning at startup and falls back to BM25-only recall, keeping the
  service usable in offline / no-key environments.

### Added

- Six new audit event types: `memory.write`, `memory.supersede`,
  `memory.deprecate`, `memory.promote`, `knowledge.write`,
  `knowledge.supersede`. Existing event-type strings unchanged.
  (`CW-20260419-0040`)
- `memory.AuditSink` interface + `memory.Store.SetAuditSink(sink)` setter —
  new dependency-injection seam for domain-layer audit. `contextstore.Store`
  satisfies the interface structurally. (`CW-20260419-0040`)
- **HTTP route: `GET /v1/recall`** — query-param-driven recall optimised
  for scripted / agent consumption. Mirrors `POST /v1/tesseract/lookup` but
  takes `?namespace=…&tags=…&limit=…&format=brief|full`. Default `brief`
  format returns condensed `{revision_id, memory_id, domain, namespace,
  memory_key, tags, confidence, summary, created_at}` items; `full`
  returns the complete `RecallResult`. Single-namespace only; respects
  bearer-token namespace ACLs. Surfaces `similarity_unavailable` (503) and
  `validation_error` (400) consistent with existing recall handlers.

### Not in scope (flagged)

- `memory.EmbedRevision` async queue audit — infrastructure-only
  observability; not an audit concern. Intentionally scoped out of
  `CW-20260419-0040`.
- `memory.Deprecate` audit events record `actor="system"` because the
  method signature has no actor parameter. Extending the signature is a
  follow-up ticket candidate. (`CW-20260419-0040`)

## [0.5.0] — 2026-04-19

MCP Surface v2 (PR #9 `feat/mcp-surface-v2`). Rewrites memory/knowledge/unified/meta MCP tool descriptions against a consistent v2 template, adds MCP protocol annotations per spec §5.4, and ships `tesseract_skills` as a progressive-discovery meta-tool backed by 11 embedded markdown skills. Additive — no Go library API changes; existing consumers are unaffected.

### Added

- **MCP tool: `tesseract_skills`** — progressive-discovery meta-tool. No-args returns an 11-entry index; `name=<skill>` returns the full markdown body. Skills embedded via `//go:embed` at `internal/mcpadapter/skills/*.md`: `start-here`, `namespaces`, `facets-and-kinds`, `revisions`, `recall-and-ranking`, `promotion`, `views`, `memory`, `knowledge`, `context-packet`, `audit`. `start-here` is indexed first; rest alphabetical.
- **MCP protocol annotations** on all 12 memory / knowledge / unified / meta tools — `ReadOnlyHint`, `IdempotentHint`, `DestructiveHint`, `OpenWorldHint` per spec §5.4. `memory_deprecate` correctly carries `IdempotentHint=true` (second call is a no-op).
- **Drift guardrail:** `internal/mcpadapter/annotations_test.go` fails CI if any of the 12 v2-template tools drops its required annotations.

### Changed

- **MCPServer version string** bumped `0.4.0 → 0.5.0`.
- **12 MCP tool descriptions** rewritten to v2 template — bold action phrase + `Kind of content` / `Scope` / `Use this when` / `Don't use this for` / `Deeper:` bullets plus concrete parameter-value examples. Covers all memory, knowledge, unified-lookup, and meta surfaces.
- **30 context-domain tool descriptions** get an appended `See tesseract_skills start-here for the primitive model.` footer. Minimum-touch; full context-domain rewrite is deferred.
- **Parity catalog** — `tesseract_skills` registered as MCP-only meta-tool in `surfaceCatalog`.
- **`docs/MCP_TOOLS.md`** refreshed — new Skills section, `Deeper` column on memory/knowledge/unified tables, new Meta section for `tesseract_skills`.

### Follow-ups filed (not in this release)

- `CW-20260419-0040` — `memory_write` / `memory_deprecate` / `knowledge_write` don't call `RecordAuditEvent`; `context_audit` silently omits those writes. `AuditEvent.Metadata` persisted but not projected in the MCP response.
- `CW-20260419-0041` — `context_packet` uses `max_items` / `max_tokens_estimate` while `context_broker_*` uses `budget_items` / `budget_tokens` (same knob, two names, divergent defaults 8000 vs 4000).
- `CW-20260419-0042` — `docs/SPECS/VIEWS.md` documents `evaluation_meta` fields (`normalized_selector`, `returned_count`) absent from the actual `EvaluateResult` struct / `views_evaluate` handler.

## [0.4.0] — 2026-04-16

Hybrid Relevance S1 (`SPR-20260414-hybrid-relevance-s1`, `EPIC-20260414-19124`). Adds a fourth ranking mode `relevance` that fuses BM25 (FTS5) and cosine via Reciprocal Rank Fusion and multiplies by the existing activation-style modifiers. Becomes the smart default for query-backed agent recall so freshly-written memories surface immediately via the BM25 arm (previously: invisible to semantic search until the async embedder caught up). Also ships BLG-037 (context-budget fix for MCP recall responses).

Minor-bump candidate — default ranking changes when a query is provided, and access reinforcement widens from activation-only to every recall mode.

### Added

- **Ranking mode: `relevance`** — `RRF(bm25_rank, cosine_rank, k=60) * statusW * originW * confidence * recency * activation`. Cosine arm is optional; BM25-only fires when no embedder is configured.
- **Schema v12** — FTS5 external-content virtual table `memory_revisions_fts` over `payload_summary`, `payload_body`, `tags`, with AFTER INSERT / AFTER DELETE sync triggers and one-shot backfill for existing rows. Content-only index; status filtering happens at query time via JOIN to keep the BM25 arm deterministic.
- **Reranker interface** (`memory.Reranker`, `RerankerFunc`) + **Cohere/Voyage-compatible HTTP adapter** (`memory.HTTPReranker`). Per-call opt-in via `RecallInput.Reranker` naming a reranker registered with `Store.RegisterReranker`. Self-hosted `bge-reranker` gateways that mimic the same JSON shape also work.
- **Recall regression gate** (`internal/memory/recall_eval_test.go`) — seeds a deterministic corpus, runs three fixture query classes (exact acronym, multi-token semantic, mixed), computes nDCG@10 and hit-rate@10 per mode, and enforces: (1) relevance's aggregate nDCG does not regress below similarity's, (2) relevance strictly outperforms similarity on ≥1 fixture.

### Changed

- **Ranking default is now smart**: empty `Ranking` with a non-empty `Query` resolves to `relevance`; empty `Ranking` with no query stays on `activation`. Explicit callers are unaffected.
- **MCP tool descriptions** updated on `memory_recall` and `tesseract_lookup`: `activation, chronological, similarity, or relevance (default: relevance when query is set, else activation)`.
- **Access reinforcement widens to all recall modes** (was activation-only). Dense-only or chronological queries now bump `last_accessed_at` and `access_count` too so hot memories keep their activation trail when agents switch ranking modes.

### Fixed

- **BLG-037** — `memory_recall` and `tesseract_lookup` were shipping the full `EmbeddingVector` (~39KB/record for text-embedding-3-large) in every `Revision` JSON response, blowing agent context budgets on recalls of 5+. `json:"-"` on `Revision.EmbeddingVector` drops the field from every JSON response universally. Struct field is retained — similarity ranking still reads it directly via `similarityScore`/`CosineSimilarity`; SQL storage is untouched. Smoke-test on a live store: `memory_recall limit=10` went from ~390KB to 26KB; `tesseract_lookup limit=20` went from ~660KB to 40KB.

### Operator notes

- First boot after upgrade runs migration case 12, which creates the FTS5 index and backfills every existing `memory_revisions` row. On a fresh / small corpus this is instant; on larger stores, expect a one-shot startup cost proportional to revision count × payload length.
- `modernc.org/sqlite` ships FTS5 by default — no build tags or external sqlite binary needed.
- Out of scope for this release (tracked separately): external vector DB evaluation (`BLG-20260414-016`), markdown/code-aware chunking (moved to `SPR-20260416-ingest-s1` under `EPIC-20260415-64937`).

## [0.3.0] — 2026-04-15

MCP↔HTTP parity batch 1: agent-access fixes plus a durable drift guardrail. Closes the gaps surfaced after the knowledge-domain S1 merge so an agent booted against `mcp__tesseract__*` has functional parity with the HTTP `/v1/*` surface for context, memory, and knowledge reads.

### Added

- **MCP tool: `context_estimate`** — record count + payload bytes + token proxy for a selector. Peer of `POST /v1/context/estimate`.
- **MCP tool: `views_evaluate`** — full-power selector evaluation with `evaluation_meta`. Peer of `POST /v1/views/evaluate`.
- **MCP tool: `memory_get_revision`** — fetch a memory revision by id. Scope `memory:read`. Peer of `GET /v1/memory/revisions/{id}`.
- **MCP tool: `knowledge_get`** — current knowledge revision for `(namespace, key)`. Scope `memory:read`. Peer of new `GET /v1/knowledge/current`.
- **MCP tool: `knowledge_history`** — full revision history for a knowledge entry, newest-first. Scope `memory:read`. Peer of new `GET /v1/knowledge/history`.
- **HTTP routes:** `GET /v1/knowledge/current?namespace=&key=` and `GET /v1/knowledge/history?namespace=&key=`.
- **Store helpers:** `contextstore.Store.Estimate`, `contextstore.Store.Evaluate`, `contextstore.NormalizedScope`. Used by both MCP and HTTP — no duplicated logic.
- **Knowledge store reads:** `knowledge.Store.GetCurrent` / `knowledge.Store.GetHistory`. Domain-filtered wrappers; non-knowledge revisions return `memory.ErrNotFound`.
- **Drift guardrail:** `tests/parity/parity_test.go` — fails CI if a tool or HTTP route is added without a matching `surfaceCatalog` entry. Each one-sided op carries an explicit waiver string with a reason.
- **Catalog doc:** `docs/MCP_TOOLS.md` — agent-facing per-domain tool tables (scopes, HTTP peers), transport config for `~/.claude.json`, five playbooks (write memory, write knowledge, unified lookup, boot-time packet fetch, resolve revision id).
- **Adapter introspection:** `mcpadapter.Adapter.RegisterAllTools` exported so the parity test (and other tooling) can list registered tools without a stdio server.

### Changed

- `MCPServer` version string bumped to `0.3.0` (was `0.1.0`).
- `contextapi.handleEstimate` now delegates to `contextstore.Store.Estimate`. Response shape unchanged.
- `contextapi.handleView` now delegates to `contextstore.Store.Evaluate`. Response shape unchanged.
- Duplicate `normalizedScope` helper consolidated from `contextstore` + `contextapi` into one exported `contextstore.NormalizedScope`.

### Documentation

- `README.md` links to `docs/MCP_TOOLS.md`.

### Operator notes

The MCP client config should point at the `contextd mcp` binary and the same Tesseract data root used by the HTTP server.

## [0.2.0] — 2026-04-15

Knowledge Domain S1 (`SPR-20260414-knowledge-domain-s1`, `EPIC-20260414-84967`). Adds a second info domain (`knowledge`) alongside `memory`, wires the memory subsystem into HTTP serve mode, and introduces `tesseract_lookup` for cross-domain search. Merged as PR #3.

### Added

- **Domain discriminator** — `domains.Domain` type + in-tree `DomainPolicy` interface (`MemoryDomain`, `KnowledgeDomain`).
- **Knowledge facets** — `kind`, `source`, `pointer{scheme, locator, resolved_at}` on `memory.Revision`. Required on knowledge writes.
- **MCP tool: `knowledge_write`** + HTTP `POST /v1/knowledge/write` — pointer-first writes with required facets.
- **MCP tool: `tesseract_lookup`** + HTTP `POST /v1/tesseract/lookup` — unified search across memory + knowledge with facet histogram.
- **HTTP `/v1/memory/*` surface** — `write`, `current`, `history`, `revisions/{id}`, `recall`, `promote`, `deprecate` routes (`TASK-20260414-001`).
- **`memory.RecallFilters`** — new filter fields `Domains`, `FacetKinds`, `FacetSources`.
- **Schema migrations** — v10 adds `domain` (default `'memory'`) to `memory_state` + `memory_revisions` with indexes; v11 adds nullable flat facet columns with partial indexes on `facet_kind` and `facet_source`.

### Changed

- `memory.Recall` namespace validation relaxed to non-empty; per-domain shape enforced on write via `DomainPolicy`.

### Deferred (post-MVP)

- React GUI parity — `BLG-20260415-003`
- Ingester adapter + plugin ecosystem — `EPIC-20260415-64937`
- Decay policy per `kind` — `BLG-20260415-001`
- Additional "search first" surfaces — `BLG-20260415-002`

## [0.1.0] — 2026-04-08

Foundational embedding + memory release. Bundles PR #1 (go-queue integration) and PR #2 (D-deferred tracks: auto-embed, similarity recall, facade, ordering, config, dedup).

### Added

- **`go-queue` integration** — SQLite-backed `go-queue` instance for async embed jobs. `QueueAdapter` bridges `memory.JobQueue` → `queue.Queue`. `WithQueue()` functional option on `tesseract.Open()`. Worker lifecycle with retry (3 max tries / 30s delay). Separate `queue.db` for write safety.
- **Auto-embed on write** — `memory.Store.EmbedRevision()` loads revision, extracts text, calls embedder, writes vector inline to `embedding_model`/`embedding_vector` columns. Queue embed handler wired to call it on every write.
- **Similarity recall** — `Recall(RankingSimilarity)` embeds the query and ranks candidates by cosine similarity. Exposed on both MCP `memory_recall` and library API. Unembedded revisions filtered out.
- **Tesseract facade** — `WriteMemory`, `RecallMemory`, `GetCurrentRevision`, `GetRevisionHistory`, `EmbedRevision` on `*Tesseract`. Library consumers no longer need to reach through `.Store()` / `.MemoryStore()`.
- **Backfill CLI** — `contextd backfill-embeddings [--namespace=...]` iterates unembedded revisions and embeds them.
- **Semantic dedup** — opt-in via `Dedup: "semantic"` on `WriteInput`. Same-key matches auto-supersede; cross-key matches set `DedupMatch` without superseding. MCP `memory_write` exposes `dedup` + `dedup_threshold` params.
- **Config file** — `~/.tesseract/config.yaml` for embedding provider/model and dedup threshold. Loaded by `internal/config.Load()`. Defaults: OpenAI `text-embedding-3-large`, threshold 0.85. Falls back to env vars for auth.
- **SQLite WAL mode** enabled for better concurrent read/write performance.

### Changed

- **Monotonic ULIDs** (`ulid.Monotonic`) + **RFC3339Nano timestamps** eliminate the nondeterministic revision ordering bug. `parseMemoryTime()` falls back to `time.DateTime` for backward compat.
- All stale `tesseract` references replaced with `tesseract` (plugin CLI usage strings, env vars).

### Fixed

- Stale `mcp-helpers` and `otel` replace directives (`fragments-engine/libs/` → `framework/libs/`).
- Empty-text guard in `EmbedRevision` (would previously call the embedder with empty input).

## [0.0.1] — 2026-04-08

Initial standalone-repo baseline tag at commit `3b92f5c`. Captures the post-rename state of the codebase extracted from `fragments-engine/tesseract/` to its own repo at `github.com/hollis-labs/tesseract`. No formal release notes — this tag exists primarily to anchor `git describe` output.

[Unreleased]: https://github.com/hollis-labs/tesseract/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/hollis-labs/tesseract/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/hollis-labs/tesseract/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/hollis-labs/tesseract/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/hollis-labs/tesseract/compare/v0.6.1...v0.7.0
[0.6.0]: https://github.com/hollis-labs/tesseract/compare/v0.5.3...v0.6.0
[0.5.3]: https://github.com/hollis-labs/tesseract/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/hollis-labs/tesseract/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/hollis-labs/tesseract/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/hollis-labs/tesseract/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/hollis-labs/tesseract/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/hollis-labs/tesseract/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/hollis-labs/tesseract/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/hollis-labs/tesseract/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/hollis-labs/tesseract/releases/tag/v0.0.1

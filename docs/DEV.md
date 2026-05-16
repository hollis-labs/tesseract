# Local Development

Status: active draft

## Prerequisites
- Go toolchain (version per `go.mod`)
- Git

## Planned workflows
- Build and test commands for service components
- Local run instructions for CLI/API/desktop app
- Test data and fixture workflows

## Build and test
- Run full tests: `go test ./...`
- Run targeted integration/auth tests: `go test ./tests/integration -run AuthGate`

## API auth (MVP)
- Mutating HTTP endpoints support bearer-token auth.
- Configure the server with an auth token and send:
  - `Authorization: Bearer <token>`
- If token is configured and missing/mismatched, server returns:
  - HTTP `401`
  - `{"code":"auth_required","message":"missing or invalid bearer token","details":null}`

### Managed token lifecycle (local admin path)
- Issue token: `contextd context token issue --label admin --ttl 24h`
- Rotate token: `contextd context token rotate --token <old-token> --label admin --ttl 24h`
- Revoke token: `contextd context token revoke --token <token>`
- List token metadata: `contextd context token list --output table`
- In managed mode, revoked/expired tokens are rejected for mutating API endpoints.

Safe local handling:
- Do not commit tokens into the repository.
- Prefer exporting tokens into a local shell session (`export CONTEXTD_TOKEN=...`) or an untracked env file.
- Rotate tokens after local sharing events and revoke obsolete tokens promptly.

Rotation checklist:
1. Issue or rotate to obtain a new token.
2. Update local runtime environment with the new token.
3. Verify API mutating route access with the new token.
4. Revoke the old token.

## Service runner
- Start API server (default `:8080`): `contextd serve`
- Start with managed token auth: `contextd serve --managed-auth --addr :8080`
- Start with legacy static token: `contextd serve --static-token <token> --addr :8080`
- Enable local diagnostics endpoint: `contextd serve --metrics --addr :8080`
- Enable structured request logging: `contextd serve --request-logs --addr :8080`
- Request log redaction mode (default redacted): `contextd serve --request-logs --request-log-mode redacted --addr :8080`
- Request log full query mode (local debugging only): `contextd serve --request-logs --request-log-mode full --addr :8080`
- Configure graceful shutdown timeout: `contextd serve --shutdown-timeout 10s`
- Managed auth guard: server startup fails if no active token exists.
- Metrics endpoint: `GET /v1/metrics` (available only when `--metrics` is enabled).
- Service lifecycle: `SIGINT`/`SIGTERM` trigger graceful shutdown and deterministic shutdown logs.
- Request-log security posture:
  - Default to `--request-log-mode redacted` in shared/dev-team environments.
  - Use `--request-log-mode full` only for short local debugging sessions.
  - Avoid `full` mode when tokens/session IDs may appear in query strings.

## Namespace schema contract operations
- Register namespace: `contextd context namespace register --namespace app/editor/session --owner-type app --owner-id editor`
- Show namespace policy: `contextd context namespace show --namespace app/editor/session`
- Use `required_keys` policy to enforce top-level payload keys for writes/promotions.

### Policy/Promote Operator Quick Reference
1. Register app namespace policy:
   - `contextd context namespace register --namespace app/editor/session --owner-type app --owner-id editor`
2. Write app candidate record:
   - `contextd context put --client-id editor-ui --actor app:editor-ui --namespace app/editor/session --key summary --json '{"text":"candidate summary"}'`
3. Promote into protected user namespace:
   - `contextd context promote --client-id user-shell --actor user --from-namespace app/editor/session --from-key summary --to-namespace user/profile --to-key summary`
4. Verify promotion via audit trail:
   - `contextd context audit --limit 20 --namespace user/profile --event-type promote --output table`

See policy semantics: `docs/SPECS/API.md#actor-namespace-contract-matrix`.

## Consistency operations
- CLI scan report: `contextd context doctor --output json`
- CLI head rebuild + report: `contextd context repair-heads --output table`
- API scan endpoint: `GET /v1/context/consistency/scan`
- API repair endpoint: `POST /v1/context/consistency/repair` (mutating auth rules apply)

## Audit operations
- CLI audit query: `contextd context audit --limit 50 --output table`
- CLI filtered audit query: `contextd context audit --limit 50 --namespace user/notes --event-type promote --output json`
- CLI cursor query: `contextd context audit --cursor <next_cursor> --limit 50 --output json`
- API audit query: `GET /v1/context/audit?limit=50`
- API filtered audit query: `GET /v1/context/audit?limit=50&event_type=write&namespace=app/editor/session`
- Cursor pagination flow (fixture-aligned):
  1. `curl -s 'http://127.0.0.1:8080/v1/context/audit?limit=2' | jq .`
  2. Extract `next_cursor` from response.
  3. `curl -s \"http://127.0.0.1:8080/v1/context/audit?limit=2&cursor=<next_cursor>\" | jq .`
- Expected response fields: `items`, `count`, `next_cursor`.

## Backup and restore
- Export snapshot: `contextd context backup export --out /path/to/backup.json`
- Verify snapshot integrity: `contextd context backup verify --in /path/to/backup.json`
- Restore snapshot: `contextd context backup restore --in /path/to/backup.json`
- Expected behavior: deterministic record/history parity and restored audit/token metadata.

## Readiness checks
- CLI readiness report: `contextd context health --output table`
- CLI readiness summary: `contextd context health --summary --output table`
- API readiness report: `GET /v1/health/readiness`
- Readiness includes storage-path status, schema version, consistency issue count, and status tier:
  - `healthy`: all core checks pass.
  - `degraded`: store accessible but non-fatal consistency issues detected.
  - `failing`: required storage path missing or critical readiness preconditions unmet.

## First-time bootstrap
- Run deterministic setup: `contextd context bootstrap --default-app editor --output json`
- Command seeds baseline namespaces and returns readiness summary.
- Re-running bootstrap is idempotent.

## Retention and compaction
- Compact revisions + trim audit events:
  - `contextd context compact --keep-revisions 5 --keep-audit 5000 --output table`
- Compaction preserves latest heads per `(namespace,key)` and rebuilds head pointers deterministically.
- Suggested local baseline (see storage spec proposal):
  - `--keep-revisions 20`
  - `--keep-audit 10000`
- Operational profile examples:
  - `light` (minimal local footprint):
    - `contextd context compact --keep-revisions 5 --keep-audit 2000 --output table`
  - `standard` (recommended baseline):
    - `contextd context compact --keep-revisions 20 --keep-audit 10000 --output table`
  - `high-trace` (extended debugging/audit retention):
    - `contextd context compact --keep-revisions 50 --keep-audit 50000 --output table`
- Baseline rationale and tradeoffs: `docs/SPECS/STORAGE.md#retention-baseline-proposal-local-first-default`.

## API contract fixtures
- Golden API contract fixture: `tests/integration/fixtures/api_contract_golden.json`
- Contract suite: `go test ./tests/integration -run APIContract`
- Update process: modify fixture intentionally alongside API shape changes and include rationale in commit message.
- Golden audit contract fixture: `tests/integration/fixtures/audit_contract_golden.json`
- Audit contract suite: `go test ./tests/integration -run AuditContract`
- Golden API error fixture: `tests/integration/fixtures/api_error_contract_golden.json`
- Error contract suite: `go test ./tests/integration -run APIErrorContract`
- Golden readiness contract fixture: `tests/integration/fixtures/readiness_contract_golden.json`
- Readiness contract suite: `go test ./tests/integration -run ReadinessContract`
- Golden CLI health summary fixture: `tests/integration/fixtures/cli_health_summary_contract_golden.json`
- CLI health summary contract suite: `go test ./tests/integration -run CLIHealthSummaryContract`
- Golden CLI audit fixture: `tests/integration/fixtures/cli_audit_contract_golden.json`
- CLI audit contract suite: `go test ./tests/integration -run CLIAuditContract`
- Golden CLI contract command fixture: `tests/integration/fixtures/cli_contract_command_golden.json`
- CLI contract command suite: `go test ./tests/integration -run CLIContractCommand`
- Golden contract list default-output fixture: `tests/integration/fixtures/contract_list_default_output_golden.json`
- Contract list default-output suite: `go test ./tests/integration -run ContractListDefaultOutputContract`
- Contract list deterministic-order suite: `go test ./tests/integration -run ContractListDefaultOutputDeterministicContract`
- Contract list count-parity suite: `go test ./tests/integration -run ContractListCountParityContract`
- Golden contract list invalid-output fixture: `tests/integration/fixtures/contract_list_invalid_output_error_golden.json`
- Contract list invalid-output suite: `go test ./tests/integration -run ContractListInvalidOutputErrorContract`
- Golden contract list table fixture: `tests/integration/fixtures/contract_list_table_golden.json`
- Contract list table suite: `go test ./tests/integration -run ContractListTableContract`
- Contract list table header suite: `go test ./tests/integration -run ContractListTableHeaderContract`
- Golden contract list table count-parity fixture: `tests/integration/fixtures/contract_list_table_count_parity_golden.json`
- Contract list table count-parity suite: `go test ./tests/integration -run ContractListTableCountParityContract`
- Golden contract run suite-all default-output fixture: `tests/integration/fixtures/contract_run_all_default_output_golden.json`
- Contract run suite-all default-output suite: `go test ./tests/integration -run ContractRunAllDefaultOutputContract`
- Golden contract run suite-all table fixture: `tests/integration/fixtures/contract_run_all_table_golden.json`
- Contract run suite-all table suite: `go test ./tests/integration -run ContractRunAllTableContract`
- Golden contract run suite-all execute-table fixture: `tests/integration/fixtures/contract_run_all_execute_table_golden.json`
- Contract run suite-all execute-table suite: `go test ./tests/integration -run ContractRunAllExecuteTableContract`
- Golden contract run suite-all execute-json fixture: `tests/integration/fixtures/contract_run_all_execute_json_golden.json`
- Contract run suite-all execute-json suite: `go test ./tests/integration -run ContractRunAllExecuteJSONContract`
- Golden contract run default-output fixture: `tests/integration/fixtures/contract_run_default_output_golden.json`
- Contract run default-output suite: `go test ./tests/integration -run ContractRunDefaultOutputContract`
- Golden contract run dry-table fixture: `tests/integration/fixtures/contract_run_table_dry_golden.json`
- Contract run dry-table suite: `go test ./tests/integration -run ContractRunTableDryContract`
- Contract run table header suite: `go test ./tests/integration -run ContractRunTableHeaderContract`
- Golden contract run execute-table fixture: `tests/integration/fixtures/contract_run_execute_table_golden.json`
- Contract run execute-table suite: `go test ./tests/integration -run ContractRunExecuteTableContract`
- Golden contract run execute invalid-output fixture: `tests/integration/fixtures/contract_run_execute_invalid_output_error_golden.json`
- Contract run execute invalid-output suite: `go test ./tests/integration -run ContractRunExecuteInvalidOutputErrorContract`
- Golden contract run invalid-output fixture: `tests/integration/fixtures/contract_run_invalid_output_error_golden.json`
- Contract run invalid-output suite: `go test ./tests/integration -run ContractRunInvalidOutputErrorContract`
- Golden contract run unknown-suite fixture: `tests/integration/fixtures/contract_run_unknown_suite_error_golden.json`
- Contract run unknown-suite suite: `go test ./tests/integration -run ContractRunUnknownSuiteErrorContract`
- Golden make contract-cli-list fixture: `tests/integration/fixtures/make_contract_cli_list_contract_golden.json`
- Make contract-cli-list suite: `go test ./tests/integration -run MakeContractCLIListContract`
- Golden make contract-cli-run fixture: `tests/integration/fixtures/make_contract_cli_run_contract_golden.json`
- Make contract-cli-run suite: `go test ./tests/integration -run MakeContractCLIRunContract`
- Golden make contract-commands fixture: `tests/integration/fixtures/make_contract_commands_contract_golden.json`
- Make contract-commands suite: `go test ./tests/integration -run MakeContractCommandsContract`
- Golden make bootstrap-report fixture: `tests/integration/fixtures/make_bootstrap_report_contract_golden.json`
- Make bootstrap-report suite: `go test ./tests/integration -run MakeBootstrapReportContract`
- Golden make bootstrap-sync fixture: `tests/integration/fixtures/make_bootstrap_sync_contract_golden.json`
- Make bootstrap-sync suite: `go test ./tests/integration -run MakeBootstrapSyncContract`
- Golden make bootstrap-sync alias fixture: `tests/integration/fixtures/make_bootstrap_sync_alias_contract_golden.json`
- Make bootstrap-sync alias suite: `go test ./tests/integration -run MakeBootstrapSyncAliasContract`
- Golden contract-suite-commands fixture: `tests/integration/fixtures/contract_suite_commands_contract_golden.json`
- Contract-suite-commands suite: `go test ./tests/integration -run ContractSuiteCommandsScript`
- Golden suite-registry parity fixture: `tests/integration/fixtures/contract_suite_registry_parity_golden.json`
- Suite-registry parity suite: `go test ./tests/integration -run ContractSuiteRegistryParity`
- Golden smoke invalid-token fixture: `tests/integration/fixtures/smoke_invalid_token_contract_golden.json`
- Smoke invalid-token contract suite: `go test ./tests/integration -run SmokeInvalidTokenContract`
- Golden make smoke-invalid-token fixture: `tests/integration/fixtures/make_smoke_invalid_token_contract_golden.json`
- Make smoke-invalid-token contract suite: `go test ./tests/integration -run MakeSmokeInvalidTokenContract`
- Golden run-log helper fixture: `tests/integration/fixtures/runlog_helper_contract_golden.json`
- Run-log helper contract suite: `go test ./tests/integration -run RunlogHelperContract`
- Contract suite command-format guardrail: `go test ./tests/integration -run ContractSuiteCommandsFormatContract`
- Contract suite name-uniqueness guardrail: `go test ./tests/integration -run ContractSuiteCommandsUniqueContract`
- Contract suite command-prefix guardrail: `go test ./tests/integration -run ContractSuiteCommandsPrefixContract`
- Contract suite deterministic-order guardrail: `go test ./tests/integration -run ContractSuiteCommandsDeterministicContract`
- Contract suite command-suffix guardrail: `go test ./tests/integration -run ContractSuiteCommandsSuffixContract`
- Contract suite command token-count guardrail: `go test ./tests/integration -run ContractSuiteCommandsTokenCountContract`
- Contract suite non-empty output guardrail: `go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract`
- Contract fixture lint script suite: `go test ./tests/integration -run ContractFixtureLintScript`
- Golden bootstrap-sync fixture: `tests/integration/fixtures/bootstrap_sync_contract_golden.json`
- Bootstrap-sync contract suite: `go test ./tests/integration -run BootstrapSyncScript`
- Golden bootstrap-sync no-log fixture: `tests/integration/fixtures/bootstrap_sync_no_log_contract_golden.json`
- Bootstrap-sync no-log suite: `go test ./tests/integration -run BootstrapSyncNoLogContract`
- Golden bootstrap-sync idempotent fixture: `tests/integration/fixtures/bootstrap_sync_idempotent_contract_golden.json`
- Bootstrap-sync idempotent suite: `go test ./tests/integration -run BootstrapSyncIdempotentContract`
- Golden AGENTS boot check fixture: `tests/integration/fixtures/agents_boot_check_contract_golden.json`
- AGENTS boot check contract suite: `go test ./tests/integration -run AgentsBootCheckContract`
- Full fixture inventory: `docs/CONTRACT_FIXTURES.md`
- Bootstrap/make fixture index: `docs/CONTRACT_FIXTURES.md#bootstrap-and-make-fixture-index`
- Suite ordering policy: `docs/CONTRACT_FIXTURES.md#suite-ordering-policy`
- Suite naming conventions: `docs/CONTRACT_FIXTURES.md#suite-naming-conventions`
- CLI error-path suite matrix: `docs/CONTRACT_FIXTURES.md#cli-error-path-suite-matrix`
- Guardrail suite stack: `docs/CONTRACT_FIXTURES.md#guardrail-suite-stack`
- List/run parity suites: `docs/CONTRACT_FIXTURES.md#listrun-parity-suites`
- Fixture ownership/update policy: see `docs/CONTRACT_FIXTURES.md#ownership-and-update-policy`
- Fixture quickstart: see `docs/CONTRACT_FIXTURES.md#quickstart`
- Script-based suites: see `docs/CONTRACT_FIXTURES.md#script-based-suites`

## Contract helper CLI
- List suites: `contextd context contract list --output table`
- Show suite command(s) without execution: `contextd context contract run --suite api --output json`
- Execute suite command(s): `contextd context contract run --suite api --execute --output table`
- Default output behavior:
  - `contextd context contract list` defaults to `--output json`.
  - `contextd context contract run --suite <name>` defaults to `--output json` with `executed=false` unless `--execute` is set.
  - Default-output contract suites:
    - `go test ./tests/integration -run ContractListDefaultOutputContract`
    - `go test ./tests/integration -run ContractRunDefaultOutputContract`
- Make shortcuts:
  - `make contract-cli-list`
  - `make contract-cli-run SUITE=api`
  - `make contract-cli-run SUITE=api-errors`
  - `make contract-commands`

## Contract Run Mode Matrix

| Command mode | Behavior | Verification suite |
|---|---|---|
| `context contract run --suite api` | Default JSON dry mode (`executed=false`) for one suite. | `go test ./tests/integration -run ContractRunDefaultOutputContract` |
| `context contract run --suite all` | Default JSON dry mode across full suite catalog. | `go test ./tests/integration -run ContractRunAllDefaultOutputContract` |
| `context contract run --suite api --output table` | Dry-run table output with `SUITE/COMMAND` columns. | `go test ./tests/integration -run ContractRunTableDryContract` |
| `context contract run --suite all --output table` | Dry-run table output across all suites. | `go test ./tests/integration -run ContractRunAllTableContract` |
| `context contract run --suite api --execute --output table` | Execute table output with `SUITE/OK/COMMAND` columns. | `go test ./tests/integration -run ContractRunExecuteTableContract` |
| `context contract run --suite all --execute --output table` | Execute table output across all suites. | `go test ./tests/integration -run ContractRunAllExecuteTableContract` |
| `context contract run --suite all --execute` | Execute JSON output with summary + per-suite `ok` status. | `go test ./tests/integration -run ContractRunAllExecuteJSONContract` |

Header guardrail:
- `go test ./tests/integration -run ContractRunTableHeaderContract`

Related list-mode references:
- `context contract list`: `go test ./tests/integration -run ContractListDefaultOutputContract`
- `context contract list --output table`: `go test ./tests/integration -run ContractListTableContract`

## Contract Quick-Run Pack

Guardrail pack:
1. `go test ./tests/integration -run ContractSuiteCommandsFormatContract`
2. `go test ./tests/integration -run ContractSuiteCommandsUniqueContract`
3. `go test ./tests/integration -run ContractSuiteCommandsPrefixContract`
4. `go test ./tests/integration -run ContractSuiteCommandsDeterministicContract`
5. `go test ./tests/integration -run ContractSuiteCommandsSuffixContract`
6. `go test ./tests/integration -run ContractSuiteCommandsTokenCountContract`
7. `go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract`

Parity pack:
1. `go test ./tests/integration -run ContractListCountParityContract`
2. `go test ./tests/integration -run ContractListTableCountParityContract`
3. `go test ./tests/integration -run ContractRunAllDefaultOutputContract`
4. `go test ./tests/integration -run ContractRunAllTableContract`

## Contract fixture lint manifest override
- Default behavior: `scripts/contract-fixture-lint.sh` validates the repository's built-in fixture list.
- Optional override for isolated harness checks:
  - `CONTRACT_FIXTURE_LINT_MANIFEST=/tmp/fixtures.txt scripts/contract-fixture-lint.sh`
- Manifest format:
  - One fixture path per line.
  - Empty lines and `#` comment lines are ignored.
- Example manifest:
  - `tests/integration/fixtures/api_contract_golden.json`
  - `tests/integration/fixtures/cli_contract_command_golden.json`

## Metrics contract checks
- Golden metrics fixture: `tests/integration/fixtures/metrics_contract_golden.json`
- Contract suite: `go test ./tests/integration -run MetricsContract`
- Golden log/metrics mode fixture: `tests/integration/fixtures/log_metrics_contract_golden.json`
- Log/metrics mode suite: `go test ./tests/integration -run LogMetricsContract`
- Local query example: `curl -s http://127.0.0.1:8080/v1/metrics | jq .`
- Correlation fields:
  - `routes[].status_counts` tracks per-status response counts.
  - `routes[].recent_request_ids` captures recent request IDs for route-level trace correlation.
- Correlation workflow:
  1. Start service with `--metrics --request-logs`.
  2. Send request with `X-Request-Id: <id>`.
  3. Match `<id>` in request logs and `/v1/metrics` `recent_request_ids`.

## Service smoke checks
- Script: `scripts/contextd-smoke.sh`
- No-auth server:
  - `contextd serve --addr 127.0.0.1:8080`
  - `scripts/contextd-smoke.sh --base-url http://127.0.0.1:8080 --auth-mode none`
- Static auth server:
  - `contextd serve --static-token local-dev-token --addr 127.0.0.1:8080`
  - `scripts/contextd-smoke.sh --base-url http://127.0.0.1:8080 --auth-mode static --token local-dev-token`
- Managed auth server:
  - `contextd context token issue --label local --ttl 24h --output table`
  - `contextd serve --managed-auth --addr 127.0.0.1:8080 --metrics`
  - `scripts/contextd-smoke.sh --base-url http://127.0.0.1:8080 --auth-mode managed --token <issued-token> --metrics`
- In `static`/`managed` mode, smoke pre-checks mutating auth guard by asserting unauthenticated writes return `401`.
- Optional invalid-token negative check:
  - `scripts/contextd-smoke.sh --base-url http://127.0.0.1:8080 --auth-mode managed --token <valid-token> --assert-invalid-token`

## Make targets
- Run all tests: `make test`
- Run contract suites: `make contracts`
- Run smoke against active server: `make smoke BASE_URL=http://127.0.0.1:8080`
- Run smoke with auth token: `make smoke BASE_URL=http://127.0.0.1:8080 AUTH_MODE=static TOKEN=<token>`
- Run smoke with metrics check enabled: `make smoke BASE_URL=http://127.0.0.1:8080 AUTH_MODE=managed TOKEN=<token> METRICS=1`
- Run smoke with invalid-token precheck: `make smoke BASE_URL=http://127.0.0.1:8080 AUTH_MODE=managed TOKEN=<token> INVALID_TOKEN_CHECK=1`
- Smoke invalid-token preset: `make smoke-invalid-token BASE_URL=http://127.0.0.1:8080 AUTH_MODE=managed TOKEN=<token>`
- Contract fixture presence lint: `make contract-lint`
- AGENTS boot drift check: `make agents-boot-check`
- Run full local harness (boot + contracts + smoke + teardown):
  - `make e2e-local`
  - `AUTH_MODE=static make e2e-local`
  - `AUTH_MODE=managed make e2e-local`
- Managed-auth preset:
  - `make e2e-managed`

## Iteration run-log helper
- Script: `scripts/volon-runlog-init.sh`
- Example:
  - `scripts/volon-runlog-init.sh --iteration 47 --date 2026-02-25 --profile orchestrator`
- Make wrapper:
  - `make runlog-init ITERATION=47`
  - `make runlog-init ITERATION=47 RUN_DATE=2026-02-25 PROFILE=orchestrator RUNLOG_OUT=/tmp/runlog-047.md`
- Overwrite safety:
  - By default, existing run-log files are not overwritten.
  - Use script-level force only when intentional:
    - `scripts/volon-runlog-init.sh --iteration 47 --date 2026-02-25 --force`
- Bootstrap summary sync:
  - report only: `make bootstrap-report`
  - apply updates: `make bootstrap-sync`
  - explicit aliases: `make bootstrap-sync-report` and `make bootstrap-sync-apply`

## Boot profile drift checks
- Script: `scripts/agents-boot-drift-check.sh`
- Purpose: verify critical AGENTS/boot files exist and warn on AGENTS boot-reference drift.
- Default output path pattern:
  - `.agentrc/logs/run-iteration-<NNN>-<YYYY-MM-DD>.md`
- Quick validation sequence:
  1. `make contracts`
  2. Start `contextd serve ...`
  3. `make smoke BASE_URL=http://127.0.0.1:8080 TOKEN=<token>`

## Embedding smoke test (`cmd/smoke`)

A one-shot program that exercises the full embedding pipeline against the
live `~/.tesseract` store: opens `conduit.Open` with an OpenAI embedder and a
SQLite-backed queue, writes a memory revision, waits for the queue worker
to populate `embedding_model`/`embedding_vector`, then runs a
`RankingSimilarity` recall to confirm end-to-end correctness.

Use it to verify:
- `OPENAI_API_KEY` is reaching the process
- Queue + embed handler are wired
- `text-embedding-3-large` returns a vector (3072-dim) that round-trips
  through recall

Run:

```sh
set -a; source .env; set +a
go run ./cmd/smoke
```

Notes:
- Writes into `user/chrispian/project/conduit/memory`. Clean up test
  revisions periodically or change the namespace/key in the source.
- Requires the `conduit-api` cerberus service to be stopped OR relies on
  SQLite WAL concurrency — the smoke process opens the same `context.db`
  and `queue.db`. Running alongside `contextd serve` is safe for reads but
  avoid concurrent writers in MCP mode.
- Source: `cmd/smoke/main.go`.

## Repository conventions
- Product specifications: `docs/SPECS/`
- Architecture decisions: `docs/DECISIONS/`
- Agent artifacts: `artifacts/` and `outputs/`
- Volon runtime state: `.agentrc/`

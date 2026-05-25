# Contract Fixture Inventory

This index lists fixture files and their associated validation command.

| Fixture | Purpose | Test command |
|---|---|---|
| `tests/integration/fixtures/api_contract_golden.json` | Core API success response shape contracts | `go test ./tests/integration -run APIContract` |
| `tests/integration/fixtures/api_error_contract_golden.json` | API error envelope contracts (`code/message/details`) | `go test ./tests/integration -run APIErrorContract` |
| `tests/integration/fixtures/audit_contract_golden.json` | Audit endpoint pagination/filter contracts | `go test ./tests/integration -run AuditContract` |
| `tests/integration/fixtures/metrics_contract_golden.json` | Metrics endpoint route/totals contract | `go test ./tests/integration -run MetricsContract` |
| `tests/integration/fixtures/log_metrics_contract_golden.json` | Request-log mode and metrics correlation contracts | `go test ./tests/integration -run LogMetricsContract` |
| `tests/integration/fixtures/readiness_contract_golden.json` | Readiness status-tier payload contracts | `go test ./tests/integration -run ReadinessContract` |
| `tests/integration/fixtures/cli_health_summary_contract_golden.json` | CLI `context health --summary` contract | `go test ./tests/integration -run CLIHealthSummaryContract` |
| `tests/integration/fixtures/cli_audit_contract_golden.json` | CLI `context audit` cursor/filter JSON contract | `go test ./tests/integration -run CLIAuditContract` |
| `tests/integration/fixtures/cli_contract_command_golden.json` | CLI `context contract list/run` dry-mode contract | `go test ./tests/integration -run CLIContractCommand` |
| `tests/integration/fixtures/contract_list_default_output_golden.json` | CLI `context contract list` default-output JSON contract | `go test ./tests/integration -run ContractListDefaultOutputContract` |
| `tests/integration/fixtures/contract_list_invalid_output_error_golden.json` | CLI invalid contract list output-mode error contract | `go test ./tests/integration -run ContractListInvalidOutputErrorContract` |
| `tests/integration/fixtures/contract_list_table_golden.json` | CLI `context contract list --output table` contract | `go test ./tests/integration -run ContractListTableContract` |
| `tests/integration/fixtures/contract_list_table_count_parity_golden.json` | CLI contract list table-vs-json count parity contract | `go test ./tests/integration -run ContractListTableCountParityContract` |
| `tests/integration/fixtures/contract_run_all_default_output_golden.json` | CLI `context contract run --suite all` default-output JSON contract | `go test ./tests/integration -run ContractRunAllDefaultOutputContract` |
| `tests/integration/fixtures/contract_run_all_table_golden.json` | CLI `context contract run --suite all --output table` contract | `go test ./tests/integration -run ContractRunAllTableContract` |
| `tests/integration/fixtures/contract_run_all_execute_table_golden.json` | CLI `context contract run --suite all --execute --output table` contract | `go test ./tests/integration -run ContractRunAllExecuteTableContract` |
| `tests/integration/fixtures/contract_run_all_execute_json_golden.json` | CLI `context contract run --suite all --execute` JSON contract | `go test ./tests/integration -run ContractRunAllExecuteJSONContract` |
| `tests/integration/fixtures/contract_run_default_output_golden.json` | CLI `context contract run` default-output JSON contract | `go test ./tests/integration -run ContractRunDefaultOutputContract` |
| `tests/integration/fixtures/contract_run_table_dry_golden.json` | CLI `context contract run --output table` dry-mode contract | `go test ./tests/integration -run ContractRunTableDryContract` |
| `tests/integration/fixtures/contract_run_execute_table_golden.json` | CLI `context contract run --execute --output table` contract | `go test ./tests/integration -run ContractRunExecuteTableContract` |
| `tests/integration/fixtures/contract_run_execute_invalid_output_error_golden.json` | CLI invalid execute-mode output-mode error contract | `go test ./tests/integration -run ContractRunExecuteInvalidOutputErrorContract` |
| `tests/integration/fixtures/contract_run_invalid_output_error_golden.json` | CLI invalid contract run output-mode error contract | `go test ./tests/integration -run ContractRunInvalidOutputErrorContract` |
| `tests/integration/fixtures/contract_run_unknown_suite_error_golden.json` | CLI unknown contract suite error contract | `go test ./tests/integration -run ContractRunUnknownSuiteErrorContract` |
| `tests/integration/fixtures/make_contract_cli_list_contract_golden.json` | `make contract-cli-list` output marker contract | `go test ./tests/integration -run MakeContractCLIListContract` |
| `tests/integration/fixtures/make_contract_cli_run_contract_golden.json` | `make contract-cli-run SUITE=<name>` output marker contract | `go test ./tests/integration -run MakeContractCLIRunContract` |
| `tests/integration/fixtures/make_contract_commands_contract_golden.json` | `make contract-commands` suite-ordering wrapper contract | `go test ./tests/integration -run MakeContractCommandsContract` |
| `tests/integration/fixtures/contract_suite_commands_contract_golden.json` | `scripts/contract-suite-commands.sh` output ordering contract | `go test ./tests/integration -run ContractSuiteCommandsScript` |
| `tests/integration/fixtures/contract_suite_registry_parity_golden.json` | CLI suite registry and script catalog parity contract | `go test ./tests/integration -run ContractSuiteRegistryParity` |
| `tests/integration/fixtures/smoke_invalid_token_contract_golden.json` | Smoke invalid-token marker sequence contract | `go test ./tests/integration -run SmokeInvalidTokenContract` |
| `tests/integration/fixtures/make_smoke_invalid_token_contract_golden.json` | `make smoke-invalid-token` output marker contract | `go test ./tests/integration -run MakeSmokeInvalidTokenContract` |

## Make Fixture Index

| Fixture | Source suite | Refresh command |
|---|---|---|
| `tests/integration/fixtures/make_contract_cli_list_contract_golden.json` | `make contract-cli-list` | `go test ./tests/integration -run MakeContractCLIListContract` |
| `tests/integration/fixtures/make_contract_cli_run_contract_golden.json` | `make contract-cli-run SUITE=<name>` | `go test ./tests/integration -run MakeContractCLIRunContract` |
| `tests/integration/fixtures/make_contract_commands_contract_golden.json` | `make contract-commands` | `go test ./tests/integration -run MakeContractCommandsContract` |

## Suite Ordering Policy

- Canonical order is defined by `scripts/contract-suite-commands.sh`.
- `internal/contextcli/cli.go` `contractSuites()` must match the same suite sequence and commands.
- `tests/integration/fixtures/contract_suite_registry_parity_golden.json` must be updated in the same commit as ordering changes.

Update checklist:
- Update `scripts/contract-suite-commands.sh`.
- Update `internal/contextcli/cli.go` `contractSuites()`.
- Update `tests/integration/fixtures/contract_suite_commands_contract_golden.json`.
- Update `tests/integration/fixtures/cli_contract_command_golden.json`.
- Update `tests/integration/fixtures/contract_suite_registry_parity_golden.json`.
- Run `go test ./tests/integration -run ContractSuiteCommandsScript`.
- Run `go test ./tests/integration -run CLIContractCommand`.
- Run `go test ./tests/integration -run ContractSuiteRegistryParity`.
- Run `go test ./...`.

## Suite Naming Conventions

- `api` / `api-*`: HTTP API response and error envelope contracts.
- `cli-*`: direct CLI output/error contracts that do not require make wrappers.
- `make-*`: make-target wrapper contracts that validate command aliases and marker passthrough.
- `smoke-*`: end-to-end smoke checks and auth guard regressions.
- `*-script`: script-level contracts that validate helper script behavior directly.

When to add a new suite ID:
- Add a new suite when behavior has a distinct command path, output contract, or failure mode that should be independently selectable.
- Extend an existing suite when adding checks within the same command path and contract surface.
- Prefer stable, descriptive IDs over implementation details; avoid embedding dates/iteration numbers in suite names.

## CLI Error-Path Suite Matrix

| Suite | Intent | Targeted command |
|---|---|---|
| `cli-contract-list-invalid-output` | Verify invalid `context contract list --output` errors remain stable. | `go test ./tests/integration -run ContractListInvalidOutputErrorContract` |
| `cli-contract-run-execute-invalid-output` | Verify invalid `context contract run --execute --output` errors remain stable. | `go test ./tests/integration -run ContractRunExecuteInvalidOutputErrorContract` |
| `cli-contract-run-unknown-suite` | Verify unknown suite errors are stable and actionable. | `go test ./tests/integration -run ContractRunUnknownSuiteErrorContract` |
| `cli-contract-run-invalid-output` | Verify invalid `--output` mode errors remain stable. | `go test ./tests/integration -run ContractRunInvalidOutputErrorContract` |

## Guardrail Suite Stack

| Suite | Guardrail intent | Command |
|---|---|---|
| `contract-suite-commands-format` | Enforce `suite<TAB>command` line shape. | `go test ./tests/integration -run ContractSuiteCommandsFormatContract` |
| `contract-suite-commands-unique` | Prevent duplicate suite names. | `go test ./tests/integration -run ContractSuiteCommandsUniqueContract` |
| `contract-suite-commands-prefix` | Require integration-test command prefix validity. | `go test ./tests/integration -run ContractSuiteCommandsPrefixContract` |
| `contract-suite-commands-deterministic` | Enforce stable output ordering across repeated runs. | `go test ./tests/integration -run ContractSuiteCommandsDeterministicContract` |
| `contract-suite-commands-suffix` | Enforce `-count=1` command suffix consistency. | `go test ./tests/integration -run ContractSuiteCommandsSuffixContract` |
| `contract-suite-commands-token-count` | Enforce minimum command token structure (`go test ./tests/integration -run ...`). | `go test ./tests/integration -run ContractSuiteCommandsTokenCountContract` |
| `contract-suite-commands-non-empty` | Enforce non-empty script output with known suite marker presence. | `go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract` |

Recommended run order:
1. `go test ./tests/integration -run ContractSuiteCommandsFormatContract`
2. `go test ./tests/integration -run ContractSuiteCommandsUniqueContract`
3. `go test ./tests/integration -run ContractSuiteCommandsPrefixContract`
4. `go test ./tests/integration -run ContractSuiteCommandsDeterministicContract`
5. `go test ./tests/integration -run ContractSuiteCommandsSuffixContract`
6. `go test ./tests/integration -run ContractSuiteCommandsTokenCountContract`
7. `go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract`

## Default Output-Mode Suites

| Suite | Command behavior | Targeted command |
|---|---|---|
| `cli-contract-list-default-output` | `context contract list` defaults to JSON output. | `go test ./tests/integration -run ContractListDefaultOutputContract` |
| `cli-contract-run-default-output` | `context contract run --suite <name>` defaults to JSON dry mode (`executed=false`). | `go test ./tests/integration -run ContractRunDefaultOutputContract` |

See also: `docs/DEV.md#contract-run-mode-matrix`.

## Contract Run Mode Matrix

| Mode | Intent | Targeted command |
|---|---|---|
| `run-single-default-json` | Verify single-suite default dry JSON output. | `go test ./tests/integration -run ContractRunDefaultOutputContract` |
| `run-all-default-json` | Verify full-catalog default dry JSON output. | `go test ./tests/integration -run ContractRunAllDefaultOutputContract` |
| `run-single-dry-table` | Verify single-suite dry table output. | `go test ./tests/integration -run ContractRunTableDryContract` |
| `run-all-dry-table` | Verify full-catalog dry table output. | `go test ./tests/integration -run ContractRunAllTableContract` |
| `run-single-execute-table` | Verify single-suite execute table output. | `go test ./tests/integration -run ContractRunExecuteTableContract` |
| `run-all-execute-table` | Verify full-catalog execute table output. | `go test ./tests/integration -run ContractRunAllExecuteTableContract` |
| `run-all-execute-json` | Verify full-catalog execute JSON summary and per-item `ok` values. | `go test ./tests/integration -run ContractRunAllExecuteJSONContract` |
| `run-table-header-guardrail` | Verify table header token stability in dry and execute modes. | `go test ./tests/integration -run ContractRunTableHeaderContract` |

See also: `docs/DEV.md#contract-run-mode-matrix`.

## List/Run Parity Suites

| Suite | Parity intent | Targeted command |
|---|---|---|
| `cli-contract-list-table-count-parity` | Ensure `contract list --output table` row count matches JSON list item count. | `go test ./tests/integration -run ContractListTableCountParityContract` |
| `cli-contract-list-count-parity` | Ensure `contract list` JSON `count` equals `len(items)`. | `go test ./tests/integration -run ContractListCountParityContract` |
| `cli-contract-run-all-default-output` | Ensure `contract run --suite all` item count aligns with list suite count. | `go test ./tests/integration -run ContractRunAllDefaultOutputContract` |
| `cli-contract-run-all-table` | Ensure `contract run --suite all --output table` row count matches JSON run item count. | `go test ./tests/integration -run ContractRunAllTableContract` |

See also: `docs/DEV.md#contract-quick-run-pack`.

## Full validation

- `go test ./...`
- `make contract-lint`

## Quickstart

1. `make contract-lint`
2. `go test ./tests/integration -run APIContract`
3. `go test ./tests/integration -run CLIContractCommand`
4. `go test ./tests/integration -run SmokeInvalidTokenContract`
5. `go test ./...`

## Script-Based Suites

- Smoke and make wrappers:
  - `go test ./tests/integration -run SmokeInvalidTokenContract`
  - `go test ./tests/integration -run MakeSmokeInvalidTokenContract`
  - `go test ./tests/integration -run MakeContractCLIListContract`
  - `go test ./tests/integration -run MakeContractCLIRunContract`
  - `go test ./tests/integration -run MakeContractCommandsContract`
- Script command stability:
  - `go test ./tests/integration -run ContractSuiteCommandsScript`
  - `go test ./tests/integration -run ContractSuiteRegistryParity`
  - `go test ./tests/integration -run ContractListDefaultOutputContract`
  - `go test ./tests/integration -run ContractListDefaultOutputDeterministicContract`
  - `go test ./tests/integration -run ContractListCountParityContract`
  - `go test ./tests/integration -run ContractListInvalidOutputErrorContract`
  - `go test ./tests/integration -run ContractListTableContract`
  - `go test ./tests/integration -run ContractListTableHeaderContract`
  - `go test ./tests/integration -run ContractListTableCountParityContract`
  - `go test ./tests/integration -run ContractRunAllDefaultOutputContract`
  - `go test ./tests/integration -run ContractRunAllTableContract`
  - `go test ./tests/integration -run ContractRunAllExecuteTableContract`
  - `go test ./tests/integration -run ContractRunAllExecuteJSONContract`
  - `go test ./tests/integration -run ContractRunDefaultOutputContract`
  - `go test ./tests/integration -run ContractRunTableDryContract`
  - `go test ./tests/integration -run ContractRunTableHeaderContract`
  - `go test ./tests/integration -run ContractRunExecuteTableContract`
  - `go test ./tests/integration -run ContractRunExecuteInvalidOutputErrorContract`
  - `go test ./tests/integration -run ContractRunInvalidOutputErrorContract`
  - `go test ./tests/integration -run ContractRunUnknownSuiteErrorContract`
  - `go test ./tests/integration -run ContractSuiteCommandsFormatContract`
  - `go test ./tests/integration -run ContractSuiteCommandsUniqueContract`
  - `go test ./tests/integration -run ContractSuiteCommandsPrefixContract`
  - `go test ./tests/integration -run ContractSuiteCommandsDeterministicContract`
  - `go test ./tests/integration -run ContractSuiteCommandsSuffixContract`
  - `go test ./tests/integration -run ContractSuiteCommandsTokenCountContract`
  - `go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract`
  - `go test ./tests/integration -run ContractFixtureLintScript`
- Lint-script contract (manifest override behavior):
  - `go test ./tests/integration -run ContractFixtureLintScript`

## Ownership And Update Policy

- Ownership:
  - API/runtime fixture changes: reviewers for `internal/contextapi/` and `internal/contextstore/`.
  - CLI fixture changes: reviewers for `internal/contextcli/`.
  - Smoke/workflow fixture changes: reviewers for `scripts/` and `.agentrc/` workflow docs.
- Update policy:
  - Any fixture change must be paired with the corresponding test change and rationale in the same commit.
  - If output/shape changes are intentional, update fixture first, then tests, then docs references.
  - If fixture changes are unintentional, treat as regression and fix code/tests before merge.
- Review expectations:
  - Validate fixture file remains non-empty and listed in `scripts/contract-fixture-lint.sh`.
  - Run targeted suite(s) for modified fixture and then `go test ./...`.

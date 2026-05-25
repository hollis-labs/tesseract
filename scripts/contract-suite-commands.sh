#!/usr/bin/env bash
set -euo pipefail

cat <<'CMDS'
api	go test ./tests/integration -run APIContract -count=1
api-errors	go test ./tests/integration -run APIErrorContract -count=1
audit	go test ./tests/integration -run AuditContract -count=1
metrics	go test ./tests/integration -run MetricsContract -count=1
log-metrics	go test ./tests/integration -run LogMetricsContract -count=1
readiness	go test ./tests/integration -run ReadinessContract -count=1
cli-health-summary	go test ./tests/integration -run CLIHealthSummaryContract -count=1
cli-audit	go test ./tests/integration -run CLIAuditContract -count=1
cli-contract-list-default-output	go test ./tests/integration -run ContractListDefaultOutputContract -count=1
cli-contract-list-deterministic-order	go test ./tests/integration -run ContractListDefaultOutputDeterministicContract -count=1
cli-contract-list-count-parity	go test ./tests/integration -run ContractListCountParityContract -count=1
cli-contract-list-invalid-output	go test ./tests/integration -run ContractListInvalidOutputErrorContract -count=1
cli-contract-list-table	go test ./tests/integration -run ContractListTableContract -count=1
cli-contract-list-table-header	go test ./tests/integration -run ContractListTableHeaderContract -count=1
cli-contract-list-table-count-parity	go test ./tests/integration -run ContractListTableCountParityContract -count=1
cli-contract-run-all-default-output	go test ./tests/integration -run ContractRunAllDefaultOutputContract -count=1
cli-contract-run-all-table	go test ./tests/integration -run ContractRunAllTableContract -count=1
cli-contract-run-all-execute-table	go test ./tests/integration -run ContractRunAllExecuteTableContract -count=1
cli-contract-run-all-execute-json	go test ./tests/integration -run ContractRunAllExecuteJSONContract -count=1
cli-contract-run-default-output	go test ./tests/integration -run ContractRunDefaultOutputContract -count=1
cli-contract-run-dry-table	go test ./tests/integration -run ContractRunTableDryContract -count=1
cli-contract-run-table-header	go test ./tests/integration -run ContractRunTableHeaderContract -count=1
cli-contract-run-table	go test ./tests/integration -run ContractRunExecuteTableContract -count=1
cli-contract-run-invalid-output	go test ./tests/integration -run ContractRunInvalidOutputErrorContract -count=1
cli-contract-run-execute-invalid-output	go test ./tests/integration -run ContractRunExecuteInvalidOutputErrorContract -count=1
cli-contract-run-unknown-suite	go test ./tests/integration -run ContractRunUnknownSuiteErrorContract -count=1
smoke-invalid-token	go test ./tests/integration -run SmokeInvalidTokenContract -count=1
make-contract-cli-list	go test ./tests/integration -run MakeContractCLIListContract -count=1
make-contract-cli-run	go test ./tests/integration -run MakeContractCLIRunContract -count=1
make-smoke-invalid-token	go test ./tests/integration -run MakeSmokeInvalidTokenContract -count=1
contract-suite-commands-format	go test ./tests/integration -run ContractSuiteCommandsFormatContract -count=1
contract-suite-commands-unique	go test ./tests/integration -run ContractSuiteCommandsUniqueContract -count=1
contract-suite-commands-prefix	go test ./tests/integration -run ContractSuiteCommandsPrefixContract -count=1
contract-suite-commands-deterministic	go test ./tests/integration -run ContractSuiteCommandsDeterministicContract -count=1
contract-suite-commands-suffix	go test ./tests/integration -run ContractSuiteCommandsSuffixContract -count=1
contract-suite-commands-token-count	go test ./tests/integration -run ContractSuiteCommandsTokenCountContract -count=1
contract-suite-commands-non-empty	go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract -count=1
fixture-lint-script	go test ./tests/integration -run ContractFixtureLintScript -count=1
CMDS

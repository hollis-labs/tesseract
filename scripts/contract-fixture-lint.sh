#!/usr/bin/env bash
set -euo pipefail

fixtures=(
  "tests/integration/fixtures/api_contract_golden.json"
  "tests/integration/fixtures/api_error_contract_golden.json"
  "tests/integration/fixtures/audit_contract_golden.json"
  "tests/integration/fixtures/metrics_contract_golden.json"
  "tests/integration/fixtures/log_metrics_contract_golden.json"
  "tests/integration/fixtures/readiness_contract_golden.json"
  "tests/integration/fixtures/cli_health_summary_contract_golden.json"
  "tests/integration/fixtures/cli_audit_contract_golden.json"
  "tests/integration/fixtures/cli_contract_command_golden.json"
  "tests/integration/fixtures/contract_list_default_output_golden.json"
  "tests/integration/fixtures/contract_list_invalid_output_error_golden.json"
  "tests/integration/fixtures/contract_list_table_golden.json"
  "tests/integration/fixtures/contract_list_table_count_parity_golden.json"
  "tests/integration/fixtures/contract_run_all_default_output_golden.json"
  "tests/integration/fixtures/contract_run_all_table_golden.json"
  "tests/integration/fixtures/contract_run_all_execute_table_golden.json"
  "tests/integration/fixtures/contract_run_all_execute_json_golden.json"
  "tests/integration/fixtures/contract_run_default_output_golden.json"
  "tests/integration/fixtures/contract_run_table_dry_golden.json"
  "tests/integration/fixtures/contract_run_execute_table_golden.json"
  "tests/integration/fixtures/contract_run_execute_invalid_output_error_golden.json"
  "tests/integration/fixtures/contract_run_invalid_output_error_golden.json"
  "tests/integration/fixtures/contract_run_unknown_suite_error_golden.json"
  "tests/integration/fixtures/make_contract_cli_list_contract_golden.json"
  "tests/integration/fixtures/make_contract_cli_run_contract_golden.json"
  "tests/integration/fixtures/make_contract_commands_contract_golden.json"
  "tests/integration/fixtures/make_bootstrap_report_contract_golden.json"
  "tests/integration/fixtures/make_bootstrap_sync_contract_golden.json"
  "tests/integration/fixtures/make_bootstrap_sync_alias_contract_golden.json"
  "tests/integration/fixtures/contract_suite_commands_contract_golden.json"
  "tests/integration/fixtures/contract_suite_registry_parity_golden.json"
  "tests/integration/fixtures/smoke_invalid_token_contract_golden.json"
  "tests/integration/fixtures/make_smoke_invalid_token_contract_golden.json"
  "tests/integration/fixtures/runlog_helper_contract_golden.json"
  "tests/integration/fixtures/bootstrap_sync_contract_golden.json"
  "tests/integration/fixtures/bootstrap_sync_no_log_contract_golden.json"
  "tests/integration/fixtures/bootstrap_sync_idempotent_contract_golden.json"
  "tests/integration/fixtures/agents_boot_check_contract_golden.json"
)

if [[ -n "${CONTRACT_FIXTURE_LINT_MANIFEST:-}" ]]; then
  fixtures=()
  while IFS= read -r line || [[ -n "$line" ]]; do
    trimmed="$(echo "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    if [[ -z "$trimmed" || "$trimmed" == \#* ]]; then
      continue
    fi
    fixtures+=("$trimmed")
  done < "$CONTRACT_FIXTURE_LINT_MANIFEST"
fi

missing=0
for file in "${fixtures[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "missing fixture: $file" >&2
    missing=1
    continue
  fi
  if [[ ! -s "$file" ]]; then
    echo "empty fixture: $file" >&2
    missing=1
  fi
done

if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

echo "fixture lint ok (${#fixtures[@]} files)"

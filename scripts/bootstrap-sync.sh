#!/usr/bin/env bash
set -euo pipefail

BOOTSTRAP_PATH=".agentrc/bootstrap.md"
TASKS_DIR=".agentrc/tasks"
LOGS_DIR=".agentrc/logs"
APPLY=0

usage() {
  cat <<USAGE
Usage: $(basename "$0") [--apply]

Reports derived task counts and latest run-log path.
With --apply, updates .agentrc/bootstrap.md Current state summary lines.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply)
      APPLY=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ ! -f "$BOOTSTRAP_PATH" ]]; then
  echo "missing bootstrap file: $BOOTSTRAP_PATH" >&2
  exit 1
fi
if [[ ! -d "$TASKS_DIR" ]]; then
  echo "missing tasks dir: $TASKS_DIR" >&2
  exit 1
fi

todo=$( (rg -n '^status:\s+todo$' "$TASKS_DIR" || true) | wc -l | tr -d ' ' )
doing=$( (rg -n '^status:\s+doing$' "$TASKS_DIR" || true) | wc -l | tr -d ' ' )
blocked=$( (rg -n '^status:\s+blocked$' "$TASKS_DIR" || true) | wc -l | tr -d ' ' )
done_count=$( (rg -n '^status:\s+done$' "$TASKS_DIR" || true) | wc -l | tr -d ' ' )

latest_log=""
if compgen -G "$LOGS_DIR/run-iteration-*.md" > /dev/null; then
  latest_file=$(ls -1 "$LOGS_DIR"/run-iteration-*.md | sort | tail -n1)
  latest_log=".agentrc/logs/$(basename "$latest_file")"
fi

echo "todo=$todo doing=$doing blocked=$blocked done=$done_count"
if [[ -n "$latest_log" ]]; then
  echo "latest_log=$latest_log"
else
  echo "latest_log=<none>"
fi

if [[ "$APPLY" -ne 1 ]]; then
  exit 0
fi

tasks_line="- Tasks: ${todo} todo, ${blocked} blocked, ${done_count} done"
if [[ -n "$latest_log" ]]; then
  log_line="- Last run log: \`${latest_log}\`"
else
  log_line="- Last run log: \`<none>\`"
fi
updated_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

BOOTSTRAP_TASKS_LINE="$tasks_line" \
BOOTSTRAP_LOG_LINE="$log_line" \
BOOTSTRAP_UPDATED_AT="$updated_at" \
perl -0777 -i -pe '
  s/^- Tasks: .*$/$ENV{BOOTSTRAP_TASKS_LINE}/m;
  s/^- Last run log: .*$/$ENV{BOOTSTRAP_LOG_LINE}/m;
  s/^updated_at: .*$/updated_at: $ENV{BOOTSTRAP_UPDATED_AT}/m;
' "$BOOTSTRAP_PATH"

echo "applied bootstrap summary updates"

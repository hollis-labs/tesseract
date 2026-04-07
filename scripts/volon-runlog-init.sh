#!/usr/bin/env bash
set -euo pipefail

ITERATION=""
RUN_DATE="$(date +%F)"
PROFILE="orchestrator"
OUT_PATH=""
FORCE=0

usage() {
  cat <<USAGE
Usage: $(basename "$0") --iteration N [--date YYYY-MM-DD] [--profile NAME] [--out PATH] [--force]

Generates a run-log markdown skeleton matching .agentrc/logs conventions.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iteration)
      ITERATION="$2"
      shift 2
      ;;
    --date)
      RUN_DATE="$2"
      shift 2
      ;;
    --profile)
      PROFILE="$2"
      shift 2
      ;;
    --out)
      OUT_PATH="$2"
      shift 2
      ;;
    --force)
      FORCE=1
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

if [[ -z "$ITERATION" ]]; then
  echo "--iteration is required" >&2
  exit 2
fi
if ! [[ "$ITERATION" =~ ^[0-9]+$ ]]; then
  echo "--iteration must be a positive integer" >&2
  exit 2
fi
if ! [[ "$RUN_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  echo "--date must be YYYY-MM-DD" >&2
  exit 2
fi

if [[ -z "$OUT_PATH" ]]; then
  OUT_PATH=".agentrc/logs/run-iteration-$(printf '%03d' "$ITERATION")-${RUN_DATE}.md"
fi

mkdir -p "$(dirname "$OUT_PATH")"
if [[ -f "$OUT_PATH" && "$FORCE" -ne 1 ]]; then
  echo "output exists: $OUT_PATH (use --force to overwrite)" >&2
  exit 1
fi

cat > "$OUT_PATH" <<EOF_LOG
---
type: run-log
iteration: $((10#$ITERATION))
date: ${RUN_DATE}
profile: ${PROFILE}
---

# Iteration $((10#$ITERATION)) Run Log

## Objectives
- 

## Completed
### TASK-
- 

## Verification
- 

## Next Action
-
EOF_LOG

echo "$OUT_PATH"

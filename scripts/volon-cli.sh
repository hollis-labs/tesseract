#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FORGE_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

usage() {
  cat <<EOF
Usage: volon-cli [--repo PATH] [additional claude args...]

Optional environment variables:
  CLAUDE_BIN     Override Claude binary (default: 'claude')
  FORGE_NO_SYNC  Set to 1 to skip git fetch/pull before launching
  FORGE_AGENT_PROFILE  Default agent profile for this run (architect|orchestrator|worker|reviewer)

Flags:
  --agent NAME   Override FORGE_AGENT_PROFILE for this invocation
  --prompt-text TEXT  Inject extra boot instructions (forwarded to the Claude CLI)
  --prompt-file PATH  Inject a file's contents at boot (forwarded to the Claude CLI)
EOF
}

# Allow overriding the Claude binary (defaults to 'claude').
CLAUDE_BIN="${CLAUDE_BIN:-claude}"

TARGET_REPO=""
CLAUDE_ARGS=()
AGENT_PROFILE="${FORGE_AGENT_PROFILE:-}"
PROMPT_TEXT=""
PROMPT_FILE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      [[ $# -lt 2 ]] && { echo >&2 "[volon-cli] --repo needs a path"; usage; exit 1; }
      TARGET_REPO="$(cd "$2" && pwd)"
      shift 2
      ;;
    --agent)
      [[ $# -lt 2 ]] && { echo >&2 "[volon-cli] --agent needs a profile name"; usage; exit 1; }
      AGENT_PROFILE="$2"
      shift 2
      ;;
    --prompt-text)
      [[ $# -lt 2 ]] && { echo >&2 "[volon-cli] --prompt-text needs a value"; usage; exit 1; }
      PROMPT_TEXT="$2"
      shift 2
      ;;
    --prompt-file)
      [[ $# -lt 2 ]] && { echo >&2 "[volon-cli] --prompt-file needs a path"; usage; exit 1; }
      PROMPT_FILE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      CLAUDE_ARGS+=("$@")
      break
      ;;
    *)
      CLAUDE_ARGS+=("$1")
      shift
      ;;
  esac
done

# Toggle with FORGE_NO_SYNC=1 to skip pulling latest changes.
if [[ "${FORGE_NO_SYNC:-0}" != "1" ]]; then
  echo "[volon-cli] Resyncing Volon repo at ${FORGE_ROOT}..."
  git -C "${FORGE_ROOT}" fetch --prune
  git -C "${FORGE_ROOT}" pull --ff-only
fi

PLUGIN_DIRS=(
  "${FORGE_ROOT}/plugins/core"
  "${FORGE_ROOT}/plugins/workflows"
  "${FORGE_ROOT}/plugins/git"
  "${FORGE_ROOT}/plugins/tasks-nanite"
  "${FORGE_ROOT}/plugins/docsmith"
  "${FORGE_ROOT}/plugins/quality"
  "${FORGE_ROOT}/plugins/backlog"
  "${FORGE_ROOT}/plugins/workflow-author"
  "${FORGE_ROOT}/plugins/prompt-volon"
)

CMD=("${CLAUDE_BIN}")
for dir in "${PLUGIN_DIRS[@]}"; do
  CMD+=("--plugin-dir" "${dir}")
done

# Pass any additional args (e.g., --model ...), already parsed.
if ((${#CLAUDE_ARGS[@]})); then
  CMD+=("${CLAUDE_ARGS[@]}")
fi
if [[ -n "${PROMPT_TEXT}" ]]; then
  CMD+=("--prompt-text" "${PROMPT_TEXT}")
fi
if [[ -n "${PROMPT_FILE}" ]]; then
  CMD+=("--prompt-file" "${PROMPT_FILE}")
fi

if [[ -n "${TARGET_REPO}" ]]; then
  echo "[volon-cli] Working directory: ${TARGET_REPO}"
  cd "${TARGET_REPO}"
fi

if [[ -n "${AGENT_PROFILE:-}" ]]; then
  export FORGE_AGENT_PROFILE="${AGENT_PROFILE}"
fi

if [[ -n "${FORGE_AGENT_PROFILE:-}" ]]; then
  echo "[volon-cli] Agent profile: ${FORGE_AGENT_PROFILE}"
fi

echo "[volon-cli] Launching (pwd=$(pwd)): ${CMD[*]}"
exec "${CMD[@]}"

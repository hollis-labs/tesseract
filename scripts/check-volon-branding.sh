#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESULT=$(cd "${ROOT}" && rg -n '\\bForge\\b' --glob '!:volon/**' --glob '!:volon/logs/**' --glob '!:volon/tasks/**' --glob '!tasks/**' --glob '!docs/volon-dev.md' || true)
if [[ -n "${RESULT}" ]]; then
  echo "[branding-check] Found forbidden 'Forge' references:" >&2
  echo "${RESULT}" >&2
  exit 1
fi
RESULT=$(cd "${ROOT}" && rg -n '\\bforge\\b' --glob '!:volon/**' --glob '!:volon/logs/**' --glob '!:volon/tasks/**' --glob '!tasks/**' || true)
if [[ -n "${RESULT}" ]]; then
  echo "[branding-check] Found forbidden 'forge' references:" >&2
  echo "${RESULT}" >&2
  exit 1
fi
echo "[branding-check] OK"

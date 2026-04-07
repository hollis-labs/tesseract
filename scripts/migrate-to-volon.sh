#!/usr/bin/env bash

set -euo pipefail

REPO="${1:-.}"
REPO="$(cd "${REPO}" && pwd)"

move_file() {
  local src="$1"
  local dst="$2"
  if [[ -e "${src}" ]]; then
    if [[ -e "${dst}" ]]; then
      echo "[migrate] Skipping ${src} → ${dst}: destination already exists" >&2
    else
      echo "[migrate] Moving ${src} → ${dst}"
      mv "${src}" "${dst}"
    fi
  fi
}

echo "[migrate] Repo: ${REPO}"

move_file "${REPO}/forge.yaml" "${REPO}/agentrc.yaml"
move_file "${REPO}/.forge" "${REPO}/.agentrc"

echo "[migrate] Done. Review agentrc.yaml + .agentrc/, then rerun scripts/volon-cli.sh /bootstrap-update."

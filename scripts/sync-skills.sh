#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
SKILLS_DIR="${HOME}/.claude/skills"

usage() {
  cat <<'EOF'
Usage: scripts/sync-skills.sh [--source PATH]

Scans plugins/*/skills/ in SOURCE and syncs symlinks into ~/.claude/skills/.
Creates missing symlinks, updates changed ones, and removes stale volon-managed
symlinks (those whose targets match */plugins/*/skills/*).

Options:
  --source PATH   Volon repo root to scan (default: this script's repo root)
  -h, --help      Show this help text
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source)
      [[ $# -lt 2 ]] && { echo "[volon] --source requires a path" >&2; usage; exit 1; }
      SOURCE_DIR="$(cd "$2" && pwd)"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "[volon] Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

mkdir -p "${SKILLS_DIR}"

# Discover all skills in source plugins
declare -A EXPECTED
for skill_dir in "${SOURCE_DIR}"/plugins/*/skills/*/; do
  [[ -d "${skill_dir}" ]] || continue
  skill_name="$(basename "${skill_dir%/}")"
  EXPECTED["${skill_name}"]="${skill_dir%/}"
done

created=0
updated=0
removed=0
skipped=0

# Create or update symlinks
for skill_name in "${!EXPECTED[@]}"; do
  source_path="${EXPECTED[${skill_name}]}"
  link_path="${SKILLS_DIR}/${skill_name}"

  if [[ -L "${link_path}" ]]; then
    current_target="$(readlink "${link_path}")"
    if [[ "${current_target}" == "${source_path}" ]]; then
      : # Already correct, nothing to do
    else
      ln -sfn "${source_path}" "${link_path}"
      echo "[volon] Updated:  ${skill_name}"
      echo "          ${current_target}"
      echo "       -> ${source_path}"
      (( updated++ )) || true
    fi
  elif [[ -e "${link_path}" ]]; then
    echo "[volon] Skipped:  ${skill_name} (exists but is not a symlink — manage manually)"
    (( skipped++ )) || true
  else
    ln -s "${source_path}" "${link_path}"
    echo "[volon] Created:  ${skill_name} -> ${source_path}"
    (( created++ )) || true
  fi
done

# Remove stale volon-managed symlinks (target matches */plugins/*/skills/*)
for link_path in "${SKILLS_DIR}"/*/; do
  link_path="${link_path%/}"
  [[ -L "${link_path}" ]] || continue
  skill_name="$(basename "${link_path}")"
  link_target="$(readlink "${link_path}")"

  # Only touch symlinks that point into a volon plugins tree
  if [[ "${link_target}" == */plugins/*/skills/* ]] && [[ -z "${EXPECTED[${skill_name}]+x}" ]]; then
    rm "${link_path}"
    echo "[volon] Removed:  ${skill_name} (stale — no longer in plugins)"
    (( removed++ )) || true
  fi
done

echo ""
echo "[volon] Skills sync complete — created: ${created}, updated: ${updated}, removed: ${removed}, skipped: ${skipped}"
echo "[volon] Skills dir: ${SKILLS_DIR}"

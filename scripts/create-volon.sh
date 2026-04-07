#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FORGE_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DEFAULT_TARGET="${HOME}/Projects-apps/volon"
TARGET_PATH="${DEFAULT_TARGET}"
RELEASE_TAG=""

usage() {
  cat <<'EOF'
Usage: scripts/create-volon.sh [--target PATH] [--release-tag TAG]

Copies the current Volon repo into a sanitized Volon template suitable for
other projects. Existing contents at the target path will be removed.

Options:
  --target PATH     Destination directory (default: ~/Projects-apps/volon)
  --release-tag TAG Create an annotated git tag named TAG and generate a tarball
                    at <target>/dist/volon-TAG.tar.gz
  -h, --help        Show this help text
EOF
}

expand_path() {
  local input="$1"
  if [[ "${input}" == "~/"* ]]; then
    printf '%s/%s' "${HOME}" "${input#~/}"
  elif [[ "${input}" == "~" ]]; then
    printf '%s' "${HOME}"
  else
    printf '%s' "${input}"
  fi
}

require_bin() {
  local bin="$1"
  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "[volon] Missing required command: ${bin}" >&2
    exit 1
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      [[ $# -lt 2 ]] && { echo "[volon] --target requires a path" >&2; usage; exit 1; }
      TARGET_PATH="$2"
      shift 2
      ;;
    --release-tag)
      [[ $# -lt 2 ]] && { echo "[volon] --release-tag requires a value" >&2; usage; exit 1; }
      RELEASE_TAG="$2"
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

require_bin rsync
require_bin git

TARGET_PATH="$(expand_path "${TARGET_PATH}")"
TARGET_PATH="${TARGET_PATH%/}"

if [[ -z "${TARGET_PATH}" ]]; then
  echo "[volon] Target path cannot be empty" >&2
  exit 1
fi

if [[ "${TARGET_PATH}" == "/" ]]; then
  echo "[volon] Refusing to use '/' as target" >&2
  exit 1
fi

if [[ "${TARGET_PATH}" == "${FORGE_ROOT}" ]]; then
  echo "[volon] Target cannot be the Volon repo itself" >&2
  exit 1
fi

PARENT_DIR="$(dirname "${TARGET_PATH}")"
mkdir -p "${PARENT_DIR}"

if [[ -d "${TARGET_PATH}" ]]; then
  echo "[volon] Removing existing target directory: ${TARGET_PATH}"
  rm -rf "${TARGET_PATH}"
fi

mkdir -p "${TARGET_PATH}"

RSYNC_EXCLUDES=(
  ".git/"
  ".gitmodules"
  ".worktrees/"
  ".claude/"
  ".DS_Store"
  "todo.db"
  "todo.db-wal"
  "todo.db-shm"
  ".cache/"
  "volon"
  "forge"
  ".agentrc/tasks/"
  ".agentrc/backlog/"
  ".agentrc/logs/"
  ".agentrc/pcc/"
  ".agentrc/bootstrap.md"
  ".agentrc/bootstrap/"
  ".agentrc/state/"
  ".agentrc/.gocache/"
  ".agentrc/.gomodcache/"
  ".agentrc/agents/"
  "tasks/"
  "artifacts/plan/"
  "artifacts/spec/"
)

RSYNC_ARGS=()
for pattern in "${RSYNC_EXCLUDES[@]}"; do
  RSYNC_ARGS+=(--exclude "${pattern}")
done

echo "[volon] Copying Volon repo to ${TARGET_PATH}..."
rsync -a "${RSYNC_ARGS[@]}" "${FORGE_ROOT}/" "${TARGET_PATH}/"

if [[ -d "${TARGET_PATH}/.git" ]]; then
  rm -rf "${TARGET_PATH}/.git"
fi

find "${TARGET_PATH}" -name '.DS_Store' -delete

echo "[volon] Initializing git repository..."
if git -C "${TARGET_PATH}" init -b main >/dev/null 2>&1; then
  :
else
  git -C "${TARGET_PATH}" init >/dev/null
  git -C "${TARGET_PATH}" symbolic-ref HEAD refs/heads/main >/dev/null 2>&1 || true
fi

git -C "${TARGET_PATH}" add -A
git -C "${TARGET_PATH}" -c user.name="Volon Template" \
  -c user.email="volon-template@example.com" \
  commit -m "Initial Volon template" >/dev/null

ARCHIVE_PATH=""
if [[ -n "${RELEASE_TAG}" ]]; then
  echo "[volon] Creating release tag ${RELEASE_TAG}..."
  git -C "${TARGET_PATH}" tag -a "${RELEASE_TAG}" -m "Release ${RELEASE_TAG}"
  ARCHIVE_DIR="$(dirname "${TARGET_PATH}")/volon-release-artifacts"
  mkdir -p "${ARCHIVE_DIR}"
  ARCHIVE_PATH="${ARCHIVE_DIR}/volon-${RELEASE_TAG}.tar.gz"
  git -C "${TARGET_PATH}" archive --format=tar.gz --output "${ARCHIVE_PATH}" "${RELEASE_TAG}"
fi

STATUS_OUTPUT="$(git -C "${TARGET_PATH}" status --short)"
if [[ -n "${STATUS_OUTPUT}" ]]; then
  echo "[volon] Warning: git status not clean:"
  echo "${STATUS_OUTPUT}"
  exit 1
fi

cat <<EOF
[volon] Template ready at: ${TARGET_PATH}
[volon] Exclusions applied:
$(printf '  - %s\n' "${RSYNC_EXCLUDES[@]}")
[volon] Next steps:
  1. cd ${TARGET_PATH}
  2. scripts/volon-cli.sh /bootstrap-update
  3. git remote add origin git@github.com:hollis-labs/volon.git (optional)
EOF

if [[ -n "${RELEASE_TAG}" ]]; then
  cat <<EOF
[volon] Release artifacts:
  - tag: ${RELEASE_TAG}
  - archive: ${ARCHIVE_PATH}
EOF
fi

echo ""
echo "[volon] Syncing skills to ~/.claude/skills/..."
bash "${TARGET_PATH}/scripts/sync-skills.sh" --source "${TARGET_PATH}"

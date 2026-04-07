#!/usr/bin/env bash
set -euo pipefail

AUTH_MODE="${AUTH_MODE:-noauth}" # noauth|static|managed
BASE_URL="${BASE_URL:-http://127.0.0.1:18092}"
ENABLE_METRICS="${ENABLE_METRICS:-1}"
STATIC_TOKEN="${STATIC_TOKEN:-local-dev-token}"

root_dir="$(mktemp -d)"
log_file="$(mktemp)"

cleanup() {
  if [[ -n "${pid:-}" ]]; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$root_dir" "$log_file"
}
trap cleanup EXIT

host_port="${BASE_URL#http://}"
serve_args=(serve --addr "$host_port")
smoke_args=(smoke BASE_URL="$BASE_URL" AUTH_MODE="$AUTH_MODE")

if [[ "$ENABLE_METRICS" == "1" ]]; then
  serve_args+=(--metrics)
  smoke_args+=(METRICS=1)
fi

case "$AUTH_MODE" in
  noauth)
    ;;
  static)
    serve_args+=(--static-token "$STATIC_TOKEN")
    smoke_args+=(TOKEN="$STATIC_TOKEN")
    ;;
  managed)
    issue_out="$(CONTEXTD_ROOT="$root_dir" go run ./cmd/contextd context token issue --label e2e-local --ttl 1h --output json)"
    managed_token="$(printf '%s' "$issue_out" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
    if [[ -z "$managed_token" ]]; then
      echo "failed to issue managed token: $issue_out" >&2
      exit 1
    fi
    serve_args+=(--managed-auth)
    smoke_args+=(TOKEN="$managed_token")
    ;;
  *)
    echo "unsupported AUTH_MODE: $AUTH_MODE" >&2
    exit 2
    ;;
esac

CONTEXTD_ROOT="$root_dir" go run ./cmd/contextd "${serve_args[@]}" >"$log_file" 2>&1 &
pid=$!
sleep 1

make contracts
make "${smoke_args[@]}"

echo "e2e-local ok (AUTH_MODE=$AUTH_MODE BASE_URL=$BASE_URL ENABLE_METRICS=$ENABLE_METRICS)"

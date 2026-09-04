#!/usr/bin/env bash
set -euo pipefail

AUTH_MODE="${AUTH_MODE:-none}" # none|static|managed
BASE_URL="${BASE_URL:-http://127.0.0.1:18092}"
ENABLE_METRICS="${ENABLE_METRICS:-1}"
STATIC_TOKEN="${STATIC_TOKEN:-local-dev-token}"

root_dir="$(mktemp -d)"
root_dir="$(cd "$root_dir" && pwd -P)"
log_file="$(mktemp)"
binary="$root_dir/tesseract-e2e"

# go-apppaths has no single "one base for everything" knob. Clear every direct
# path/benchmark override, then place all four XDG roots beneath this run's
# temporary directory. Provider keys are cleared so the smoke cannot make an
# accidental external request.
isolated_env=(
  env
  -u TESSERACT_DB_PATH
  -u TESSERACT_WORKSPACE
  -u TESSERACT_PLUGINS_DIR
  -u TESSERACT_MEMORY_DECAY_INTERVAL
  -u TESS_MEASURE_DB
  -u TESS_MEASURE_NS
  -u TESS_MEASURE_RANKING
  -u OPENAI_API_KEY
  -u ANTHROPIC_API_KEY
  "XDG_DATA_HOME=$root_dir/data"
  "XDG_STATE_HOME=$root_dir/state"
  "XDG_CACHE_HOME=$root_dir/cache"
  "XDG_CONFIG_HOME=$root_dir/config"
)

cleanup() {
  if [[ -n "${pid:-}" ]]; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$root_dir" "$log_file"
}
trap cleanup EXIT

verify_isolated_path() {
  local path_output
  local checked=0
  local label
  local value

  if ! path_output="$("${isolated_env[@]}" "$binary" path)"; then
    echo "failed to resolve isolated tesseract path" >&2
    return 1
  fi

  while read -r label value; do
    case "$label" in
      data|state|cache|config|workspace-dir|main-db|config-file|records|queue-db)
        if [[ "$value" != "$root_dir/"* ]]; then
          echo "resolved path escaped temporary root: $label -> $value" >&2
          return 1
        fi
        checked=$((checked + 1))
        ;;
    esac
  done <<<"$path_output"

  if [[ "$checked" -ne 9 ]]; then
    echo "tesseract path returned $checked checked paths, want 9" >&2
    return 1
  fi
}

# This must be the first Tesseract command: managed token issuance and serve
# both open the store, so neither may run until the layout has been proved safe.
go build -o "$binary" ./cmd/tesseract
verify_isolated_path

host_port="${BASE_URL#http://}"
serve_args=(serve --addr "$host_port")
smoke_args=(smoke BASE_URL="$BASE_URL" AUTH_MODE="$AUTH_MODE")

if [[ "$ENABLE_METRICS" == "1" ]]; then
  serve_args+=(--metrics)
  smoke_args+=(METRICS=1)
fi

case "$AUTH_MODE" in
  none)
    ;;
  static)
    serve_args+=(--static-token "$STATIC_TOKEN")
    smoke_args+=(TOKEN="$STATIC_TOKEN")
    ;;
  managed)
    issue_out="$("${isolated_env[@]}" "$binary" context token issue --label e2e-local --ttl 1h --output json)"
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

if curl --silent --output /dev/null --connect-timeout 1 --max-time 1 "$BASE_URL/"; then
  echo "refusing to run e2e against an address that already responds: $BASE_URL" >&2
  exit 2
fi

"${isolated_env[@]}" "$binary" "${serve_args[@]}" >"$log_file" 2>&1 &
pid=$!

ready=0
for _ in {1..120}; do
  if ! kill -0 "$pid" >/dev/null 2>&1; then
    echo "tesseract exited before becoming ready" >&2
    tail -100 "$log_file" >&2
    exit 1
  fi
  if curl --silent --show-error --fail --max-time 1 "$BASE_URL/v1/health/readiness" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.25
done

if [[ "$ready" -ne 1 ]]; then
  echo "timed out waiting for tesseract readiness at $BASE_URL" >&2
  tail -100 "$log_file" >&2
  exit 1
fi

"${isolated_env[@]}" make contracts
make "${smoke_args[@]}"

echo "e2e-local ok (AUTH_MODE=$AUTH_MODE BASE_URL=$BASE_URL ENABLE_METRICS=$ENABLE_METRICS)"

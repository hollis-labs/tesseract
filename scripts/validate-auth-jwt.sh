#!/usr/bin/env bash
# Side-port validation of an auth.jwt / auth.deny_list config block.
#
# Starts a throwaway tesseract on 127.0.0.1:${PORT:-18089} against a COPY of
# the database with the candidate block in an isolated config dir, then checks
# startup, an anonymous 401, a managed token 200 and a JWT-shaped bad token 401.
# The live service, its config and its database are never touched. Everything
# the script creates lives under one temp dir that is deleted on exit.
#
# Usage:
#   scripts/validate-auth-jwt.sh [--bin PATH] [--db SRC_MAIN_DB] [--config FRAGMENT]
#                                [--token-file FILE] [--port N]
#
#   --bin         tesseract binary (default: tesseract on PATH)
#   --db          source main.db to copy (default: start from an empty database)
#   --config      YAML fragment with the auth block (default: examples/hub-auth-jwt.yaml)
#   --token-file  an existing managed token (0600 file) to prove it still works;
#                 without it a token is minted inside the copy
#   --port        default 18089
#
# Example:
#   XDG_STATE_HOME=/path/to/state scripts/validate-auth-jwt.sh \
#     --bin ./tesseract.next --db /path/to/main.db --token-file ~/token \
#     --config ./filled-in-auth-block.yaml
#
# The deny-list bearer is a go-secrets reference resolved from
# $XDG_STATE_HOME/hollis/secrets, so the real reference is exercised when the
# block enables deny_list. No secret value is printed.
set -uo pipefail

BIN=tesseract SRC_DB="" CFG="" TOKEN_FILE="" PORT=18089
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CFG="$here/../examples/hub-auth-jwt.yaml"
while [ $# -gt 0 ]; do
  case "$1" in
    --bin) BIN="$2"; shift 2;;
    --db) SRC_DB="$2"; shift 2;;
    --config) CFG="$2"; shift 2;;
    --token-file) TOKEN_FILE="$2"; shift 2;;
    --port) PORT="$2"; shift 2;;
    -h|--help) sed -n '2,30p' "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

for c in curl sqlite3; do command -v "$c" >/dev/null || { echo "missing: $c" >&2; exit 2; }; done
[ -f "$CFG" ] || { echo "no config fragment: $CFG" >&2; exit 2; }
command -v "$BIN" >/dev/null 2>&1 || [ -x "$BIN" ] || { echo "no binary: $BIN" >&2; exit 2; }

work="$(mktemp -d)"
pid=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
  rm -rf "$work"
}
trap cleanup EXIT

# Isolated roots. Secrets still resolve from the real store (same XDG_STATE_HOME
# the caller exported) so a deny_list bearer_ref is exercised for real.
secrets_state="${XDG_STATE_HOME:-$HOME/.local/state}"
export XDG_CONFIG_HOME="$work/config" XDG_DATA_HOME="$work/data" XDG_CACHE_HOME="$work/cache"
export XDG_STATE_HOME="$work/state"
mkdir -p "$XDG_CONFIG_HOME/tesseract" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME"
chmod 700 "$work" "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/tesseract" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME"
if [ -d "$secrets_state/hollis/secrets" ]; then
  mkdir -p "$XDG_STATE_HOME/hollis"
  ln -s "$secrets_state/hollis/secrets" "$XDG_STATE_HOME/hollis/secrets"
fi
unset HOLLIS_AUTH HOLLIS_AUTH_TESSERACT TESSERACT_DB_PATH TESSERACT_WORKSPACE

db="$("$BIN" path | awk '$1=="main-db"{print $2}')"
mkdir -p "$(dirname "$db")"
if [ -n "$SRC_DB" ]; then
  sqlite3 "$SRC_DB" ".backup '$db'" || { echo "FAIL  could not copy $SRC_DB" >&2; exit 1; }
fi
cp "$CFG" "$XDG_CONFIG_HOME/tesseract/config.yaml"
chmod 600 "$XDG_CONFIG_HOME/tesseract/config.yaml"

fail=0
ok()   { printf 'ok    %s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@"; }

tok="$TOKEN_FILE"
if [ -z "$tok" ]; then
  tok="$work/token"
  "$BIN" context token create -name validate-auth-jwt -scopes read,write -namespaces 'project/*' \
      -ttl 1h -secret-file "$tok" >/dev/null 2>&1 || { bad "could not mint a token in the copy"; exit 1; }
fi

jwks="$(awk '/^[[:space:]]*jwks_url:/{print $2; exit}' "$CFG")"
if [ -n "$jwks" ]; then
  body="$(curl -s --max-time 10 -w '\n%{http_code}' "$jwks")"
  if [ "${body##*$'\n'}" = 200 ] && printf '%s' "$body" | grep -q '"keys"' && ! printf '%s' "$body" | grep -q '"d"'; then
    ok "JWKS reachable, has keys, no private member ($jwks)"
  else
    bad "JWKS fetch from $jwks did not return a public key set (HTTP ${body##*$'\n'})"
  fi
fi

# The managed token goes to curl via a 0600 header file, never argv (ps).
authhdr="$work/authhdr"
( umask 077; printf 'Authorization: Bearer %s\n' "$(cat "$tok")" >"$authhdr" )

log="$work/serve.log"
"$BIN" serve -addr "127.0.0.1:$PORT" -managed-auth >"$log" 2>&1 &
pid=$!
up=0
for _ in $(seq 1 50); do
  kill -0 "$pid" 2>/dev/null || break
  [ "$(code "http://127.0.0.1:$PORT/healthz")" = 200 ] && { up=1; break; }
  sleep 0.2
done
if [ "$up" != 1 ]; then
  bad "startup: server did not become healthy (a bad auth block stops startup)"
  sed 's/^/      /' "$log" | head -20
  exit 1
fi
ok "startup with the candidate auth block on 127.0.0.1:$PORT"
grep -i 'auth' "$log" | head -2 | sed 's/^/      /'

url="http://127.0.0.1:$PORT/v1/auth/whoami"
c=$(code "$url");                                               [ "$c" = 401 ] && ok "anonymous request -> 401" || bad "anonymous request -> $c (want 401)"
c=$(code -H "@$authhdr" "$url");      [ "$c" = 200 ] && ok "managed token -> 200" || bad "managed token -> $c (want 200)"
# JWT-shaped, EdDSA header, signature that no issuer key produced.
h=$(printf '{"alg":"EdDSA","typ":"JWT","kid":"x"}' | base64 | tr -d '=\n' | tr '+/' '-_')
p=$(printf '{"iss":"tether","aud":"tesseract","sub":"x","exp":4102444800}' | base64 | tr -d '=\n' | tr '+/' '-_')
s=$(head -c 64 /dev/zero | base64 | tr -d '=\n' | tr '+/' '-_')
c=$(code -H "Authorization: Bearer $h.$p.$s" "$url");           [ "$c" = 401 ] && ok "JWT-shaped bad token -> 401" || bad "JWT-shaped bad token -> $c (want 401)"
c=$(code -H "Authorization: Bearer not-a-token" "$url");        [ "$c" = 401 ] && ok "opaque bad token -> 401" || bad "opaque bad token -> $c (want 401)"

echo
[ "$fail" = 0 ] && echo "auth.jwt validation passed" || echo "auth.jwt validation FAILED"
exit "$fail"

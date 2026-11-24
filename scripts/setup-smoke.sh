#!/usr/bin/env bash
# Builds the server, starts it with no account and no config on 127.0.0.1, and
# drives first-run setup in headless Chrome (scripts/setup-smoke.mjs). Run from
# anywhere: scripts/setup-smoke.sh. Port: VCRX_SETUP_PORT (3753).
set -euo pipefail
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
port="${VCRX_SETUP_PORT:-3753}"
dir="$(mktemp -d "${TMPDIR:-/tmp}/vcrx-setup-XXXXXX")"
pid=""
# shellcheck disable=SC2329 # invoked by the EXIT trap
cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2> /dev/null || true
    wait "$pid" 2> /dev/null || true
  fi
  rm -rf "$dir"
}
trap cleanup EXIT
mkdir -p "$dir/config" "$dir/cache" "$dir/media"
(cd "$repo/server" && go build -o "$dir/vcrx" ./cmd/vcrx)
VCRX_CONFIG_DIR="$dir/config" VCRX_CACHE_DIR="$dir/cache" VCRX_MEDIA_ROOTS="$dir/media" \
  VCRX_BIND=127.0.0.1 VCRX_PORT="$port" VCRX_WEB_DIR="$repo/server/web/static" \
  "$dir/vcrx" > "$dir/server.log" 2>&1 &
pid=$!
up=0
for _ in $(seq 1 100); do
  if curl -fsS "http://127.0.0.1:$port/healthz" > /dev/null 2>&1; then
    up=1
    break
  fi
  sleep 0.2
done
if [ "$up" -ne 1 ]; then
  echo "setup-smoke: server did not start" >&2
  tail -n 40 "$dir/server.log" >&2
  exit 1
fi
status=0
node "$repo/scripts/setup-smoke.mjs" "http://127.0.0.1:$port" || status=$?
if [ "$status" -ne 0 ]; then tail -n 40 "$dir/server.log" >&2; fi
exit "$status"

#!/usr/bin/env bash
# Deploys VCRX on the host that runs it, from the clone this script lives in:
#
#   ~/docker/vcrx/deploy/deploy.sh            # refuses while someone is watching
#   ~/docker/vcrx/deploy/deploy.sh --force    # restart anyway
#
# 1. Refuses a dirty or diverged checkout, then fast-forwards to origin/main.
# 2. Refuses while a stream is playing (checked as the flexbot admin, whose
#    password stays in deploy/.flexbot-password on this host), and refuses
#    when that check cannot run.
# 3. Builds with the commit as the version (an old VCRX_VERSION in .env would
#    otherwise label every build with a stale commit), restarts, waits for
#    /healthz, checks the server reports that version, and runs
#    scripts/smoke-api.sh.
set -euo pipefail

force=0
[ "${1:-}" = "--force" ] && force=1
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(dirname "$here")"
cd "$repo"
url="${VCRX_URL:-http://127.0.0.1:${VCRX_PORT:-3750}}"
pwfile="$here/.flexbot-password"

die() { echo "deploy: $*" >&2; exit 1; }

[ -z "$(git status --porcelain --untracked-files=no)" ] || die "the checkout has local changes; commit or remove them first"
git fetch -q origin
git merge-base --is-ancestor HEAD origin/main || die "this checkout has commits origin/main does not; resolve that by hand"
before=$(git rev-parse --short HEAD)

# The guard fails closed: if it cannot sign in or read the activity count, it
# cannot say nobody is watching, so it refuses rather than assume zero.
if [ "$force" = 0 ]; then
  cant() { die "cannot tell whether anyone is watching ($1); fix that, or run with --force"; }
  [ -f "$pwfile" ] || cant "no $pwfile"
  command -v jq > /dev/null || cant "jq is not installed"
  body=$(jq -n --arg p "$(cat "$pwfile")" '{username:"flexbot", password:$p, device:{id:"deploy", name:"deploy.sh", platform:"script", client:"deploy", client_version:"1"}}')
  token=$(curl -fsS -H 'Content-Type: application/json' -d "$body" "$url/api/v1/auth/login" 2> /dev/null | jq -r '.token // empty') || token=""
  [ -n "$token" ] || cant "the flexbot sign-in failed"
  playing=$(curl -fsS -H "Authorization: Bearer $token" "$url/api/v1/admin/activity/now" 2> /dev/null | jq -r '.stream_count // empty') || playing=""
  curl -fsS -X POST -H "Authorization: Bearer $token" "$url/api/v1/auth/logout" > /dev/null 2>&1 || true
  case "$playing" in '' | *[!0-9]*) cant "the activity check failed" ;; esac
  [ "$playing" = 0 ] || die "$playing stream(s) playing now; run again later, or with --force"
fi

git merge -q --ff-only origin/main
after=$(git rev-parse --short HEAD)
echo "deploy: $before -> $after"

cd "$here"
version="$(git describe --always --dirty)"
VCRX_VERSION="$version" docker compose build > "${TMPDIR:-/tmp}/vcrx-build.log" 2>&1 ||
  { tail -30 "${TMPDIR:-/tmp}/vcrx-build.log"; die "build failed"; }
docker compose up -d

for _ in $(seq 1 60); do
  curl -fsS "$url/healthz" > /dev/null 2>&1 && break
  sleep 1
done
served=$(curl -fsS "$url/api/v1/server" | jq -r .version) || die "the server did not come back"
# /healthz answers for whatever container is up; the version says it is the new one.
[ "$served" = "$version" ] || die "the server reports version $served, not the $version just built; check docker compose ps"
echo "deploy: serving $served"

if [ -f "$pwfile" ]; then
  VCRX_URL="$url" VCRX_USER=flexbot VCRX_PASSWORD="$(cat "$pwfile")" VCRX_KEEP_POSITION=0 bash "$repo/scripts/smoke-api.sh"
else
  echo "deploy: no $pwfile, so the smoke test was skipped"
fi

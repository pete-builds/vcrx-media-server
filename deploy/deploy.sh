#!/usr/bin/env bash
# Deploys VCRX on the host that runs it, from the clone this script lives in:
#
#   ~/docker/vcrx/deploy/deploy.sh                    # build main here
#   ~/docker/vcrx/deploy/deploy.sh --release v0.2.0   # run that released image
#   ~/docker/vcrx/deploy/deploy.sh --force ...        # restart even mid-stream
#
# 1. Refuses a dirty or diverged checkout, then fast-forwards to origin/main.
# 2. Refuses while a stream is playing (checked as the flexbot admin, whose
#    password stays in deploy/.flexbot-password on this host), and refuses
#    when that check cannot run.
# 3. Without --release, builds with the commit as the version (an old
#    VCRX_VERSION in .env would otherwise label every build with a stale
#    commit). With --release vX.Y.Z, pulls ghcr.io's image for that version,
#    built and attested in CI, and records it as VCRX_IMAGE in .env so a
#    restart keeps it; rolling back is --release with the previous version.
# 4. Restarts, waits for /healthz, checks the server reports the version just
#    deployed, and runs scripts/smoke-api.sh.
set -euo pipefail

die() { echo "deploy: $*" >&2; exit 1; }

force=0
release=""
while [ $# -gt 0 ]; do
  case "$1" in
    --force) force=1 ;;
    --release)
      [ $# -ge 2 ] || die "--release needs a version, like v0.2.0"
      release="$2"
      shift
      ;;
    *) die "unknown argument $1 (use --force and/or --release vX.Y.Z)" ;;
  esac
  shift
done
if [ -n "$release" ]; then
  [[ "$release" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "--release takes a version like v0.2.0, not $release"
fi
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(dirname "$here")"
cd "$repo"
url="${VCRX_URL:-http://127.0.0.1:${VCRX_PORT:-3750}}"
pwfile="$here/.flexbot-password"
registry="${VCRX_REGISTRY:-ghcr.io/pete-builds/vcrx-media-server}"

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
# VCRX_IMAGE in .env is what a plain `docker compose up` runs, so it always
# names the image deployed last: the release's, or none for a local build.
set_image() {
  touch .env
  # .env holds API keys: the working copy is private like the file itself.
  (umask 077 && grep -v '^VCRX_IMAGE=' .env > .env.tmp) || true
  [ -z "$1" ] || echo "VCRX_IMAGE=$1" >> .env.tmp
  cat .env.tmp > .env && rm -f .env.tmp
}
if [ -n "$release" ]; then
  version="$release"
  image="$registry:${release#v}"
  VCRX_IMAGE="$image" docker compose pull vcrx > "${TMPDIR:-/tmp}/vcrx-build.log" 2>&1 ||
    { tail -30 "${TMPDIR:-/tmp}/vcrx-build.log"; die "could not pull $image"; }
  set_image "$image"
  VCRX_IMAGE="$image" docker compose up -d --no-build
else
  version="$(git describe --always --dirty)"
  VCRX_IMAGE=vcrx-media-server:local VCRX_VERSION="$version" docker compose build > "${TMPDIR:-/tmp}/vcrx-build.log" 2>&1 ||
    { tail -30 "${TMPDIR:-/tmp}/vcrx-build.log"; die "build failed"; }
  set_image ""
  VCRX_IMAGE=vcrx-media-server:local docker compose up -d
fi

for _ in $(seq 1 60); do
  curl -fsS "$url/healthz" > /dev/null 2>&1 && break
  sleep 1
done
served=$(curl -fsS "$url/api/v1/server" | jq -r .version) || die "the server did not come back"
# /healthz answers for whatever container is up; the version says it is the new one.
[ "$served" = "$version" ] || die "the server reports version $served, not the $version just built; check docker compose ps"
echo "deploy: serving $served"

if [ -f "$pwfile" ]; then
  VCRX_URL="$url" VCRX_USER=flexbot VCRX_PASSWORD="$(cat "$pwfile")" VCRX_KEEP_POSITION=0 VCRX_REQUIRE_ADMIN=1 bash "$repo/scripts/smoke-api.sh"
else
  echo "deploy: no $pwfile, so the smoke test was skipped"
fi

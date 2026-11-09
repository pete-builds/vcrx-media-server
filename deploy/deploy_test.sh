#!/usr/bin/env bash
# Tests deploy/deploy.sh with stand-ins for docker and curl: it must refuse
# while a stream plays, deploy with --force, build with the commit as the
# version, and stop before the restart when the new image's migrations fail
# on a copy of the live database. Run from anywhere: deploy/deploy_test.sh
set -euo pipefail
src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# A bare "origin" and a clone of it laid out like ~/docker/vcrx.
git init -q --bare "$work/origin.git"
git clone -q "$work/origin.git" "$work/vcrx" 2> /dev/null
cd "$work/vcrx"
git config user.email t@t && git config user.name t
mkdir -p deploy scripts
cp "$src" deploy/deploy.sh
printf '#!/usr/bin/env bash\necho smoke ran\n' > scripts/smoke-api.sh
echo x > deploy/.flexbot-password
printf 'deploy/.flexbot-password\n' > .gitignore
git add -A && git commit -qm one && git push -q origin HEAD:main 2> /dev/null
git branch -q -u origin/main
echo two > file && git add file && git commit -qm two && git push -q origin HEAD:main 2> /dev/null
git reset -q --hard HEAD~1

stub="$work/bin"
mkdir -p "$stub"
cat > "$stub/docker" << 'S'
#!/usr/bin/env bash
echo "docker $* VCRX_VERSION=${VCRX_VERSION:-} VCRX_IMAGE=${VCRX_IMAGE:-}" >> "$STUB_LOG"
# The restarted server reports what was built or pulled, unless the test says
# it is stale; a pulled release reports its tag.
[ "${1:-} ${2:-}" = "compose build" ] && echo "${VCRX_VERSION:-}" > "$STUB_LOG.version"
if [ "${1:-} ${2:-}" = "compose pull" ]; then
  [ "${FAIL:-}" = pull ] && exit 1
  echo "v${VCRX_IMAGE##*:}" > "$STUB_LOG.version"
fi
# The resolved compose file, as much of it as the migration dry run reads.
if [ "${1:-} ${2:-}" = "compose config" ]; then
  echo "{\"services\":{\"vcrx\":{\"user\":\"3750:3750\",\"volumes\":[{\"type\":\"bind\",\"source\":\"$CONFIG_DIR\",\"target\":\"/config\"}]}}}"
fi
# The new image: its help, and its migration dry run.
if [ "${1:-}" = run ]; then
  case " $* " in
    *" help "*) [ "${OLD_IMAGE:-}" = 1 ] && echo "vcrx migrate" || echo "vcrx migrate-check -db PATH" ;;
    *" migrate-check "*)
      [ "${FAIL:-}" = dryrun ] && { echo "vcrx: the dry run failed at schema version 21: foreign key check failed"; exit 1; }
      echo "schema version 21 -> 23: passed"
      ;;
  esac
fi
exit 0
S
cat > "$stub/curl" << 'S'
#!/usr/bin/env bash
for a in "$@"; do
  case "$a" in
    */auth/login) [ "${FAIL:-}" = login ] && exit 22; echo '{"token":"t"}'; exit 0 ;;
    */activity/now) [ "${FAIL:-}" = activity ] && exit 22; echo "{\"stream_count\":$PLAYING}"; exit 0 ;;
    */api/v1/server) echo "{\"name\":\"VCRX\",\"version\":\"${SERVED:-$(cat "$STUB_LOG.version")}\"}"; exit 0 ;;
  esac
done
exit 0
S
chmod +x "$stub/docker" "$stub/curl"
export PATH="$stub:$PATH" STUB_LOG="$work/docker.log" VCRX_URL=http://stub CONFIG_DIR="$work/config"
mkdir -p "$work/config" && echo db > "$work/config/vcrx.db"

fail() { echo "deploy_test: $*" >&2; exit 1; }

# Someone is watching: refused, nothing built, checkout untouched.
if PLAYING=1 bash deploy/deploy.sh > "$work/out" 2>&1; then fail "deployed while a stream played"; fi
grep -q "1 stream(s) playing" "$work/out" || fail "no reason given: $(cat "$work/out")"
[ ! -s "$work/docker.log" ] || fail "built while a stream played"
[ "$(git log --oneline | wc -l | tr -d ' ')" = 1 ] || fail "pulled while a stream played"

# The check cannot run: refused too, since nobody can say no one is watching.
refused() {
  if bash deploy/deploy.sh > "$work/out" 2>&1; then fail "deployed when $1"; fi
  grep -q "cannot tell whether anyone is watching" "$work/out" || fail "$1, no reason given: $(cat "$work/out")"
  [ ! -s "$work/docker.log" ] || fail "built when $1"
}
FAIL=login PLAYING=0 refused "the sign-in failed"
FAIL=activity PLAYING=0 refused "the activity check failed"
mv deploy/.flexbot-password "$work/pw"
PLAYING=0 refused "the password file was missing"
mv "$work/pw" deploy/.flexbot-password

# --force deploys, fast-forwards, and labels the build with the new commit.
PLAYING=1 bash deploy/deploy.sh --force > "$work/out" 2>&1 || fail "--force failed: $(cat "$work/out")"
head=$(git rev-parse --short HEAD)
[ "$(git log --oneline | wc -l | tr -d ' ')" = 2 ] || fail "did not fast-forward"
grep -q "compose build VCRX_VERSION=$head" "$work/docker.log" || fail "build not labelled $head: $(cat "$work/docker.log")"
grep -q "smoke ran" "$work/out" || fail "smoke test not run"
# --force still dry-runs the new image's migrations: offline, as the server's
# user, on the live config folder, after the build and before the restart.
dry='docker run --rm --network none .* -v '"$work"'/config:/live --user 3750:3750 --entrypoint vcrx vcrx-media-server:local migrate-check -from /live/vcrx.db -db /work/vcrx.db'
grep -q "$dry" "$work/docker.log" || fail "no migration dry run with --force: $(cat "$work/docker.log")"
order=$(grep -oE "compose build|migrate-check -from|compose up" "$work/docker.log" | tr '\n' ' ')
case "$order" in *"compose build"*"migrate-check"*"compose up"*) ;; *) fail "dry run out of order: $order" ;; esac
grep -q "schema version 21 -> 23" "$work/out" || fail "dry run result not shown: $(cat "$work/out")"

# A dry run that fails stops the deploy before the restart, --force or not,
# and says why; the old container is never replaced.
for flag in "" --force; do
  git reset -q --hard HEAD~1
  : > "$work/docker.log"
  if FAIL=dryrun PLAYING=0 bash deploy/deploy.sh ${flag:+"$flag"} > "$work/out" 2>&1; then fail "deployed though the dry run failed ($flag)"; fi
  grep -q "migration dry run failed" "$work/out" || fail "failed dry run not named: $(cat "$work/out")"
  grep -q "foreign key check failed" "$work/out" || fail "dry run error not shown: $(cat "$work/out")"
  grep -q "migrate-check" "$work/docker.log" || fail "no dry run ran ($flag): $(cat "$work/docker.log")"
  ! grep -q "compose up" "$work/docker.log" || fail "restarted after a failed dry run ($flag): $(cat "$work/docker.log")"
done

# An image from before migrate-check (a rollback) deploys with a warning, and
# a host with no database yet has nothing to dry-run.
git reset -q --hard HEAD~1
: > "$work/docker.log"
OLD_IMAGE=1 PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1 || fail "an image without migrate-check failed: $(cat "$work/out")"
grep -q "has no migrate-check" "$work/out" || fail "skipped dry run not named: $(cat "$work/out")"
! grep -q " migrate-check -from" "$work/docker.log" || fail "ran migrate-check on an image without it"
git reset -q --hard HEAD~1
mv "$work/config/vcrx.db" "$work/db"
PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1 || fail "a host with no database failed: $(cat "$work/out")"
grep -q "no database at" "$work/out" || fail "missing database not named: $(cat "$work/out")"
mv "$work/db" "$work/config/vcrx.db"

# A server that comes back on another version (the old container still up)
# fails the deploy rather than reporting success.
git reset -q --hard HEAD~1
if SERVED=old PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1; then fail "deployed though the server reports another version"; fi
grep -q "reports version old" "$work/out" || fail "stale version not named: $(cat "$work/out")"

# --release pulls the CI-built image for that version, builds nothing, runs it,
# and records it in .env (keeping every other line) so a restart keeps it.
printf 'VCRX_TMDB_API_KEY=k\nVCRX_IMAGE=old\n' > deploy/.env
: > "$work/docker.log"
PLAYING=0 bash deploy/deploy.sh --release v0.2.0 > "$work/out" 2>&1 || fail "--release failed: $(cat "$work/out")"
registry_img=ghcr.io/pete-builds/vcrx-media-server
img=$registry_img:0.2.0
grep -q "compose pull vcrx .*VCRX_IMAGE=$img" "$work/docker.log" || fail "did not pull $img: $(cat "$work/docker.log")"
grep -q "compose up -d --no-build .*VCRX_IMAGE=$img" "$work/docker.log" || fail "did not start $img: $(cat "$work/docker.log")"
! grep -q "compose build" "$work/docker.log" || fail "built though releasing: $(cat "$work/docker.log")"
grep -q "serving v0.2.0" "$work/out" || fail "release version not checked: $(cat "$work/out")"
grep -q "entrypoint vcrx $img migrate-check" "$work/docker.log" || fail "release not dry-run: $(cat "$work/docker.log")"
if [ "$(grep -c '^VCRX_IMAGE=' deploy/.env)" != 1 ] || ! grep -q "^VCRX_IMAGE=$img$" deploy/.env; then fail ".env image: $(cat deploy/.env)"; fi
grep -q '^VCRX_TMDB_API_KEY=k$' deploy/.env || fail ".env lost another line: $(cat deploy/.env)"

# Still refused mid-stream, and a version that is not vX.Y.Z or cannot be pulled stops it.
if PLAYING=1 bash deploy/deploy.sh --release v0.2.0 > "$work/out" 2>&1; then fail "released while a stream played"; fi
if PLAYING=0 bash deploy/deploy.sh --release latest > "$work/out" 2>&1; then fail "released 'latest'"; fi
grep -q "like v0.2.0" "$work/out" || fail "bad version not named: $(cat "$work/out")"
: > "$work/docker.log"
if FAIL=pull PLAYING=0 bash deploy/deploy.sh --release v0.3.0 > "$work/out" 2>&1; then fail "deployed an image that did not pull"; fi
! grep -q "compose up" "$work/docker.log" || fail "restarted after a failed pull"
grep -q "^VCRX_IMAGE=$img$" deploy/.env || fail "a failed pull changed .env: $(cat deploy/.env)"
# A release whose dry run fails is not started and not recorded.
: > "$work/docker.log"
if FAIL=dryrun PLAYING=0 bash deploy/deploy.sh --release v0.3.0 > "$work/out" 2>&1; then fail "released though the dry run failed"; fi
grep -q "entrypoint vcrx $registry_img:0.3.0 migrate-check" "$work/docker.log" || fail "release v0.3.0 not dry-run: $(cat "$work/docker.log")"
! grep -q "compose up" "$work/docker.log" || fail "restarted after a failed release dry run"
grep -q "^VCRX_IMAGE=$img$" deploy/.env || fail "a failed dry run changed .env: $(cat deploy/.env)"

# Building again goes back to the local image and drops the release from .env.
: > "$work/docker.log"
PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1 || fail "build after a release failed: $(cat "$work/out")"
grep -q "compose up -d VCRX_VERSION= VCRX_IMAGE=vcrx-media-server:local" "$work/docker.log" || fail "did not go back to the local image: $(cat "$work/docker.log")"
! grep -q '^VCRX_IMAGE=' deploy/.env || fail "release still in .env after a build: $(cat deploy/.env)"
grep -q '^VCRX_TMDB_API_KEY=k$' deploy/.env || fail ".env lost another line: $(cat deploy/.env)"
rm deploy/.env

# A local change stops it before anything else.
echo dirty >> file
if PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1; then fail "deployed a dirty checkout"; fi
grep -q "local changes" "$work/out" || fail "dirty checkout not named: $(cat "$work/out")"
echo "deploy_test: ok"

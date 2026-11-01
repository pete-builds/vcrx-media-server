#!/usr/bin/env bash
# Tests deploy/deploy.sh with stand-ins for docker and curl: it must refuse
# while a stream plays, deploy with --force, and build with the commit as the
# version. Run from anywhere: deploy/deploy_test.sh
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
echo "docker $* VCRX_VERSION=${VCRX_VERSION:-}" >> "$STUB_LOG"
# The restarted server reports what was built, unless the test says it is stale.
[ "${1:-} ${2:-}" = "compose build" ] && echo "${VCRX_VERSION:-}" > "$STUB_LOG.version"
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
export PATH="$stub:$PATH" STUB_LOG="$work/docker.log" VCRX_URL=http://stub

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

# A server that comes back on another version (the old container still up)
# fails the deploy rather than reporting success.
git reset -q --hard HEAD~1
if SERVED=old PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1; then fail "deployed though the server reports another version"; fi
grep -q "reports version old" "$work/out" || fail "stale version not named: $(cat "$work/out")"

# A local change stops it before anything else.
echo dirty >> file
if PLAYING=0 bash deploy/deploy.sh > "$work/out" 2>&1; then fail "deployed a dirty checkout"; fi
grep -q "local changes" "$work/out" || fail "dirty checkout not named: $(cat "$work/out")"
echo "deploy_test: ok"

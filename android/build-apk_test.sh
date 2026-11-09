#!/usr/bin/env bash
# Tests android/build-apk.sh's version code with a stand-in for docker: a full clone
# builds with the commit count, and a shallow clone, which would count from 1
# and make an APK the phone refuses to install over the current one, is refused
# before anything is built. Run from anywhere: android/build-apk_test.sh
set -euo pipefail
src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/build-apk.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git init -q "$work/repo"
cd "$work/repo"
git config user.email t@t && git config user.name t
mkdir -p android/signing
cp "$src" android/build-apk.sh
git add android && git commit -qm one
for n in two three four; do echo "$n" > "$n" && git add "$n" && git commit -qm "$n"; done

stub="$work/bin"
mkdir -p "$stub"
cat > "$stub/docker" << 'S'
#!/usr/bin/env bash
[ "${1:-} ${2:-}" = "buildx version" ] && exit 0
echo "docker $*" >> "$STUB_LOG"
# keytool in a container: the keystore comes back base64 on stdout.
[ "${1:-}" = run ] && echo a2V5c3RvcmU=
exit 0
S
chmod +x "$stub/docker"
export PATH="$stub:$PATH" STUB_LOG="$work/docker.log"

fail() { echo "build-apk_test: $*" >&2; exit 1; }
# An existing key, so the script does not make one (that also runs docker).
keys() { echo k > "$1/android/signing/vcrx-mobile.jks" && echo p > "$1/android/signing/vcrx-mobile.pass"; }

# Full clone: the version code is the commit count.
keys "$work/repo"
bash android/build-apk.sh > "$work/out" 2>&1 || fail "full clone failed: $(cat "$work/out")"
grep -q "VERSION_CODE=4 " "$work/docker.log" || fail "version code is not the commit count: $(cat "$work/docker.log")"

# Shallow clone: refused, nothing built.
: > "$work/docker.log"
git clone -q --depth 1 "file://$work/repo" "$work/shallow"
mkdir -p "$work/shallow/android/signing" && keys "$work/shallow"
if bash "$work/shallow/android/build-apk.sh" > "$work/out" 2>&1; then fail "built from a shallow clone"; fi
grep -q "shallow" "$work/out" || fail "no reason given: $(cat "$work/out")"
[ ! -s "$work/docker.log" ] || fail "ran docker for a shallow clone: $(cat "$work/docker.log")"
# No key yet and a keytool that cannot run (macOS's stub with no JDK): the key is
# made in a container instead, and the build goes on.
: > "$work/docker.log"
rm -rf "$work/repo/android/signing"
echo "ARG JDK_IMAGE=jdk" > "$work/repo/android/Dockerfile"
printf '#!/bin/sh\necho "Unable to locate a Java Runtime." >&2\nexit 1\n' > "$stub/keytool"
chmod +x "$stub/keytool"
(cd "$work/repo" && bash android/build-apk.sh > "$work/out" 2>&1) || fail "no working keytool stopped the build: $(cat "$work/out")"
grep -q "^docker run " "$work/docker.log" || fail "keytool was not run in a container: $(cat "$work/docker.log")"
[ -s "$work/repo/android/signing/vcrx-mobile.jks" ] || fail "no keystore made"
echo "build-apk_test: ok"

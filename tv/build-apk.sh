#!/usr/bin/env bash
# Builds the VCRX TV APKs with Docker and writes them to tv/build/out/.
#
# Works against any Docker host with buildx, including a remote one:
#   DOCKER_HOST=ssh://nix1 ./tv/build-apk.sh
#
# The first run creates the signing key at tv/signing/vcrx-tv.jks with a random
# password in tv/signing/vcrx-tv.pass. Both are gitignored. Back them up: Android
# refuses to upgrade an installed app with an APK signed by a different key.
set -euo pipefail

tv_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out_dir="$tv_dir/build/out"
signing_dir="$tv_dir/signing"
keystore="$signing_dir/vcrx-tv.jks"
pass_file="$signing_dir/vcrx-tv.pass"

die() {
  echo "build-apk: $*" >&2
  exit 1
}

sha256() {
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

command -v docker > /dev/null 2>&1 || die "docker is not on PATH"
docker buildx version > /dev/null 2>&1 || die "docker buildx is required (BuildKit secrets and local output)"

make_keystore() {
  echo "build-apk: creating signing key $keystore"
  mkdir -p "$signing_dir"
  chmod 700 "$signing_dir"
  umask 077
  head -c 48 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-48 > "$pass_file"
  local dname="CN=VCRX TV, O=VCRX Media Server"
  if command -v keytool > /dev/null 2>&1; then
    keytool -genkeypair -keystore "$keystore" -storetype PKCS12 -alias vcrx-tv \
      -keyalg RSA -keysize 4096 -validity 36500 -dname "$dname" \
      -storepass:file "$pass_file" -keypass:file "$pass_file" > /dev/null
  else
    # No local JDK: run keytool in a throwaway container with no network. The password
    # goes in on stdin and the keystore comes back on stdout, so nothing is left on
    # the Docker host and neither appears in a process list or image layer.
    local jdk_image
    jdk_image="$(sed -n 's/^ARG JDK_IMAGE=//p' "$tv_dir/Dockerfile")"
    [ -n "$jdk_image" ] || die "cannot read JDK_IMAGE from the Dockerfile"
    docker run --rm -i --network none --entrypoint sh "$jdk_image" -c '
      read -r P; export P
      keytool -genkeypair -keystore /tmp/k.p12 -storetype PKCS12 -alias vcrx-tv \
        -keyalg RSA -keysize 4096 -validity 36500 -dname "'"$dname"'" \
        -storepass:env P -keypass:env P > /dev/null 2>&1 && base64 /tmp/k.p12' \
      < "$pass_file" | base64 --decode > "$keystore"
  fi
  [ -s "$keystore" ] || { rm -f "$keystore" "$pass_file"; die "keystore creation failed"; }
  echo "build-apk: back up $signing_dir somewhere safe; losing it means uninstalling to upgrade"
}

if [ ! -f "$keystore" ] || [ ! -f "$pass_file" ]; then
  [ -f "$keystore" ] && die "$keystore exists without $pass_file; restore the password file from your backup"
  make_keystore
fi

# Android refuses downgrades, so the version code grows with the commit count.
version_code="$(git -C "$tv_dir" rev-list --count HEAD 2> /dev/null || echo 1)"
[ "$version_code" -ge 1 ] 2> /dev/null || version_code=1

mkdir -p "$out_dir"
docker buildx build \
  --progress=plain \
  --target apk \
  --secret id=release_keystore,src="$keystore" \
  --secret id=release_keystore_pass,src="$pass_file" \
  --build-arg VERSION_CODE="$version_code" \
  --build-arg KEYSTORE_ID="$(sha256 "$keystore" | cut -c1-16)" \
  --output type=local,dest="$out_dir" \
  "$tv_dir"

echo
ls -la "$out_dir"

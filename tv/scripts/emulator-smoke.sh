#!/usr/bin/env bash
# Emulator smoke test: boots a headless Android TV emulator (x86_64, KVM) in a
# throwaway container, installs the APK, walks the app against a mock server and
# copies screenshots and logs to tv/scripts/out/.
#
#   DOCKER_HOST=ssh://nix1 ./tv/scripts/emulator-smoke.sh [path/to.apk]
#
# Needs a Docker host with /dev/kvm. Publishes no ports and uses no network. The
# container is always removed; the image (vcrx-tv-emulator:smoke) stays for reruns.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tv_dir="$(dirname "$scripts_dir")"
out_dir="$scripts_dir/out"
image=vcrx-tv-emulator:smoke
name="vcrx-tv-smoke-$$"

apk="${1:-}"
if [ -z "$apk" ]; then
  apk="$tv_dir/build/out/vcrx-tv-release.apk"
fi
[ -f "$apk" ] || { echo "emulator-smoke: no APK; run tv/build-apk.sh first or pass a path" >&2; exit 1; }

docker buildx build --load --progress=plain -t "$image" "$scripts_dir/emulator" 2>&1 | grep -E '^#[0-9]+ (\[|DONE|ERROR)' || true
docker image inspect "$image" > /dev/null

trap 'docker rm -f "$name" > /dev/null 2>&1 || true' EXIT
docker run -d --name "$name" --device /dev/kvm --network none "$image" sleep infinity > /dev/null
docker cp "$apk" "$name:/tmp/app.apk"
status=0
docker exec "$name" timeout 1200 /opt/smoke/run.sh || status=$?

rm -rf "$out_dir"
docker cp "$name:/out" "$out_dir"
ls -la "$out_dir"
exit "$status"

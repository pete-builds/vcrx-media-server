#!/usr/bin/env bash
# Runs the server from source against a small synthetic library, for working on
# the web app or the API. One command: ./scripts/dev-server.sh
#
# Media, config and cache live in $VCRX_DEV_DIR (default: a folder in the
# system temp dir). The first run builds a few test clips with ffmpeg (invented
# titles, test patterns and tones) and creates the admin "dev" with the password
# "devpassword". The server listens on 127.0.0.1 only, port $VCRX_PORT (3751),
# and signs this machine in without a password.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="${VCRX_DEV_DIR:-${TMPDIR:-/tmp}/vcrx-dev}"
port="${VCRX_PORT:-3751}"
media="$dir/media"

command -v go > /dev/null || { echo "dev-server: go is not on PATH" >&2; exit 1; }
command -v ffmpeg > /dev/null || { echo "dev-server: ffmpeg is not on PATH" >&2; exit 1; }
mkdir -p "$dir/config" "$dir/cache"

clip() { # out seconds vcodec acodec [extra ffmpeg args]
  local out="$1" secs="$2" vc="$3" ac="$4"
  shift 4
  [ -f "$out" ] && return
  mkdir -p "$(dirname "$out")"
  ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=24:duration=$secs" \
    -f lavfi -i "sine=frequency=440:duration=$secs" -c:v "$vc" -pix_fmt yuv420p -c:a "$ac" -ac 2 "$@" "$out"
}
cues() { # out text
  printf '1\n00:00:01,000 --> 00:00:04,000\n%s\n\n2\n00:00:05,000 --> 00:00:09,000\n%s, second line\n' "$2" "$2" > "$1"
}

if [ ! -d "$media" ]; then
  echo "dev-server: building sample media in $media"
  clip "$media/Movies/Harbor Lights (2001)/Harbor Lights (2001).mp4" 60 libx264 aac -movflags +faststart
  ffmpeg -loglevel error -y -f lavfi -i "color=c=0x1b3a6b:s=600x900" -frames:v 1 "$media/Movies/Harbor Lights (2001)/poster.jpg"
  ffmpeg -loglevel error -y -f lavfi -i "color=c=0x0d1f3a:s=1920x1080" -frames:v 1 "$media/Movies/Harbor Lights (2001)/fanart.jpg"
  # Matroska with two embedded SubRip tracks and one sidecar.
  mkdir -p "$media/Movies/Copper Valley (2015)"
  cues "$dir/a.srt" "Embedded English"
  cues "$dir/b.srt" "Eingebettet Deutsch"
  ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=24:duration=60" -f lavfi -i "sine=frequency=330:duration=60" \
    -i "$dir/a.srt" -i "$dir/b.srt" -map 0 -map 1 -map 2 -map 3 -c:v libx264 -pix_fmt yuv420p -c:a aac -c:s srt \
    -metadata:s:s:0 language=eng -metadata:s:s:1 language=ger "$media/Movies/Copper Valley (2015)/Copper Valley (2015).mkv"
  rm -f "$dir/a.srt" "$dir/b.srt"
  cues "$media/Movies/Copper Valley (2015)/Copper Valley (2015).en.srt" "Sidecar English"
  # HEVC with AC-3: most browsers refuse it, which exercises the refusal path.
  clip "$media/Movies/Night Garden (2020)/Night Garden (2020).mkv" 30 libx265 ac3 -tag:v hvc1
  clip "$media/Movies/Paper Trail (1998)/Paper Trail (1998).mp4" 30 libx264 aac
  for e in 1 2 3; do clip "$media/TV/Quiet Street/Season 01/Quiet Street - S01E0$e.mp4" 40 libx264 aac; done
  clip "$media/TV/Quiet Street/Season 02/Quiet Street - S02E01.mp4" 40 libx264 aac
fi

cat > "$dir/config/vcrx.yaml" << YAML
server_name: VCRX Dev
auto_sign_in_user: dev
trusted_networks: [127.0.0.0/8, "::1/128"]
libraries:
  - { name: Movies, kind: movie, path: $media/Movies, grouping: title_year, scan_interval_minutes: 60 }
  - { name: TV, kind: show, path: $media/TV, scan_interval_minutes: 60 }
YAML

export VCRX_CONFIG_DIR="$dir/config" VCRX_CACHE_DIR="$dir/cache" VCRX_MEDIA_ROOTS="$media"
export VCRX_BIND=127.0.0.1 VCRX_PORT="$port" VCRX_LOG_FORMAT="${VCRX_LOG_FORMAT:-text}"
# Serve the web app from the source tree, so an edit shows on reload.
export VCRX_WEB_DIR="$repo/server/web/static"
cd "$repo/server"
go build -o "$dir/vcrx" ./cmd/vcrx
if ! "$dir/vcrx" user list 2> /dev/null | grep -qw dev; then
  echo devpassword | "$dir/vcrx" user add -username dev -admin
fi
echo "dev-server: http://127.0.0.1:$port/ (admin dev / devpassword; this machine signs in automatically)"
"$dir/vcrx" serve &
server=$!
trap 'kill "$server" 2> /dev/null' EXIT INT TERM
# The server creates the libraries from vcrx.yaml at startup; scan them once
# now rather than waiting for the first scheduled scan.
for _ in $(seq 1 50); do
  curl -fsS "http://127.0.0.1:$port/healthz" > /dev/null 2>&1 && break
  sleep 0.2
done
for lib in Movies TV; do "$dir/vcrx" library scan -name "$lib" || true; done
wait "$server"

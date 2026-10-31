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

# H.264 with AC-3 audio in Matroska, the common case browsers cannot play
# directly: it plays as a converted stream (audio to AAC). Keyframes every 2 s
# and an embedded English track, to exercise seeking and subtitle timing.
river="$media/Movies/River Line (2011)/River Line (2011).mkv"
if [ ! -f "$river" ]; then
  mkdir -p "$(dirname "$river")"
  cues "$dir/r.srt" "River Line subtitle"
  ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=24:duration=90" -f lavfi -i "sine=frequency=550:duration=90" \
    -i "$dir/r.srt" -map 0 -map 1 -map 2 -c:v libx264 -g 48 -sc_threshold 0 -pix_fmt yuv420p -c:a ac3 -ac 2 -c:s srt \
    -metadata:s:s:0 language=eng "$river"
  rm -f "$dir/r.srt"
fi

# Two AC-3 audio tracks (English at 550 Hz, German at 880 Hz): a browser plays
# only the default one, so the player's Audio menu switches through a
# converted stream.
twin="$media/Movies/Twin Rivers (2018)/Twin Rivers (2018).mkv"
if [ ! -f "$twin" ]; then
  mkdir -p "$(dirname "$twin")"
  ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=24:duration=60" \
    -f lavfi -i "sine=frequency=550:duration=60" -f lavfi -i "sine=frequency=880:duration=60" \
    -map 0 -map 1 -map 2 -c:v libx264 -g 48 -sc_threshold 0 -pix_fmt yuv420p -c:a ac3 -ac 2 \
    -metadata:s:a:0 language=eng -metadata:s:a:1 language=ger "$twin"
fi

# MPEG-4 Part 2 video in AVI with MP3 audio, the old Xvid shape: no browser
# decodes the video, so it plays as a transcoded stream (H.264 made on the
# fly, MP3 copied). Keyframes only every 10 s, so a seek that started on one
# would land visibly early.
kettle="$media/Movies/Tin Kettle Road (1994)/Tin Kettle Road (1994).avi"
if [ ! -f "$kettle" ]; then
  mkdir -p "$(dirname "$kettle")"
  ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=640x360:rate=24:duration=60" -f lavfi -i "sine=frequency=660:duration=60" \
    -c:v mpeg4 -q:v 4 -g 240 -c:a libmp3lame -ac 2 "$kettle"
fi

# A season with a shared theme and shared end credits, for Skip Intro and
# Skip Credits: a cold open of a different length in each episode, then the
# theme (20 s), the episode (noise unique to it), and the credits (15 s). The
# server finds the markers itself once the scans are done (marker_hours below).
theme="0.25*sin(2*PI*t*220*pow(2\,mod(floor(t*3)*5\,12)/12))*exp(-3*mod(t\,1/3))+0.15*sin(2*PI*(400*mod(t\,2)+400*mod(t\,2)*mod(t\,2)))+0.1*sin(2*PI*t*110*pow(2\,mod(floor(t/2)*7\,12)/12))*(0.6+0.4*sin(2*PI*5*t))"
credits="(0.2*sin(2*PI*t*660*pow(2\,-mod(floor(t*4)*7+floor(t/1.1)*5\,12)/12))+0.1*sin(2*PI*t*1320*pow(2\,-mod(floor(t*4)*7+floor(t/1.1)*5\,12)/12)))*exp(-5*mod(t\,0.25))+0.12*sin(2*PI*(1800*mod(t\,1.7)-300*mod(t\,1.7)*mod(t\,1.7)))"
bay="$media/TV/Lantern Bay/Season 01"
if [ ! -f "$bay/Lantern Bay - S01E03.mp4" ]; then
  mkdir -p "$bay"
  e=0
  for cold in 6 12 3; do
    e=$((e + 1))
    total=$((cold + 20 + 30 + 15))
    ffmpeg -loglevel error -y \
      -f lavfi -i "anoisesrc=color=pink:sample_rate=44100:amplitude=0.4:seed=${e}1:duration=$cold" \
      -f lavfi -i "aevalsrc=exprs='$theme':s=44100:d=20" \
      -f lavfi -i "anoisesrc=color=pink:sample_rate=44100:amplitude=0.4:seed=${e}2:duration=30" \
      -f lavfi -i "aevalsrc=exprs='$credits':s=44100:d=15" \
      -f lavfi -i "testsrc2=size=640x360:rate=24:duration=$total" \
      -filter_complex "[0:a][1:a][2:a][3:a]concat=n=4:v=0:a=1,aformat=channel_layouts=stereo[a]" \
      -map 4:v -map "[a]" -c:v libx264 -pix_fmt yuv420p -c:a aac -movflags +faststart -shortest "$bay/Lantern Bay - S01E0$e.mp4"
  done
fi

# Extras: a trailer beside a film, two extras folders, and a show's theme
# music. None may become a title; they attach to Harbor Lights and Quiet Street.
harbor="$media/Movies/Harbor Lights (2001)"
clip "$harbor/Harbor Lights (2001)-trailer.mp4" 12 libx264 aac -movflags +faststart
clip "$harbor/Featurettes/Raising the Lighthouse.mp4" 8 libx264 aac -movflags +faststart
clip "$harbor/Deleted Scenes/The Long Night.mp4" 6 libx264 aac -movflags +faststart
theme="$media/TV/Quiet Street/theme.mp3"
[ -f "$theme" ] || ffmpeg -loglevel error -y -f lavfi -i "sine=frequency=262:duration=12" -c:a libmp3lame -q:a 6 "$theme"

# NFO ratings let the poster overlays show without network access; the optional
# fifth argument is more NFO XML, such as an invented cast.
nfo() { # dir file rating votes [extra-xml]
  [ -f "$1/$2" ] && return
  printf '<?xml version="1.0" encoding="UTF-8"?>\n<%s><ratings><rating name="imdb" max="10"><value>%s</value><votes>%s</votes></rating></ratings>%s</%s>\n' \
    "${2%.nfo}" "$3" "$4" "${5:-}" "${2%.nfo}" > "$1/$2"
}
nfo "$media/Movies/Harbor Lights (2001)" movie.nfo 8.4 310000 \
  '<actor><name>Mara Quill</name><role>Lighthouse Keeper</role><order>0</order></actor><actor><name>Tomas Brandt</name><role>Harbor Master</role><order>1</order></actor><actor><name>June Okafor</name><order>2</order></actor>'
nfo "$media/Movies/Copper Valley (2015)" movie.nfo 6.9 41000
nfo "$media/Movies/Paper Trail (1998)" movie.nfo 7.7 88000
nfo "$media/TV/Quiet Street" tvshow.nfo 8.8 120000

cat > "$dir/config/vcrx.yaml" << YAML
server_name: VCRX Dev
auto_sign_in_user: dev
trusted_networks: [127.0.0.0/8, "::1/128"]
# Detect intros and credits at any hour (a real server waits for the night).
marker_hours: always
libraries:
  - name: Movies
    kind: movie
    path: $media/Movies
    grouping: title_year
    scan_interval_minutes: 60
    # The sample clips are 720p, so the resolution badge includes it here.
    overlays:
      - { type: rating, position: top_left, sources: [imdb, tmdb] }
      - { type: resolution, position: bottom_right, resolutions: [4K, 1080p, 720p] }
  - name: TV
    kind: show
    path: $media/TV
    scan_interval_minutes: 60
    overlays:
      - { type: ribbon }
YAML

export VCRX_CONFIG_DIR="$dir/config" VCRX_CACHE_DIR="$dir/cache" VCRX_MEDIA_ROOTS="$media"
export VCRX_BIND=127.0.0.1 VCRX_PORT="$port" VCRX_LOG_FORMAT="${VCRX_LOG_FORMAT:-text}"
# Serve the web app from the source tree, so an edit shows on reload.
export VCRX_WEB_DIR="$repo/server/web/static"
cd "$repo/server"
go build -o "$dir/vcrx" ./cmd/vcrx
# Capture first: under pipefail, grep -q exiting at its first match would
# SIGPIPE the lister and fail the check once there is more than one user.
users="$("$dir/vcrx" user list 2> /dev/null || true)"
if ! grep -qw dev <<< "$users"; then
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

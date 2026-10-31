#!/usr/bin/env bash
# Exercise a running VCRX server the way the TV app does: sign in, browse, start
# playback of a movie, read the stream with Range requests, report progress and
# stop, then confirm the next session resumes from the saved position. Then the
# browser side: the web app and its host check, a sidecar subtitle as WebVTT, a
# converted stream for a file whose audio a browser cannot decode, collections,
# filters and (as an admin on the LAN) Activity.
#
#   VCRX_URL=http://192.168.1.20:3750 VCRX_USER=pete VCRX_PASSWORD=... scripts/smoke-api.sh
#
# Needs curl and jq. Leaves one finished playback session in the activity log and
# a resume point on the movie it picked; pass VCRX_KEEP_POSITION=0 to clear it.
set -euo pipefail

url="${VCRX_URL:?set VCRX_URL, e.g. http://192.168.1.20:3750}"
user="${VCRX_USER:?set VCRX_USER}"
pass="${VCRX_PASSWORD:?set VCRX_PASSWORD}"
keep="${VCRX_KEEP_POSITION:-1}"

fail() { echo "FAIL: $*" >&2; exit 1; }
tmp_headers=$(mktemp)
trap 'rm -f "$tmp_headers"' EXIT
step() { printf '%-34s' "$1"; }

step "server identity"
product=$(curl -fsS "$url/api/v1/server" | jq -r .product)
[ "$product" = "vcrx-media-server" ] || fail "not a VCRX server: $product"
echo "ok"

step "readiness"
curl -sS "$url/readyz" | jq -c .

step "login"
token=$(jq -n --arg u "$user" --arg p "$pass" \
  '{username:$u, password:$p, device:{id:"smoke-test", name:"smoke-api.sh", platform:"script", client:"smoke", client_version:"1"}}' |
  curl -fsS -H 'Content-Type: application/json' -d @- "$url/api/v1/auth/login" | jq -r .token)
[ -n "$token" ] && [ "$token" != "null" ] || fail "no token"
echo "ok"
auth=(-H "Authorization: Bearer $token")
api() { curl -fsS "${auth[@]}" "$@"; }

step "libraries"
libs=$(api "$url/api/v1/libraries")
echo "$libs" | jq -c '[.libraries[] | {name, kind, item_count}]'
movie_lib=$(echo "$libs" | jq -r '[.libraries[] | select(.kind == "movie")][0].id // empty')
[ -n "$movie_lib" ] || fail "no movie library"

step "home rows"
api "$url/api/v1/home" | jq -c '[.rows[] | {id, n: (.items | length)}]'

step "pick a direct-play movie"
caps='{"containers":["mkv","mp4","webm","ts"],
  "video_codecs":[{"codec":"h264","profiles":["baseline","main","high","constrained baseline"],"max_bit_depth":8,"max_width":3840,"max_height":2160},
                  {"codec":"hevc","profiles":["main","main 10"],"max_bit_depth":10,"max_width":3840,"max_height":2160}],
  "audio_codecs":[{"codec":"aac"},{"codec":"ac3"},{"codec":"eac3"},{"codec":"dts"},{"codec":"truehd"},{"codec":"mp3"},{"codec":"flac"},{"codec":"opus"}],
  "hdr_types":["hdr10","hlg"]}'
session=""
for id in $(api "$url/api/v1/libraries/$movie_lib/items?sort=added&limit=40" | jq -r '.items[].id'); do
  body=$(jq -n --argjson id "$id" --argjson caps "$caps" '{item_id:$id, start_position_ms:0, capabilities:$caps}')
  if resp=$(api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions" 2>/dev/null); then
    session="$resp"
    break
  fi
done
[ -n "$session" ] || fail "none of the 40 newest movies direct plays with these capabilities"
item=$(echo "$session" | jq -r .item_id)
sid=$(echo "$session" | jq -r .session_id)
stream=$(echo "$session" | jq -r .stream.url)
size=$(echo "$session" | jq -r .version.size)
echo "item $item: $(echo "$session" | jq -r '.version.file_name')"
echo "  decision: $(echo "$session" | jq -r '.decision + ": " + .reason')"

step "stream HEAD"
len=$(curl -fsSI "${auth[@]}" "$url$stream" | tr -d '\r' | awk -F': ' 'tolower($1)=="content-length"{print $2}')
[ "$len" = "$size" ] || fail "Content-Length $len, version size $size"
echo "ok ($len bytes)"

step "range: first 1 MiB"
got=$(api -r 0-1048575 "$url$stream" | wc -c | tr -d ' ')
[ "$got" = "1048576" ] || fail "got $got bytes"
echo "ok"

step "range: seek to the middle"
mid=$((size / 2))
code=$(curl -sS -o /dev/null -w '%{http_code} %{size_download}' "${auth[@]}" -r "$mid-$((mid + 65535))" "$url$stream")
[ "$code" = "206 65536" ] || fail "got $code"
echo "ok ($code)"

ev() {
  local body
  body=$(jq -n --arg t "$1" --argjson p "$2" '{type:$t, position_ms:$p}')
  api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions/$sid/events" >/dev/null
}
# Positions are fractions of the file's length, so a short file is not
# "finished" (past 90%) by a seek meant for a feature film.
dur=$(echo "$session" | jq -r '.version.duration_ms // 6000000')
early=$((dur / 50)) seek_to=$((dur * 2 / 5)) stop_at=$((dur * 21 / 50))
step "events: start, progress, seek, stop"
ev start 0
ev progress "$early"
body=$(jq -n --argjson p "$seek_to" --argjson f "$early" '{type:"seek", position_ms:$p, from_position_ms:$f}')
api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions/$sid/events" >/dev/null
ev progress "$seek_to"
ev stop "$stop_at"
echo "ok"

step "resume position"
body=$(jq -n --argjson id "$item" --argjson caps "$caps" '{item_id:$id, capabilities:$caps}')
second=$(api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions")
resume=$(echo "$second" | jq -r .resume_position_ms)
[ "$resume" = "$stop_at" ] || fail "resume_position_ms $resume, want $stop_at"
sid=$(echo "$second" | jq -r .session_id)
ev stop "$resume"
echo "ok ($stop_at ms)"

step "continue watching"
api "$url/api/v1/home" | jq -e --argjson id "$item" '.rows[] | select(.id == "continue_watching") | .items[] | select(.id == $id)' >/dev/null ||
  fail "item $item not in Continue Watching"
echo "ok"

if [ "$keep" = "0" ]; then
  api -X PUT -H 'Content-Type: application/json' -d '{"played":false}' "$url/api/v1/items/$item/watched"
fi

step "activity log"
if hist=$(api "$url/api/v1/admin/activity/sessions?limit=2" 2>/dev/null); then
  echo "$hist" | jq -c '[.sessions[] | {decision, state, device_name, last_position_ms}]'
else
  echo "skipped (not an admin, or not on the LAN)"
fi

step "web app"
page=$(curl -fsS -D "$tmp_headers" "$url/")
grep -q 'js/app.js' <<< "$page" || fail "/ is not the web app"
grep -qi '^content-security-policy:' "$tmp_headers" || fail "no Content-Security-Policy on /"
echo "ok"

step "host check (DNS rebinding)"
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: rebind.example' "$url/api/v1/server")
[ "$code" = "421" ] || fail "a foreign Host got $code, want 421"
echo "ok (421)"

step "sidecar subtitle as WebVTT"
vtt_url=$(echo "$second" | jq -r '[.webvtt_subtitles[]? | select(.embedded | not)][0].url // empty')
if [ -n "$vtt_url" ]; then
  head=$(api "$url$vtt_url" | head -c 6)
  [ "$head" = "WEBVTT" ] || fail "subtitle did not convert: $head"
  echo "ok"
else
  # Embedded tracks are skipped on purpose: reading one out of a large file is a full pass over the NAS.
  echo "skipped (this movie has no sidecar subtitle)"
fi

step "converted stream for a browser"
browser='{"containers":["mp4","webm"],"video_codecs":[{"codec":"h264"},{"codec":"hevc","max_bit_depth":10}],
  "audio_codecs":[{"codec":"aac","max_channels":8},{"codec":"opus"}],"fmp4_stream":true,"default_audio_only":true}'
conv=""
for id in $(api "$url/api/v1/libraries/$movie_lib/items?sort=added&limit=40" | jq -r '.items[].id'); do
  body=$(jq -n --argjson id "$id" --argjson caps "$browser" '{item_id:$id, start_position_ms:0, capabilities:$caps}')
  if resp=$(api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions" 2>/dev/null) &&
    [ "$(echo "$resp" | jq -r .stream.protocol)" = "fmp4" ]; then
    conv="$resp"
    break
  fi
  # A direct-play session that is not used still needs closing.
  if [ -n "${resp:-}" ]; then
    csid=$(echo "$resp" | jq -r '.session_id // empty')
    [ -z "$csid" ] || api -H 'Content-Type: application/json' -d '{"type":"stop","position_ms":0}' "$url/api/v1/playback/sessions/$csid/events" >/dev/null || true
  fi
done
if [ -n "$conv" ]; then
  csid=$(echo "$conv" | jq -r .session_id)
  seek=$(api "$url$(echo "$conv" | jq -r .stream.seek_url)?position_ms=60000")
  start=$(echo "$seek" | jq -r .start_ms)
  [ "$start" -le 60000 ] && [ "$start" -gt 0 ] || fail "keyframe for 60 s at $start ms"
  # The stream is fragmented MP4: the first box is ftyp. Reading a little and
  # hanging up also proves the server stops ffmpeg when the client leaves.
  box=$(api "$url$(echo "$seek" | jq -r .url)" 2> /dev/null | head -c 8 | tail -c 4 || true)
  [ "$box" = "ftyp" ] || fail "converted stream does not start with an MP4 ftyp box ($box)"
  api -H 'Content-Type: application/json' -d '{"type":"stop","position_ms":0}' "$url/api/v1/playback/sessions/$csid/events" >/dev/null
  echo "ok ($(echo "$conv" | jq -r .decision), starts at ${start} ms)"
else
  echo "skipped (every one of the 40 newest movies plays directly in a browser)"
fi

step "collections and filters"
cols=$(api "$url/api/v1/libraries/$movie_lib/collections" | jq '.collections | length')
genres=$(api "$url/api/v1/libraries/$movie_lib/filters" | jq '.genres | length')
echo "ok ($cols collections, $genres genres)"

step "activity: now, stats, users"
if now=$(api "$url/api/v1/admin/activity/now" 2>/dev/null); then
  plays=$(api "$url/api/v1/admin/activity/stats?days=7" | jq .plays)
  users=$(api "$url/api/v1/admin/activity/users" | jq '.users | length')
  echo "ok ($(echo "$now" | jq .stream_count) playing, $plays plays this week, $users users)"
else
  echo "skipped (not an admin, or not on the LAN)"
fi

api -X POST "$url/api/v1/auth/logout" >/dev/null
echo "PASS"

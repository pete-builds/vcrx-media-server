#!/usr/bin/env bash
# Exercise a running VCRX server the way the TV app does: sign in, browse, start
# playback of a movie, read the stream with Range requests, report progress and
# stop, then confirm the next session resumes from the saved position.
#
#   VCRX_URL=http://192.168.86.20:3750 VCRX_USER=pete VCRX_PASSWORD=... scripts/smoke-api.sh
#
# Needs curl and jq. Leaves one finished playback session in the activity log and
# a resume point on the movie it picked; pass VCRX_KEEP_POSITION=0 to clear it.
set -euo pipefail

url="${VCRX_URL:?set VCRX_URL, e.g. http://192.168.86.20:3750}"
user="${VCRX_USER:?set VCRX_USER}"
pass="${VCRX_PASSWORD:?set VCRX_PASSWORD}"
keep="${VCRX_KEEP_POSITION:-1}"

fail() { echo "FAIL: $*" >&2; exit 1; }
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
step "events: start, progress, seek, stop"
ev start 0
ev progress 15000
body=$(jq -n '{type:"seek", position_ms:600000, from_position_ms:15000}')
api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions/$sid/events" >/dev/null
ev progress 612000
ev stop 615000
echo "ok"

step "resume position"
body=$(jq -n --argjson id "$item" --argjson caps "$caps" '{item_id:$id, capabilities:$caps}')
second=$(api -H 'Content-Type: application/json' -d "$body" "$url/api/v1/playback/sessions")
resume=$(echo "$second" | jq -r .resume_position_ms)
[ "$resume" = "615000" ] || fail "resume_position_ms $resume, want 615000"
sid=$(echo "$second" | jq -r .session_id)
ev stop "$resume"
echo "ok (615000 ms)"

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

api -X POST "$url/api/v1/auth/logout" >/dev/null
echo "PASS"

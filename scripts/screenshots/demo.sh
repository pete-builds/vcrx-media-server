#!/usr/bin/env bash
# Runs a throwaway VCRX with a demo library, for the README screenshots.
#
#   VCRX_TMDB_API_KEY_FILE=/path/to/key scripts/screenshots/demo.sh      start it
#   scripts/screenshots/demo.sh down                                     remove it
#
# The library is stub files (black video, silence, each film's real length,
# padded sparsely to a believable size) named for Blender Studio open movies
# (CC BY) and US public-domain films; TMDb fills in the posters and plots, so
# the key file (holding only the key) is required. Everything lives in
# $VCRX_DEMO_DIR (default ~/vcrx-demo). The container runs from $VCRX_IMAGE
# (default vcrx-media-server:local), listens on 127.0.0.1:$VCRX_DEMO_PORT
# (3790) only, signs that address in as "admin", and has two limited users,
# Family and Guest, with a few plays for Continue Watching and Home's rows.
# Needs Docker, curl and jq; ffmpeg runs inside the image.
set -euo pipefail

demo="${VCRX_DEMO_DIR:-$HOME/vcrx-demo}"
image="${VCRX_IMAGE:-vcrx-media-server:local}"
port="${VCRX_DEMO_PORT:-3790}"
name=vcrx-demo
url="http://127.0.0.1:$port"

if [ "${1:-}" = down ]; then
  docker rm -f "$name" > /dev/null 2>&1 || true
  rm -rf "$demo"
  echo "demo: removed"
  exit 0
fi
key="${VCRX_TMDB_API_KEY_FILE:?set VCRX_TMDB_API_KEY_FILE to a file holding a TMDb key}"
[ -s "$key" ] || { echo "demo: $key is empty or missing" >&2; exit 1; }
mkdir -p "$demo/config" "$demo/cache" "$demo/media/Movies"

# title|year|minutes|4k (also a 2160p version)
films=(
  "Big Buck Bunny|2008|10|" "Sintel|2010|15|4k" "Tears of Steel|2012|12|4k" "Elephants Dream|2006|11|"
  "Cosmos Laundromat|2015|12|" "Spring|2019|8|" "Agent 327: Operation Barbershop|2017|4|" "Wing It!|2023|4|"
  "Caminandes: Llama Drama|2013|2|" "Caminandes: Gran Dillama|2013|3|" "Caminandes: Llamigos|2016|3|"
  "Night of the Living Dead|1968|96|" "Nosferatu|1922|94|" "His Girl Friday|1940|92|" "The General|1926|78|"
  "Metropolis|1927|153|" "Sherlock Jr.|1924|45|" "The Phantom of the Opera|1925|93|" "Charade|1963|113|"
  "Plan 9 from Outer Space|1957|79|" "Detour|1945|67|" "The Little Shop of Horrors|1960|72|" "D.O.A.|1949|83|"
  "Carnival of Souls|1962|78|" "The Kid|1921|68|" "Safety Last!|1923|74|" "The Gold Rush|1925|95|"
  "The Cabinet of Dr. Caligari|1920|77|" "A Trip to the Moon|1902|13|" "The Last Man on Earth|1964|86|"
  "House on Haunted Hill|1959|75|" "My Man Godfrey|1936|94|" "Nothing Sacred|1937|77|" "The Hitch-Hiker|1953|71|"
  "Scarlet Street|1945|102|" "Kansas City Confidential|1952|99|" "McLintock!|1963|127|" "Meet John Doe|1941|122|"
)

stub() { # file seconds size megabytes
  [ -f "$demo/media/$1" ] && return
  docker run --rm --user "$(id -u):$(id -g)" --entrypoint ffmpeg -v "$demo/media:/m" "$image" -loglevel error -y \
    -f lavfi -i "color=c=black:s=$3:r=1/10:d=$2" -f lavfi -i "anullsrc=r=48000:cl=stereo" -t "$2" \
    -c:v libx264 -preset ultrafast -tune stillimage -pix_fmt yuv420p -c:a aac -b:a 16k "/m/$1"
  # Sparse: the version picker shows a real film's size, the disk holds a few MB.
  truncate -s "${4}M" "$demo/media/$1"
}
for f in "${films[@]}"; do
  IFS='|' read -r title year mins extra <<< "$f"
  dir="Movies/$title ($year)"
  mkdir -p "$demo/media/$dir"
  stub "$dir/$title ($year) - 1080p.mkv" $((mins * 60)) 1920x1080 $((mins * 66))
  if [ "$extra" = 4k ]; then stub "$dir/$title ($year) - 2160p.mkv" $((mins * 60)) 3840x2160 $((mins * 210)); fi
done

cat > "$demo/config/vcrx.yaml" << 'YAML'
server_name: VCRX
auto_sign_in_user: admin
trusted_networks: [127.0.0.1/32, "::1/128"]
libraries:
  - name: Movies
    kind: movie
    path: /media/Movies
    scan_interval_minutes: 60
    overlays:
      - { type: rating, position: top_left, sources: [tmdb] }
      - { type: resolution, position: bottom_right }
YAML

docker rm -f "$name" > /dev/null 2>&1 || true
docker run -d --name "$name" --network host --user "$(id -u):$(id -g)" \
  -e VCRX_BIND=127.0.0.1 -e VCRX_PORT="$port" -e TZ=America/New_York -e VCRX_TMDB_API_KEY_FILE=/run/tmdb-key \
  -v "$(cd "$(dirname "$key")" && pwd)/$(basename "$key"):/run/tmdb-key:ro" \
  -v "$demo/config:/config" -v "$demo/cache:/cache" -v "$demo/media:/media:ro" \
  --cpus 2 --memory 1g "$image" serve > /dev/null
for _ in $(seq 1 50); do curl -fsS "$url/healthz" > /dev/null 2>&1 && break; sleep 0.3; done

if [ ! -f "$demo/admin.pass" ]; then
  (umask 077; head -c 18 /dev/urandom | base64 | tr -d '/+=' > "$demo/admin.pass")
fi
users="$(docker exec "$name" vcrx user list 2> /dev/null || true)"
if ! grep -qw admin <<< "$users"; then docker exec -i "$name" vcrx user add -username admin -admin < "$demo/admin.pass"; fi
docker exec "$name" vcrx library scan -name Movies
docker exec "$name" vcrx metadata refresh -all > /dev/null 2>&1 || true
docker exec "$name" vcrx metadata status || true

login() { # user password
  jq -n --arg u "$1" --arg p "$2" \
    '{username:$u, password:$p, device:{id:("demo-"+$u), name:"Living Room", platform:"web", client:"web", client_version:"1"}}' |
    curl -fsS -H 'Content-Type: application/json' -d @- "$url/api/v1/auth/login" | jq -r .token
}
api() { local t="$1"; shift; curl -fsS -H "Authorization: Bearer $t" "$@"; }
admin=$(login admin "$(cat "$demo/admin.pass")")
lib=$(api "$admin" "$url/api/v1/libraries" | jq -r '.libraries[0].id')
if [ -f "$demo/users.pass" ]; then
  echo "demo: users and plays already seeded"
  exit 0
fi
(umask 077; head -c 18 /dev/urandom | base64 | tr -d '/+=' > "$demo/users.pass")
pw="$(cat "$demo/users.pass")"
uid() { api "$admin" "$url/api/v1/admin/users" | jq -r --arg u "$1" '.users[] | select(.username == $u) | .id'; }
for u in Family Guest; do
  jq -n --arg u "$u" --arg p "$pw" --argjson l "$lib" '{username:$u, password:$p, library_ids:[$l]}' |
    api "$admin" -H 'Content-Type: application/json' -d @- "$url/api/v1/admin/users" > /dev/null
done
api "$admin" -X PUT -H 'Content-Type: application/json' "$url/api/v1/admin/users/$(uid Family)/policy" \
  -d '{"max_resolution":"1080p","max_content_rating":"PG-13","allow_transcode":true,"allow_4k_transcode":false,"allow_labels":[],"deny_labels":[]}'
api "$admin" -X PUT -H 'Content-Type: application/json' "$url/api/v1/admin/users/$(uid Guest)/policy" \
  -d '{"max_resolution":"1080p","allow_transcode":false,"allow_4k_transcode":false,"allow_labels":[],"deny_labels":[]}'

caps='{"containers":["mkv","mp4"],"video_codecs":[{"codec":"h264","profiles":["baseline","main","high","constrained baseline"],"max_bit_depth":8,"max_width":3840,"max_height":2160}],"audio_codecs":[{"codec":"aac"}],"hdr_types":[]}'
id_of() { api "$admin" "$url/api/v1/libraries/$lib/items?limit=100" | jq -r --arg t "$1" '.items[] | select(.title == $t) | .id'; }
play() { # token title fraction-watched
  local s sid at
  s=$(jq -n --argjson id "$(id_of "$2")" --argjson caps "$caps" '{item_id:$id, start_position_ms:0, capabilities:$caps}' |
    api "$1" -H 'Content-Type: application/json' -d @- "$url/api/v1/playback/sessions")
  sid=$(jq -r .session_id <<< "$s")
  at=$(jq -r --argjson f "$3" '.version.duration_ms * $f | floor' <<< "$s")
  for ev in "start 0" "progress $((at / 2))" "stop $at"; do
    jq -n --arg t "${ev% *}" --argjson p "${ev#* }" '{type:$t, position_ms:$p}' |
      api "$1" -H 'Content-Type: application/json' -d @- "$url/api/v1/playback/sessions/$sid/events" > /dev/null
  done
}
family=$(login Family "$pw")
guest=$(login Guest "$pw")
for t in "Big Buck Bunny" "Sintel" "Spring" "The General" "Caminandes: Llama Drama"; do play "$admin" "$t" 0.97; done
# Family has a PG-13 ceiling, so only rated titles.
for t in "Big Buck Bunny" "Sintel" "Spring" "A Trip to the Moon"; do play "$family" "$t" 0.97; done
for t in "Sintel" "Night of the Living Dead" "Big Buck Bunny"; do play "$guest" "$t" 0.97; done
play "$admin" "Metropolis" 0.41
play "$admin" "His Girl Friday" 0.63
play "$admin" "Tears of Steel" 0.28
for t in "$family" "$guest" "$admin"; do api "$t" -X POST "$url/api/v1/auth/logout" > /dev/null || true; done
echo "demo: $url (from another machine: ssh -L $port:127.0.0.1:$port <host>)"

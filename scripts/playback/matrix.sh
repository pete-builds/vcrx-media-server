#!/usr/bin/env bash
# Playback decision matrix: for every "Corpus" movie on a VCRX dev server, ask
# for a session with each modelled client profile, then for converted streams
# fetch the first seconds and ffprobe them. Prints a TSV.
# Usage: matrix.sh <base-url> <workdir>
# The server must sign in loopback clients without a password (scripts/dev-server.sh does).
set -euo pipefail
url=${1:?url}
work=${2:?workdir}
mkdir -p "$work"
H=(-H 'Content-Type: application/json' -H 'X-VCRX-Client: matrix')
jar="$work/jar"
curl -fsS -c "$jar" "${H[@]}" -d '{"cookie":true,"device":{"id":"matrix","name":"matrix"}}' "$url/api/v1/auth/auto" > /dev/null
api() { curl -sS -b "$jar" "${H[@]}" "$@"; }

aud_all='[{"codec":"aac","max_channels":8},{"codec":"mp3","max_channels":2},{"codec":"opus","max_channels":8},{"codec":"flac","max_channels":8},{"codec":"ac3","max_channels":6},{"codec":"eac3","max_channels":8}]'
aud_ff='[{"codec":"aac","max_channels":8},{"codec":"mp3","max_channels":2},{"codec":"opus","max_channels":8},{"codec":"flac","max_channels":8}]'
h264='{"codec":"h264","profiles":["constrained baseline","baseline","main","high"],"max_bit_depth":8,"max_width":3840,"max_height":2160,"max_fps":60}'
hevc='{"codec":"hevc","profiles":["main","main 10"],"max_bit_depth":10,"max_width":3840,"max_height":2160,"max_fps":60}'
av1='{"codec":"av1","profiles":[],"max_bit_depth":10,"max_width":3840,"max_height":2160,"max_fps":60}'

profile() {
  case $1 in
    chrome) echo "{\"containers\":[\"mp4\",\"mov\",\"webm\",\"mkv\"],\"video_codecs\":[$h264,$hevc,$av1],\"audio_codecs\":$aud_all,\"hdr_types\":[],\"subtitle_codecs\":[\"webvtt\"],\"max_width\":3840,\"max_height\":2160,\"fmp4_stream\":true,\"default_audio_only\":true}" ;;
    firefox) echo "{\"containers\":[\"mp4\",\"mov\",\"webm\"],\"video_codecs\":[$h264,$av1],\"audio_codecs\":$aud_ff,\"hdr_types\":[],\"subtitle_codecs\":[\"webvtt\"],\"max_width\":3840,\"max_height\":2160,\"fmp4_stream\":true,\"default_audio_only\":true}" ;;
    safari) echo "{\"containers\":[\"mp4\",\"mov\"],\"video_codecs\":[$h264,$hevc],\"audio_codecs\":$aud_all,\"hdr_types\":[\"hdr10\",\"hlg\"],\"subtitle_codecs\":[\"webvtt\"],\"max_width\":3840,\"max_height\":2160,\"hls_stream\":true,\"default_audio_only\":true}" ;;
  esac
}

lib=$(api "$url/api/v1/libraries" | jq -r '[.libraries[] | select(.kind=="movie")][0].id')
printf 'title\tclient\tdecision\tprotocol\tout_video\tout_audio\treason\n'
api "$url/api/v1/libraries/$lib/items?limit=200" | jq -r '.items[] | select(.title|startswith("Corpus")) | "\(.id)\t\(.title)"' | sort -t$'\t' -k2 |
while IFS=$'\t' read -r id _; do
  for c in chrome firefox safari; do
    body=$(jq -n --argjson id "$id" --argjson caps "$(profile "$c")" '{item_id:$id,start_position_ms:0,capabilities:$caps}')
    resp=$(api -d "$body" "$url/api/v1/playback/sessions")
    dec=$(jq -r '.decision // ("ERR " + (.error // "?"))' <<< "$resp")
    proto=$(jq -r '.stream.protocol // "-"' <<< "$resp")
    reason=$(jq -r '.reason // .message // ""' <<< "$resp" | tr '\t\n' '  ' | cut -c1-90)
    ov=- oa=-
    sid=$(jq -r '.session_id // empty' <<< "$resp")
    if [ "$proto" = fmp4 ]; then
      su=$(jq -r '.stream.url' <<< "$resp")
      f="$work/$id-$c.mp4"
      api --max-time 12 -o "$f" "$url$su" 2> /dev/null || true
      if [ -s "$f" ]; then
        # Count packets, not just the header: ffmpeg writes the header before it
        # can fail, so a stream that died at once still probes as h264+aac.
        ov=$(ffprobe -v error -count_packets -select_streams v:0 -show_entries stream=codec_name,height,nb_read_packets -of csv=p=0 "$f" 2> /dev/null | head -1 | tr ',' '/')
        oa=$(ffprobe -v error -count_packets -select_streams a:0 -show_entries stream=codec_name,channels,nb_read_packets -of csv=p=0 "$f" 2> /dev/null | head -1 | tr ',' '/')
        [ -n "$ov" ] || ov=unprobeable
      else
        ov=empty
      fi
    fi
    [ -n "$sid" ] && api -d '{"event":"stop","position_ms":0}' "$url/api/v1/playback/sessions/$sid/events" > /dev/null 2>&1 || true
    fname=$(jq -r ".version.file_name // \"?\"" <<< "$resp"); printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n" "${fname#Corpus }" "$c" "$dec" "$proto" "${ov:--}" "${oa:--}" "$reason"
  done
done

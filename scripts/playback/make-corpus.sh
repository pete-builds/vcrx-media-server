#!/usr/bin/env bash
# Generate a small synthetic playback corpus for VCRX compatibility testing.
# Usage: make-corpus.sh <outdir>
set -euo pipefail
out=${1:?outdir}
mkdir -p "$out"
cd "$out"
D=8   # seconds per clip
V="testsrc2=size=640x360:rate=24"
V4K="testsrc2=size=3840x2160:rate=24"
ff() { ffmpeg -hide_banner -loglevel error -y "$@"; }

# Multichannel sources
ch() { # $1 = channels -> lavfi audio input spec
  case $1 in
    2) echo "aevalsrc=sin(440*2*PI*t)|sin(550*2*PI*t):s=48000:c=stereo" ;;
    6) echo "aevalsrc=sin(440*2*PI*t)|sin(495*2*PI*t)|sin(550*2*PI*t)|sin(110*2*PI*t)|sin(660*2*PI*t)|sin(770*2*PI*t):s=48000:c=5.1" ;;
    8) echo "aevalsrc=sin(440*2*PI*t)|sin(495*2*PI*t)|sin(550*2*PI*t)|sin(110*2*PI*t)|sin(660*2*PI*t)|sin(770*2*PI*t)|sin(880*2*PI*t)|sin(990*2*PI*t):s=48000:c=7.1" ;;
  esac
}

# Video codec x container, AAC stereo
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a aac h264_aac.mp4
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a aac h264_aac.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a aac -output_ts_offset 600 h264_aac_offset600.ts
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libx265 -x265-params log-level=error -tag:v hvc1 -pix_fmt yuv420p -c:a aac hevc_aac.mp4
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libx265 -x265-params log-level=error -pix_fmt yuv420p10le -c:a aac hevc10_aac.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libsvtav1 -pix_fmt yuv420p10le -c:a aac av1_aac.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -c:v libsvtav1 -pix_fmt yuv420p -c:a aac av1_aac.mp4

# Audio codecs on H.264 MKV
ff -f lavfi -i "$V" -f lavfi -i "$(ch 6)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a ac3 h264_ac3_51.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 6)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a eac3 h264_eac3_51.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 6)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a dca -strict -2 h264_dts_51.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 6)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a truehd -strict -2 h264_truehd_51.mkv
ff -f lavfi -i "$V" -f lavfi -i "$(ch 8)" -t $D -c:v libx264 -g 48 -pix_fmt yuv420p -c:a aac h264_aac_71.mkv

# HDR10 tagged HEVC (BT.2020 PQ, mastering metadata), and a 4K SDR H.264
ff -f lavfi -i "$V" -f lavfi -i "$(ch 2)" -t $D -vf "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,format=yuv420p10le" -c:v libx265 \
  -x265-params "log-level=error:hdr10=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400" \
  -c:a aac hevc_hdr10.mkv
ff -f lavfi -i "$V4K" -f lavfi -i "$(ch 2)" -t 4 -c:v libx264 -preset ultrafast -g 48 -pix_fmt yuv420p -c:a aac h264_4k.mp4

# Subtitles: external SRT, embedded SRT and ASS
cat > subs.srt <<'EOF'
1
00:00:01,000 --> 00:00:03,000
Hello from VCRX

2
00:00:04,000 --> 00:00:06,000
Second cue
EOF
ff -i subs.srt subs.ass
cp h264_aac.mkv external_srt.mkv && cp subs.srt external_srt.en.srt
ff -i h264_aac.mkv -i subs.srt -c copy -c:s srt -metadata:s:s:0 language=eng embedded_srt.mkv
ff -i h264_aac.mkv -i subs.ass -c copy -c:s ass -metadata:s:s:0 language=eng embedded_ass.mkv

# Long-GOP TS for seek tests (60 s, keyframe every 50 frames, offset start time)
ff -f lavfi -i "testsrc2=size=640x360:rate=25" -f lavfi -i "$(ch 2)" -t 60 -c:v libx264 -g 50 -keyint_min 50 -sc_threshold 0 -pix_fmt yuv420p -c:a aac -output_ts_offset 600 seek_60s_offset600.ts
ff -f lavfi -i "testsrc2=size=640x360:rate=25" -f lavfi -i "$(ch 2)" -t 60 -c:v libx264 -g 50 -keyint_min 50 -sc_threshold 0 -pix_fmt yuv420p -c:a aac seek_60s.mkv

find . -type f | wc -l

# Playback compatibility

What VCRX does with each kind of file, for each kind of client, and how much of that has actually been run.
A row is PASS only when a test executed it. "Decided" means the server chose a path and, for fMP4 output,
ffprobe read the converted stream; it does not mean a browser rendered frames.

Last run: 2026-11-01 01:50 ET, main after #81 to #92, macOS, ffmpeg 8.1 (Homebrew), dev server
(`scripts/dev-server.sh`). First run 00:00 ET on fe064b5.
Production runs Debian's ffmpeg in the image, which is a different version: results can differ (see "ffmpeg version" below).

## How to rerun

1. Generate the corpus: `scripts/playback/make-corpus.sh <dir>` (19 clips, test patterns and tones, about 35 s).
2. Put each clip in its own `Movies/Corpus <name> (<year>)/` folder of a dev server's media and rescan.
3. Run `scripts/playback/matrix.sh <url> <workdir>`: for each clip and client profile it starts a session, records
   decision and protocol, fetches any fMP4 stream and counts its video and audio packets, then stops the session.

Count packets, never just probe: ffmpeg writes the MP4 header before it can fail, so a stream that died at once
still probes as "h264 + aac". The first run of this matrix made exactly that mistake and passed broken TS playback.
A CI job for the matrix is still to do.

## Client profiles

Modelled from `server/web/static/js/caps.js`, not captured from real browsers:

- **chrome**: mp4, mov, webm, mkv; H.264, HEVC (main, main 10), AV1 10-bit; AAC, MP3, Opus, FLAC, AC-3 (6 ch), E-AC-3; SDR display; fmp4_stream.
- **firefox**: mp4, mov, webm; H.264, AV1; AAC, MP3, Opus, FLAC; SDR; fmp4_stream.
- **safari**: mp4, mov; H.264, HEVC; AAC, MP3, Opus, FLAC, AC-3, E-AC-3; HDR10 and HLG display; hls_stream.
- **google tv**: capabilities come from the device's MediaCodecList at runtime. Not modelled; hardware only.

## Results

| Source | Chrome | Firefox | Safari |
|---|---|---|---|
| H.264 + AAC, MP4 | direct | direct | direct |
| H.264 + AAC, MKV | direct | remux, fMP4 h264+aac probed | remux, HLS (not fetched) |
| H.264 4K, MP4 | direct | direct | direct |
| H.264 + AAC, TS (start 601 s) | remux, fMP4, full 192 frames | same | remux, HLS |
| H.264 + AC-3 5.1, MKV | direct | audio_transcode, aac 2 ch probed | remux, HLS |
| H.264 + E-AC-3 5.1, MKV | direct | audio_transcode, aac 2 ch probed | remux, HLS |
| H.264 + DTS 5.1, MKV | audio_transcode, aac 2 ch probed | same | audio_transcode, HLS |
| H.264 + TrueHD 5.1, MKV | audio_transcode, aac 2 ch probed | same | audio_transcode, HLS |
| H.264 + AAC 7.1, MKV | direct | remux, aac 8 ch probed | remux, HLS |
| HEVC 8-bit + AAC, MP4 | direct | transcode to H.264, probed | direct |
| HEVC 10-bit + AAC, MKV | direct | transcode to H.264, probed | remux, HLS |
| HEVC HDR10, MKV | direct, with HDR-on-SDR warning | transcode (tone map path), probed | remux, HLS |
| AV1 + AAC, MP4 | direct | direct | transcode, HLS |
| AV1 + AAC, MKV | direct | remux, av1+aac probed | transcode, HLS |
| Embedded SRT, MKV | direct | remux | remux, HLS |
| Embedded ASS, MKV | direct | remux | remux, HLS |
| External .en.srt | direct | remux | remux, HLS |

Every decision above matches the decision ladder in `server/internal/playback/decision.go`. Every fMP4 stream
delivered its whole clip (192 video frames for the 8 s clips, 1500 for the 60 s ones). On fe064b5 the two TS clips
delivered 5 frames and stopped (fixed in #84).

## Seeking (automated, API level)

`GET /playback/sessions/{id}/seek?position_ms=N` on a remuxed stream, same 60 s content, keyframe every 2 s:

| Asked | MKV, fe064b5 | TS (start 601 s), fe064b5 | MKV, now | TS, now |
|---|---|---|---|---|
| 10000 | 10000 | **0** | 10000 | 8021 |
| 30000 | 30000 | **0** | 30000 | 28021 |
| 41300 | 40000 | **0** | 40000 | 40021 |
| 55000 | 54000 | **0** | 54000 | 54021 |

PASS now. The TS keyframes sit 21 ms after each 2 s mark from the file's start, so the keyframe at or before 10 s
is the one at 8.021 s. A stream from 30 s delivers 750 frames (30 s at 25 fps) for both. Regression test:
`TestKeyframeAtTransportStream`.

## Not yet tested

- **Rendering.** No row above proves a browser decoded frames. The CI job `web` (headless Chrome,
  `scripts/web-smoke.mjs`) plays the dev library's own clips; it does not cover this corpus.
- **HLS output** for Safari was decided, not fetched or validated.
- **Dolby Vision.** No synthetic sample exists; needs a real profile 5 and profile 8 file. Code reading suggests
  profile 5 transcodes with wrong colours (P2, suspected).
- **PGS and VobSub subtitles.** Not playable in browsers: the web player lists them greyed out with "Picture subtitles need the TV app" (web smoke test, against a PGS sidecar); untested on TV.
- **Skip intro, skip credits, Up Next, completion.** Covered by unit tests on the server, not by this corpus.
- **Concurrent clients and interrupted streams.** Covered by fake-ffmpeg slot tests; one session can hold every
  conversion slot (P2).
- **Hardware.** Google TV (adb, only when the TV is idle), real Safari on macOS, real Firefox. Never claim these pass
  without running them.

## ffmpeg version

`TestFederationConvertedAndHLSThroughTheProxy` failed 3 of 3 runs on macOS with ffmpeg 8.1 on fe064b5 and passes
with #84 merged (#84 was not isolated as the cause); the fixture's start time also differs between ffmpeg builds (0 locally, a few ms before 0 in CI). The production image installs Debian's ffmpeg without a version pin, so a rebuild can change behaviour with
no code change. Pinning it is a release-checklist item.

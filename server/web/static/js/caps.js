// What this browser can decode, in the server's vocabulary (ffprobe codec and
// profile names), so the server picks a version the browser can play directly
// and says plainly why when there is none.

function can(type) {
  const v = document.createElement("video");
  const a = v.canPlayType(type);
  if (a === "probably" || a === "maybe") return true;
  return typeof MediaSource !== "undefined" && MediaSource.isTypeSupported && MediaSource.isTypeSupported(type);
}

export function capabilities() {
  const containers = [];
  if (can("video/mp4")) containers.push("mp4", "mov");
  if (can("video/webm")) containers.push("webm");
  // Chromium plays Matroska through its own demuxer and says so for this type.
  if (can('video/x-matroska; codecs="avc1.64001F, mp4a.40.2"') || can("video/x-matroska")) containers.push("mkv");

  const video = [];
  if (can('video/mp4; codecs="avc1.640028"')) {
    video.push({ codec: "h264", profiles: ["constrained baseline", "baseline", "main", "high"], max_bit_depth: 8, max_width: 3840, max_height: 2160, max_fps: 60 });
  }
  const hevc8 = can('video/mp4; codecs="hvc1.1.6.L153.B0"');
  const hevc10 = can('video/mp4; codecs="hvc1.2.4.L153.B0"');
  if (hevc8 || hevc10) {
    video.push({ codec: "hevc", profiles: hevc10 ? ["main", "main 10"] : ["main"], max_bit_depth: hevc10 ? 10 : 8, max_width: 3840, max_height: 2160, max_fps: 60 });
  }
  if (can('video/mp4; codecs="av01.0.08M.10"')) video.push({ codec: "av1", profiles: [], max_bit_depth: 10, max_width: 3840, max_height: 2160, max_fps: 60 });
  else if (can('video/mp4; codecs="av01.0.08M.08"')) video.push({ codec: "av1", profiles: [], max_bit_depth: 8, max_width: 3840, max_height: 2160, max_fps: 60 });
  if (can('video/webm; codecs="vp9"')) video.push({ codec: "vp9", profiles: [], max_bit_depth: 8, max_width: 3840, max_height: 2160, max_fps: 60 });
  if (can('video/webm; codecs="vp8"')) video.push({ codec: "vp8", profiles: [], max_bit_depth: 8, max_width: 1920, max_height: 1080, max_fps: 60 });

  const audio = [];
  const add = (codec, type, channels = 8) => { if (can(type)) audio.push({ codec, max_channels: channels, decoder: "browser" }); };
  add("aac", 'audio/mp4; codecs="mp4a.40.2"');
  add("mp3", "audio/mpeg", 2);
  add("opus", 'audio/webm; codecs="opus"');
  add("vorbis", 'audio/webm; codecs="vorbis"');
  add("flac", "audio/flac");
  add("ac3", 'audio/mp4; codecs="ac-3"', 6);
  add("eac3", 'audio/mp4; codecs="ec-3"');

  const hdr = window.matchMedia && window.matchMedia("(dynamic-range: high)").matches ? ["hdr10", "hlg"] : [];
  return {
    containers, video_codecs: video, audio_codecs: audio, hdr_types: hdr, subtitle_codecs: ["webvtt"],
    max_width: 3840, max_height: 2160, max_bitrate: 0,
    // The player handles converted streams, and a <video> plays only the
    // file's default audio track. Safari plays video only from a server that
    // answers byte ranges, which a converted stream cannot, so it is not
    // offered one: files that need converting are refused with the reason.
    fmp4_stream: !isSafari(), default_audio_only: true,
  };
}

// isSafari is true for WebKit Safari (desktop and iOS), false for Chromium,
// Edge and Firefox, whose user agents also mention Safari.
export function isSafari() {
  const ua = navigator.userAgent;
  return /Safari\//.test(ua) && !/Chrome\/|Chromium\/|CriOS\/|Edg\/|FxiOS\/|Firefox\//.test(ua);
}

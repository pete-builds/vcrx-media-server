import { api, ApiError } from "../api.js";
import { h, icon, errorBox, episodeCode, clock, toast } from "../dom.js";
import { capabilities, isSafari } from "../caps.js";

// The player: asks the server for a session with this browser's capabilities,
// plays it, and reports start, pause, seek, progress and stop so resume points
// and Activity stay right.
//
// Two kinds of stream:
// - progressive: the file itself, seekable by range; native controls.
// - fmp4: a converted stream (video copied, audio converted) that cannot be
//   seeked by range. Seeking asks the server for a new stream; the server says
//   where it starts (a keyframe), and the position is that start plus the
//   element's own time. These get VCRX controls.
export async function playerView({ match, params }) {
  const itemId = Number(match[1]);
  const body = { item_id: itemId, capabilities: capabilities() };
  if (params.get("version")) body.version_id = Number(params.get("version"));
  if (params.get("start") !== null) body.start_position_ms = Number(params.get("start")) || 0;

  const back = () => (history.length > 1 ? history.back() : (location.hash = `#/item/${itemId}`));
  const backBtn = h("button", { class: "btn btn-small", type: "button", onclick: back, "aria-label": "Back" }, icon("back"), "Back");
  const shell = h("div", { class: "player" });

  let session, item;
  try {
    [session, item] = await Promise.all([api("/playback/sessions", { method: "POST", body }), api(`/items/${itemId}`)]);
  } catch (err) {
    const why = err instanceof ApiError ? err.message : "The server could not start playback.";
    shell.append(h("div", { class: "top" }, backBtn), h("div", { class: "overlay" },
      errorBox("This cannot play in the browser", why, h("p", { class: "faint" },
        isSafari() ? "Safari cannot play files that need converting yet. Chrome or Edge can, and so can the TV app."
          : "The TV app may still play it."))));
    return { el: shell, title: "Cannot play" };
  }

  const converted = session.stream.protocol === "fmp4";
  const totalMs = session.version.duration_ms || 0;
  const title = item.kind === "episode" ? `${item.show_title || ""} ${episodeCode(item)} · ${item.title}` : item.title;
  const video = h("video", { autoplay: true, playsinline: true, preload: "auto" });
  video.controls = !converted;
  const status = h("div", { class: "status", hidden: true });
  const overlay = h("div", { class: "overlay", hidden: true });
  let offsetMs = 0; // where the current converted stream starts in the file
  const pos = () => offsetMs + Math.round((video.currentTime || 0) * 1000);
  const dur = () => (converted ? totalMs || null : Number.isFinite(video.duration) ? Math.round(video.duration * 1000) : null);

  function showStatus(msg) {
    status.textContent = msg;
    status.hidden = !msg;
  }

  // Subtitles: fetched only when chosen, so an embedded track is not read out
  // of a large file unless someone wants it. Cues are kept and re-timed when a
  // converted stream restarts somewhere else.
  const subs = session.webvtt_subtitles || [];
  const subSel = subs.length ? h("select", { "aria-label": "Subtitles" },
    h("option", { value: "" }, "Subtitles off"),
    subs.map((t, i) => h("option", { value: String(i) }, subLabel(t)))) : null;
  const cueCache = new Map();
  let shownCues = null;
  let textTrack = null;
  let subToken = 0;
  function applyCues() {
    if (!textTrack) {
      textTrack = video.addTextTrack("subtitles", "Subtitles", "und");
    }
    for (const c of [...(textTrack.cues || [])]) textTrack.removeCue(c);
    textTrack.mode = shownCues ? "showing" : "hidden";
    if (!shownCues) return;
    const shift = offsetMs / 1000;
    for (const c of shownCues) {
      if (c.end - shift <= 0) continue;
      textTrack.addCue(new VTTCue(Math.max(0, c.start - shift), c.end - shift, c.text));
    }
  }
  if (subSel) subSel.addEventListener("change", async () => {
    const token = ++subToken;
    if (subSel.value === "") { shownCues = null; applyCues(); return; }
    const t = subs[Number(subSel.value)];
    try {
      let cues = cueCache.get(t.track_id);
      if (!cues) {
        const text = await fetchVTT(t.url, (msg) => { if (token === subToken) showStatus(msg); });
        cues = await parseVTT(text);
        cueCache.set(t.track_id, cues);
      }
      if (token !== subToken) return;
      shownCues = cues;
      applyCues();
      showStatus("");
    } catch (e) {
      if (token === subToken) { showStatus(""); toast(e.message || "Subtitles could not be loaded."); subSel.value = ""; }
    }
  });

  // Converted streams: start or restart at a position, on the chosen audio
  // track (null lets the server pick one the browser decodes).
  let loadSeq = 0;
  let audioIndex = null;
  async function loadAt(ms) {
    const seq = ++loadSeq;
    showStatus("Loading");
    const target = Math.max(0, Math.min(ms, totalMs ? totalMs - 1000 : ms));
    const at = await api(`/playback/sessions/${session.session_id}/seek?position_ms=${target}`);
    if (seq !== loadSeq) return;
    offsetMs = at.start_ms;
    video.src = audioIndex === null ? at.url : `${at.url}&audio=${audioIndex}`;
    applyCues();
    video.play().catch(() => {});
  }

  const sid = session.session_id;
  let started = false, ended = false, paused = false, lastPos = 0, seekFrom = null;
  const report = (type, extra = {}, keepalive = false) =>
    api(`/playback/sessions/${sid}/events`, { method: "POST", body: { type, position_ms: pos(), duration_ms: dur(), ...extra }, keepalive })
      .catch((err) => {
        if (err instanceof ApiError && err.code === "session_terminated") terminated(err.message);
      });

  function terminated(message) {
    if (ended) return;
    ended = true;
    video.pause();
    video.removeAttribute("src");
    video.load();
    overlay.replaceChildren(errorBox("Playback stopped", message || "An administrator stopped this stream.", h("button", { class: "btn", type: "button", onclick: back }, "Back")));
    overlay.hidden = false;
  }

  // Converted-stream controls.
  let controls = null;
  let seekTo = null;
  if (converted) {
    const playBtn = h("button", { class: "pc-btn", type: "button", "aria-label": "Play or pause" });
    const timeLbl = h("span", { class: "pc-time" });
    const bar = h("input", { class: "pc-bar", type: "range", min: "0", max: String(totalMs || 1), step: "1000", value: "0", "aria-label": "Position" });
    const muteBtn = h("button", { class: "pc-btn", type: "button", "aria-label": "Mute" }, "Mute");
    const fsBtn = h("button", { class: "pc-btn", type: "button", "aria-label": "Full screen" }, "Full screen");
    let dragging = false;
    const draw = () => {
      playBtn.textContent = video.paused ? "Play" : "Pause";
      if (!dragging) bar.value = String(pos());
      timeLbl.textContent = `${clock(dragging ? Number(bar.value) : pos())} / ${clock(totalMs)}`;
      muteBtn.textContent = video.muted ? "Unmute" : "Mute";
    };
    playBtn.addEventListener("click", () => (video.paused ? video.play() : video.pause()));
    muteBtn.addEventListener("click", () => { video.muted = !video.muted; draw(); });
    fsBtn.addEventListener("click", () => (document.fullscreenElement ? document.exitFullscreen() : shell.requestFullscreen().catch(() => {})));
    bar.addEventListener("input", () => { dragging = true; draw(); });
    bar.addEventListener("change", () => { dragging = false; seekTo(Number(bar.value)); });
    for (const ev of ["timeupdate", "play", "pause", "volumechange"]) video.addEventListener(ev, draw);
    video.addEventListener("dblclick", () => fsBtn.click());
    video.addEventListener("click", () => playBtn.click());
    controls = h("div", { class: "pc" }, playBtn, timeLbl, bar, muteBtn, fsBtn);
    draw();
  }

  seekTo = (ms) => {
    const from = pos();
    if (converted) {
      if (started) report("seek", { from_position_ms: from });
      loadAt(ms).catch((e) => toast(e.message));
    } else {
      video.currentTime = ms / 1000;
    }
  };

  // A browser plays only a file's default audio track, so other languages are
  // reachable only through a converted stream, which picks the track itself.
  const tracks = (session.version.audio_tracks || []).filter((t) => t.index !== null && t.index !== undefined);
  let audioSel = null;
  if (converted && tracks.length > 1) {
    audioSel = h("select", { "aria-label": "Audio" },
      h("option", { value: "" }, "Audio: automatic"),
      tracks.map((t) => h("option", { value: String(t.index) }, audioLabel(t))));
    audioSel.addEventListener("change", () => {
      audioIndex = audioSel.value === "" ? null : Number(audioSel.value);
      loadAt(pos()).catch((e) => toast(e.message));
    });
  }

  shell.append(video, h("div", { class: "top" }, backBtn, h("div", { class: "ptitle" }, title), audioSel,
    converted ? h("span", { class: "pill", title: session.reason }, session.decision === "audio_transcode" ? "Audio converted" : "Repackaged") : null,
    subSel), controls, status, overlay);

  video.addEventListener("loadedmetadata", () => {
    if (!converted && session.resume_position_ms > 0 && video.currentTime < 1) video.currentTime = session.resume_position_ms / 1000;
  }, { once: true });
  video.addEventListener("playing", () => {
    showStatus("");
    if (!started) { started = true; report("start"); }
    else if (paused) { paused = false; report("resume"); }
  });
  video.addEventListener("waiting", () => { if (converted) showStatus("Loading"); });
  video.addEventListener("pause", () => {
    if (started && !video.ended && !ended) { paused = true; report("pause"); }
  });
  if (!converted) {
    video.addEventListener("seeking", () => { if (seekFrom === null) seekFrom = lastPos; });
    video.addEventListener("seeked", () => {
      if (started && seekFrom !== null) report("seek", { from_position_ms: seekFrom });
      seekFrom = null;
    });
  }
  video.addEventListener("timeupdate", () => { if (!video.seeking) lastPos = pos(); });
  video.addEventListener("ended", () => {
    if (!ended) { ended = true; report("stop"); }
    if (item.kind === "episode" && item.next_episode) upNext(item.next_episode);
  });

  // Up Next: after an episode, count down and play the following one.
  let upNextTimer = null;
  function upNext(next) {
    let left = 10;
    const msg = h("p", { class: "muted" });
    const play = () => { clearInterval(upNextTimer); location.hash = `#/play/${next.id}`; };
    const cancel = () => { clearInterval(upNextTimer); back(); };
    const tick = () => { msg.textContent = `${episodeCode(next)} · ${next.title} starts in ${left} s.`; };
    tick();
    overlay.replaceChildren(h("div", { class: "error-box up-next" }, h("h2", {}, "Up next"), msg,
      h("div", { class: "actions" },
        h("button", { class: "btn btn-primary", type: "button", onclick: play }, icon("play"), "Play now"),
        h("button", { class: "btn", type: "button", onclick: cancel }, "Back"))));
    overlay.hidden = false;
    overlay.querySelector(".btn-primary").focus();
    upNextTimer = setInterval(() => {
      left -= 1;
      if (left <= 0) play();
      else tick();
    }, 1000);
  }
  video.addEventListener("error", async () => {
    if (ended || !video.getAttribute("src")) return;
    // A 410 on the stream means it was stopped from Activity.
    try {
      const r = await fetch(video.currentSrc, { method: "HEAD", credentials: "same-origin" });
      if (r.status === 410) return terminated();
    } catch { /* fall through */ }
    const code = video.error ? video.error.code : 0;
    const msg = code === 4 ? "The browser cannot decode this file." : "The stream was interrupted.";
    report("error", { message: `web: ${msg} (media error ${code})` });
    ended = true;
    overlay.replaceChildren(errorBox("Playback failed", msg + " The TV app may still play it.", h("button", { class: "btn", type: "button", onclick: back }, "Back")));
    overlay.hidden = false;
  });

  if (converted) {
    loadAt(session.resume_position_ms || 0).catch((e) => {
      overlay.replaceChildren(errorBox("Playback failed", e.message, h("button", { class: "btn", type: "button", onclick: back }, "Back")));
      overlay.hidden = false;
    });
  } else {
    video.src = session.stream.url;
  }

  const beat = setInterval(() => {
    if (started && !ended && !video.paused) report("progress");
  }, Math.max(5000, session.heartbeat_interval_ms || 10000));

  // Hide the top bar and controls while playing and the mouse is still.
  let idleTimer;
  const wake = () => {
    shell.classList.remove("idle");
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => { if (!video.paused) shell.classList.add("idle"); }, 3000);
  };
  shell.addEventListener("mousemove", wake);
  shell.addEventListener("keydown", wake);
  wake();

  const onKey = (e) => {
    if (e.target.tagName === "SELECT" || e.target.tagName === "INPUT") return;
    if (e.key === " " || e.key === "k") { e.preventDefault(); video.paused ? video.play() : video.pause(); }
    else if (e.key === "ArrowRight") seekTo(pos() + (converted ? 30000 : 10000));
    else if (e.key === "ArrowLeft") seekTo(Math.max(0, pos() - (converted ? 30000 : 10000)));
    else if (e.key === "f") document.fullscreenElement ? document.exitFullscreen() : shell.requestFullscreen().catch(() => {});
    else if (e.key === "m") video.muted = !video.muted;
    else if (e.key === "Escape" && !document.fullscreenElement) back();
  };
  document.addEventListener("keydown", onKey);

  const stop = () => {
    if (started && !ended) { ended = true; report("stop", {}, true); }
  };
  window.addEventListener("pagehide", stop);

  return {
    el: shell,
    title,
    cleanup() {
      stop();
      loadSeq++;
      clearInterval(beat);
      clearInterval(upNextTimer);
      clearTimeout(idleTimer);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("pagehide", stop);
      video.pause();
      video.removeAttribute("src");
      video.load();
      if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    },
  };
}

function subLabel(t) {
  const lang = t.language ? languageName(t.language) : "Unknown";
  return [t.title || lang, t.is_forced ? "(forced)" : "", t.embedded ? "" : "(file)"].filter(Boolean).join(" ");
}

function audioLabel(t) {
  const ch = t.channels ? (t.channels >= 6 ? `${t.channels - 1}.1` : t.channels === 2 ? "stereo" : `${t.channels} ch`) : "";
  return [t.title || (t.language ? languageName(t.language) : "Unknown"), (t.codec || "").toUpperCase(), ch].filter(Boolean).join(" · ");
}

function languageName(code) {
  try {
    return new Intl.DisplayNames([navigator.language || "en"], { type: "language" }).of(code) || code;
  } catch {
    return code;
  }
}

// parseVTT lets the browser parse the file, then keeps plain cue data so the
// cues can be re-timed for a converted stream that starts mid-file.
function parseVTT(text) {
  return new Promise((resolve, reject) => {
    const v = document.createElement("video");
    const url = URL.createObjectURL(new Blob([text], { type: "text/vtt" }));
    const el = document.createElement("track");
    el.kind = "subtitles";
    el.src = url;
    el.default = true;
    v.append(el);
    el.track.mode = "hidden";
    const done = (fn) => { URL.revokeObjectURL(url); fn(); };
    el.addEventListener("load", () => done(() => resolve([...(el.track.cues || [])].map((c) => ({ start: c.startTime, end: c.endTime, text: c.text })))));
    el.addEventListener("error", () => done(() => reject(new Error("The subtitle file could not be read."))));
  });
}

// fetchVTT waits out the server's 202 while it reads an embedded track out of
// the file, which takes minutes on a large file.
async function fetchVTT(url, onWait) {
  const started = Date.now();
  for (;;) {
    const r = await fetch(url, { credentials: "same-origin", headers: { "X-VCRX-Client": "web" } });
    if (r.status === 200) return r.text();
    if (r.status === 202) {
      const secs = Math.min(15, Math.max(2, Number(r.headers.get("Retry-After")) || 5));
      const waited = Math.round((Date.now() - started) / 1000);
      onWait(`Preparing subtitles${waited > 10 ? ` (${waited} s; large files take a few minutes)` : ""}`);
      if (Date.now() - started > 20 * 60 * 1000) throw new Error("Subtitles took too long to prepare.");
      await new Promise((res) => setTimeout(res, secs * 1000));
      continue;
    }
    let msg = "Subtitles could not be loaded.";
    try { msg = (await r.json()).message || msg; } catch { /* keep default */ }
    throw new Error(msg);
  }
}

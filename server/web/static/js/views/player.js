import { api, ApiError } from "../api.js";
import { h, icon, errorBox, episodeCode, toast } from "../dom.js";
import { capabilities } from "../caps.js";

// The player: asks the server for a session with this browser's capabilities,
// plays the stream in a <video>, and reports start, pause, seek, progress and
// stop so resume points and Activity stay right.
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
      errorBox("This cannot play in the browser", why, h("p", { class: "faint" }, "Files the browser cannot decode will play here once VCRX can convert them. The TV app plays them now."))));
    return { el: shell, title: "Cannot play" };
  }

  const title = item.kind === "episode" ? `${item.show_title || ""} ${episodeCode(item)} · ${item.title}` : item.title;
  const video = h("video", { controls: true, autoplay: true, playsinline: true, preload: "auto" });
  video.src = session.stream.url;
  const status = h("div", { class: "status", hidden: true });
  const overlay = h("div", { class: "overlay", hidden: true });

  // Subtitles: fetched only when chosen, so an embedded track is not read out
  // of a large file unless someone wants it.
  const subs = session.webvtt_subtitles || [];
  const subSel = subs.length ? h("select", { "aria-label": "Subtitles" },
    h("option", { value: "" }, "Subtitles off"),
    subs.map((t, i) => h("option", { value: String(i) }, subLabel(t)))) : null;
  const loadedTracks = new Map();
  let subToken = 0;
  if (subSel) subSel.addEventListener("change", async () => {
    const token = ++subToken;
    for (const tt of video.textTracks) tt.mode = "disabled";
    if (subSel.value === "") return;
    const t = subs[Number(subSel.value)];
    try {
      let track = loadedTracks.get(t.track_id);
      if (!track) {
        const text = await fetchVTT(t.url, (msg) => { if (token === subToken) showStatus(msg); });
        if (token !== subToken) return;
        const url = URL.createObjectURL(new Blob([text], { type: "text/vtt" }));
        const el = h("track", { kind: "subtitles", label: subLabel(t), srclang: t.language || "und", src: url });
        video.append(el);
        track = el.track;
        loadedTracks.set(t.track_id, track);
      }
      track.mode = "showing";
      showStatus("");
    } catch (e) {
      if (token === subToken) { showStatus(""); toast(e.message || "Subtitles could not be loaded."); subSel.value = ""; }
    }
  });

  function showStatus(msg) {
    status.textContent = msg;
    status.hidden = !msg;
  }

  shell.append(video, h("div", { class: "top" }, backBtn, h("div", { class: "ptitle" }, title), subSel), status, overlay);

  // Reporting.
  const sid = session.session_id;
  let started = false, ended = false, paused = false, lastPos = 0, seekFrom = null;
  const pos = () => Math.round((video.currentTime || 0) * 1000);
  const dur = () => (Number.isFinite(video.duration) ? Math.round(video.duration * 1000) : null);
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

  video.addEventListener("loadedmetadata", () => {
    if (session.resume_position_ms > 0 && video.currentTime < 1) video.currentTime = session.resume_position_ms / 1000;
  }, { once: true });
  video.addEventListener("playing", () => {
    if (!started) { started = true; report("start"); }
    else if (paused) { paused = false; report("resume"); }
  });
  video.addEventListener("pause", () => {
    if (started && !video.ended && !ended) { paused = true; report("pause"); }
  });
  video.addEventListener("seeking", () => { if (seekFrom === null) seekFrom = lastPos; });
  video.addEventListener("seeked", () => {
    if (started && seekFrom !== null) report("seek", { from_position_ms: seekFrom });
    seekFrom = null;
  });
  video.addEventListener("timeupdate", () => { if (!video.seeking) lastPos = pos(); });
  video.addEventListener("ended", () => { if (!ended) { ended = true; report("stop"); } });
  video.addEventListener("error", async () => {
    if (ended) return;
    // A 410 on the stream means it was stopped from Activity.
    try {
      const r = await fetch(session.stream.url, { method: "HEAD", credentials: "same-origin" });
      if (r.status === 410) return terminated();
    } catch { /* fall through */ }
    const code = video.error ? video.error.code : 0;
    const msg = code === 4 ? "The browser cannot decode this file." : "The stream was interrupted.";
    report("error", { message: `web: ${msg} (media error ${code})` });
    ended = true;
    overlay.replaceChildren(errorBox("Playback failed", msg + " The TV app may still play it.", h("button", { class: "btn", type: "button", onclick: back }, "Back")));
    overlay.hidden = false;
  });

  const beat = setInterval(() => {
    if (started && !ended && !video.paused) report("progress");
  }, Math.max(5000, session.heartbeat_interval_ms || 10000));

  // Hide the top bar while playing and the mouse is still.
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
    else if (e.key === "ArrowRight") video.currentTime += 10;
    else if (e.key === "ArrowLeft") video.currentTime = Math.max(0, video.currentTime - 10);
    else if (e.key === "f") document.fullscreenElement ? document.exitFullscreen() : shell.requestFullscreen().catch(() => {});
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
      clearInterval(beat);
      clearTimeout(idleTimer);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("pagehide", stop);
      video.pause();
      video.removeAttribute("src");
      video.load();
      for (const t of video.querySelectorAll("track")) URL.revokeObjectURL(t.src);
      if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    },
  };
}

function subLabel(t) {
  const lang = t.language ? languageName(t.language) : "Unknown";
  return [t.title || lang, t.is_forced ? "(forced)" : "", t.embedded ? "" : "(file)"].filter(Boolean).join(" ");
}

function languageName(code) {
  try {
    return new Intl.DisplayNames([navigator.language || "en"], { type: "language" }).of(code) || code;
  } catch {
    return code;
  }
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


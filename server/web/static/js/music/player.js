// The music player: one queue, two <audio> elements, and a bar along the
// bottom of the window. The bar lives outside the routed view (#view), so the
// router never replaces it and music keeps playing while the user moves
// around the app. Space plays and pauses anywhere outside a text field.
//
// Gapless, or near enough: live albums run one track into the next, so a gap
// of a few hundred milliseconds while the browser opens the next file is
// audible. Two audio elements take turns. Once the playing track has less
// than PRELOAD_S seconds left, the next track loads into the idle element
// (preload="auto"), and the browser buffers and decodes its start. When the
// playing track ends, the idle element is already able to play, so play()
// starts it at once; the gap is the time between the "ended" event and the
// first sample of the next file, tens of milliseconds rather than a network
// round trip. True sample-accurate gapless would need every track decoded
// into Web Audio buffers, which costs hundreds of megabytes for a long jam.
// FLAC, which tapers use, has no encoder padding, so this is enough there;
// an MP3 may still carry its encoder's few milliseconds of silence.

import { api, ApiError } from "../api.js";
import { h, clock, toast } from "../dom.js";
import { micon as icon } from "./icons.js";
import { audioCapabilities } from "../caps.js";

const PRELOAD_S = 20;

const state = {
  // Tracks as the server sent them (each with stream or refusal).
  tracks: [],
  // Play order: indexes into tracks, shuffled or not.
  order: [],
  pos: -1,
  shuffle: false,
  // off, all (the queue loops) or one (the track loops).
  repeat: "off",
  // Where the queue came from, for ReplayGain: an album in order uses album gain.
  source: "",
};

// Two elements take turns; cur plays, the other preloads the next track.
const els = [new Audio(), new Audio()];
let cur = 0;
for (const el of els) {
  el.preload = "auto";
  el.dataset.offset = "0";
}

// Debug facts the browser test reads: how each track change happened.
export const debug = { transitions: [] };

const listeners = new Set();
// onChange calls fn after every change of track or play state. It returns
// the function that stops the calls, for a view's cleanup.
export function onChange(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
function changed() {
  for (const fn of listeners) {
    try { fn(); } catch (e) { console.error(e); }
  }
  draw();
}

export function current() {
  return state.pos >= 0 ? state.tracks[state.order[state.pos]] || null : null;
}
export function isPlaying() {
  return !!current() && !els[cur].paused;
}

const el = () => els[cur];
const idle = () => els[1 - cur];
const posMs = () => Math.round((Number(el().dataset.offset) + el().currentTime) * 1000);
const durMs = () => {
  const t = current();
  if (t && t.duration_ms) return t.duration_ms;
  const d = el().duration;
  return Number.isFinite(d) ? Math.round(d * 1000) : 0;
};

// ---- Play queues --------------------------------------------------------

async function fetchQueue(body) {
  const res = await api("/music/queue", { method: "POST", body: { ...body, capabilities: audioCapabilities() } });
  return res.tracks || [];
}

// playAlbum plays an album in order, from startTrackId when given.
export async function playAlbum(albumId, { shuffle = false, startTrackId = null } = {}) {
  const tracks = await fetchQueue({ album_id: albumId });
  start(tracks, Math.max(0, tracks.findIndex((t) => t.id === startTrackId)), { shuffle, source: shuffle ? "" : "album" });
}

// playArtist plays everything an artist has, shuffled.
export async function playArtist(artistId) {
  const tracks = await fetchQueue({ artist_id: artistId, shuffle: true });
  start(tracks, 0, { source: "artist" });
}

// playTracks plays a list of tracks (search results, say) from one of them.
export async function playTracks(ids, startIndex = 0) {
  const tracks = await fetchQueue({ track_ids: ids, shuffle: false });
  start(tracks, Math.min(startIndex, tracks.length - 1), { source: "tracks" });
}

function start(tracks, index, { shuffle = false, source = "" } = {}) {
  if (!tracks.length) {
    toast("Nothing here can be played.");
    return;
  }
  endListen();
  state.tracks = tracks;
  state.source = source;
  state.order = tracks.map((_, i) => i);
  state.shuffle = false;
  state.pos = index;
  if (shuffle) setShuffle(true);
  clearIdle();
  show(true);
  loadCurrent(0, true);
}

// setShuffle reorders what is left after the current track, or puts the queue
// back in order, keeping the current track playing either way.
export function setShuffle(on) {
  const playing = state.order[state.pos];
  if (on) {
    const rest = state.order.filter((i) => i !== playing);
    for (let i = rest.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [rest[i], rest[j]] = [rest[j], rest[i]];
    }
    state.order = playing === undefined ? rest : [playing, ...rest];
    state.pos = playing === undefined ? -1 : 0;
  } else {
    state.order = state.tracks.map((_, i) => i);
    state.pos = playing === undefined ? -1 : playing;
  }
  state.shuffle = on;
  clearIdle();
  changed();
}

export function cycleRepeat() {
  state.repeat = { off: "all", all: "one", one: "off" }[state.repeat];
  clearIdle();
  changed();
}

// nextPos is where Next goes, or -1 at the end of a queue that does not loop.
function nextPos() {
  if (state.pos + 1 < state.order.length) return state.pos + 1;
  return state.repeat === "all" && state.order.length ? 0 : -1;
}

export function next() {
  const p = nextPos();
  if (p < 0) return;
  state.pos = p;
  loadCurrent(0, true);
}

// previous restarts the track when it has played a few seconds, as players
// do, and otherwise goes back one.
export function previous() {
  if (posMs() > 3000 || state.pos === 0) {
    seek(0);
    return;
  }
  state.pos -= 1;
  loadCurrent(0, true);
}

export function jump(orderPos) {
  if (orderPos < 0 || orderPos >= state.order.length) return;
  state.pos = orderPos;
  loadCurrent(0, true);
}

export function toggle() {
  if (!current()) return;
  if (el().paused) el().play().catch(() => {});
  else el().pause();
}

export function clear() {
  endListen();
  for (const e of els) {
    e.pause();
    e.removeAttribute("src");
    e.load();
  }
  state.tracks = [];
  state.order = [];
  state.pos = -1;
  show(false);
  changed();
}

// ---- Loading and the gapless hand-over ---------------------------------

function srcWithStart(stream, ms) {
  if (stream.protocol !== "converted" || ms <= 0) return stream.url;
  return stream.url + (stream.url.includes("?") ? "&" : "?") + "start_ms=" + Math.round(ms);
}

function gainFor(t) {
  const g = state.source === "album" && !state.shuffle ? (t.replaygain.album_gain ?? t.replaygain.track_gain) : t.replaygain.track_gain;
  // An element's volume only turns down, so a positive gain plays at full.
  return g == null ? 1 : Math.min(1, Math.pow(10, g / 20));
}

function clearIdle() {
  const e = idle();
  if (e.dataset.trackId) {
    e.removeAttribute("src");
    e.load();
    delete e.dataset.trackId;
  }
}

// loadCurrent plays the track at state.pos from atMs, using the idle element
// when it already holds that track.
function loadCurrent(atMs, autoplay) {
  const t = current();
  endListen();
  if (!t) return;
  if (!t.stream) {
    toast(t.refusal ? t.refusal.message : "This track cannot be played here.");
    changed();
    return;
  }
  const old = el();
  let preloaded = false;
  if (atMs === 0 && idle().dataset.trackId === String(t.id)) {
    cur = 1 - cur;
    preloaded = true;
  } else {
    el().src = srcWithStart(t.stream, atMs);
    el().dataset.offset = t.stream.protocol === "converted" ? String(atMs / 1000) : "0";
    el().dataset.trackId = String(t.id);
    if (t.stream.protocol !== "converted" && atMs > 0) el().currentTime = atMs / 1000;
  }
  if (old !== el()) {
    old.pause();
    old.removeAttribute("src");
    old.load();
    delete old.dataset.trackId;
  }
  el().volume = gainFor(t);
  if (autoplay) el().play().catch((e) => { if (e.name !== "AbortError") toast("Press play to start the music."); });
  beginListen(t, preloaded);
  changed();
}

// preloadNext fills the idle element with the next track once the playing
// one is near its end.
function preloadNext() {
  if (state.repeat === "one") return;
  const p = nextPos();
  if (p < 0) return;
  const t = state.tracks[state.order[p]];
  if (!t || !t.stream || idle().dataset.trackId === String(t.id)) return;
  const e = idle();
  e.src = t.stream.url;
  e.dataset.offset = "0";
  e.dataset.trackId = String(t.id);
  e.preload = "auto";
  e.load();
}

let endedAt = 0;
function wire(e) {
  e.addEventListener("timeupdate", () => {
    if (e !== el()) return;
    const left = durMs() / 1000 - (Number(e.dataset.offset) + e.currentTime);
    if (left > 0 && left < PRELOAD_S) preloadNext();
    drawTime();
    positionState();
  });
  e.addEventListener("ended", () => {
    if (e !== el()) return;
    if (state.repeat === "one") {
      endListen();
      seek(0);
      el().play().catch(() => {});
      beginListen(current(), false);
      return;
    }
    const p = nextPos();
    if (p < 0) {
      endListen();
      changed();
      return;
    }
    endedAt = performance.now();
    const from = current();
    const ready = idle().dataset.trackId === String((state.tracks[state.order[p]] || {}).id) && idle().readyState >= 3;
    state.pos = p;
    loadCurrent(0, true);
    debug.transitions.push({ from: from && from.id, to: current() && current().id, preloaded: ready, gapMs: null });
  });
  e.addEventListener("playing", () => {
    if (e !== el()) return;
    const last = debug.transitions[debug.transitions.length - 1];
    if (last && last.gapMs === null && endedAt) {
      last.gapMs = Math.round(performance.now() - endedAt);
      endedAt = 0;
    }
    report(listen && listen.started ? "resume" : "start");
    if (listen) listen.started = true;
    changed();
  });
  e.addEventListener("pause", () => {
    if (e !== el() || e.ended) return;
    report("pause");
    changed();
  });
  e.addEventListener("error", () => {
    if (e !== el() || !e.getAttribute("src")) return;
    const t = current();
    toast(`Could not play ${t ? t.title : "this track"}.`);
    endListen();
    if (nextPos() >= 0) setTimeout(next, 1500);
    else changed();
  });
}
els.forEach(wire);

export function seek(ms) {
  const t = current();
  if (!t || !t.stream) return;
  ms = Math.max(0, Math.min(ms, Math.max(0, durMs() - 500)));
  report("seek", { from_position_ms: posMs() });
  if (t.stream.protocol === "converted") {
    // A converted stream cannot seek by range: ask for a new one from there.
    const playing = !el().paused;
    el().src = srcWithStart(t.stream, ms);
    el().dataset.offset = String(ms / 1000);
    if (playing) el().play().catch(() => {});
  } else {
    el().currentTime = ms / 1000;
  }
  clearIdle();
  drawTime();
}

// ---- Listens in Activity -----------------------------------------------

let listen = null;
let beat = null;

function beginListen(t, preloaded) {
  if (!t) return;
  const me = { trackId: t.id, id: null, started: false, preloaded };
  listen = me;
  api(`/music/tracks/${t.id}/sessions`, { method: "POST", body: { capabilities: audioCapabilities() } })
    .then((s) => {
      if (listen !== me) {
        // The track changed before the server answered: close this one.
        api(`/playback/sessions/${s.session_id}/events`, { method: "POST", body: { type: "stop", position_ms: 0 } }).catch(() => {});
        return;
      }
      me.id = s.session_id;
      if (!el().paused) report("start");
      me.started = !el().paused;
      clearInterval(beat);
      beat = setInterval(() => { if (!el().paused) report("progress"); }, Math.max(5000, s.heartbeat_interval_ms || 10000));
    })
    .catch((err) => {
      if (err instanceof ApiError && listen === me) {
        toast(err.message);
        el().pause();
      }
    });
}

function report(type, extra = {}, keepalive = false) {
  const l = listen;
  if (!l || !l.id) return;
  api(`/playback/sessions/${l.id}/events`, { method: "POST", keepalive, body: { type, position_ms: posMs(), duration_ms: durMs() || undefined, ...extra } })
    .catch((err) => {
      if (err instanceof ApiError && err.code === "session_terminated" && listen === l) {
        // An admin stopped this from Activity.
        clear();
        toast(err.message || "An administrator stopped the music.");
      }
    });
}

function endListen(keepalive = false) {
  clearInterval(beat);
  if (listen && listen.id) report("stop", {}, keepalive);
  listen = null;
}

window.addEventListener("pagehide", () => endListen(true));

// ---- The bar ------------------------------------------------------------

const ui = {};
function build() {
  const btn = (name, label, onclick, cls = "") => h("button", { class: "mb-btn " + cls, type: "button", "aria-label": label, title: label, onclick }, icon(name));
  ui.cover = h("div", { class: "mb-cover" });
  ui.title = h("a", { class: "mb-title" });
  ui.artist = h("a", { class: "mb-artist" });
  ui.play = btn("play", "Play", toggle, "mb-play");
  ui.prev = btn("prevtrack", "Previous", previous);
  ui.next = btn("nexttrack", "Next", next);
  ui.shuffle = btn("shuffle", "Shuffle", () => setShuffle(!state.shuffle), "mb-toggle");
  ui.repeat = btn("repeat", "Repeat", cycleRepeat, "mb-toggle");
  ui.queueBtn = btn("queue", "Queue", () => { ui.queue.hidden = !ui.queue.hidden; drawQueue(); }, "mb-toggle");
  ui.close = btn("close", "Stop and clear the queue", clear);
  ui.time = h("span", { class: "mb-time" }, "0:00");
  ui.dur = h("span", { class: "mb-time" }, "0:00");
  ui.bar = h("input", { class: "mb-seek", type: "range", min: "0", max: "1", step: "1", value: "0", "aria-label": "Position" });
  ui.bar.addEventListener("input", () => { ui.dragging = true; ui.time.textContent = clock(Number(ui.bar.value)); });
  ui.bar.addEventListener("change", () => { ui.dragging = false; seek(Number(ui.bar.value)); });
  ui.queue = h("div", { class: "mb-queue", hidden: true, role: "region", "aria-label": "Queue" });
  ui.root = h("div", { class: "music-bar", id: "music-bar", hidden: true, role: "region", "aria-label": "Music player" },
    ui.queue,
    h("div", { class: "mb-row" },
      h("div", { class: "mb-now" }, ui.cover, h("div", { class: "mb-meta" }, ui.title, ui.artist)),
      h("div", { class: "mb-main" },
        h("div", { class: "mb-buttons" }, ui.shuffle, ui.prev, ui.play, ui.next, ui.repeat),
        h("div", { class: "mb-progress" }, ui.time, ui.bar, ui.dur)),
      h("div", { class: "mb-side" }, ui.queueBtn, ui.close)));
  document.body.append(ui.root, ...els.map((e) => { e.className = "mb-audio"; return e; }));
}

function show(on) {
  if (!ui.root) build();
  ui.root.hidden = !on || onVideoPage();
  document.body.classList.toggle("has-music-bar", !ui.root.hidden);
}

const onVideoPage = () => location.hash.startsWith("#/play/");

function draw() {
  if (!ui.root) return;
  const t = current();
  if (!t) {
    show(false);
    return;
  }
  const art = t.has_cover ? `/api/v1/music/albums/${t.album_id}/cover?width=160` : null;
  if (ui.cover.dataset.album !== String(t.album_id)) {
    ui.cover.dataset.album = String(t.album_id);
    ui.cover.replaceChildren(art ? h("img", { src: art, alt: "" }) : icon("disc"));
  }
  ui.title.textContent = t.title || "";
  ui.title.href = `#/music/album/${t.album_id}`;
  ui.artist.textContent = t.artist_name || "";
  ui.artist.href = `#/music/artist/${t.artist_id}`;
  const playing = !el().paused;
  ui.play.replaceChildren(icon(playing ? "pause" : "play"));
  ui.play.setAttribute("aria-label", playing ? "Pause" : "Play");
  ui.play.title = playing ? "Pause" : "Play";
  ui.shuffle.classList.toggle("is-on", state.shuffle);
  ui.shuffle.setAttribute("aria-pressed", String(state.shuffle));
  ui.repeat.classList.toggle("is-on", state.repeat !== "off");
  ui.repeat.replaceChildren(icon(state.repeat === "one" ? "repeatone" : "repeat"));
  ui.repeat.setAttribute("aria-label", { off: "Repeat is off", all: "Repeating the queue", one: "Repeating this track" }[state.repeat]);
  ui.repeat.title = ui.repeat.getAttribute("aria-label");
  ui.next.disabled = nextPos() < 0;
  ui.root.dataset.state = playing ? "playing" : "paused";
  ui.root.dataset.track = String(t.id);
  drawTime();
  if (!ui.queue.hidden) drawQueue();
  mediaSession(t);
}

function drawTime() {
  if (!ui.root || ui.dragging) return;
  const d = durMs();
  ui.bar.max = String(Math.max(1, d));
  ui.bar.value = String(Math.min(posMs(), d));
  ui.time.textContent = clock(posMs());
  ui.dur.textContent = clock(d);
}

function drawQueue() {
  const rows = state.order.map((ti, p) => {
    const t = state.tracks[ti];
    return h("li", { class: p === state.pos ? "is-current" : "" },
      h("button", { type: "button", onclick: () => jump(p), disabled: !t.stream },
        h("span", { class: "q-title" }, t.title || ""), h("span", { class: "q-artist" }, t.artist_name || ""),
        h("span", { class: "q-time" }, clock(t.duration_ms || 0))));
  });
  ui.queue.replaceChildren(h("div", { class: "mb-queue-head" }, h("h2", {}, "Queue"), h("span", { class: "muted" }, `${state.order.length} tracks`)),
    h("ol", {}, rows));
}

// ---- Lock screens and media keys ----------------------------------------

let sessionTrack = null;
function mediaSession(t) {
  if (!("mediaSession" in navigator)) return;
  navigator.mediaSession.playbackState = el().paused ? "paused" : "playing";
  if (sessionTrack === t.id) return;
  sessionTrack = t.id;
  const artwork = t.has_cover ? [96, 240, 480].map((w) => ({ src: new URL(`/api/v1/music/albums/${t.album_id}/cover?width=${w}`, location.href).href,
    sizes: `${w}x${w}`, type: "image/jpeg" })) : [];
  try {
    navigator.mediaSession.metadata = new MediaMetadata({ title: t.title || "", artist: t.artist_name || "", album: t.album_title || "", artwork });
  } catch (e) {
    console.warn("media session", e);
  }
  positionState();
}

function positionState() {
  if (!("mediaSession" in navigator) || !navigator.mediaSession.setPositionState) return;
  const d = durMs() / 1000;
  if (!(d > 0)) return;
  try {
    navigator.mediaSession.setPositionState({ duration: d, position: Math.min(d, posMs() / 1000), playbackRate: 1 });
  } catch {
    // A position past the end while a track changes; the next update fixes it.
  }
}

// reclaimMediaSession hands the lock screen back to music once the video
// player, which sets its own handlers and title, closes.
export function reclaimMediaSession() {
  if (!("mediaSession" in navigator)) return;
  installMediaSession();
  sessionTrack = null;
  try { navigator.mediaSession.metadata = null; } catch { /* read-only here */ }
  const t = current();
  if (t) mediaSession(t);
  else navigator.mediaSession.playbackState = "none";
}

function installMediaSession() {
  if (!("mediaSession" in navigator)) return;
  const set = (action, fn) => {
    try { navigator.mediaSession.setActionHandler(action, fn); } catch { /* not supported here */ }
  };
  set("play", () => el().play().catch(() => {}));
  set("pause", () => el().pause());
  set("previoustrack", previous);
  set("nexttrack", next);
  set("seekto", (d) => seek(d.seekTime * 1000));
  set("seekbackward", (d) => seek(posMs() - (d.seekOffset || 10) * 1000));
  set("seekforward", (d) => seek(posMs() + (d.seekOffset || 10) * 1000));
}

// ---- Keys and pages ------------------------------------------------------

function typingOrPressing(t) {
  return !!t && (t.isContentEditable || ["INPUT", "TEXTAREA", "SELECT", "BUTTON", "SUMMARY"].includes(t.tagName));
}

export function installMusicPlayer() {
  installMediaSession();
  document.addEventListener("keydown", (e) => {
    if (e.key !== " " || e.ctrlKey || e.metaKey || e.altKey || !current() || onVideoPage() || typingOrPressing(e.target)) return;
    e.preventDefault();
    toggle();
  });
  // The video player takes over the screen and the speakers.
  window.addEventListener("hashchange", () => {
    if (onVideoPage() && current()) el().pause();
    if (ui.root) show(!!current());
  });
}

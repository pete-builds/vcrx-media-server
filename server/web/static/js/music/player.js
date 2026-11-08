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
//
// Audiobooks play through the same queue: a book's files are its tracks, and
// state.book turns the bar into a book player (skip back 15 s and forward
// 30 s, chapters, speed, a sleep timer). A position in a book is seconds from
// its start across every file; the server keeps it, saved every SAVE_MS
// while playing and on every pause.
//
// A podcast episode plays as a book of one file with no chapters (state.book
// with podcast set): the same skips, speed and sleep timer, its place saved
// to /podcasts/episodes/{id}/progress the same way, and its audio played
// through VCRX's stream route, which passes byte ranges on to the host.

import { api, ApiError } from "../api.js";
import { h, clock, toast } from "../dom.js";
import { micon as icon } from "./icons.js";
import { audioCapabilities } from "../caps.js";

const PRELOAD_S = 20;
const SAVE_MS = 15000;
export const SPEEDS = [0.8, 1, 1.2, 1.5, 1.75, 2, 2.5];
// Sleep timer choices: minutes, or "chapter" for the end of this chapter.
export const SLEEPS = [15, 30, 45, 60, "chapter"];

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
  // The book playing, or null for music: {id, title, author, has_cover,
  // duration_s, chapters}. Its files are the tracks, each with start_s. A
  // podcast episode is a book with podcast: {id} (the show), one file and
  // no chapters; its duration_s is 0 until known.
  book: null,
  // Playback speed for books; music always plays at 1.
  rate: 1,
  // The sleep timer: null, {at: ms clock time} or {chapterEnd: seconds}.
  sleep: null,
};

// Two elements take turns; cur plays, the other preloads the next track.
const els = [new Audio(), new Audio()];
let cur = 0;
for (const el of els) {
  el.preload = "auto";
  el.dataset.offset = "0";
}

// Debug facts the browser test reads: how each track change happened, and
// each book position saved to the server.
export const debug = { transitions: [], saves: [] };

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

function start(tracks, index, { shuffle = false, source = "", book = null, atMs = 0 } = {}) {
  if (!tracks.length) {
    toast("Nothing here can be played.");
    return;
  }
  endListen();
  if (state.book) saveProgress();
  state.book = book;
  state.repeat = book ? "off" : state.repeat;
  state.sleep = null;
  clearTimeout(sleepTimer);
  setRate(book ? state.rate : 1);
  state.tracks = tracks;
  state.source = source;
  state.order = tracks.map((_, i) => i);
  state.shuffle = false;
  state.pos = index;
  if (shuffle) setShuffle(true);
  clearIdle();
  show(true);
  loadCurrent(atMs, true);
}

// ---- Audiobooks ---------------------------------------------------------

// locate finds the file a book position falls in and the offset into it.
export function locate(files, posS) {
  let i = 0;
  for (let j = files.length - 1; j >= 0; j--) {
    if (posS >= files[j].start_s) { i = j; break; }
  }
  const f = files[i];
  return { index: i, offsetS: Math.max(0, Math.min(posS - f.start_s, f.duration_s)) };
}

// playBook plays a book from where the caller left off, from atS seconds
// when given, or from the beginning with fromStart.
export async function playBook(bookId, { atS = null, fromStart = false } = {}) {
  const q = await api(`/audiobooks/books/${bookId}/queue`, { method: "POST", body: { capabilities: audioCapabilities() } });
  if (!q.files.length) {
    toast("Nothing in this book can be played.");
    return;
  }
  const p = q.progress || {};
  let pos = atS != null ? atS : fromStart || p.finished ? 0 : p.position_s || 0;
  if (pos >= q.duration_s - 1) pos = 0;
  const tracks = q.files.map((f) => ({ ...f, duration_ms: Math.round(f.duration_s * 1000), book_id: q.book_id }));
  const at = locate(tracks, pos);
  start(tracks, at.index, { source: "book", book: { id: q.book_id, title: q.title, author: q.author, has_cover: q.has_cover,
    duration_s: q.duration_s, chapters: q.chapters }, atMs: Math.round(at.offsetS * 1000) });
}

export const currentBook = () => state.book;

// playEpisode plays a podcast episode (as the API lists it) from the
// caller's saved place, or from the start when it was played or fromStart.
export function playEpisode(ep, { fromStart = false } = {}) {
  const dur = ep.duration_seconds || 0;
  let pos = fromStart || ep.played ? 0 : ep.position_seconds || 0;
  if (dur && pos >= dur - 1) pos = 0;
  const track = { id: ep.id, title: ep.title, start_s: 0, duration_s: dur, duration_ms: Math.round(dur * 1000),
    stream: { url: ep.stream_url, protocol: "direct", mime_type: ep.mime_type || "" } };
  start([track], 0, { source: "podcast", book: { id: ep.id, title: ep.title, author: ep.podcast_title, has_cover: true,
    duration_s: dur, chapters: [], podcast: { id: ep.podcast_id, title: ep.podcast_title } }, atMs: Math.round(pos * 1000) });
}

// currentEpisode is the podcast episode playing: {id, podcast_id}, or null.
export const currentEpisode = () => (state.book && state.book.podcast ? { id: state.book.id, podcast_id: state.book.podcast.id } : null);

// bookDur is the book's length in seconds; an episode whose feed gave none
// goes by what the browser measured.
const bookDur = () => (state.book && state.book.duration_s) || durMs() / 1000;

// bookPos is where the book is, in seconds from its start.
export function bookPos() {
  const t = current();
  return state.book && t ? t.start_s + posMs() / 1000 : 0;
}

export function chapterAt(posS) {
  const chs = state.book ? state.book.chapters : [];
  let i = -1;
  for (let j = 0; j < chs.length; j++) if (posS >= chs[j].start_s - 0.05) i = j;
  return i;
}

// seekBook moves to a position in the book, changing files when it has to.
export function seekBook(posS) {
  if (!state.book) return;
  posS = Math.max(0, Math.min(posS, bookDur() - 0.5));
  const at = locate(state.tracks, posS);
  if (at.index === state.order[state.pos]) {
    seek(at.offsetS * 1000);
  } else {
    const playing = !el().paused;
    state.pos = state.order.indexOf(at.index);
    loadCurrent(Math.round(at.offsetS * 1000), playing);
  }
  saveProgress();
}

export const skip = (deltaS) => seekBook(bookPos() + deltaS);

// chapter moves to the next chapter (1) or back (-1): back restarts the
// chapter when it has played a few seconds, as Previous does for a track.
export function chapter(dir) {
  if (!state.book) return;
  const chs = state.book.chapters;
  const pos = bookPos();
  const i = chapterAt(pos);
  if (dir > 0) {
    if (i + 1 < chs.length) seekBook(chs[i + 1].start_s);
    return;
  }
  if (i >= 0 && pos - chs[i].start_s > 3) seekBook(chs[i].start_s);
  else if (i > 0) seekBook(chs[i - 1].start_s);
  else seekBook(0);
}

function setRate(r) {
  for (const e of els) {
    e.defaultPlaybackRate = r;
    e.playbackRate = r;
  }
}

// setSpeed sets a book's playback speed, kept for the next book too.
export function setSpeed(r) {
  state.rate = SPEEDS.includes(r) ? r : 1;
  if (state.book) setRate(state.rate);
  try { localStorage.setItem("vcrx.bookSpeed", String(state.rate)); } catch { /* private mode */ }
  changed();
}
try {
  const saved = Number(localStorage.getItem("vcrx.bookSpeed"));
  if (SPEEDS.includes(saved)) state.rate = saved;
} catch { /* private mode */ }

let sleepTimer = null;
// setSleep pauses after some minutes, or at the end of this chapter; null
// turns it off.
export function setSleep(choice) {
  clearTimeout(sleepTimer);
  state.sleep = null;
  if (choice === "chapter") {
    const chs = state.book ? state.book.chapters : [];
    const i = chapterAt(bookPos());
    if (i >= 0) state.sleep = { chapterEnd: chs[i].end_s };
  } else if (choice) {
    state.sleep = { at: Date.now() + choice * 60000 };
    sleepTimer = setTimeout(() => { el().pause(); state.sleep = null; toast("Sleep timer: paused."); changed(); }, choice * 60000);
  }
  changed();
}

function checkSleep() {
  if (state.sleep && state.sleep.chapterEnd != null && bookPos() >= state.sleep.chapterEnd - 0.25) {
    state.sleep = null;
    el().pause();
    toast("Sleep timer: paused at the end of the chapter.");
    changed();
  }
}

let lastSave = 0;
// saveProgress sends the book position to the server.
function saveProgress(keepalive = false, finished) {
  const b = state.book;
  if (!b || !current()) return;
  if (b.podcast) {
    saveEpisode(b, keepalive, finished);
    return;
  }
  const pos = finished ? b.duration_s : bookPos();
  lastSave = Date.now();
  const body = { position_s: Math.round(pos * 1000) / 1000 };
  if (finished) body.finished = true;
  api(`/audiobooks/books/${b.id}/progress`, { method: "PUT", keepalive, body })
    .then((p) => { debug.saves.push({ book: b.id, position_s: p.position_s, finished: p.finished }); })
    .catch(() => {});
}
document.addEventListener("visibilitychange", () => { if (document.visibilityState === "hidden") saveProgress(true); });

// saveEpisode sends an episode's place, with the length the browser found
// (the server keeps it when the feed gave none); at its end, it is played.
function saveEpisode(b, keepalive, finished) {
  lastSave = Date.now();
  const measured = Number.isFinite(el().duration) ? el().duration : 0;
  const req = finished
    ? api(`/podcasts/episodes/${b.id}/played`, { method: "PUT", keepalive, body: { played: true } })
    : api(`/podcasts/episodes/${b.id}/progress`, { method: "PUT", keepalive,
      body: { position_seconds: Math.round(bookPos() * 1000) / 1000, duration_seconds: Math.round(measured * 1000) / 1000 } });
  req.then((p) => { debug.saves.push({ episode: b.id, position_s: p.position_seconds, finished: p.played }); }).catch(() => {});
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
  if (state.book) saveProgress(true);
  state.book = null;
  state.sleep = null;
  clearTimeout(sleepTimer);
  setRate(1);
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
  if (!t.replaygain) return 1;
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
  el().volume = state.book ? 1 : gainFor(t);
  el().playbackRate = state.book ? state.rate : 1;
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
    if (left > 0 && left < PRELOAD_S * (state.book ? state.rate : 1)) preloadNext();
    if (state.book) {
      checkSleep();
      if (!e.paused && Date.now() - lastSave >= SAVE_MS) saveProgress();
    }
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
      if (state.book) saveProgress(false, true);
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
    saveProgress();
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
  // A podcast episode is not a listen in Activity: its place is saved instead.
  if (!t || (state.book && state.book.podcast)) return;
  const me = { trackId: t.id, id: null, started: false, preloaded };
  listen = me;
  const path = state.book ? `/audiobooks/files/${t.id}/sessions` : `/music/tracks/${t.id}/sessions`;
  api(path, { method: "POST", body: { capabilities: audioCapabilities() } })
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

window.addEventListener("pagehide", () => {
  saveProgress(true);
  endListen(true);
});

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
  // A book's controls, shown instead of shuffle, repeat and the queue.
  ui.back15 = btn("back15", "Back 15 seconds", () => skip(-15), "mb-book");
  ui.fwd30 = btn("fwd30", "Forward 30 seconds", () => skip(30), "mb-book");
  ui.chPrev = btn("prevtrack", "Previous chapter", () => chapter(-1), "mb-book");
  ui.chNext = btn("nexttrack", "Next chapter", () => chapter(1), "mb-book");
  ui.speed = h("select", { class: "mb-select mb-book", "aria-label": "Playback speed", title: "Playback speed",
    onchange: (e) => setSpeed(Number(e.target.value)) }, SPEEDS.map((r) => h("option", { value: String(r) }, `${r}×`)));
  ui.sleepSel = h("select", { class: "mb-select mb-book", "aria-label": "Sleep timer", title: "Sleep timer",
    onchange: (e) => setSleep(e.target.value === "" ? null : e.target.value === "chapter" ? "chapter" : Number(e.target.value)) },
  h("option", { value: "" }, "Sleep off"), SLEEPS.map((m) => h("option", { value: String(m) }, m === "chapter" ? "End of chapter" : `${m} min`)));
  ui.time = h("span", { class: "mb-time" }, "0:00");
  ui.dur = h("span", { class: "mb-time" }, "0:00");
  ui.bar = h("input", { class: "mb-seek", type: "range", min: "0", max: "1", step: "1", value: "0", "aria-label": "Position" });
  ui.bar.addEventListener("input", () => { ui.dragging = true; ui.time.textContent = clock(Number(ui.bar.value)); });
  ui.bar.addEventListener("change", () => {
    ui.dragging = false;
    if (state.book) seekBook(Number(ui.bar.value) / 1000);
    else seek(Number(ui.bar.value));
  });
  ui.queue = h("div", { class: "mb-queue", hidden: true, role: "region", "aria-label": "Queue" });
  ui.root = h("div", { class: "music-bar", id: "music-bar", hidden: true, role: "region", "aria-label": "Music player" },
    ui.queue,
    h("div", { class: "mb-row" },
      h("div", { class: "mb-now" }, ui.cover, h("div", { class: "mb-meta" }, ui.title, ui.artist)),
      h("div", { class: "mb-main" },
        h("div", { class: "mb-buttons" }, ui.shuffle, ui.back15, ui.chPrev, ui.prev, ui.play, ui.next, ui.chNext, ui.fwd30, ui.repeat),
        h("div", { class: "mb-progress" }, ui.time, ui.bar, ui.dur)),
      h("div", { class: "mb-side" }, ui.speed, ui.sleepSel, ui.queueBtn, ui.close)));
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
  const b = state.book;
  const pod = b && b.podcast;
  const coverKey = pod ? `episode:${b.id}` : b ? `book:${b.id}` : String(t.album_id);
  const art = coverAt(160);
  if (ui.cover.dataset.album !== coverKey) {
    ui.cover.dataset.album = coverKey;
    const fallback = icon(pod ? "podcast" : b ? "book" : "disc");
    const img = art ? h("img", { src: art, alt: "" }) : null;
    if (img) img.addEventListener("error", () => img.replaceWith(fallback));
    ui.cover.replaceChildren(img || fallback);
  }
  ui.root.classList.toggle("is-book", !!b);
  ui.root.classList.toggle("is-podcast", !!pod);
  for (const n of [ui.back15, ui.fwd30, ui.speed, ui.sleepSel]) n.hidden = !b;
  for (const n of [ui.chPrev, ui.chNext]) n.hidden = !b || !!pod;
  for (const n of [ui.shuffle, ui.repeat, ui.queueBtn, ui.prev, ui.next]) n.hidden = !!b;
  if (pod) {
    ui.title.textContent = b.title || "";
    ui.title.href = `#/podcasts/show/${pod.id}`;
    ui.artist.textContent = pod.title || "";
    ui.artist.href = `#/podcasts/show/${pod.id}`;
    ui.speed.value = String(state.rate);
    if (!state.sleep) ui.sleepSel.value = "";
    delete ui.root.dataset.book;
    ui.root.dataset.episode = String(b.id);
  } else if (b) {
    const i = chapterAt(bookPos());
    ui.title.textContent = i >= 0 ? b.chapters[i].title : t.title || "";
    ui.title.href = `#/audiobooks/book/${b.id}`;
    ui.artist.textContent = [b.title, b.author].filter(Boolean).join(" · ");
    ui.artist.href = `#/audiobooks/book/${b.id}`;
    ui.chNext.disabled = i + 1 >= b.chapters.length;
    ui.speed.value = String(state.rate);
    ui.sleepSel.value = !state.sleep ? "" : state.sleep.chapterEnd != null ? "chapter" : ui.sleepSel.value;
    ui.root.dataset.book = String(b.id);
    delete ui.root.dataset.episode;
  } else {
    delete ui.root.dataset.episode;
    ui.title.textContent = t.title || "";
    ui.title.href = `#/music/album/${t.album_id}`;
    ui.artist.textContent = t.artist_name || "";
    ui.artist.href = `#/music/artist/${t.artist_id}`;
    delete ui.root.dataset.book;
  }
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

// coverAt is the art of what is playing at width w: the album's, the
// book's, or the episode's (its own, else its show's); null when none.
function coverAt(w) {
  const b = state.book, t = current();
  if (b && b.podcast) return `/api/v1/podcasts/episodes/${b.id}/artwork`;
  if (b) return b.has_cover ? `/api/v1/audiobooks/books/${b.id}/cover?width=${w}` : null;
  return t && t.has_cover ? `/api/v1/music/albums/${t.album_id}/cover?width=${w}` : null;
}

// barPosMs and barDurMs are what the seek bar shows: the track, or for a
// book the whole book.
const barPosMs = () => (state.book ? Math.round(bookPos() * 1000) : posMs());
const barDurMs = () => (state.book ? Math.round(bookDur() * 1000) : durMs());

function drawTime() {
  if (!ui.root || ui.dragging) return;
  const d = barDurMs();
  ui.bar.max = String(Math.max(1, d));
  ui.bar.value = String(Math.min(barPosMs(), d));
  ui.time.textContent = clock(barPosMs());
  ui.dur.textContent = clock(d);
  if (state.book && !state.book.podcast && ui.root.dataset.chapter !== String(chapterAt(bookPos()))) {
    ui.root.dataset.chapter = String(chapterAt(bookPos()));
    changed();
  }
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
  const b = state.book;
  // A book's lock screen shows the chapter, so it changes with the chapter.
  const key = b && b.podcast ? `episode:${b.id}` : b ? `book:${b.id}:${chapterAt(bookPos())}` : t.id;
  if (sessionTrack === key) return;
  sessionTrack = key;
  // An episode's artwork is whatever image its feed had, one size.
  const artwork = b && b.podcast ? [{ src: new URL(coverAt(0), location.href).href }]
    : coverAt(96) ? [96, 240, 480].map((w) => ({ src: new URL(coverAt(w), location.href).href, sizes: `${w}x${w}`, type: "image/jpeg" })) : [];
  const i = b ? chapterAt(bookPos()) : -1;
  const meta = b && b.podcast ? { title: b.title, artist: b.podcast.title, album: b.podcast.title, artwork }
    : b ? { title: i >= 0 ? b.chapters[i].title : b.title, artist: b.author, album: b.title, artwork }
      : { title: t.title || "", artist: t.artist_name || "", album: t.album_title || "", artwork };
  try {
    navigator.mediaSession.metadata = new MediaMetadata(meta);
  } catch (e) {
    console.warn("media session", e);
  }
  positionState();
}

function positionState() {
  if (!("mediaSession" in navigator) || !navigator.mediaSession.setPositionState) return;
  const d = barDurMs() / 1000;
  if (!(d > 0)) return;
  try {
    navigator.mediaSession.setPositionState({ duration: d, position: Math.min(d, barPosMs() / 1000), playbackRate: state.book ? state.rate : 1 });
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
  // A book's previous and next are chapters, and its skips 15 and 30 s.
  set("previoustrack", () => (state.book ? chapter(-1) : previous()));
  set("nexttrack", () => (state.book ? chapter(1) : next()));
  set("seekto", (d) => (state.book ? seekBook(d.seekTime) : seek(d.seekTime * 1000)));
  set("seekbackward", (d) => (state.book ? skip(-(d.seekOffset || 15)) : seek(posMs() - (d.seekOffset || 10) * 1000)));
  set("seekforward", (d) => (state.book ? skip(d.seekOffset || 30) : seek(posMs() + (d.seekOffset || 10) * 1000)));
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

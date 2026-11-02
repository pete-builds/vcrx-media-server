import { api, ApiError, artUrl } from "../api.js";
import { reclaimMediaSession } from "../music/player.js";
import { h, icon, errorBox, episodeCode, clock, toast, loadBar } from "../dom.js";
import { capabilities } from "../caps.js";
import { skipButtons } from "./skip.js";
import { subtitleTiming, subtitleSearch } from "./subtitle-tools.js";
import { state } from "../app.js";

// The player: asks the server for a session with this browser's capabilities,
// plays it, and reports start, pause, seek, progress and stop so resume points
// and Activity stay right.
//
// Two kinds of stream:
// - progressive: the file itself, seekable by range; native controls.
// - fmp4: a converted stream (video copied or transcoded, audio copied or
//   converted) that cannot be seeked by range. Seeking asks the server for a
//   new stream; the server says where it starts (the keyframe before the
//   position when the video is copied, the position itself when it is
//   transcoded), and the position is that start plus the element's own time.
//   These get VCRX controls.
// - hls: the same converted stream as an HLS playlist, for Safari, which
//   cannot play the fmp4 one. It seeks the same way: the seek endpoint starts
//   a stream at the position and answers with its playlist.
export async function playerView({ match, params }) {
  // #/peer/<peer>/play/<item> plays a friend's title: the same player, every
  // call under /peers/<peer>, which this server relays to the friend's.
  const peerId = match[2] ? Number(match[1]) : null;
  const itemId = Number(match[2] || match[1]);
  const base = peerId ? `/peers/${peerId}` : "";
  const itemHash = peerId ? `#/peer/${peerId}/item/${itemId}` : `#/item/${itemId}`;
  const body = { item_id: itemId, capabilities: capabilities() };
  if (params.get("version")) body.version_id = Number(params.get("version"));
  if (params.get("start") !== null) body.start_position_ms = Number(params.get("start")) || 0;
  // A trailer or other extra of the item (views/extras.js) plays from its start.
  if (params.get("extra")) Object.assign(body, { extra_id: Number(params.get("extra")), start_position_ms: 0 });

  const back = () => (history.length > 1 ? history.back() : (location.hash = itemHash));
  const backBtn = h("button", { class: "btn btn-small", type: "button", onclick: back, "aria-label": "Back" }, icon("back"), "Back");
  const shell = h("div", { class: "player" });

  // Before a movie (Settings, Playback): trailers for other titles, then a
  // pre-roll, when a movie on this server starts from the beginning. The
  // movie itself opens once they end or Skip is pressed, so nothing played
  // first touches watch state.
  if (!peerId && !body.extra_id && params.get("start") === "0" && params.get("before") !== "done") {
    const before = await api(`/items/${itemId}/before`).catch(() => ({ clips: [] }));
    if ((before.clips || []).length) {
      const next = new URLSearchParams(params);
      next.set("before", "done");
      return beforeView(before.clips, shell, backBtn, () => location.replace(`#/play/${itemId}?${next}`));
    }
  }

  let session, item;
  try {
    [session, item] = await Promise.all([api(`${base}/playback/sessions`, { method: "POST", body }), api(`${base}/items/${itemId}`)]);
  } catch (err) {
    const why = err instanceof ApiError ? err.message : "The server could not start playback.";
    shell.append(h("div", { class: "top" }, backBtn), h("div", { class: "overlay" },
      errorBox("This cannot play in the browser", why, h("p", { class: "faint" }, "The TV app may still play it."))));
    return { el: shell, title: "Cannot play" };
  }

  const hls = session.stream.protocol === "hls";
  const converted = hls || session.stream.protocol === "fmp4";
  const totalMs = session.version.duration_ms || 0;
  const playingExtra = body.extra_id ? (item.extras || []).find((x) => x.id === body.extra_id) : null;
  const title = playingExtra ? `${item.title} · ${playingExtra.title}` : item.kind === "episode" ? `${item.show_title || ""} ${episodeCode(item)} · ${item.title}` : item.title;
  const video = h("video", { autoplay: true, playsinline: true, preload: "auto" });
  video.controls = !converted;
  const status = h("div", { class: "status", hidden: true });
  const overlay = h("div", { class: "overlay", hidden: true });
  let offsetMs = 0; // where the current converted stream starts in the file
  const pos = () => offsetMs + Math.round((video.currentTime || 0) * 1000);
  const dur = () => (converted ? totalMs || null : Number.isFinite(video.duration) ? Math.round(video.duration * 1000) : null);

  // An iPhone puts only a <video> full screen, with its own controls.
  // A phone that allows it (Android Chrome, only while full screen) turns to
  // landscape for a wide picture, and back when full screen ends.
  const fullscreen = () => {
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    else if (shell.requestFullscreen) {
      shell.requestFullscreen().then(() => {
        const o = screen.orientation;
        if (o && o.lock && video.videoWidth > video.videoHeight) o.lock("landscape").catch(() => {});
      }).catch(() => {});
    } else if (video.webkitEnterFullscreen) video.webkitEnterFullscreen();
  };
  const onFullscreenChange = () => {
    const o = screen.orientation;
    if (!document.fullscreenElement && o && o.unlock) {
      try { o.unlock(); } catch { /* not locked */ }
    }
  };
  document.addEventListener("fullscreenchange", onFullscreenChange);

  // "Loading" is the loading bar, not a word in the middle of the picture;
  // other notes ("Preparing subtitles") still show as text.
  let barOn = false;
  const setLoading = (on) => {
    if (on === barOn) return;
    barOn = on;
    if (on) loadBar.show();
    else loadBar.hide();
  };
  function showStatus(msg) {
    const loading = msg === "Loading";
    setLoading(loading);
    status.textContent = loading ? "" : msg;
    status.hidden = loading || !msg;
  }

  // Subtitles: fetched only when chosen, so an embedded track is not read out
  // of a large file unless someone wants it. Cues are kept and re-timed when a
  // converted stream restarts somewhere else.
  const subs = session.webvtt_subtitles || [];
  const canSearch = !!(session.subtitle_search && session.subtitle_search.enabled);
  // Picture subtitles (PGS, VobSub, DVB) cannot be drawn in a browser, so the
  // server sends no WebVTT copy. They are listed greyed out, so the file does
  // not look as if it had none.
  const pictureSubs = ((session.version && session.version.subtitle_tracks) || []).filter((t) => t.is_image_based);
  const subSel = subs.length || canSearch || pictureSubs.length ? h("select", { "aria-label": "Subtitles" },
    h("option", { value: "" }, "Subtitles off"),
    subs.map((t, i) => h("option", { value: String(i) }, subLabel(t))),
    pictureSubs.map((t) => h("option", { value: `picture-${t.id}`, class: "sub-picture", disabled: true, title: PICTURE_SUBS_NOTE },
      pictureLabel(t))),
    canSearch ? h("option", { value: "search" }, "Search for subtitles…") : null) : null;
  // Search and timing live in subtitle-tools.js. The server shifts the cues it
  // serves by the saved delay, so a change refetches the chosen track.
  let subChoice = "";
  const finder = canSearch ? subtitleSearch({ versionId: session.version.id, canDownload: session.subtitle_search.can_download,
    onPicked: (t) => {
      let i = subs.findIndex((x) => x.track_id === t.track_id);
      if (i < 0) {
        i = subs.push(t) - 1;
        subSel.insertBefore(h("option", { value: String(i) }, subLabel(t)), subSel.querySelector('option.sub-picture, option[value="search"]'));
      }
      subSel.value = String(i);
      subSel.dispatchEvent(new Event("change"));
    } }) : null;
  const timing = subSel ? subtitleTiming({ versionId: session.version.id, initialMs: session.subtitle_offset_ms,
    onChange: () => { cueCache.clear(); if (subChoice !== "") subSel.dispatchEvent(new Event("change")); } }) : null;
  if (timing) timing.el.hidden = true;
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
  // CC: one press (the button or C) turns subtitles off, or on with the
  // language this profile last chose in this browser, else the browser's, a
  // full track before a forced one, else the first full track.
  const ccKey = `vcrx.cc.${(state.me && state.me.id) || 0}`;
  const ccBtn = subs.length ? h("button", { class: "btn btn-small cc-btn", type: "button", "aria-pressed": "false",
    title: "Subtitles on or off (C)" }, "CC") : null;
  function toggleCC() {
    if (!subSel || !subs.length) return;
    if (subChoice !== "") { subSel.value = ""; subSel.dispatchEvent(new Event("change")); return; }
    const want = [localStorage.getItem(ccKey), navigator.language].filter(Boolean).map(englishLanguage);
    const pick = (lang, forced) => subs.findIndex((t) => !!t.is_forced === forced && englishLanguage(t.language || "") === lang);
    let i = -1;
    for (const lang of want) {
      i = pick(lang, false);
      if (i < 0) i = pick(lang, true);
      if (i >= 0) break;
    }
    if (i < 0) i = subs.findIndex((t) => !t.is_forced);
    if (i < 0) i = 0;
    subSel.value = String(i);
    subSel.dispatchEvent(new Event("change"));
  }
  if (ccBtn) ccBtn.addEventListener("click", toggleCC);

  if (subSel) subSel.addEventListener("change", async () => {
    if (subSel.value === "search") { subSel.value = subChoice; finder.open(); return; }
    subChoice = subSel.value;
    if (ccBtn) ccBtn.setAttribute("aria-pressed", String(subChoice !== ""));
    const chosen = subChoice === "" ? null : subs[Number(subChoice)];
    if (chosen && chosen.language) localStorage.setItem(ccKey, chosen.language);
    timing.el.hidden = subChoice === "";
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
      if (token === subToken) {
        showStatus("");
        toast(e.message || "Subtitles could not be loaded.");
        subSel.value = subChoice = "";
        timing.el.hidden = true;
      }
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
    // The audio track goes on the fmp4 stream's URL, or to the seek that
    // starts an HLS stream.
    const audio = audioIndex === null ? "" : `&audio=${audioIndex}`;
    const at = await api(`${base}/playback/sessions/${session.session_id}/seek?position_ms=${target}${hls ? audio : ""}`);
    if (seq !== loadSeq) return;
    offsetMs = at.start_ms;
    video.src = hls ? at.url : at.url + audio;
    applyCues();
    video.play().catch(() => {});
  }

  const sid = session.session_id;
  let started = false, ended = false, paused = false, lastPos = 0, seekFrom = null;
  const report = (type, extra = {}, keepalive = false) =>
    api(`${base}/playback/sessions/${sid}/events`, { method: "POST", body: { type, position_ms: pos(), duration_ms: dur(), ...extra }, keepalive })
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
  // A converted stream restarts on every seek, so its skip is 30 s, as its keys.
  const skipMs = converted ? 30000 : 10000;
  let playBtn = null;
  if (converted) {
    // Round icon buttons, each named for what it does now, so a screen reader
    // hears "Pause" while playing and "Mute" while the sound is on.
    playBtn = h("button", { class: "pc-icon pc-main", type: "button", "aria-label": "Play" });
    const iconBtn = (name, label, extra = null) => h("button", { class: "pc-icon" + (extra ? " pc-skip" : ""), type: "button", "aria-label": label, title: label }, icon(name), extra);
    const backBtn10 = iconBtn("replay", `Back ${skipMs / 1000} seconds`, h("small", { "aria-hidden": "true" }, String(skipMs / 1000)));
    const fwdBtn = iconBtn("forward", `Forward ${skipMs / 1000} seconds`, h("small", { "aria-hidden": "true" }, String(skipMs / 1000)));
    const timeLbl = h("span", { class: "pc-time" });
    const bar = h("input", { class: "pc-bar", type: "range", min: "0", max: String(totalMs || 1), step: "1000", value: "0", "aria-label": "Position" });
    const muteBtn = iconBtn("volume", "Mute");
    const fsBtn = iconBtn("fullscreen", "Full screen");
    let dragging = false;
    let drawnPaused = null, drawnMuted = null;
    const draw = () => {
      // Icons change only when the state does, not on every timeupdate.
      if (drawnPaused !== video.paused) {
        drawnPaused = video.paused;
        playBtn.replaceChildren(icon(video.paused ? "play" : "pause"));
        playBtn.setAttribute("aria-label", video.paused ? "Play" : "Pause");
      }
      if (drawnMuted !== video.muted) {
        drawnMuted = video.muted;
        muteBtn.replaceChildren(icon(video.muted ? "muted" : "volume"));
        muteBtn.setAttribute("aria-label", video.muted ? "Unmute" : "Mute");
        muteBtn.title = muteBtn.getAttribute("aria-label");
      }
      if (!dragging) bar.value = String(pos());
      timeLbl.textContent = `${clock(dragging ? Number(bar.value) : pos())} / ${clock(totalMs)}`;
      // A screen reader reads the position as a time, not as milliseconds.
      bar.setAttribute("aria-valuetext", `${clock(Number(bar.value))} of ${clock(totalMs)}`);
    };
    playBtn.addEventListener("click", () => (video.paused ? video.play() : video.pause()));
    backBtn10.addEventListener("click", () => seekTo(Math.max(0, pos() - skipMs)));
    fwdBtn.addEventListener("click", () => seekTo(pos() + skipMs));
    muteBtn.addEventListener("click", () => { video.muted = !video.muted; draw(); });
    fsBtn.addEventListener("click", fullscreen);
    bar.addEventListener("input", () => { dragging = true; draw(); });
    bar.addEventListener("change", () => { dragging = false; seekTo(Number(bar.value)); });
    for (const ev of ["timeupdate", "play", "pause", "volumechange"]) video.addEventListener(ev, draw);
    video.addEventListener("dblclick", () => fsBtn.click());
    controls = h("div", { class: "pc" }, backBtn10, playBtn, fwdBtn, timeLbl, bar, muteBtn, fsBtn);
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

  // Native append, unlike h(), writes a null child as the text "null", and
  // direct play has no VCRX controls.
  shell.append(...[video, h("div", { class: "top" }, backBtn, h("div", { class: "ptitle" }, title), audioSel,
    converted ? h("span", { class: "pill", title: session.reason }, convertedLabel(session.decision)) : null,
    ccBtn, subSel, timing && timing.el), controls, status, overlay, finder && finder.el].filter(Boolean));
  skipButtons({ shell, video, session, pos, seekTo, durationMs: dur, hasNext: !!item.next_episode });

  video.addEventListener("loadedmetadata", () => {
    if (!converted && session.resume_position_ms > 0 && video.currentTime < 1) video.currentTime = session.resume_position_ms / 1000;
  }, { once: true });
  // An HLS stream nobody asks anything of for two minutes is deleted, and a
  // paused Safari may ask nothing, so after a long pause the stream starts
  // again where it stopped.
  let pausedAt = 0;
  video.addEventListener("play", () => {
    if (hls && pausedAt && Date.now() - pausedAt > 90000) loadAt(pos()).catch((e) => toast(e.message));
    pausedAt = 0;
  });
  let recoveries = 0;
  video.addEventListener("playing", () => {
    recoveries = 0;
    showStatus("");
    if (!started) { started = true; report("start"); }
    else if (paused) { paused = false; report("resume"); }
  });
  video.addEventListener("waiting", () => showStatus("Loading"));
  video.addEventListener("pause", () => {
    pausedAt = Date.now();
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
    const play = () => { clearInterval(upNextTimer); location.hash = peerId ? `#/peer/${peerId}/play/${next.id}` : `#/play/${next.id}`; };
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
    // A file played directly: a dropped connection (a phone between Wi-Fi and
    // mobile data, a tunnel) loads it again where it stopped, three times,
    // waiting longer each time, before giving up.
    if (!converted && code === 2 && recoveries < 3) {
      recoveries++;
      reconnect(recoveries * 1500);
      return;
    }
    // An HLS stream can lose a segment the server already deleted (a seek
    // far back in the native player); starting again there recovers.
    if (hls && code !== 4 && recoveries < 2) {
      recoveries++;
      loadAt(pos()).catch((e) => {
        ended = true;
        overlay.replaceChildren(errorBox("Playback failed", e.message, h("button", { class: "btn", type: "button", onclick: back }, "Back")));
        overlay.hidden = false;
      });
      return;
    }
    const msg = code === 4 ? "The browser cannot decode this file." : "The stream was interrupted.";
    report("error", { message: `web: ${msg} (media error ${code})` });
    ended = true;
    overlay.replaceChildren(errorBox("Playback failed", msg + " The TV app may still play it.", h("button", { class: "btn", type: "button", onclick: back }, "Back")));
    overlay.hidden = false;
  });

  let reconnectTimer = 0;
  function reconnect(waitMs) {
    const at = lastPos;
    showStatus("Reconnecting");
    clearTimeout(reconnectTimer);
    reconnectTimer = setTimeout(() => {
      if (ended) return;
      video.addEventListener("loadedmetadata", () => { video.currentTime = at / 1000; }, { once: true });
      video.src = session.stream.url;
      video.play().catch(() => {});
    }, waitMs);
  }
  // Back online after a drop that left a direct stream stuck: try at once.
  const onOnline = () => {
    if (!converted && !ended && started && video.readyState < 3 && !video.paused) reconnect(0);
  };
  window.addEventListener("online", onOnline);

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
  // Touch: a tap on the picture shows the controls, or hides them if they are
  // showing; it never pauses, which a stray tap on a phone would. The mouse
  // keeps click to pause. Native controls (a file played directly) handle
  // their own taps; the tap still brings back the top bar.
  let touchTap = false;
  shell.addEventListener("pointerdown", (e) => {
    touchTap = e.pointerType !== "mouse";
    if (!touchTap) return;
    const wasIdle = shell.classList.contains("idle");
    if (e.target === video && converted && !wasIdle && !video.paused) {
      clearTimeout(idleTimer);
      shell.classList.add("idle");
    } else wake();
  }, true);
  if (converted) {
    video.addEventListener("click", () => {
      if (!touchTap) playBtn.click();
    });
  }
  wake();

  // Lock screen, notification and headphone controls, as the music player has.
  const ms = "mediaSession" in navigator ? navigator.mediaSession : null;
  const sessionPosition = () => {
    const d = dur();
    if (!ms || !ms.setPositionState || !(d > 0)) return;
    try { ms.setPositionState({ duration: d / 1000, position: Math.min(d, pos()) / 1000, playbackRate: video.playbackRate || 1 }); } catch { /* past the end mid-seek */ }
  };
  if (ms) {
    // An episode shows its show's poster.
    const posterId = item.kind === "episode" && item.show_id ? item.show_id : item.has_poster ? item.id : null;
    const art = posterId ? [240, 480].map((w) => ({ src: new URL(artUrl(posterId, "poster", w, base), location.href).href,
      sizes: `${w}x${Math.round(w * 1.5)}`, type: "image/jpeg" })) : [];
    try {
      ms.metadata = new MediaMetadata({ title: playingExtra ? playingExtra.title : item.title || "",
        artist: item.kind === "episode" ? `${item.show_title || ""} ${episodeCode(item)}`.trim() : item.year ? String(item.year) : "",
        album: playingExtra ? item.title : "", artwork: art });
    } catch (e) { console.warn("media session", e); }
    const set = (action, fn) => { try { ms.setActionHandler(action, fn); } catch { /* not supported here */ } };
    set("play", () => video.play().catch(() => {}));
    set("pause", () => video.pause());
    set("seekbackward", (d) => seekTo(Math.max(0, pos() - (d.seekOffset ? d.seekOffset * 1000 : skipMs))));
    set("seekforward", (d) => seekTo(pos() + (d.seekOffset ? d.seekOffset * 1000 : skipMs)));
    set("seekto", (d) => seekTo(d.seekTime * 1000));
    set("previoustrack", null);
    set("nexttrack", item.kind === "episode" && item.next_episode ? () => {
      location.hash = peerId ? `#/peer/${peerId}/play/${item.next_episode.id}` : `#/play/${item.next_episode.id}`;
    } : null);
    for (const ev of ["play", "pause", "seeked", "loadedmetadata"]) video.addEventListener(ev, sessionPosition);
    video.addEventListener("play", () => { ms.playbackState = "playing"; });
    video.addEventListener("pause", () => { ms.playbackState = "paused"; });
  }

  // A phone may suspend or close a page it hides, without pagehide: report
  // where playback is as the page goes out of sight, so Resume is exact.
  const onHidden = () => {
    if (document.visibilityState === "hidden" && started && !ended) report("progress", {}, true);
  };
  document.addEventListener("visibilitychange", onHidden);

  const onKey = (e) => {
    if (e.target.tagName === "SELECT" || e.target.tagName === "INPUT") return;
    if (e.key === " " || e.key === "k") { e.preventDefault(); video.paused ? video.play() : video.pause(); }
    else if (e.key === "ArrowRight") seekTo(pos() + (converted ? 30000 : 10000));
    else if (e.key === "ArrowLeft") seekTo(Math.max(0, pos() - (converted ? 30000 : 10000)));
    else if (e.key === "f") fullscreen();
    else if (e.key === "m") video.muted = !video.muted;
    else if (e.key === "c") toggleCC();
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
      setLoading(false);
      loadSeq++;
      clearInterval(beat);
      clearInterval(upNextTimer);
      clearTimeout(idleTimer);
      clearTimeout(reconnectTimer);
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("visibilitychange", onHidden);
      document.removeEventListener("fullscreenchange", onFullscreenChange);
      window.removeEventListener("pagehide", stop);
      window.removeEventListener("online", onOnline);
      video.pause();
      video.removeAttribute("src");
      video.load();
      if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
      try { if (screen.orientation && screen.orientation.unlock) screen.orientation.unlock(); } catch { /* not locked */ }
      reclaimMediaSession();
    },
  };
}

// beforeView plays the clips Before a movie lists, one after another, with
// one Skip for each (Enter or S too), then calls done to start the movie.
function beforeView(clips, shell, backBtn, done) {
  let i = 0;
  let finished = false;
  const video = h("video", { autoplay: true, playsinline: true, preload: "auto" });
  const label = h("div", { class: "ptitle" });
  const skip = h("button", { class: "btn btn-primary preroll-skip", type: "button" });
  const show = () => {
    const c = clips[i];
    label.textContent = c.kind === "trailer" ? `Trailer · ${c.title}` : "Your movie is next";
    skip.textContent = i < clips.length - 1 ? "Skip" : "Skip to the movie";
    video.src = c.url;
    video.play().catch(() => {});
  };
  const next = () => {
    if (finished) return;
    if (++i < clips.length) return show();
    finished = true;
    done();
  };
  video.addEventListener("ended", next);
  // A clip that will not play is skipped, never a dead end before the movie.
  video.addEventListener("error", next);
  skip.addEventListener("click", next);
  const onKey = (e) => {
    if (e.key === "Enter" || e.key === "s") { e.preventDefault(); next(); }
  };
  document.addEventListener("keydown", onKey);
  shell.classList.add("preroll");
  shell.append(video, h("div", { class: "top" }, backBtn, label), h("div", { class: "preroll-bar" }, skip));
  show();
  return {
    el: shell,
    title: "Before the movie",
    cleanup() {
      finished = true;
      document.removeEventListener("keydown", onKey);
      video.pause();
      video.removeAttribute("src");
      video.load();
    },
  };
}

function subLabel(t) {
  const lang = t.language ? languageName(t.language) : "Unknown";
  return [t.title || lang, t.is_forced ? "(forced)" : "", t.downloaded ? "(downloaded)" : t.embedded ? "" : "(file)"].filter(Boolean).join(" ");
}

const PICTURE_SUBS_NOTE = "Picture subtitles need the TV app";
const PICTURE_SUB_FORMATS = { hdmv_pgs_subtitle: "PGS", dvd_subtitle: "VobSub", dvb_subtitle: "DVB", xsub: "XSUB" };

// pictureLabel names a picture subtitle track and says why it cannot be chosen.
function pictureLabel(t) {
  const lang = t.language ? languageName(t.language) : "Unknown";
  const format = PICTURE_SUB_FORMATS[(t.codec || "").toLowerCase()] || "";
  return [t.title || lang, format ? `(${format})` : "", t.is_forced ? "(forced)" : "", "·", PICTURE_SUBS_NOTE].filter(Boolean).join(" ");
}

function audioLabel(t) {
  const ch = t.channels ? (t.channels >= 6 ? `${t.channels - 1}.1` : t.channels === 2 ? "stereo" : `${t.channels} ch`) : "";
  return [t.title || (t.language ? languageName(t.language) : "Unknown"), (t.codec || "").toUpperCase(), ch].filter(Boolean).join(" · ");
}

// englishLanguage names a language code in English, so "eng", "en" and
// "en-US" all read "English" and compare equal.
function englishLanguage(code) {
  try {
    return new Intl.DisplayNames(["en"], { type: "language" }).of(code.split("-")[0]) || code;
  } catch {
    return code;
  }
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

function convertedLabel(decision) {
  if (decision === "transcode") return "Video converted";
  return decision === "audio_transcode" ? "Audio converted" : "Repackaged";
}

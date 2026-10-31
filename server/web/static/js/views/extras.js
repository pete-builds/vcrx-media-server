// Trailers, extras and theme music on the details page. Local files play in
// the VCRX player like any title (#/play/<item>?extra=<id>); a YouTube trailer,
// offered only when the admin turned trailers_youtube on, opens in YouTube's
// no-cookie player in a dialog, the one frame the server's CSP then allows.

import { h, icon, clock } from "../dom.js";
import { go } from "../app.js";

const KINDS = {
  trailer: "Trailer", featurette: "Featurette", behind_the_scenes: "Behind the scenes", deleted_scene: "Deleted scene",
  interview: "Interview", scene: "Scene", short: "Short", other: "Extra",
};

// A YouTube video id. The server sends nothing else, and this check keeps it
// that way before a key goes into a URL.
const YOUTUBE_KEY = /^[A-Za-z0-9_-]{11}$/;

export function extraPlayPath(itemId, extraId) {
  return `/play/${itemId}?extra=${extraId}`;
}

// extraActions are the buttons beside Play: Trailer, and for a show with a
// theme.mp3, its theme music.
export function extraActions(item) {
  const out = [];
  const t = item.trailer;
  if (t && t.source === "local" && t.extra_id) {
    out.push(h("button", { class: "btn btn-glass trailer-btn", type: "button", onclick: () => go(extraPlayPath(item.id, t.extra_id)) },
      icon("play"), "Trailer"));
  } else if (t && t.source === "youtube" && YOUTUBE_KEY.test(t.youtube_key || "")) {
    const btn = h("button", { class: "btn btn-glass trailer-btn", type: "button", title: "Plays from YouTube" }, icon("play"), "Trailer");
    btn.addEventListener("click", () => youTubeDialog(t, btn));
    out.push(btn);
  }
  if (item.theme_music && item.theme_music.url) out.push(themeButton(item.theme_music.url));
  return out;
}

// extrasSection is the Extras row: every local extra but the theme music.
export function extrasSection(item) {
  const extras = item.extras || [];
  if (!extras.length) return null;
  return h("section", { class: "section extras" }, h("h2", {}, "Extras"),
    h("div", { class: "extras-row" }, extras.map((x) => h("button", { class: "extra", type: "button",
      onclick: () => go(extraPlayPath(item.id, x.id)), "aria-label": `Play ${x.title}` },
    h("span", { class: "extra-icon" }, icon("play")),
    h("span", { class: "extra-text" },
      h("span", { class: "extra-title" }, x.title),
      h("span", { class: "extra-sub" }, [KINDS[x.kind] || "Extra", x.duration_ms ? clock(x.duration_ms) : ""].filter(Boolean).join(" · ")))))));
}

// youTubeDialog plays a YouTube trailer over the page. Closing it removes the
// frame, which stops the video, and returns focus to the button.
function youTubeDialog(t, returnTo) {
  const close = () => {
    backdrop.remove();
    window.removeEventListener("keydown", onKey, true);
    window.removeEventListener("hashchange", close);
    if (returnTo && returnTo.isConnected) returnTo.focus();
  };
  const onKey = (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  };
  const closeBtn = h("button", { class: "btn btn-small", type: "button", onclick: close }, icon("close"), "Close");
  // YouTube refuses embeds that arrive with no referrer, and the app's own
  // policy sends none, so this frame alone sends the origin (never the path).
  const frame = h("iframe", {
    src: `https://www.youtube-nocookie.com/embed/${t.youtube_key}?autoplay=1&rel=0`,
    title: t.title || "Trailer", allow: "autoplay; encrypted-media; picture-in-picture; fullscreen", allowfullscreen: true,
    referrerpolicy: "origin",
  });
  const backdrop = h("div", { class: "yt-backdrop" },
    h("div", { class: "yt-dialog", role: "dialog", "aria-modal": "true", "aria-label": t.title || "Trailer" },
      h("div", { class: "yt-head" }, h("span", { class: "yt-title" }, t.title || "Trailer"), h("span", { class: "faint" }, "YouTube"), closeBtn),
      h("div", { class: "yt-frame" }, frame)));
  backdrop.addEventListener("click", (e) => { if (e.target === backdrop) close(); });
  window.addEventListener("keydown", onKey, true);
  window.addEventListener("hashchange", close);
  document.body.append(backdrop);
  closeBtn.focus();
}

// themeButton plays a show's theme music quietly, only when asked: browsers
// rightly refuse sound before a click, and a page that starts singing is rude.
// It stops when the page changes.
function themeButton(url) {
  const audio = new Audio();
  audio.preload = "none";
  audio.loop = true;
  audio.volume = 0.3;
  const label = h("span", {}, "Theme music");
  const btn = h("button", { class: "btn btn-glass theme-btn", type: "button", "aria-pressed": "false" }, icon("play"), label);
  const stop = () => {
    audio.pause();
    btn.setAttribute("aria-pressed", "false");
    btn.firstChild.replaceWith(icon("play"));
    label.textContent = "Theme music";
  };
  btn.addEventListener("click", () => {
    if (!audio.paused) return stop();
    if (!audio.src) audio.src = url;
    audio.play().then(() => {
      btn.setAttribute("aria-pressed", "true");
      btn.firstChild.replaceWith(icon("stop"));
      label.textContent = "Stop theme";
    }).catch(stop);
  });
  window.addEventListener("hashchange", () => { audio.pause(); audio.removeAttribute("src"); }, { once: true });
  return btn;
}

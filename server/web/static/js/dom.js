// DOM helpers. Every value from the server goes in through textContent or an
// attribute, never through innerHTML, so a title cannot inject markup.

import { artUrl } from "./api.js";

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
    else if (k === "style" && typeof v === "object") Object.assign(el.style, v);
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c === undefined || c === null || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

const SVG = "http://www.w3.org/2000/svg";
export function s(tag, attrs, ...children) {
  const el = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v !== undefined && v !== null) el.setAttribute(k, String(v));
  }
  for (const c of children.flat(Infinity)) {
    if (c === undefined || c === null) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

export function icon(name) {
  const paths = {
    play: "M8 5v14l11-7z",
    check: "M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z",
    back: "M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20z",
    stop: "M6 6h12v12H6z",
    close: "M19 6.4 17.6 5 12 10.6 6.4 5 5 6.4 10.6 12 5 17.6 6.4 19 12 13.4 17.6 19 19 17.6 13.4 12z",
    left: "M15.4 7.4 14 6l-6 6 6 6 1.4-1.4-4.6-4.6z",
    right: "M8.6 16.6 10 18l6-6-6-6-1.4 1.4 4.6 4.6z",
    info: "M11 7h2v2h-2zm0 4h2v6h-2zm1-9a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm0 18a8 8 0 1 1 0-16 8 8 0 0 1 0 16z",
    restart: "M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z",
    menu: "M3 6h18v2H3zm0 5h18v2H3zm0 5h18v2H3z",
    star: "m12 17.3 6.2 3.7-1.6-7 5.4-4.7-7.2-.6L12 2 9.2 8.7 2 9.3l5.4 4.7-1.6 7z",
    // A cassette: the shell, a window, and two reels (holes, by even-odd).
    tape: "M2 5h20v14H2zM4 7v10h16V7zm1.5 2h13v6h-13zM9 10.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zm6 0a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z",
    up: "M7.4 15.4 12 10.8l4.6 4.6L18 14l-6-6-6 6z",
    down: "M7.4 8.6 12 13.2l4.6-4.6L18 10l-6 6-6-6z",
    grip: "M9 4h2v2H9zm4 0h2v2h-2zM9 9h2v2H9zm4 0h2v2h-2zm-4 5h2v2H9zm4 0h2v2h-2zm-4 5h2v2H9zm4 0h2v2h-2z",
  };
  return s("svg", { viewBox: "0 0 24 24", "aria-hidden": "true" }, s("path", { d: paths[name], "fill-rule": name === "tape" ? "evenodd" : null }));
}

export function clock(ms) {
  if (ms == null) return "";
  const t = Math.max(0, Math.round(ms / 1000));
  const hh = Math.floor(t / 3600), mm = Math.floor((t % 3600) / 60), ss = t % 60;
  return hh > 0 ? `${hh}:${String(mm).padStart(2, "0")}:${String(ss).padStart(2, "0")}` : `${mm}:${String(ss).padStart(2, "0")}`;
}

export function runtime(ms) {
  if (!ms) return "";
  const m = Math.round(ms / 60000);
  return m >= 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m} min`;
}

export function hours(ms) {
  const hrs = ms / 3600000;
  if (hrs >= 10) return `${Math.round(hrs)} h`;
  if (hrs >= 1) return `${hrs.toFixed(1)} h`;
  return `${Math.round(ms / 60000)} min`;
}

export function when(ms) {
  if (!ms) return "";
  const d = new Date(ms), now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const time = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  if (sameDay) return `Today ${time}`;
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return `Yesterday ${time}`;
  return d.toLocaleDateString([], { month: "short", day: "numeric", year: d.getFullYear() === now.getFullYear() ? undefined : "numeric" }) + ` ${time}`;
}

export function bitrate(bps) {
  if (!bps) return "";
  return bps >= 1e6 ? `${(bps / 1e6).toFixed(1)} Mbps` : `${Math.round(bps / 1e3)} kbps`;
}

export function size(bytes) {
  if (!bytes) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, n = bytes;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n >= 10 || i === 0 ? 0 : 1)} ${u[i]}`;
}

export function episodeCode(item) {
  if (item.season_number == null || item.episode_number == null) return "";
  const end = item.episode_number_end ? `-E${String(item.episode_number_end).padStart(2, "0")}` : "";
  return `S${String(item.season_number).padStart(2, "0")}E${String(item.episode_number).padStart(2, "0")}${end}`;
}

export function subtitle(item) {
  switch (item.kind) {
    case "movie": return item.year ? String(item.year) : "";
    case "show": return item.child_count ? `${item.child_count} season${item.child_count === 1 ? "" : "s"}` : (item.year ? String(item.year) : "");
    case "season": return item.child_count ? `${item.child_count} episode${item.child_count === 1 ? "" : "s"}` : "";
    case "episode": return [item.show_title, episodeCode(item)].filter(Boolean).join(" · ");
    default: return "";
  }
}

// fadeImg is an image that fades in once it has loaded (the art box shows its
// own colour until then) and calls onFail if the server has none after all.
export function fadeImg(src, { alt = "", lazy = true, onFail, srcset, sizes } = {}) {
  const el = h("img", { src, srcset, sizes, alt, loading: lazy ? "lazy" : null, decoding: "async", class: "fade" });
  el.addEventListener("load", () => el.classList.add("loaded"), { once: true });
  el.addEventListener("error", () => { el.remove(); if (onFail) onFail(); }, { once: true });
  return el;
}

// placeholderArt stands in for missing artwork: the title on a dark gradient.
export function placeholderArt(text) {
  return h("div", { class: "placeholder" }, h("span", {}, text));
}

// cardArt is a card's picture: the poster (or, landscape, a still or backdrop)
// with its watched badge and progress bar, or a placeholder when there is none.
export function cardArt(item, { landscape = false, base = "" } = {}) {
  const kind = landscape ? (item.has_thumb ? "thumb" : "backdrop") : "poster";
  const has = landscape ? (item.has_thumb || item.has_backdrop) : item.has_poster;
  const art = h("div", { class: "art" });
  const placeholder = () => art.prepend(placeholderArt(item.kind === "episode" ? episodeCode(item) || item.title : item.title));
  if (has) art.append(fadeImg(artUrl(item.id, kind, landscape ? 480 : 320, base), { onFail: placeholder }));
  else placeholder();
  const w = item.watch || {};
  if (item.kind === "show" || item.kind === "season") {
    if (w.unplayed_count) art.append(h("span", { class: "badge", title: "Unwatched episodes" }, w.unplayed_count));
    else if (w.played) art.append(h("span", { class: "badge watched", title: "Watched" }, icon("check")));
  } else if (w.played) {
    art.append(h("span", { class: "badge watched", title: "Watched" }, icon("check")));
  }
  if (w.position_ms > 0 && w.duration_ms) {
    art.append(h("div", { class: "progress" }, h("i", { style: { width: `${Math.min(100, (100 * w.position_ms) / w.duration_ms)}%` } })));
  }
  return art;
}

// card draws a poster (or, landscape, a thumbnail) linking to the item page.
// A friend's title passes base (its server's API path) and badge, which says
// where it lives.
export function card(item, { landscape = false, href, base = "", badge = "" } = {}) {
  const art = cardArt(item, { landscape, base });
  if (badge) art.append(h("span", { class: "source-badge" }, badge));
  const title = item.kind === "episode" && landscape ? `${episodeCode(item)} ${item.title}`.trim() : item.title;
  const sub = item.kind === "episode" && landscape ? item.show_title || "" : subtitle(item);
  // Some Home rows add a note, such as "3 new episodes"; it takes the sub line.
  const line = item.note ? h("div", { class: "sub note" }, item.note) : sub ? h("div", { class: "sub" }, sub) : null;
  return h("a", { class: "card" + (landscape ? " landscape" : ""), href: href || `#/item/${item.id}`, title: item.title },
    art, h("div", { class: "title" }, title), line);
}

// skeleton is what a page shows while it loads: grey shapes in the layout the
// page will have, rather than the word "Loading". Screen readers hear "Loading".
export function skeleton(path) {
  const bar = (cls) => h("div", { class: "sk " + cls });
  const posters = (n, cls = "") => h("div", { class: "sk-strip " + cls }, Array.from({ length: n }, () => bar("sk-poster")));
  let body;
  if (path.startsWith("/play/")) body = [];
  else if (path === "/") body = [bar("sk-feature"), bar("sk-heading"), posters(8), bar("sk-heading"), posters(8)];
  else if (path.startsWith("/item/")) body = [h("div", { class: "sk-hero" }, bar("sk-hero-poster"), h("div", { class: "sk-lines" }, bar("sk-title"), bar("sk-line"), bar("sk-line short"), bar("sk-button")))];
  else if (/^\/(library|collection|search|row)/.test(path)) body = [bar("sk-title"), h("div", { class: "sk-grid" }, Array.from({ length: 18 }, () => bar("sk-poster")))];
  else body = [bar("sk-title"), bar("sk-line"), bar("sk-line"), bar("sk-line short")];
  return h("div", { class: "skeleton" + (path === "/" || path.startsWith("/item/") ? " sk-over-hero" : ""), "aria-busy": "true" },
    h("span", { class: "sr-only", role: "status" }, "Loading"), body);
}

export function errorBox(title, message, action) {
  return h("div", { class: "error-box", role: "alert" }, h("h2", {}, title), h("p", { class: "muted" }, message), action || null);
}

// loadBar is the one loading sign in place of a spinner: a thin VCRX Orange
// bar top left (app.css .load-bar), shown only once a load passes 150 ms. Each
// show is matched by one hide; it stays while any load is running.
let loads = 0;
let barEl = null;
export const loadBar = {
  show() {
    if (!barEl) {
      barEl = h("div", { class: "load-bar", role: "progressbar", "aria-label": "Loading", hidden: true });
      document.body.append(barEl);
    }
    if (loads++ === 0) barEl.hidden = false;
  },
  hide() {
    if (loads > 0 && --loads === 0 && barEl) barEl.hidden = true;
  },
};

let toastTimer;
export function toast(msg) {
  const t = document.getElementById("toast");
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

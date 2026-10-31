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
  };
  return s("svg", { viewBox: "0 0 24 24", "aria-hidden": "true" }, s("path", { d: paths[name] }));
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

function img(src, alt, onFail) {
  const el = h("img", { src, alt, loading: "lazy", decoding: "async" });
  el.addEventListener("error", () => { el.remove(); onFail(); }, { once: true });
  return el;
}

// card draws a poster (or, landscape, a thumbnail) linking to the item page.
export function card(item, { landscape = false, href } = {}) {
  const kind = landscape ? (item.has_thumb ? "thumb" : "backdrop") : "poster";
  const has = landscape ? (item.has_thumb || item.has_backdrop) : item.has_poster;
  const art = h("div", { class: "art" });
  const placeholder = () => art.prepend(h("div", { class: "placeholder" }, item.kind === "episode" ? episodeCode(item) || item.title : item.title));
  if (has) art.append(img(artUrl(item.id, kind, landscape ? 480 : 300), "", placeholder));
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
  const title = item.kind === "episode" && landscape ? `${episodeCode(item)} ${item.title}`.trim() : item.title;
  const sub = item.kind === "episode" && landscape ? item.show_title || "" : subtitle(item);
  return h("a", { class: "card" + (landscape ? " landscape" : ""), href: href || `#/item/${item.id}`, title: item.title },
    art, h("div", { class: "title" }, title), sub ? h("div", { class: "sub" }, sub) : null);
}

export function errorBox(title, message, action) {
  return h("div", { class: "error-box", role: "alert" }, h("h2", {}, title), h("p", { class: "muted" }, message), action || null);
}

let toastTimer;
export function toast(msg) {
  const t = document.getElementById("toast");
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

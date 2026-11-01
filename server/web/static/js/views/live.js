// Live TV: the channels the viewer may watch with what is on now and next
// (GET /live/channels), and a player that pulls one (POST .../watch). Safari
// plays VCRX's HLS playlist natively; every other browser plays the same pull
// as one fragmented MP4 (stream.mp4), so no HLS library is needed. Up and
// Down, or Page Up and Page Down, change channel; Escape goes back.

import { api, ApiError } from "../api.js";
import { h, errorBox } from "../dom.js";
import { isSafari } from "../caps.js";

const logoUrl = (c) => `/api/v1/live/channels/${c.id}/logo`;

function timeOfDay(ms) {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// The share of a programme already shown, 0 to 1.
function progress(p, now) {
  return Math.min(1, Math.max(0, (now - p.start) / (p.stop - p.start)));
}

function channelRow(c, now) {
  const on = c.now;
  return h("a", { class: "live-row", href: `#/live/${c.id}`, "data-name": `${c.number || ""} ${c.name} ${on ? on.title : ""}`.toLowerCase() },
    h("span", { class: "live-num" }, c.number || ""),
    c.has_logo ? h("img", { class: "live-logo", src: logoUrl(c), alt: "", loading: "lazy" }) : h("span", { class: "live-logo none" }, c.name.slice(0, 2)),
    h("span", { class: "live-name" }, c.name),
    h("span", { class: "live-now" },
      on ? [h("span", { class: "live-title" }, on.title, on.episode ? h("span", { class: "live-ep" }, " " + on.episode) : null),
        h("span", { class: "live-bar" }, h("span", { style: `width:${Math.round(progress(on, now) * 100)}%` }))]
        : h("span", { class: "live-title dim" }, "No guide")),
    h("span", { class: "live-next" }, c.next ? [h("span", { class: "dim" }, timeOfDay(c.next.start) + " "), c.next.title] : ""));
}

export async function liveView() {
  const data = await api("/live/channels");
  const channels = data.channels || [];
  if (!channels.length) {
    return { el: h("div", { class: "empty" }, "No channels. An admin lists playlists under live_tv in vcrx.yaml, and grants Live TV in Settings, Users."), title: "Live TV" };
  }
  const now = Date.now();
  const list = h("div", { class: "live-list" }, channels.map((c) => channelRow(c, now)));
  const filter = h("input", { type: "search", class: "live-filter", placeholder: "Filter channels", "aria-label": "Filter channels" });
  const count = h("span", { class: "count" }, `${channels.length} channels`);
  filter.addEventListener("input", () => {
    const q = filter.value.trim().toLowerCase();
    let shown = 0;
    for (const row of list.children) {
      const hit = !q || row.dataset.name.includes(q);
      row.hidden = !hit;
      if (hit) shown++;
    }
    count.textContent = q ? `${shown} of ${channels.length} channels` : `${channels.length} channels`;
  });
  const el = h("div", { class: "live-page" }, h("div", { class: "toolbar" }, h("h1", {}, "Live TV"), filter, count), list);
  // What is on now changes; refresh the list every minute while it is open.
  const timer = setInterval(async () => {
    try {
      const fresh = (await api("/live/channels")).channels || [];
      const t = Date.now();
      const byId = new Map(fresh.map((c) => [c.id, c]));
      for (const row of list.children) {
        const c = byId.get(Number(row.getAttribute("href").split("/").pop()));
        if (c) row.replaceWith(Object.assign(channelRow(c, t), { hidden: row.hidden }));
      }
    } catch (e) { /* the next minute tries again */ }
  }, 60000);
  return { el, title: "Live TV", cleanup: () => clearInterval(timer) };
}

export async function livePlayerView({ match }) {
  const id = Number(match[1]);
  const channels = (await api("/live/channels")).channels || [];
  const idx = channels.findIndex((c) => c.id === id);
  if (idx < 0) throw new ApiError(404, "not_found", "No such channel.");
  const c = channels[idx];
  const video = h("video", { autoplay: "", playsinline: "", controls: "" });
  const status = h("div", { class: "status", hidden: "" });
  const title = h("span", { class: "ptitle" }, `${c.number ? c.number + "  " : ""}${c.name}${c.now ? "  ·  " + c.now.title : ""}`);
  const back = h("a", { class: "btn", href: "#/live", "aria-label": "Back to the channels" }, "Back");
  const step = (d) => {
    const next = channels[(idx + d + channels.length) % channels.length];
    location.hash = `/live/${next.id}`;
  };
  const up = h("button", { class: "btn", type: "button", title: "Channel up (Up arrow)" }, "Ch +");
  const down = h("button", { class: "btn", type: "button", title: "Channel down (Down arrow)" }, "Ch −");
  up.addEventListener("click", () => step(1));
  down.addEventListener("click", () => step(-1));
  const el = h("div", { class: "player live-player" }, h("div", { class: "top" }, back, title, down, up), video, status);

  const say = (msg) => { status.textContent = msg; status.hidden = !msg; };
  say("Tuning…");
  let alive = true;
  try {
    const w = await api(`/live/channels/${id}/watch`, { method: "POST" });
    if (!alive) return { el };
    video.src = isSafari() ? w.playlist : w.playlist.replace(/index\.m3u8$/, "stream.mp4");
    video.addEventListener("playing", () => say(""), { once: true });
    video.addEventListener("error", () => say("The channel stopped playing. Press Up or Down to try another."));
  } catch (err) {
    const busy = err instanceof ApiError && err.code === "streams_busy";
    el.replaceChildren(el.firstChild, errorBox(busy ? "All streams in use" : "This channel will not play", err.message || "Try another channel.",
      h("a", { class: "btn", href: "#/live" }, "Channels")));
  }
  const onKey = (e) => {
    if (e.target instanceof HTMLInputElement) return;
    if (e.key === "ArrowUp" || e.key === "PageUp") { e.preventDefault(); step(1); }
    else if (e.key === "ArrowDown" || e.key === "PageDown") { e.preventDefault(); step(-1); }
    else if (e.key === "Escape") location.hash = "/live";
  };
  document.addEventListener("keydown", onKey);
  return {
    el, title: c.name,
    cleanup: () => {
      alive = false;
      document.removeEventListener("keydown", onKey);
      // Stop downloading at once; the server stops the pull a minute after the last request.
      video.removeAttribute("src");
      video.load();
    },
  };
}

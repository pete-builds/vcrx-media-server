import { api, artUrl } from "../api.js";
import { h, s, clock, hours, when, bitrate, errorBox, toast } from "../dom.js";
import { state } from "../app.js";

// Activity, Tautulli style: now playing (refreshed every five seconds),
// history, stats and users. Admin only; the server enforces it too.
export async function activityView({ match, params }) {
  if (!state.me || !state.me.is_admin) {
    return { el: errorBox("Admins only", "Activity is only available to an admin account on the local network.") };
  }
  const tab = match[1] || "now";
  const tabs = h("nav", { class: "tabs", "aria-label": "Activity" },
    [["now", "Now Playing"], ["history", "History"], ["stats", "Stats"], ["users", "Users"]]
      .map(([k, label]) => h("a", { href: `#/activity/${k}`, class: k === tab ? "active" : "" }, label)));
  const body = h("div", {});
  const el = h("div", {}, h("h1", {}, "Activity"), tabs, body);
  const view = { now: nowTab, history: historyTab, stats: statsTab, users: usersTab }[tab];
  const cleanup = await view(body, params);
  return { el, title: "Activity", cleanup };
}

// Now Playing.

async function nowTab(body) {
  let timer = null;
  let editing = false;
  const draw = async () => {
    if (editing) return;
    try {
      const now = await api("/admin/activity/now");
      body.replaceChildren(
        h("div", { class: "tiles" },
          tile("Streams", now.stream_count),
          tile("Direct play", now.direct_play),
          tile("Converting", now.converting),
          tile("Bandwidth", bitrate(now.bandwidth_bps) || "0")),
        now.sessions.length ? h("div", { class: "sessions" }, now.sessions.map((x) => sessionCard(x, draw, (v) => { editing = v; })))
          : h("div", { class: "empty" }, "Nothing is playing right now."));
    } catch (e) {
      body.replaceChildren(errorBox("Could not load activity", e.message));
    }
  };
  await draw();
  timer = setInterval(draw, 5000);
  return () => clearInterval(timer);
}

// who names the principal; an invited device gets a tag and, while its
// invite still works, a Revoke button, so a friend's device can be cut off
// from the stream it is playing.
function who(x, redraw) {
  if (x.principal_kind !== "token") return x.username || "";
  const revoke = x.token_active && x.token_id ? h("button", { class: "btn btn-small btn-danger revoke-device", type: "button" }, "Revoke device") : null;
  if (revoke) {
    revoke.addEventListener("click", async () => {
      if (!confirm(`Revoke ${x.username || "this device"}? Its invite stops working and any stream it is playing stops now.`)) return;
      try {
        await api(`/admin/devices/${x.token_id}`, { method: "DELETE" });
        toast(`Revoked ${x.username || "the device"}`);
      } catch (e) {
        toast(e.message);
      }
      redraw();
    });
  }
  return h("span", { class: "who" }, x.username || "Invited device", " ", h("span", { class: "pill" }, "invited"), revoke ? " " : null, revoke);
}

function tile(k, v) {
  return h("div", { class: "tile" }, h("div", { class: "k" }, k), h("div", { class: "v" }, v));
}

function sessionCard(x, redraw, setEditing) {
  const posterId = x.show_id || x.item_id;
  const art = h("div", { class: "art" });
  const placeholder = () => art.append(h("div", { class: "placeholder" }, (x.item_title || "?").slice(0, 1)));
  if (posterId) {
    const img = h("img", { src: artUrl(posterId, "poster", 200), alt: "", loading: "lazy" });
    img.addEventListener("error", () => { img.remove(); placeholder(); }, { once: true });
    art.append(img);
  } else {
    placeholder();
  }
  const pct = x.progress != null ? Math.round(x.progress * 100) : null;
  const stopBtn = h("button", { class: "btn btn-small btn-danger", type: "button" }, "Stop stream");
  const form = h("form", { class: "stop-form", hidden: true },
    h("input", { name: "message", maxlength: "200", placeholder: "Message for the viewer (optional)", "aria-label": "Message for the viewer" }),
    h("button", { class: "btn btn-small btn-danger", type: "submit" }, "Stop"),
    h("button", { class: "btn btn-small", type: "button", onclick: () => { form.hidden = true; setEditing(false); } }, "Cancel"));
  stopBtn.addEventListener("click", () => { form.hidden = false; setEditing(true); form.elements.message.focus(); });
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      await api(`/admin/activity/sessions/${encodeURIComponent(x.id)}/terminate`, { method: "POST", body: { message: form.elements.message.value } });
      toast(x.username ? `Stopped ${x.username}’s stream` : "Stream stopped");
    } catch (err) {
      toast(err.message);
    }
    setEditing(false);
    redraw();
  });
  return h("article", { class: "session" },
    h("a", { class: "card", href: x.item_id ? `#/item/${x.item_id}` : "#/activity" }, art),
    h("div", {},
      h("div", { class: "s-title" }, x.item_title || "Unknown title"),
      h("div", { class: "s-line" }, x.principal_kind === "token" ? [who(x, redraw), " · "] : null,
        [x.principal_kind === "token" ? null : x.username, x.device_name || x.client, address(x.ip, x.location)].filter(Boolean).join(" · ")),
      h("div", { class: "s-line" }, [x.version_summary, bitrate(x.bitrate)].filter(Boolean).join(" · ")),
      h("div", { class: "bar", role: "progressbar", "aria-valuenow": pct ?? 0, "aria-valuemin": 0, "aria-valuemax": 100 },
        h("i", { style: { width: `${pct ?? 0}%` } })),
      h("div", { class: "s-foot" },
        h("span", {}, h("span", { class: `state ${x.state}` }, x.state), " · ", `${clock(x.position_ms)}${x.duration_ms ? " / " + clock(x.duration_ms) : ""}`),
        h("span", { class: `decision ${x.decision}`, title: x.decision_reason }, x.decision.replace(/_/g, " "))),
      h("div", { class: "s-foot" }, h("span", { class: "faint" }, `Started ${when(x.started_at)}`), stopBtn),
      form));
}

// History.

async function historyTab(body, params) {
  const PAGE = 50;
  const offset = Math.max(0, Number(params.get("offset")) || 0);
  const userId = params.get("user_id") || "";
  const q = new URLSearchParams({ limit: String(PAGE), offset: String(offset) });
  if (userId) q.set("user_id", userId);
  if (params.get("refused") === "1") q.set("include_refused", "true");
  const data = await api("/admin/activity/history?" + q);
  const link = (o) => {
    const p = new URLSearchParams(params);
    p.set("offset", String(o));
    return `#/activity/history?${p}`;
  };
  const rows = data.sessions.map((x) => h("tr", {},
    h("td", {}, when(x.started_at)),
    h("td", {}, who(x, () => historyTab(body, params))),
    h("td", {}, x.item_id ? h("a", { href: `#/item/${x.item_id}` }, x.item_title || "") : x.item_title || ""),
    h("td", {}, x.device_name || x.client || ""),
    h("td", {}, x.terminated_message ? h("span", { title: x.terminated_message }, "stopped by admin")
      : h("span", { class: `decision ${x.decision}`, title: x.decision_reason }, x.decision.replace(/_/g, " "))),
    h("td", { class: "num" }, hours(x.played_ms)),
    h("td", { class: "num" }, x.progress != null ? `${Math.round(x.progress * 100)}%` : ""),
    h("td", {}, address(x.ip, x.location))));
  // replaceChildren writes a null argument as the text "null"; h() skips it.
  body.replaceChildren(h("div", {},
    data.total ? h("table", { class: "list" },
      h("thead", {}, h("tr", {}, ["When", "User", "Title", "Device", "Playback", "Watched", "Reached", "Address"]
        .map((c, i) => h("th", { class: i === 5 || i === 6 ? "num" : "" }, c)))),
      h("tbody", {}, rows)) : h("div", { class: "empty" }, "No plays yet."),
    data.total > PAGE ? h("div", { class: "pager" },
      `${offset + 1} to ${Math.min(offset + PAGE, data.total)} of ${data.total}`,
      offset > 0 ? h("a", { class: "btn btn-small", href: link(Math.max(0, offset - PAGE)) }, "Newer") : null,
      offset + PAGE < data.total ? h("a", { class: "btn btn-small", href: link(offset + PAGE) }, "Older") : null) : null));
}

// Stats.

async function statsTab(body, params) {
  const days = Number(params.get("days")) || 30;
  const st = await api(`/admin/activity/stats?days=${days}`);
  const range = h("div", { class: "toolbar" },
    h("h2", {}, `Last ${days} days`),
    [7, 30, 90, 365].map((d) => h("a", { class: "btn btn-small" + (d === days ? " btn-primary" : ""), href: `#/activity/stats?days=${d}` }, `${d} days`)));
  const weekdays = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const titleLink = (r) => (r.id ? `#/item/${r.id}` : null);
  body.replaceChildren(range,
    h("div", { class: "tiles" },
      tile("Plays", st.plays),
      tile("Watch time", hours(st.watch_ms)),
      tile("People", st.users),
      h("div", { class: "tile", title: st.peak_at ? `Began ${when(st.peak_at)}` : "" },
        h("div", { class: "k" }, "Most at once"), h("div", { class: "v" }, st.peak_streams)),
      tile("Refused", st.refused)),
    h("div", { class: "panel", style: { marginBottom: "24px" } },
      h("h3", {}, "Plays per day"),
      barChart(st.by_day.map((d) => ({ label: d.date.slice(5), full: d.date, value: d.plays, note: hours(d.watch_ms) })),
        { every: Math.ceil(st.by_day.length / 10), heading: "Day", skipZeros: true })),
    h("div", { class: "panels" },
      sharePanel(st.playback_types),
      rankPanel("Platforms", st.platform_types),
      h("div", { class: "panel" }, h("h3", {}, "Plays by hour"),
        barChart(st.by_hour.map((v, i) => ({ label: String(i), full: `${i}:00 to ${i}:59`, value: v })), { every: 3, heading: "Hour" })),
      h("div", { class: "panel" }, h("h3", {}, "Plays by weekday"),
        barChart(st.by_weekday.map((v, i) => ({ label: weekdays[i], full: weekdays[i], value: v })), { every: 1, heading: "Day" })),
      rankPanel("Top movies", st.top_movies, titleLink, true),
      rankPanel("Top shows", st.top_shows, titleLink, true),
      rankPanel("Top people", st.top_users, (r) => (r.id ? `#/activity/history?user_id=${r.id}` : null)),
      rankPanel("Clients", st.platforms),
      rankPanel("Devices", st.devices)));
}

// address shows an IP with the kind of network it is on, worked out offline.
function address(ip, location) {
  if (!ip) return "";
  return location ? `${ip} (${location})` : ip;
}

const playbackNames = {
  direct_play: "Direct play",
  remux: "Converted: container only",
  audio_transcode: "Converted: audio",
  transcode: "Converted: video",
};

// sharePanel is Tautulli's stream type split: direct play against each kind of
// conversion, as a share of counted plays. A ranked list rather than a pie, so
// each row carries its own label and percentage and needs no legend.
function sharePanel(list) {
  const total = list.reduce((n, r) => n + r.plays, 0);
  const direct = list.filter((r) => r.name === "direct_play").reduce((n, r) => n + r.plays, 0);
  const pct = (n) => (total ? Math.round((100 * n) / total) : 0);
  return h("div", { class: "panel" }, h("h3", {}, "Direct play or converted"),
    total ? h("p", { class: "muted share-head" }, `${pct(direct)}% direct play, ${pct(total - direct)}% converted`) : null,
    list.length ? h("ol", { class: "rank" }, list.map((r) => h("li", {},
      h("span", {}, playbackNames[r.name] || r.name.replace(/_/g, " ")),
      h("span", { class: "muted" }, `${r.plays} ${r.plays === 1 ? "play" : "plays"} · ${pct(r.plays)}%`),
      h("div", { class: "meter", "aria-hidden": "true" }, h("i", { style: { width: `${pct(r.plays)}%` } })))))
      : h("p", { class: "muted" }, "Nothing yet."));
}

function rankPanel(title, list, href, people = false) {
  const max = Math.max(1, ...list.map((r) => r.plays));
  return h("div", { class: "panel" }, h("h3", {}, title),
    list.length ? h("ol", { class: "rank" }, list.map((r) => {
      const target = href ? href(r) : null;
      const who = people ? ` · ${r.users} ${r.users === 1 ? "person" : "people"}` : "";
      return h("li", {},
        target ? h("a", { href: target }, r.name) : h("span", {}, r.name),
        h("span", { class: "muted" }, `${r.plays} ${r.plays === 1 ? "play" : "plays"} · ${hours(r.watch_ms)}${who}`),
        h("div", { class: "meter", "aria-hidden": "true" }, h("i", { style: { width: `${(100 * r.plays) / max}%` } })));
    })) : h("p", { class: "muted" }, "Nothing yet."));
}

// barChart draws one series of vertical bars: rounded tops anchored to the
// baseline, a 2px gap between bars, a hover tooltip per bar, recessive axes.
// It draws at the container's real pixel width, so text never scales. A
// collapsed table under it holds the same numbers for screen readers, for
// anyone who cannot tell bar heights apart, and for exact values on a phone.
function barChart(points, { every = 1, height = 180, heading = "", skipZeros = false } = {}) {
  const box = h("div", { class: "chart-box" });
  const rows = skipZeros ? points.filter((p) => p.value > 0) : points;
  const table = h("details", { class: "chart-table" }, h("summary", {}, "Show as a table"),
    rows.length ? h("table", {},
      h("thead", {}, h("tr", {}, h("th", { scope: "col" }, heading), h("th", { scope: "col", class: "num" }, "Plays"),
        points.some((p) => p.note) ? h("th", { scope: "col", class: "num" }, "Watch time") : null)),
      h("tbody", {}, rows.map((p) => h("tr", {}, h("th", { scope: "row" }, p.full), h("td", { class: "num" }, p.value),
        p.note ? h("td", { class: "num" }, p.note) : null))))
      : h("p", { class: "muted" }, "No plays in this range."));
  let lastW = 0;
  const draw = () => {
    const W = Math.round(box.clientWidth);
    if (!W || W === lastW) return;
    lastW = W;
    box.replaceChildren(svgBars(points, W, height, every));
  };
  const ro = new ResizeObserver(draw);
  ro.observe(box);
  return h("div", {}, box, table);
}

function svgBars(points, W, H, every) {
  const padL = 30, padB = 22, padT = 8;
  const max = Math.max(1, ...points.map((p) => p.value));
  const nice = niceMax(max);
  const slot = (W - padL) / Math.max(1, points.length);
  const bw = Math.max(2, Math.min(40, slot - 2));
  // Skip labels when slots are too narrow to hold them.
  const step = Math.max(every, Math.ceil(36 / slot));
  const y = (v) => padT + (H - padT - padB) * (1 - v / nice);
  const grid = [0, nice / 2, nice].map((v) => [
    s("line", { class: "grid-line", x1: padL, x2: W, y1: y(v), y2: y(v) }),
    s("text", { class: "axis", x: padL - 8, y: y(v) + 4, "text-anchor": "end" }, Math.round(v)),
  ]);
  const bars = points.map((p, i) => {
    const x = padL + i * slot + (slot - bw) / 2;
    const top = y(p.value), base = y(0);
    const hgt = base - top;
    const r = Math.min(4, bw / 2, hgt);
    const d = hgt <= 0 ? "" : `M${x},${base} V${top + r} Q${x},${top} ${x + r},${top} H${x + bw - r} Q${x + bw},${top} ${x + bw},${top + r} V${base} Z`;
    // The hit target is the whole column, wider and taller than the bar.
    return s("g", {},
      s("title", {}, `${p.full}: ${p.value} ${p.value === 1 ? "play" : "plays"}${p.note ? " · " + p.note : ""}`),
      s("rect", { x: padL + i * slot, y: padT, width: slot, height: H - padT - padB, fill: "transparent" }),
      d ? s("path", { class: "bar", d }) : null,
      i % step === 0 ? s("text", { class: "axis", x: x + bw / 2, y: H - 6, "text-anchor": "middle" }, p.label) : null);
  });
  return s("svg", { class: "chart", width: W, height: H, viewBox: `0 0 ${W} ${H}`, role: "img",
    "aria-label": `${points.length} bars, highest ${Math.max(0, ...points.map((p) => p.value))}` }, grid, bars);
}

function niceMax(v) {
  if (v <= 4) return 4;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

// Users.

async function usersTab(body) {
  const data = await api("/admin/activity/users");
  body.replaceChildren(h("table", { class: "list" },
    h("thead", {}, h("tr", {}, ["User", "Plays", "Watch time", "Last seen", "Last played", "Device", "Address"]
      .map((c, i) => h("th", { class: i === 1 || i === 2 ? "num" : "" }, c)))),
    h("tbody", {}, data.users.map((u) => h("tr", {},
      h("td", {}, h("a", { href: `#/activity/history?user_id=${u.user_id}` }, u.username), u.is_admin ? h("span", { class: "faint" }, " admin") : null),
      h("td", { class: "num" }, u.plays),
      h("td", { class: "num" }, hours(u.watch_ms)),
      h("td", {}, u.last_seen ? when(u.last_seen) : "Never"),
      h("td", {}, u.last_title || ""),
      h("td", {}, u.last_device || ""),
      h("td", {}, address(u.last_ip, u.last_location)))))));
}

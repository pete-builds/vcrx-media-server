import { api, artUrl } from "../api.js";
import { h, icon, runtime, episodeCode, fadeImg, placeholderArt, card } from "../dom.js";
import { go } from "../app.js";

// A friend's title (milestone 9, item 3): #/peer/<peer>/item/<id>. The page
// comes from the friend's server through this one, which checked every field
// and applied this household's own policy to it. Playing streams it from the
// friend's server; nothing is copied here.

// sourceBadge says where a friend's title lives: "on Mark's server, 4K".
export function sourceBadge(peerName, resolution) {
  return `on ${peerName}${resolution ? ", " + resolution : ""}`;
}

export async function peerItemView({ match }) {
  const peerId = Number(match[1]);
  const id = Number(match[2]);
  const base = `/peers/${peerId}`;
  const item = await api(`${base}/items/${id}`);
  const peer = item.peer || { name: "a friend's server" };
  const playable = item.kind === "movie" || item.kind === "episode";
  const children = playable ? null : await api(`${base}/items/${id}/children`);

  const backdrop = h("div", { class: "backdrop" });
  if (item.has_backdrop) backdrop.append(fadeImg(artUrl(item.id, "backdrop", 1920, base), { lazy: false }));
  else if (item.has_poster) {
    backdrop.classList.add("from-poster");
    backdrop.append(fadeImg(artUrl(item.id, "poster", 320, base) + "&overlays=0", { lazy: false }));
  }
  const art = h("div", { class: "art" });
  if (item.has_poster) art.append(fadeImg(artUrl(item.id, "poster", 480, base), { lazy: false, onFail: () => art.append(placeholderArt(item.title)) }));
  else art.append(placeholderArt(item.kind === "episode" ? episodeCode(item) || item.title : item.title));

  const versions = item.versions || [];
  const best = versions.reduce((b, v) => (!b || rank(v.resolution) > rank(b.resolution) ? v : b), null);
  const chips = [];
  if (item.year) chips.push(h("span", {}, String(item.year)));
  if (item.runtime_ms) chips.push(h("span", {}, runtime(item.runtime_ms)));
  if (item.content_rating) chips.push(h("span", { class: "chip" }, item.content_rating));
  const res = (best && best.resolution) || item.resolution;
  if (res) chips.push(h("span", { class: "chip" }, res));

  const actions = h("div", { class: "actions" });
  if (playable) {
    let versionSel = null;
    if (versions.length > 1) {
      versionSel = h("select", { "aria-label": "Version" }, versions.map((v, i) =>
        h("option", { value: String(v.id), selected: v === best }, [v.resolution, v.video_codec, v.label ? `(${v.label})` : ""].filter(Boolean).join(" ") || `Version ${i + 1}`)));
    }
    const play = () => go(`/peer/${peerId}/play/${item.id}?start=0${versionSel ? "&version=" + versionSel.value : ""}`);
    actions.append(h("button", { class: "btn btn-primary btn-large", type: "button", onclick: play }, icon("play"), "Play"));
    if (versionSel) actions.append(versionSel);
  }

  const crumbs = [];
  if (item.show_id && item.kind !== "show") crumbs.push(h("a", { href: `#/peer/${peerId}/item/${item.show_id}` }, item.show_title || "Show"));
  const eyebrow = item.kind === "episode" && item.episode_number != null ? `Season ${item.season_number} · Episode ${item.episode_number}` : "";
  const heroEl = h("section", { class: "hero", "data-kind": item.kind }, backdrop,
    h("div", { class: "inner" }, h("div", { class: "poster card" }, art),
      h("div", { class: "info" },
        crumbs.length ? h("nav", { class: "crumbs", "aria-label": "Breadcrumb" }, crumbs) : null,
        h("p", { class: "eyebrow source-line" }, sourceBadge(peer.name, res)),
        eyebrow ? h("p", { class: "eyebrow" }, eyebrow) : null,
        h("h1", {}, item.title),
        chips.length ? h("div", { class: "chips" }, chips) : null,
        (item.genres || []).length ? h("p", { class: "genres" }, item.genres.slice(0, 4).join(" · ")) : null,
        item.tagline ? h("p", { class: "tagline" }, item.tagline) : null,
        item.overview ? h("p", { class: "overview" }, item.overview) : null,
        h("p", { class: "faint" }, `Streams from ${peer.name}. Nothing is copied to this server.`),
        actions)));

  let list = null;
  if (children) {
    const items = children.items || [];
    const landscape = item.kind === "season";
    list = h("section", { class: "section" }, h("h2", {}, item.kind === "show" ? "Seasons" : "Episodes"),
      items.length ? h("div", { class: "grid" + (landscape ? " wide" : "") }, items.map((c) =>
        card(c, { landscape, base, href: c.kind === "episode" ? `#/peer/${peerId}/play/${c.id}?start=0` : `#/peer/${peerId}/item/${c.id}` })))
        : h("p", { class: "muted" }, "Nothing here that you can see."));
  }
  return { el: h("div", {}, heroEl, list), title: item.title, hero: true };
}

const RES = ["SD", "480p", "576p", "720p", "1080p", "4K", "8K"];
function rank(r) {
  return r ? RES.indexOf(r) : -1;
}

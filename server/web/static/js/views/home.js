import { api, artUrl } from "../api.js";
import { h, card, icon, toast, fadeImg, runtime, episodeCode } from "../dom.js";
import { go } from "../app.js";
import { sourceBadge } from "./peer.js";

// Home shows a featured title, then the server's rows in the order the user
// chose (Customize Home): Continue Watching, recently added, seasonal picks,
// trending, top rated and the rest. The client draws whatever rows come. Friends' servers' titles
// come in after Continue Watching once they arrive.
export async function homeView() {
  const data = await api("/home");
  const rows = (data.rows || []).filter((r) => r.items && r.items.length);
  const customize = h("p", { class: "home-foot" }, h("a", { class: "btn btn-small", href: "#/settings/home" }, "Customize Home"));
  if (!rows.length) {
    const empty = h("div", { class: "empty" }, "Nothing here yet. Once a library has been scanned, its titles show up here.");
    const list = h("div", { class: "rows" }, empty);
    friendsRow(list, () => empty.remove());
    return { el: h("div", { class: "home" }, list, customize), title: "Home" };
  }
  const featured = await feature(rows);
  const el = h("div", { class: "home" + (featured ? " with-feature" : "") }, featured, h("div", { class: "rows" }, rows.map((row) => {
    const continuing = row.id === "continue_watching";
    const landscape = continuing || row.items.every((i) => i.kind === "episode");
    const section = h("section", { class: "row", "data-row": row.id, "aria-label": row.title },
      h("div", { class: "row-head" }, h("h2", {}, row.title), seeAll(row)));
    const strip = h("div", { class: "strip" + (landscape ? " wide" : "") },
      row.items.map((i) => (continuing ? continueCard(i, section) : card(i, { landscape }))));
    section.append(scroller(strip, row.title));
    return section;
  })), customize);
  friendsRow(el.querySelector(".rows"));
  return { el, title: "Home", hero: !!featured };
}

const PEER_STATES = { offline: "offline", checking: "not answering yet", bad_answer: "answering badly", unlinked: "ended the link",
  token_unreadable: "linked, but its token cannot be read here" };

// friendsRow adds what linked friends' servers share: their newest titles in
// one row, each badged with the server it lives on ("on Mark's server, 4K"),
// and a note for any server that is down. It loads after the local rows, so
// a slow friend never holds Home up, and a failure only leaves it out.
async function friendsRow(rowsEl, onShown) {
  let data;
  try {
    data = await api("/peers/home");
  } catch {
    return;
  }
  const peers = data.peers || [];
  const items = data.items || [];
  if (!peers.length || !rowsEl.isConnected) return;
  const title = "From friends' servers";
  const section = h("section", { class: "row friends-row", "data-row": "friends", "aria-label": title },
    h("div", { class: "row-head" }, h("h2", {}, title)));
  const down = peers.filter((p) => p.state !== "online");
  if (down.length) {
    section.append(h("div", { class: "peer-states" }, down.map((p) =>
      h("span", { class: `pill peer-state ${p.state}` }, `${p.name} is ${PEER_STATES[p.state] || p.state}`))));
  }
  if (items.length) {
    const strip = h("div", { class: "strip" }, items.map((i) => card(i, { href: `#/peer/${i.peer_id}/item/${i.id}`,
      base: `/peers/${i.peer_id}`, badge: sourceBadge(i.peer_name, i.resolution) })));
    section.append(scroller(strip, title));
  } else {
    section.append(h("p", { class: "muted" }, "Nothing from friends' servers right now."));
  }
  // After Continue Watching, else first.
  const cw = rowsEl.querySelector('[data-row="continue_watching"]');
  rowsEl.insertBefore(section, cw ? cw.nextSibling : rowsEl.firstChild);
  if (onShown) onShown();
}

// seeAll links a row to its full list: a library's own page for Recently
// Added (it pages and filters), the row's grid for the rest.
function seeAll(row) {
  if (!row.see_all) return null;
  const label = `See all ${row.title}`;
  if (row.library_id && row.kind && row.kind.startsWith("recently_added")) {
    return h("a", { href: `#/library/${row.library_id}?sort=added`, "aria-label": label }, "See all");
  }
  return h("a", { href: `#/row/${encodeURIComponent(row.id)}`, "aria-label": label }, "See all");
}

// feature is the large title at the top of Home, like a streaming service's:
// one movie or show from the recently added rows, preferring ones with
// backdrop art. The pick changes once a day rather than on every visit, so
// Home does not reshuffle under someone coming back to it.
async function feature(rows) {
  const pool = [];
  // Recently added first, as before Home had more rows; then the others.
  const recent = (r) => !!r.kind && r.kind.startsWith("recently_added");
  for (const row of [...rows.filter(recent), ...rows.filter((r) => !recent(r))]) {
    if (row.id === "continue_watching") continue;
    for (const i of row.items) {
      if ((i.kind === "movie" || i.kind === "show") && !pool.some((p) => p.id === i.id)) pool.push(i);
    }
  }
  const withArt = pool.filter((i) => i.has_backdrop);
  const choices = (withArt.length ? withArt : pool).slice(0, 8);
  if (!choices.length) return null;
  const pick = choices[Math.floor(Date.now() / 86400000) % choices.length];
  // The summary has no plot or rating; the details do. Without them the
  // feature still shows, with what the summary has.
  const item = await api(`/items/${pick.id}`).catch(() => pick);

  const art = h("div", { class: "feature-art" });
  if (item.has_backdrop) {
    art.append(fadeImg(artUrl(item.id, "backdrop", 1920), { lazy: false,
      srcset: `${artUrl(item.id, "backdrop", 960)} 960w, ${artUrl(item.id, "backdrop", 1920)} 1920w`, sizes: "100vw" }));
  } else if (item.has_poster) {
    // No backdrop: the poster, blurred into a wash of its colours.
    art.classList.add("from-poster");
    art.append(fadeImg(artUrl(item.id, "poster", 320) + "&overlays=0", { lazy: false }));
  }

  const chips = [];
  if (item.year) chips.push(h("span", {}, String(item.year)));
  if (item.content_rating) chips.push(h("span", { class: "chip" }, item.content_rating));
  if (item.runtime_ms) chips.push(h("span", {}, runtime(item.runtime_ms)));
  if (item.kind === "show" && item.child_count) chips.push(h("span", {}, `${item.child_count} season${item.child_count === 1 ? "" : "s"}`));
  if (item.resolution) chips.push(h("span", { class: "chip" }, item.resolution));
  const score = item.ratings && (item.ratings.imdb || item.ratings.tmdb);
  if (score) chips.push(h("span", { class: "score" }, icon("star"), score.toFixed(1)));

  const buttons = h("div", { class: "actions" });
  const w = item.watch || {};
  if (item.kind === "movie") {
    const resume = w.position_ms > 0 && !w.played;
    buttons.append(h("button", { class: "btn btn-primary btn-large", type: "button", onclick: () => go(resume ? `/play/${item.id}` : `/play/${item.id}?start=0`) },
      icon("play"), resume ? "Resume" : "Play"));
  } else if (item.next_episode) {
    const n = item.next_episode;
    buttons.append(h("button", { class: "btn btn-primary btn-large", type: "button", onclick: () => go(`/play/${n.id}`) },
      icon("play"), `${n.watch && n.watch.position_ms > 0 ? "Resume" : "Play"} ${episodeCode(n)}`.trim()));
  }
  buttons.append(h("a", { class: "btn btn-glass btn-large", href: `#/item/${item.id}` }, icon("info"), "More info"));

  const overview = item.overview || item.tagline;
  return h("section", { class: "feature", "aria-label": `Featured: ${item.title}` }, art,
    h("div", { class: "feature-body" },
      h("p", { class: "eyebrow" }, item.kind === "show" ? "Featured show" : "Featured movie"),
      h("h1", { class: "feature-title" }, item.title),
      chips.length ? h("div", { class: "chips" }, chips) : null,
      overview ? h("p", { class: "feature-overview" }, overview) : null,
      buttons));
}

// scroller wraps a row's strip with Back and Next buttons for mouse users;
// touch screens swipe and keyboards tab through the cards. The buttons hide at
// either end and when everything fits.
function scroller(strip, label) {
  const prev = h("button", { class: "row-arrow prev", type: "button", "aria-label": `Scroll ${label} back` }, icon("left"));
  const next = h("button", { class: "row-arrow next", type: "button", "aria-label": `Scroll ${label} forward` }, icon("right"));
  const reduce = window.matchMedia("(prefers-reduced-motion: reduce)");
  const by = (dir) => strip.scrollBy({ left: dir * Math.max(200, strip.clientWidth * 0.85), behavior: reduce.matches ? "auto" : "smooth" });
  prev.addEventListener("click", () => by(-1));
  next.addEventListener("click", () => by(1));
  const update = () => {
    const max = strip.scrollWidth - strip.clientWidth;
    prev.hidden = strip.scrollLeft <= 2;
    next.hidden = strip.scrollLeft >= max - 2;
  };
  strip.addEventListener("scroll", update, { passive: true });
  // Sizes are known only once the row is on the page, and change with the window.
  new ResizeObserver(update).observe(strip);
  return h("div", { class: "scroller" }, prev, strip, next);
}

// continueCard is a Continue Watching card with a Remove button beside the
// link, not inside it, so pressing it never opens the title.
function continueCard(item, section) {
  const label = item.kind === "episode" && item.show_title ? `${item.show_title}: ${item.title}` : item.title;
  const remove = h("button", { class: "cw-remove", type: "button", title: "Remove from Continue Watching",
    "aria-label": `Remove ${label} from Continue Watching` }, icon("close"), h("span", {}, "Remove"));
  const wrap = h("div", { class: "cw-card" }, card(item, { landscape: true }), remove);
  remove.addEventListener("click", async () => {
    remove.disabled = true;
    try {
      await api(`/items/${item.id}/progress`, { method: "DELETE" });
      // Keep keyboard focus in the row: on the next card's Remove, else the previous one.
      const next = wrap.nextElementSibling || wrap.previousElementSibling;
      wrap.remove();
      if (next) next.querySelector(".cw-remove").focus();
      else section.remove();
      toast(`Removed ${label} from Continue Watching`);
    } catch (e) {
      toast(e.message);
      remove.disabled = false;
    }
  });
  return wrap;
}

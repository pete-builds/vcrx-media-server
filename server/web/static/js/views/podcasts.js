// Podcast pages: the shows the user subscribes to, with new episodes across
// them, adding a show by its feed URL (or by search, when the server has it
// on), OPML import and export; and a show's page with its episodes. Playing
// an episode hands it to the player in music/player.js, whose bar becomes a
// podcast player (skips, speed, sleep timer) and saves the place as it goes.
//
// Every image and every byte of audio comes from VCRX, never from the
// podcast's host: the server fetched the artwork and passes the audio on.

import { api } from "../api.js";
import { h, clock, fadeImg, toast } from "../dom.js";
import { state, go } from "../app.js";
import { micon } from "../music/icons.js";
import * as player from "../music/player.js";

const PAGE = 50;

const showArtUrl = (id) => `/api/v1/podcasts/shows/${id}/artwork`;

// showArt is a show's artwork, or its title on a dark gradient.
function showArt(p, cls = "") {
  const art = h("div", { class: "art music-art podcast-art " + cls });
  const placeholder = () => art.prepend(h("div", { class: "placeholder" }, h("span", {}, p.title || "")));
  if (p.has_artwork) art.append(fadeImg(showArtUrl(p.id), { onFail: placeholder }));
  else placeholder();
  return art;
}

function showCard(p) {
  const art = showArt(p);
  if (p.unplayed_count > 0) {
    art.append(h("span", { class: "badge podcast-unplayed", title: `${p.unplayed_count} unplayed` }, String(p.unplayed_count)));
  }
  return h("a", { class: "card music-card podcast-card", href: `#/podcasts/show/${p.id}`, title: p.title, "data-podcast": String(p.id) },
    art, h("div", { class: "title" }, p.title), h("div", { class: "sub" }, p.author || ""));
}

// published is an episode's date: "Nov 3", or with the year when not this one.
function published(ms) {
  if (!ms) return "";
  const d = new Date(ms);
  return d.toLocaleDateString([], { month: "short", day: "numeric", year: d.getFullYear() === new Date().getFullYear() ? undefined : "numeric" });
}

function length(s) {
  if (!s) return "";
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`;
}

// episodeRow is one episode: play, title and facts, the place reached, and
// mark played. withShow adds the show's name, for lists across shows.
function episodeRow(ep, { withShow = false, onChange = () => {} } = {}) {
  const li = h("li", { class: "episode", "data-episode": String(ep.id) });
  const draw = () => {
    const facts = [published(ep.published_at), length(ep.duration_seconds)];
    if (ep.season != null && ep.episode != null) facts.push(`S${ep.season} E${ep.episode}`);
    else if (ep.episode != null) facts.push(`Episode ${ep.episode}`);
    if (ep.episode_type && ep.episode_type !== "full") facts.push(ep.episode_type === "trailer" ? "Trailer" : "Bonus");
    if (ep.explicit) facts.push("Explicit");
    const started = !ep.played && ep.position_seconds > 0;
    const mark = ep.played ? h("span", { class: "episode-state is-played" }, micon("check"), "Played")
      : started ? h("span", { class: "episode-state" }, `${clock(ep.position_seconds * 1000)} in`)
        : ep.new ? h("span", { class: "episode-state is-new" }, "New") : null;
    const bar = started && ep.duration_seconds
      ? h("div", { class: "progress episode-progress" }, h("i", { style: { width: `${Math.min(100, (100 * ep.position_seconds) / ep.duration_seconds)}%` } }))
      : null;
    const toggle = async () => {
      try {
        const p = await api(`/podcasts/episodes/${ep.id}/played`, { method: "PUT", body: { played: !ep.played } });
        ep.played = p.played;
        ep.position_seconds = p.position_seconds;
        ep.new = false;
        draw();
        onChange();
      } catch (e) {
        toast(e.message || "Could not save that.");
      }
    };
    li.replaceChildren(
      h("button", { type: "button", class: "mb-btn episode-play", "aria-label": `Play ${ep.title}`, title: "Play", onclick: () => player.playEpisode(ep) }, micon("play")),
      h("div", { class: "episode-body" },
        withShow ? h("a", { class: "episode-show", href: `#/podcasts/show/${ep.podcast_id}` }, ep.podcast_title) : null,
        h("div", { class: "episode-title" }, ep.title),
        h("div", { class: "episode-facts muted" }, facts.filter(Boolean).join(" · "), mark ? " · " : null, mark),
        bar,
        ep.description ? h("p", { class: "episode-desc" }, ep.description) : null),
      h("button", { type: "button", class: "btn btn-small episode-mark", onclick: toggle }, ep.played ? "Mark unplayed" : "Mark played"));
  };
  draw();
  li.update = (changes) => {
    Object.assign(ep, changes);
    draw();
  };
  return li;
}

// follow lights the episode playing in list, and updates a row's place each
// time the player saves one. It returns the cleanup.
function follow(list) {
  const mark = () => {
    const cur = player.currentEpisode();
    for (const li of list.querySelectorAll("li.episode")) {
      const on = !!cur && li.dataset.episode === String(cur.id);
      li.classList.toggle("is-current", on);
      li.classList.toggle("is-playing", on && player.isPlaying());
    }
  };
  mark();
  let seen = player.debug.saves.length;
  const stop = player.onChange(() => {
    mark();
    while (seen < player.debug.saves.length) {
      const s = player.debug.saves[seen++];
      const li = s.episode != null && list.querySelector(`li.episode[data-episode="${s.episode}"]`);
      if (li && li.update) li.update({ position_seconds: s.finished ? 0 : s.position_s, played: s.finished, new: false });
    }
  });
  return stop;
}

// subscribe adds a show by its feed URL and opens it.
async function subscribe(url, button) {
  if (button) button.disabled = true;
  try {
    const r = await api("/podcasts", { method: "POST", body: { url } });
    go(`/podcasts/show/${r.podcast.id}`);
  } catch (e) {
    toast(e.message || "Could not add that show.");
  } finally {
    if (button) button.disabled = false;
  }
}

function addForm() {
  const input = h("input", { type: "url", name: "url", required: true, placeholder: "https://example.com/feed.xml", "aria-label": "Feed URL",
    autocomplete: "off", spellcheck: "false" });
  const button = h("button", { class: "btn btn-primary", type: "submit" }, "Subscribe");
  return h("form", { class: "podcast-add", onsubmit: (e) => { e.preventDefault(); subscribe(input.value.trim(), button); } },
    h("label", {}, "Add a show by its feed URL"), h("div", { class: "podcast-add-row" }, input, button));
}

function searchForm() {
  const input = h("input", { type: "search", name: "q", required: true, maxlength: "200", placeholder: "Search for a show", "aria-label": "Search for a show" });
  const results = h("ul", { class: "podcast-results" });
  const button = h("button", { class: "btn", type: "submit" }, "Search");
  const run = async (e) => {
    e.preventDefault();
    button.disabled = true;
    try {
      const r = await api(`/podcasts/search?q=${encodeURIComponent(input.value.trim())}`);
      results.replaceChildren(...(r.results.length ? r.results.map((x) => h("li", {},
        h("div", { class: "podcast-result" }, h("strong", {}, x.title), h("span", { class: "muted" }, [x.author, x.genre].filter(Boolean).join(" · "))),
        h("button", { class: "btn btn-small", type: "button", onclick: (ev) => subscribe(x.feed_url, ev.currentTarget) }, "Subscribe")))
        : [h("li", { class: "muted" }, "No shows found.")]));
    } catch (err) {
      toast(err.message || "Search failed.");
    } finally {
      button.disabled = false;
    }
  };
  return h("div", { class: "podcast-search" },
    h("form", { class: "podcast-add-row", onsubmit: run }, input, button),
    h("p", { class: "muted small" }, "Search asks Apple's podcast directory; only the words you type are sent."),
    results);
}

function opmlTools() {
  const file = h("input", { type: "file", accept: ".opml,.xml,text/xml,text/x-opml,application/xml", hidden: true });
  file.addEventListener("change", async () => {
    const f = file.files && file.files[0];
    if (!f) return;
    try {
      const r = await api("/podcasts/opml", { method: "POST", raw: { type: "text/x-opml", data: await f.text() } });
      const failed = r.failed.length ? `, ${r.failed.length} could not be added` : "";
      toast(`Added ${r.added} ${r.added === 1 ? "show" : "shows"}${r.already ? `, ${r.already} already here` : ""}${failed}.`);
      go("/podcasts");
    } catch (e) {
      toast(e.message || "Could not import that file.");
    } finally {
      file.value = "";
    }
  });
  return h("div", { class: "podcast-opml" },
    h("a", { class: "btn btn-small", href: "/api/v1/podcasts/opml", download: "vcrx-podcasts.opml" }, "Export OPML"),
    h("button", { class: "btn btn-small", type: "button", onclick: () => file.click() }, "Import OPML"), file);
}

// podcastsView is the Podcasts page.
export async function podcastsView() {
  const [subs, fresh] = await Promise.all([api("/podcasts"), api("/podcasts/new?limit=20")]);
  const shows = subs.podcasts || [];
  const newList = h("ol", { class: "episodes new-episodes" }, (fresh.episodes || []).map((ep) => episodeRow(ep, { withShow: true })));
  const stop = follow(newList);
  const el = h("div", { class: "music-page podcasts-page" },
    h("div", { class: "toolbar" }, h("h1", {}, "Podcasts"),
      h("span", { class: "count" }, `${shows.length} ${shows.length === 1 ? "show" : "shows"}`)),
    h("div", { class: "podcast-tools" }, addForm(), state.me && state.me.podcast_search ? searchForm() : null, opmlTools()),
    fresh.episodes && fresh.episodes.length ? [h("h2", { class: "podcast-head" }, "New episodes"), newList] : null,
    h("h2", { class: "podcast-head" }, "Your shows"),
    shows.length ? h("div", { class: "grid music-grid podcast-grid" }, shows.map(showCard))
      : h("div", { class: "empty" }, "No shows yet. Add one by its feed URL."));
  return { el, title: "Podcasts", cleanup: stop };
}

// podcastShowView is a show: artwork, description, and its episodes newest
// first, a page at a time.
export async function podcastShowView({ match }) {
  const id = Number(match[1]);
  const first = await api(`/podcasts/shows/${id}?limit=${PAGE}`);
  let p = first.podcast;
  const list = h("ol", { class: "episodes" });
  const counts = h("p", { class: "muted" });
  const drawCounts = () => {
    counts.textContent = [`${p.episode_count} ${p.episode_count === 1 ? "episode" : "episodes"}`,
      p.unplayed_count ? `${p.unplayed_count} unplayed` : "all played"].join(" · ");
  };
  drawCounts();
  const recount = async () => {
    try {
      p = (await api(`/podcasts/shows/${id}?limit=1`)).podcast;
      drawCounts();
    } catch { /* the counts catch up next time */ }
  };
  const add = (eps) => list.append(...eps.map((ep) => episodeRow(ep, { onChange: recount })));
  add(first.episodes);
  let loaded = first.episodes.length;
  const moreBtn = h("button", { class: "btn", type: "button" }, "Show more");
  const more = h("div", { class: "more" }, moreBtn);
  more.hidden = loaded >= first.total;
  moreBtn.addEventListener("click", async () => {
    moreBtn.disabled = true;
    try {
      const page = await api(`/podcasts/shows/${id}?limit=${PAGE}&offset=${loaded}`);
      add(page.episodes);
      loaded += page.episodes.length;
      more.hidden = loaded >= page.total || page.episodes.length === 0;
    } finally {
      moreBtn.disabled = false;
    }
  });
  const stop = follow(list);

  const status = h("p", { class: "muted podcast-error" });
  const drawStatus = () => {
    status.textContent = p.last_error ? `The last refresh failed: ${p.last_error}` : "";
    status.hidden = !p.last_error;
  };
  drawStatus();
  const refresh = async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try {
      p = (await api(`/podcasts/shows/${id}/refresh`, { method: "POST" })).podcast;
      if (!p.last_error) go(`/podcasts/show/${id}`);
      else drawStatus();
    } catch (err) {
      toast(err.message || "Could not refresh.");
    } finally {
      b.disabled = false;
    }
  };
  const unsubscribe = async () => {
    if (!confirm(`Unsubscribe from ${p.title}? Your places in its episodes are forgotten.`)) return;
    try {
      await api(`/podcasts/shows/${id}`, { method: "DELETE" });
      const cur = player.currentEpisode();
      if (cur && cur.podcast_id === id) player.clear();
      go("/podcasts");
    } catch (e) {
      toast(e.message || "Could not unsubscribe.");
    }
  };
  const latest = first.episodes.find((ep) => !ep.played) || first.episodes[0];
  const el = h("div", { class: "music-page podcast-page" },
    h("header", { class: "music-head" },
      showArt(p),
      h("div", { class: "music-info" },
        h("div", { class: "kicker" }, "Podcast"),
        h("h1", {}, p.title),
        p.author ? h("p", { class: "music-by" }, p.author) : null,
        counts,
        status,
        h("div", { class: "actions" },
          latest ? h("button", { class: "btn btn-primary", type: "button", onclick: () => player.playEpisode(latest) }, micon("play"),
            latest.position_seconds > 0 && !latest.played ? "Resume latest" : "Play latest") : null,
          h("button", { class: "btn", type: "button", onclick: refresh }, "Refresh"),
          h("button", { class: "btn", type: "button", onclick: unsubscribe }, "Unsubscribe")))),
    p.description ? h("p", { class: "book-description podcast-description" }, p.description) : null,
    h("h2", { class: "podcast-head" }, "Episodes"),
    first.episodes.length ? list : h("div", { class: "empty" }, "This feed lists no episodes yet."),
    more);
  return { el, title: p.title, cleanup: stop };
}

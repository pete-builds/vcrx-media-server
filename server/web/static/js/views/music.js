// Music pages: a library's artists, albums and live shows, an artist, and an
// album with its tracklist. Playing anything hands a queue to the player in
// music/player.js, which keeps going while these pages come and go.

import { api } from "../api.js";
import { h, clock, runtime, fadeImg, toast } from "../dom.js";
import { state, go } from "../app.js";
import { micon } from "../music/icons.js";
import * as player from "../music/player.js";

const PAGE = 120;

export const coverUrl = (albumId, w) => `/api/v1/music/albums/${albumId}/cover?width=${w}`;
const artistUrl = (artistId, w) => `/api/v1/music/artists/${artistId}/image?width=${w}`;

// squareArt is a cover (or an artist's picture), or the title on a dark
// gradient when there is none.
function squareArt(src, title, { round = false } = {}) {
  const art = h("div", { class: "art music-art" + (round ? " round" : "") });
  const placeholder = () => art.prepend(h("div", { class: "placeholder" }, h("span", {}, title || "")));
  if (src) art.append(fadeImg(src, { onFail: placeholder }));
  else placeholder();
  return art;
}

// albumLine says what an album is in a few words: the year, or for a live
// show its date and venue.
export function albumLine(a, { artist = true } = {}) {
  const parts = [];
  if (artist && a.artist_name) parts.push(a.artist_name);
  if (a.live) {
    if (a.date) parts.push(a.date);
    if (a.venue) parts.push(a.venue);
  } else if (a.year) {
    parts.push(String(a.year));
  }
  return parts.join(" · ");
}

export function albumCard(a, opts) {
  const sub = albumLine(a, opts);
  return h("a", { class: "card music-card", href: `#/music/album/${a.id}`, title: a.title },
    squareArt(a.has_cover ? coverUrl(a.id, 320) : null, a.title),
    a.live ? h("span", { class: "badge live-badge", title: "Live recording" }, a.source || "Live") : null,
    h("div", { class: "title" }, a.title), sub ? h("div", { class: "sub" }, sub) : null);
}

export function artistCard(a) {
  const n = a.album_count || 0;
  return h("a", { class: "card music-card artist-card", href: `#/music/artist/${a.id}`, title: a.name },
    squareArt(a.has_image ? artistUrl(a.id, 320) : null, a.name, { round: true }),
    h("div", { class: "title" }, a.name), h("div", { class: "sub" }, `${n} ${n === 1 ? "album" : "albums"}`));
}

const playing = (fn) => async () => {
  try {
    await fn();
  } catch (e) {
    toast(e.message || "Could not start the music.");
  }
};

// musicLibraryView is a music library: artists, albums or live shows.
export async function musicLibraryView({ match, params }) {
  const id = Number(match[1]);
  const lib = state.libraries.find((l) => l.id === id);
  const tab = ["artists", "albums", "live"].includes(params.get("view")) ? params.get("view") : "artists";
  const sort = params.get("sort") || (tab === "live" ? "year" : "title");
  const query = (offset) => {
    if (tab === "artists") return `/music/libraries/${id}/artists?limit=${PAGE}&offset=${offset}`;
    const q = new URLSearchParams({ sort, limit: String(PAGE), offset: String(offset) });
    if (tab === "live") q.set("live", "true");
    return `/music/libraries/${id}/albums?${q}`;
  };
  const first = await api(query(0));
  const list = (page) => (tab === "artists" ? page.artists.map(artistCard) : page.albums.map((a) => albumCard(a)));
  const grid = h("div", { class: "grid music-grid" }, list(first));
  let loaded = grid.children.length;
  const moreBtn = h("button", { class: "btn", type: "button" }, "Show more");
  const more = h("div", { class: "more" }, moreBtn);
  more.hidden = loaded >= first.total;
  moreBtn.addEventListener("click", async () => {
    moreBtn.disabled = true;
    try {
      const page = await api(query(loaded));
      const cards = list(page);
      grid.append(...cards);
      loaded += cards.length;
      more.hidden = loaded >= page.total || cards.length === 0;
    } finally {
      moreBtn.disabled = false;
    }
  });
  const noun = { artists: ["artist", "artists"], albums: ["album", "albums"], live: ["show", "shows"] }[tab];
  const tabs = h("nav", { class: "tabs", "aria-label": "Music" },
    [["artists", "Artists"], ["albums", "Albums"], ["live", "Live"]].map(([k, label]) =>
      h("a", { href: `#/music/${id}?view=${k}`, class: k === tab ? "active" : "", "aria-current": k === tab ? "page" : null }, label)));
  const sortSel = tab === "artists" ? null : h("select", { "aria-label": "Sort", onchange: (e) => go(`/music/${id}?view=${tab}&sort=${e.target.value}`) },
    [["title", "Title"], ["artist", "Artist"], ["year", tab === "live" ? "Newest show" : "Newest"], ["added", "Recently added"]]
      .map(([v, label]) => h("option", { value: v, selected: v === sort }, label)));
  const el = h("div", { class: "music-page" },
    h("div", { class: "toolbar" }, h("h1", {}, lib ? lib.name : "Music"),
      h("span", { class: "count" }, `${first.total} ${first.total === 1 ? noun[0] : noun[1]}`), sortSel),
    tabs,
    first.total ? grid : h("div", { class: "empty" }, tab === "live" ? "No live recordings here yet." : "Nothing here yet. Scan the library from Settings."),
    more);
  return { el, title: lib ? lib.name : "Music" };
}

// musicArtistView is an artist: their albums, live shows and appearances.
export async function musicArtistView({ match }) {
  const a = await api(`/music/artists/${match[1]}`);
  const section = (label, albums) => (albums.length ? h("section", { class: "row" },
    h("div", { class: "row-head" }, h("h2", {}, label)),
    h("div", { class: "grid music-grid" }, albums.map((x) => albumCard(x, { artist: label === "Appears on" })))) : null);
  const counts = [`${a.album_count} ${a.album_count === 1 ? "album" : "albums"}`, `${a.track_count} ${a.track_count === 1 ? "track" : "tracks"}`];
  const el = h("div", { class: "music-page" },
    h("header", { class: "music-head" },
      squareArt(a.has_image ? artistUrl(a.id, 480) : null, a.name, { round: true }),
      h("div", { class: "music-info" },
        h("div", { class: "kicker" }, "Artist"),
        h("h1", {}, a.name),
        h("p", { class: "muted" }, counts.join(" · ")),
        h("div", { class: "actions" },
          h("button", { class: "btn btn-primary", type: "button", onclick: playing(() => player.playArtist(a.id)) }, micon("shuffle"), "Shuffle play")))),
    section("Albums", a.albums), section("Live recordings", a.live), section("Appears on", a.appears_on));
  return { el, title: a.name };
}

// musicAlbumView is an album: cover, facts, tracklist, and for a live show
// the taper's notes and checksums.
export async function musicAlbumView({ match }) {
  const a = await api(`/music/albums/${match[1]}`);
  const discs = new Set(a.tracks.map((t) => t.disc_number || 0));
  const meta = [];
  if (a.live && a.date) meta.push(a.date);
  else if (a.year) meta.push(String(a.year));
  meta.push(`${a.tracks.length} ${a.tracks.length === 1 ? "track" : "tracks"}`);
  if (a.duration_ms) meta.push(a.duration_ms < 60000 ? `${Math.round(a.duration_ms / 1000)} s` : runtime(a.duration_ms));
  if (a.genre) meta.push(a.genre);

  const rows = [];
  let disc = null;
  a.tracks.forEach((t) => {
    if (discs.size > 1 && t.disc_number !== disc) {
      disc = t.disc_number;
      rows.push(h("li", { class: "disc-head" }, micon("disc"), `${a.live ? "Set" : "Disc"} ${disc || 1}`));
    }
    const other = t.artist_name && t.artist_name !== a.artist_name;
    rows.push(h("li", { class: "track", "data-track": String(t.id) },
      h("button", { type: "button", class: "track-btn", onclick: playing(() => player.playAlbum(a.id, { startTrackId: t.id })),
        "aria-label": `Play ${t.title}` },
      h("span", { class: "track-no" }, t.track_number ? String(t.track_number) : ""),
      h("span", { class: "track-title" }, h("span", {}, t.title), other ? h("span", { class: "track-artist" }, t.artist_name) : null),
      h("span", { class: "track-time" }, clock(t.duration_ms || 0)))));
  });
  const list = h("ol", { class: "tracklist" }, rows);
  const mark = () => {
    const c = player.current();
    for (const li of list.querySelectorAll("li.track")) {
      const on = !!c && li.dataset.track === String(c.id);
      li.classList.toggle("is-current", on);
      li.classList.toggle("is-playing", on && player.isPlaying());
    }
  };
  mark();
  const stop = player.onChange(mark);

  const facts = [];
  const fact = (k, v) => { if (v) facts.push(h("dt", {}, k), h("dd", {}, v)); };
  if (a.live) {
    fact("Venue", [a.venue, a.location].filter(Boolean).join(", "));
    fact("Source", a.source_detail || a.source);
    fact("Lineage", a.lineage);
    fact("Taper", a.taper);
  }
  const kicker = a.live ? "Live recording" : a.compilation ? "Compilation" : "Album";
  const el = h("div", { class: "music-page" },
    h("header", { class: "music-head" },
      squareArt(a.has_cover ? coverUrl(a.id, 640) : null, a.title),
      h("div", { class: "music-info" },
        h("div", { class: "kicker" }, kicker, a.live && a.source ? h("span", { class: "pill" }, a.source) : null),
        h("h1", {}, a.title),
        h("p", { class: "music-by" }, h("a", { href: `#/music/artist/${a.artist_id}` }, a.artist_name)),
        h("p", { class: "muted" }, meta.join(" · ")),
        facts.length ? h("dl", { class: "music-facts" }, facts) : null,
        h("div", { class: "actions" },
          h("button", { class: "btn btn-primary", type: "button", onclick: playing(() => player.playAlbum(a.id)) }, micon("play"), "Play"),
          h("button", { class: "btn", type: "button", onclick: playing(() => player.playAlbum(a.id, { shuffle: true })) }, micon("shuffle"), "Shuffle")))),
    list,
    a.info_text ? h("details", { class: "music-more" }, h("summary", {}, "Info file"), h("pre", {}, a.info_text)) : null,
    a.checksums.length ? h("details", { class: "music-more" }, h("summary", {}, `Checksums (${a.checksums.length})`),
      h("div", { class: "table-wrap" }, h("table", { class: "list checksums" },
        h("thead", {}, h("tr", {}, h("th", {}, "File"), h("th", {}, "Type"), h("th", {}, "Value"))),
        h("tbody", {}, a.checksums.map((c) => h("tr", {}, h("td", {}, c.file_name), h("td", {}, c.algorithm.toUpperCase()), h("td", { class: "mono" }, c.value))))))) : null);
  return { el, title: a.title, cleanup: stop };
}

// musicResults are the music sections of the search page, or null when the
// server has no music for this user.
export async function musicResults(q) {
  let r;
  try {
    r = await api("/music/search?limit=30&q=" + encodeURIComponent(q));
  } catch {
    return null;
  }
  const n = r.artists.length + r.albums.length + r.tracks.length;
  if (!n) return { count: 0, el: null };
  const ids = r.tracks.map((t) => t.id);
  const tracks = r.tracks.length ? h("section", { class: "row" },
    h("div", { class: "row-head" }, h("h2", {}, "Songs")),
    h("ol", { class: "tracklist" }, r.tracks.map((t, i) => h("li", { class: "track" },
      h("button", { type: "button", class: "track-btn", onclick: playing(() => player.playTracks(ids, i)), "aria-label": `Play ${t.title}` },
        h("span", { class: "track-no" }, String(i + 1)),
        h("span", { class: "track-title" }, h("span", {}, t.title), h("span", { class: "track-artist" }, [t.artist_name, t.album_title].filter(Boolean).join(" · "))),
        h("span", { class: "track-time" }, clock(t.duration_ms || 0))))))) : null;
  const el = h("div", { class: "music-results" },
    r.artists.length ? h("section", { class: "row" }, h("div", { class: "row-head" }, h("h2", {}, "Artists")),
      h("div", { class: "grid music-grid" }, r.artists.map(artistCard))) : null,
    r.albums.length ? h("section", { class: "row" }, h("div", { class: "row-head" }, h("h2", {}, "Albums")),
      h("div", { class: "grid music-grid" }, r.albums.map((a) => albumCard(a)))) : null,
    tracks);
  return { count: n, el };
}

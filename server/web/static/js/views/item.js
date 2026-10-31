import { api, artUrl } from "../api.js";
import { h, icon, runtime, clock, size, bitrate, episodeCode, toast, fadeImg, placeholderArt, cardArt, when, hours } from "../dom.js";
import { go, state } from "../app.js";
import { versionTools } from "./versions.js";
import { downloadedSubtitles } from "./subtitle-admin.js";

// The details page for a movie, show, season or episode.
export async function itemView({ match, params }) {
  const id = Number(match[1]);
  const item = await api(`/items/${id}`);
  const playable = item.kind === "movie" || item.kind === "episode";
  const isAdmin = !!(state.me && state.me.is_admin);
  const [children, history] = await Promise.all([
    playable ? null : api(`/items/${id}/children`),
    isAdmin && item.kind !== "season" ? loadHistory(id) : null,
  ]);
  const admin = isAdmin && (item.kind === "movie" || item.kind === "show");
  const fix = admin ? fixMatch(item, params && params.get("fix") === "1") : null;
  const subs = playable && state.me && state.me.is_admin ? downloadedSubtitles(item) : null;
  const tools = admin && item.kind === "movie" ? versionTools(item) : null;
  const el = h("div", {}, hero(item), fix, tools, castSection(item), playable ? techFacts(item) : null, subs, children ? childrenSection(item, children.items || []) : null,
    history ? historySection(item, history) : null);
  return { el, title: item.kind === "episode" ? `${item.show_title || ""} ${episodeCode(item)}`.trim() : item.title, hero: true };
}

// hero is the top of the page: the backdrop across the full width, under the
// top bar, with the poster, the facts that decide what to watch, and Play.
function hero(item) {
  const backdropId = item.has_backdrop ? item.id : item.kind !== "show" && item.show_id ? item.show_id : null;
  const backdrop = h("div", { class: "backdrop" });
  if (item.kind === "episode" && item.has_thumb) backdrop.append(fadeImg(artUrl(item.id, "thumb", 1280), { lazy: false }));
  else if (backdropId) {
    backdrop.append(fadeImg(artUrl(backdropId, "backdrop", 1920), { lazy: false,
      srcset: `${artUrl(backdropId, "backdrop", 960)} 960w, ${artUrl(backdropId, "backdrop", 1920)} 1920w`, sizes: "100vw" }));
  } else if (item.has_poster) {
    // No backdrop: the poster, blurred into a wash of its colours.
    backdrop.classList.add("from-poster");
    backdrop.append(fadeImg(artUrl(item.id, "poster", 320) + "&overlays=0", { lazy: false }));
  }

  // Episodes show their still in the poster's place, when there is one.
  let poster = null;
  if (item.kind !== "episode" || item.has_thumb) {
    const landscape = item.kind === "episode";
    const art = h("div", { class: "art" });
    if (landscape) art.append(fadeImg(artUrl(item.id, "thumb", 640), { lazy: false, onFail: () => art.append(placeholderArt(episodeCode(item))) }));
    else if (item.has_poster) art.append(fadeImg(artUrl(item.id, "poster", 480), { lazy: false, onFail: () => art.append(placeholderArt(item.title)) }));
    else art.append(placeholderArt(item.title));
    poster = h("div", { class: "poster card" + (landscape ? " landscape" : "") }, art);
  }

  const crumbs = [];
  if (item.show_id && item.kind !== "show") crumbs.push(h("a", { href: `#/item/${item.show_id}` }, item.show_title || "Show"));
  if (item.kind === "episode" && item.parent_id) crumbs.push(h("a", { href: `#/item/${item.parent_id}` }, `Season ${item.season_number}`));
  const eyebrow = item.kind === "episode" && item.episode_number != null
    ? `Season ${item.season_number} · Episode ${item.episode_number}${item.episode_number_end ? `-${item.episode_number_end}` : ""}` : "";

  return h("section", { class: "hero", "data-kind": item.kind }, backdrop,
    h("div", { class: "inner" }, poster,
      h("div", { class: "info" },
        crumbs.length ? h("nav", { class: "crumbs", "aria-label": "Breadcrumb" }, crumbs.flatMap((c, i) => (i ? [h("span", { "aria-hidden": "true" }, " › "), c] : [c]))) : null,
        eyebrow ? h("p", { class: "eyebrow" }, eyebrow) : null,
        h("h1", {}, item.title),
        chips(item),
        (item.genres || []).length ? h("p", { class: "genres" }, item.genres.slice(0, 4).join(" · ")) : null,
        item.tagline ? h("p", { class: "tagline" }, item.tagline) : null,
        item.overview ? h("p", { class: "overview" }, item.overview) : null,
        (item.collections || []).length ? h("p", { class: "muted" }, "Part of ",
          item.collections.flatMap((c, i) => [i ? ", " : "", h("a", { class: "link", href: `#/collection/${c.id}` }, c.name)])) : null,
        resumeBar(item),
        actions(item))));
}

const HDR = { hdr10: "HDR10", hdr10plus: "HDR10+", hlg: "HLG", dolby_vision: "Dolby Vision" };
const AUDIO = { ac3: "Dolby Digital", eac3: "Dolby Digital+", truehd: "TrueHD", dts: "DTS", aac: "AAC", flac: "FLAC", opus: "Opus", mp3: "MP3", vorbis: "Vorbis" };

// chips are the facts in one line: year, length, rating, and what the best
// version is (resolution, HDR, audio), the way a streaming service shows them.
function chips(item) {
  const out = [];
  if (item.year) out.push(h("span", {}, String(item.year)));
  if (item.runtime_ms) out.push(h("span", {}, runtime(item.runtime_ms)));
  if (item.kind === "show" && item.child_count) out.push(h("span", {}, `${item.child_count} season${item.child_count === 1 ? "" : "s"}`));
  if (item.kind === "season" && item.child_count) out.push(h("span", {}, `${item.child_count} episode${item.child_count === 1 ? "" : "s"}`));
  if (item.content_rating) out.push(h("span", { class: "chip" }, item.content_rating));
  const v = (item.versions || [])[0];
  const res = (v && v.resolution) || item.resolution;
  if (res) out.push(h("span", { class: "chip" }, res));
  if (v && v.hdr_type) out.push(h("span", { class: "chip" }, HDR[v.hdr_type] || v.hdr_type.toUpperCase()));
  const a = v && ((v.audio_tracks || []).find((t) => t.is_default) || (v.audio_tracks || [])[0]);
  if (a && a.codec) {
    const ch = a.channels === 8 ? "7.1" : a.channels === 6 ? "5.1" : a.channels === 2 ? "Stereo" : a.channels === 1 ? "Mono" : "";
    out.push(h("span", { class: "chip" }, [AUDIO[a.codec] || a.codec.toUpperCase(), ch].filter(Boolean).join(" ")));
  }
  const ratings = item.ratings || {};
  if (ratings.imdb) out.push(h("span", { class: "score", title: "IMDb rating" }, icon("star"), ratings.imdb.toFixed(1), h("small", {}, "IMDb")));
  if (ratings.tmdb) out.push(h("span", { class: "score", title: "TMDb rating" }, icon("star"), ratings.tmdb.toFixed(1), h("small", {}, "TMDb")));
  return out.length ? h("div", { class: "chips" }, out) : null;
}

// resumeBar shows how far in a title someone is, above the Resume button.
function resumeBar(item) {
  const w = item.watch || {};
  if (!(item.kind === "movie" || item.kind === "episode") || !(w.position_ms > 0) || w.played) return null;
  const total = w.duration_ms || ((item.versions || [])[0] || {}).duration_ms;
  if (!total) return null;
  const pct = Math.min(100, (100 * w.position_ms) / total);
  return h("div", { class: "resume-bar" },
    h("div", { class: "bar", role: "progressbar", "aria-label": "Watched so far", "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": String(Math.round(pct)) },
      h("i", { style: { width: `${pct}%` } })),
    h("span", {}, `${clock(Math.max(0, total - w.position_ms))} left`));
}

function actions(item) {
  const row = h("div", { class: "actions" });
  const w = item.watch || {};
  if (item.kind === "movie" || item.kind === "episode") {
    const versions = item.versions || [];
    let versionSel = null;
    if (versions.length > 1) {
      versionSel = h("select", { "aria-label": "Version" }, versions.map((v, i) =>
        h("option", { value: v.id }, versionLabel(v) || `Version ${i + 1}`)));
    }
    const playUrl = (start) => {
      const q = new URLSearchParams();
      if (versionSel) q.set("version", versionSel.value);
      if (start !== undefined) q.set("start", String(start));
      const qs = q.toString();
      return `/play/${item.id}${qs ? "?" + qs : ""}`;
    };
    if (w.position_ms > 0 && !w.played) {
      row.append(
        h("button", { class: "btn btn-primary btn-large", type: "button", onclick: () => go(playUrl()) }, icon("play"), `Resume from ${clock(w.position_ms)}`),
        h("button", { class: "btn btn-glass", type: "button", onclick: () => go(playUrl(0)) }, icon("restart"), "Play from the start"));
    } else {
      row.append(h("button", { class: "btn btn-primary btn-large", type: "button", onclick: () => go(playUrl(0)) }, icon("play"), "Play"));
    }
    if (versionSel) row.append(versionSel);
  } else if (item.next_episode) {
    const n = item.next_episode;
    row.append(h("button", { class: "btn btn-primary btn-large", type: "button", onclick: () => go(`/play/${n.id}`) }, icon("play"),
      `${n.watch && n.watch.position_ms > 0 ? "Resume" : "Play"} ${episodeCode(n)}`));
  }
  const played = item.kind === "show" || item.kind === "season" ? w.played && !w.unplayed_count : w.played;
  const toggle = h("button", { class: "btn btn-glass" + (played ? " is-on" : ""), type: "button", "aria-pressed": played ? "true" : "false" },
    icon("check"), played ? "Mark unwatched" : "Mark watched");
  toggle.addEventListener("click", async () => {
    toggle.disabled = true;
    try {
      await api(`/items/${item.id}/watched`, { method: "PUT", body: { played: !played } });
      toast(played ? "Marked unwatched" : "Marked watched");
      go(location.hash.slice(1));
    } catch (e) {
      toast(e.message);
      toggle.disabled = false;
    }
  });
  row.append(toggle);
  if ((item.kind === "movie" || item.kind === "episode") && w.position_ms > 0 && !w.played) {
    const forget = h("button", { class: "btn btn-glass", type: "button" }, icon("close"), "Remove from Continue Watching");
    forget.addEventListener("click", async () => {
      forget.disabled = true;
      try {
        await api(`/items/${item.id}/progress`, { method: "DELETE" });
        toast("Removed from Continue Watching");
        go(location.hash.slice(1));
      } catch (e) {
        toast(e.message);
        forget.disabled = false;
      }
    });
    row.append(forget);
  }
  return row;
}

function versionLabel(v) {
  return [v.resolution, v.hdr_type ? v.hdr_type.toUpperCase() : "", v.video_codec, v.container, v.label ? `(${v.label})` : "", size(v.size)]
    .filter(Boolean).join(" ");
}

function trackName(t) {
  const parts = [t.title, t.language ? t.language.toUpperCase() : "", t.codec, t.channels ? `${t.channels} ch` : "", t.is_forced ? "forced" : "", t.is_downloaded ? "downloaded" : t.is_external ? "file" : ""];
  return parts.filter(Boolean).join(" · ");
}

// The cast in billing order, name over the part played.
function castSection(item) {
  const cast = item.cast || [];
  if (!cast.length) return null;
  return h("section", { class: "section cast" }, h("h2", {}, "Cast"),
    h("ul", { class: "cast-list" }, cast.map((c) => h("li", { class: "cast-member" },
      h("span", { class: "cast-name" }, c.name),
      c.role ? h("span", { class: "cast-role" }, c.role) : null))));
}

function techFacts(item) {
  const versions = item.versions || [];
  if (!versions.length) return null;
  const rows = [];
  if ((item.directors || []).length) rows.push(["Directed by", item.directors.join(", ")]);
  if ((item.studios || []).length) rows.push([item.kind === "episode" ? "Network" : "Studio", item.studios.join(", ")]);
  versions.forEach((v, i) => {
    const prefix = versions.length > 1 ? `Version ${i + 1} ` : "";
    rows.push([prefix + "File", v.file_name]);
    rows.push([prefix + "Video", [v.resolution, v.width && v.height ? `${v.width}×${v.height}` : "", v.video_codec, v.video_profile, v.bit_depth ? `${v.bit_depth}-bit` : "", v.hdr_type ? v.hdr_type.toUpperCase() : ""].filter(Boolean).join(" · ")]);
    if (v.audio_tracks.length) rows.push([prefix + "Audio", v.audio_tracks.map(trackName).join("\n")]);
    if (v.subtitle_tracks.length) rows.push([prefix + "Subtitles", v.subtitle_tracks.map(trackName).join("\n")]);
    rows.push([prefix + "Size", [size(v.size), bitrate(v.bitrate), v.container].filter(Boolean).join(" · ")]);
  });
  return h("section", { class: "section" }, h("h2", {}, "Details"),
    h("dl", { class: "facts" }, rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", { style: { whiteSpace: "pre-line" } }, v)])));
}

function childrenSection(item, children) {
  if (item.kind === "show") {
    return h("section", { class: "section seasons" }, h("h2", { id: "seasons-title" }, "Seasons"),
      children.length ? seasonTabs(item, children) : h("p", { class: "muted" }, "No seasons on disk."));
  }
  // A season: its episodes as cards with stills.
  return h("section", { class: "section" }, h("h2", {}, "Episodes"), episodeGrid(children));
}

// seasonTabs puts a show's seasons in tabs, each showing its episodes, opening
// on the season of the episode Play would start. Episodes load when a tab is
// chosen; the arrow keys move between tabs, as ARIA tabs do.
function seasonTabs(item, seasons) {
  const panel = h("div", { class: "season-panel", role: "tabpanel", id: "season-panel", tabindex: "0" });
  const tabs = seasons.map((s) => h("button", { class: "season-tab", type: "button", role: "tab", id: `season-tab-${s.id}`,
    "aria-controls": "season-panel", "aria-selected": "false", tabindex: "-1" }, s.title));
  let seq = 0;
  const select = async (i, focus) => {
    tabs.forEach((t, n) => {
      t.setAttribute("aria-selected", n === i ? "true" : "false");
      t.tabIndex = n === i ? 0 : -1;
    });
    if (focus) tabs[i].focus();
    panel.setAttribute("aria-labelledby", tabs[i].id);
    const season = seasons[i];
    const mine = ++seq;
    panel.replaceChildren(h("div", { class: "episodes", "aria-busy": "true" },
      Array.from({ length: Math.min(season.child_count || 4, 8) }, () => h("div", { class: "sk sk-episode" }))));
    try {
      const data = await api(`/items/${season.id}/children`);
      if (mine !== seq) return;
      const count = season.child_count || (data.items || []).length;
      panel.replaceChildren(
        h("div", { class: "season-head" },
          h("span", { class: "muted" }, `${count} episode${count === 1 ? "" : "s"}`),
          h("a", { class: "link", href: `#/item/${season.id}` }, `${season.title} details`)),
        episodeGrid(data.items || []));
    } catch (e) {
      if (mine === seq) panel.replaceChildren(h("p", { class: "muted" }, e.message));
    }
  };
  tabs.forEach((t, i) => t.addEventListener("click", () => select(i)));
  const list = h("div", { class: "season-tabs", role: "tablist", "aria-labelledby": "seasons-title" }, tabs);
  list.addEventListener("keydown", (e) => {
    const at = tabs.indexOf(document.activeElement);
    if (at < 0) return;
    const to = { ArrowRight: at + 1, ArrowLeft: at - 1, Home: 0, End: tabs.length - 1 }[e.key];
    if (to === undefined) return;
    e.preventDefault();
    select((to + tabs.length) % tabs.length, true);
  });
  const start = item.next_episode ? seasons.findIndex((s) => s.id === item.next_episode.parent_id) : -1;
  select(Math.max(0, start));
  return [list, panel];
}

function episodeGrid(eps) {
  if (!eps.length) return h("p", { class: "muted" }, "No episodes on disk.");
  return h("div", { class: "episodes" }, eps.map(episodeCard));
}

// episodeCard is an episode's still, which plays it, over its number, title
// and where it stands (watched, time left, or length), which open its page.
function episodeCard(ep) {
  const w = ep.watch || {};
  const code = episodeCode(ep);
  const name = `${ep.episode_number != null ? ep.episode_number + ". " : ""}${ep.title}`;
  const where = w.played ? "Watched" : w.position_ms > 0 && w.duration_ms ? `${clock(Math.max(0, w.duration_ms - w.position_ms))} left` : runtime(ep.runtime_ms);
  const link = h("a", { class: "card landscape ep-link", href: `#/item/${ep.id}` }, cardArt(ep, { landscape: true }),
    h("div", { class: "title" }, name), h("div", { class: "sub" }, [code, where].filter(Boolean).join(" · ")));
  const play = h("button", { class: "ep-play", type: "button", "aria-label": `Play ${code} ${ep.title}`.trim(), onclick: () => go(`/play/${ep.id}`) },
    h("span", {}, icon("play")));
  return h("div", { class: "ep-card" + (w.played ? " watched" : "") }, link, play);
}

// History, for admins: the latest plays of this title, like Tautulli's item
// page. A show's history is its episodes'. Admin routes answer only on the LAN,
// so a failure here leaves the section out rather than breaking the page.
const HISTORY_ROWS = 20;

async function loadHistory(id) {
  try {
    const data = await api(`/admin/items/${id}/history?limit=${HISTORY_ROWS}`);
    return data && data.plays > 0 ? data : null;
  } catch {
    return null;
  }
}

function historySection(item, data) {
  const showEpisodes = item.kind === "show";
  const head = ["When", "User", showEpisodes ? "Episode" : null, "Device", "Watched", "Reached"].filter(Boolean);
  const numeric = (c) => c === "Watched" || c === "Reached";
  const rows = data.sessions.map((x) => h("tr", {},
    h("td", {}, when(x.started_at)),
    h("td", {}, x.username || "Unknown"),
    showEpisodes ? h("td", {}, x.item_id ? h("a", { href: `#/item/${x.item_id}` }, x.item_title || "Episode") : x.item_title || "") : null,
    h("td", {}, x.device_name || x.client || ""),
    h("td", { class: "num" }, hours(x.played_ms)),
    h("td", { class: "num", title: x.decision_reason || "" },
      x.progress != null ? `${Math.round(x.progress * 100)}%` : "", " ",
      h("span", { class: `decision ${x.decision}` }, x.decision.replace(/_/g, " ")))));
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
  return h("section", { class: "section item-history" },
    h("h2", {}, "History"),
    h("p", { class: "muted" }, [plural(data.plays, "play", "plays"), plural(data.users, "person", "people"), `${hours(data.watch_ms)} watched`].join(" · "),
      data.plays > data.sessions.length ? ` · latest ${data.sessions.length} shown` : ""),
    h("table", { class: "list" },
      h("thead", {}, h("tr", {}, head.map((c) => h("th", { class: numeric(c) ? "num" : "" }, c)))),
      h("tbody", {}, rows)));
}

// Fix Match, for admins: search TMDb and pick the right title when automatic
// matching found none or the wrong one. The server keeps the choice.
function fixMatch(item, open) {
  const results = h("div", { class: "fix-results" });
  const query = h("input", { name: "query", value: item.title, "aria-label": "Title to search for" });
  const year = h("input", { name: "year", value: item.year || "", inputmode: "numeric", placeholder: "Year", "aria-label": "Year", class: "fix-year" });
  const form = h("form", { class: "fix-form" }, query, year, h("button", { class: "btn btn-primary btn-small", type: "submit" }, "Search TMDb"));
  const panel = h("section", { class: "panel fix-panel", hidden: !open },
    h("h3", {}, "Fix match"),
    h("p", { class: "muted" }, "Pick the right title. Your own values (from an NFO or set by hand) are kept; the rest come from TMDb."),
    form, results);
  const search = async () => {
    results.replaceChildren(h("p", { class: "muted" }, "Searching"));
    try {
      const q = new URLSearchParams({ query: query.value.trim() });
      if (year.value.trim()) q.set("year", year.value.trim());
      const data = await api(`/admin/items/${item.id}/match?${q}`);
      if (!data.choices.length) {
        results.replaceChildren(h("p", { class: "muted" }, "TMDb found nothing. Try fewer words, or no year."));
        return;
      }
      results.replaceChildren(...data.choices.slice(0, 12).map((c) => {
        const use = h("button", { class: "btn btn-small", type: "button" }, "Use this");
        use.addEventListener("click", async () => {
          use.disabled = true;
          try {
            await api(`/admin/items/${item.id}/match`, { method: "PUT", body: { tmdb_id: c.tmdb_id } });
            toast(`Matched to ${c.title}${c.year ? ` (${c.year})` : ""}`);
            go(`/item/${item.id}`);
          } catch (e) {
            toast(e.message);
            use.disabled = false;
          }
        });
        return h("div", { class: "fix-choice" },
          h("div", {}, h("strong", {}, c.title), c.year ? h("span", { class: "muted" }, ` ${c.year}`) : null, h("span", { class: "faint" }, ` · TMDb ${c.tmdb_id}`),
            c.overview ? h("p", { class: "muted" }, c.overview.length > 220 ? c.overview.slice(0, 220) + "…" : c.overview) : null),
          use);
      }));
    } catch (e) {
      results.replaceChildren(h("p", { class: "muted" }, e.message));
    }
  };
  form.addEventListener("submit", (e) => { e.preventDefault(); search(); });
  if (open) search();
  const toggle = h("button", { class: "btn btn-small", type: "button" }, "Fix match");
  toggle.addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    if (!panel.hidden && !results.childNodes.length) search();
  });
  return h("div", { class: "fix" }, h("div", { class: "fix-toggle" }, toggle), panel);
}

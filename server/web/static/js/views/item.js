import { api, artUrl } from "../api.js";
import { h, card, icon, runtime, clock, size, bitrate, episodeCode, toast } from "../dom.js";
import { go, state } from "../app.js";

// The details page for a movie, show, season or episode.
export async function itemView({ match, params }) {
  const id = Number(match[1]);
  const item = await api(`/items/${id}`);
  const playable = item.kind === "movie" || item.kind === "episode";
  const children = playable ? null : await api(`/items/${id}/children`);
  const admin = state.me && state.me.is_admin && (item.kind === "movie" || item.kind === "show");
  const fix = admin ? fixMatch(item, params && params.get("fix") === "1") : null;
  const el = h("div", {}, hero(item), fix, playable ? techFacts(item) : null, children ? childrenSection(item, children.items || []) : null);
  return { el, title: item.kind === "episode" ? `${item.show_title || ""} ${episodeCode(item)}`.trim() : item.title };
}

function hero(item) {
  const backdropId = item.has_backdrop ? item.id : item.kind !== "show" && item.show_id ? item.show_id : null;
  const backdrop = h("div", { class: "backdrop" });
  if (item.kind === "episode" && item.has_thumb) backdrop.append(img(artUrl(item.id, "thumb", 1280)));
  else if (backdropId) backdrop.append(img(artUrl(backdropId, "backdrop", 1600)));

  let poster = null;
  if (item.kind !== "episode") {
    const art = h("div", { class: "art" });
    if (item.has_poster) art.append(img(artUrl(item.id, "poster", 480)));
    else art.append(h("div", { class: "placeholder" }, item.title));
    poster = h("div", { class: "poster card" }, art);
  }

  const crumbs = [];
  if (item.show_id && item.kind !== "show") crumbs.push(h("a", { href: `#/item/${item.show_id}` }, item.show_title || "Show"));
  if (item.kind === "episode" && item.parent_id) crumbs.push(h("a", { href: `#/item/${item.parent_id}` }, `Season ${item.season_number}`));

  const meta = [];
  if (item.kind === "episode") meta.push(episodeCode(item));
  if (item.year) meta.push(String(item.year));
  if (item.content_rating) meta.push(h("span", { class: "pill" }, item.content_rating));
  if (item.runtime_ms) meta.push(runtime(item.runtime_ms));
  if (item.resolution) meta.push(h("span", { class: "pill" }, item.resolution));
  const ratings = item.ratings || {};
  if (ratings.tmdb) meta.push(`TMDb ${ratings.tmdb.toFixed(1)}`);
  if ((item.genres || []).length) meta.push(item.genres.slice(0, 4).join(", "));

  return h("section", { class: "hero" }, backdrop,
    h("div", { class: "inner" }, poster,
      h("div", { class: "info" },
        crumbs.length ? h("div", { class: "crumbs" }, crumbs.flatMap((c, i) => (i ? [" › ", c] : [c]))) : null,
        h("h1", {}, item.title),
        h("div", { class: "meta" }, meta.map((m) => (typeof m === "string" ? h("span", {}, m) : m))),
        item.tagline ? h("p", { class: "tagline" }, item.tagline) : null,
        item.overview ? h("p", { class: "overview" }, item.overview) : null,
        (item.collections || []).length ? h("p", { class: "muted" }, "Part of ",
          item.collections.flatMap((c, i) => [i ? ", " : "", h("a", { class: "link", href: `#/collection/${c.id}` }, c.name)])) : null,
        actions(item))));
}

function img(src) {
  const el = h("img", { src, alt: "", decoding: "async" });
  el.addEventListener("error", () => el.remove(), { once: true });
  return el;
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
        h("button", { class: "btn btn-primary", type: "button", onclick: () => go(playUrl()) }, icon("play"), `Resume from ${clock(w.position_ms)}`),
        h("button", { class: "btn", type: "button", onclick: () => go(playUrl(0)) }, "Play from the start"));
    } else {
      row.append(h("button", { class: "btn btn-primary", type: "button", onclick: () => go(playUrl(0)) }, icon("play"), "Play"));
    }
    if (versionSel) row.append(versionSel);
  } else if (item.next_episode) {
    const n = item.next_episode;
    row.append(h("button", { class: "btn btn-primary", type: "button", onclick: () => go(`/play/${n.id}`) }, icon("play"),
      `${n.watch && n.watch.position_ms > 0 ? "Resume" : "Play"} ${episodeCode(n)}`));
  }
  const played = item.kind === "show" || item.kind === "season" ? w.played && !w.unplayed_count : w.played;
  const toggle = h("button", { class: "btn", type: "button" }, icon("check"), played ? "Mark unwatched" : "Mark watched");
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
  return row;
}

function versionLabel(v) {
  return [v.resolution, v.hdr_type ? v.hdr_type.toUpperCase() : "", v.video_codec, v.container, v.label ? `(${v.label})` : "", size(v.size)]
    .filter(Boolean).join(" ");
}

function trackName(t) {
  const parts = [t.title, t.language ? t.language.toUpperCase() : "", t.codec, t.channels ? `${t.channels} ch` : "", t.is_forced ? "forced" : "", t.is_external ? "file" : ""];
  return parts.filter(Boolean).join(" · ");
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
    return h("section", { class: "section" }, h("h2", {}, "Seasons"),
      children.length ? h("div", { class: "grid" }, children.map((c) => card(c))) : h("p", { class: "muted" }, "No seasons on disk."));
  }
  // A season: episodes as a list with stills.
  return h("section", { class: "section" }, h("h2", {}, "Episodes"),
    h("div", { class: "episodes" }, children.map((ep) => h("div", { class: "episode" },
      card(ep, { landscape: true }),
      h("div", {},
        h("a", { class: "ep-title", href: `#/item/${ep.id}` }, `${ep.episode_number != null ? ep.episode_number + ". " : ""}${ep.title}`),
        h("div", { class: "muted" }, [runtime(ep.runtime_ms), ep.watch && ep.watch.played ? "Watched" : ep.watch && ep.watch.position_ms ? `${clock(ep.watch.position_ms)} watched` : ""].filter(Boolean).join(" · ")),
        h("div", { class: "actions", style: { marginTop: "8px" } },
          h("button", { class: "btn btn-small btn-primary", type: "button", onclick: () => go(`/play/${ep.id}`) }, icon("play"), "Play")))))));
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

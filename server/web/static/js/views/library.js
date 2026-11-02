import { api } from "../api.js";
import { h, card } from "../dom.js";
import { state, go } from "../app.js";
import { musicLibraryView } from "./music.js";

const PAGE = 120;

// A library grid with sort, filters and "show more" paging. The choices live in
// the address, so back and reload keep them.
export async function libraryView({ match, params }) {
  const id = Number(match[1]);
  const lib = state.libraries.find((l) => l.id === id);
  if (lib && lib.kind === "music") return musicLibraryView({ match, params });
  const sort = params.get("sort") || "title";
  const order = params.get("order") || (sort === "title" ? "asc" : "desc");
  const unwatched = params.get("unwatched") === "1";
  const resolution = params.get("resolution") || "";
  const genre = params.get("genre") || "";
  const decade = params.get("decade") || "";
  const query = (offset) => {
    const q = new URLSearchParams({ sort, order, limit: String(PAGE), offset: String(offset) });
    if (unwatched) q.set("unwatched", "true");
    if (resolution) q.set("resolution", resolution);
    if (genre) q.set("genre", genre);
    if (decade) q.set("decade", decade);
    return `/libraries/${id}/items?${q}`;
  };
  const [first, filters, cols] = await Promise.all([api(query(0)), api(`/libraries/${id}/filters`).catch(() => null),
    api(`/libraries/${id}/collections`).catch(() => ({ collections: [] }))]);
  const grid = h("div", { class: "grid" }, first.items.map((i) => card(i)));
  let loaded = first.items.length;
  const count = h("span", { class: "count" }, `${first.total} ${first.total === 1 ? "title" : "titles"}`);
  const moreBtn = h("button", { class: "btn", type: "button" }, "Show more");
  const more = h("div", { class: "more" }, moreBtn);
  more.hidden = loaded >= first.total;
  moreBtn.addEventListener("click", async () => {
    moreBtn.disabled = true;
    try {
      const page = await api(query(loaded));
      grid.append(...page.items.map((i) => card(i)));
      loaded += page.items.length;
      more.hidden = loaded >= page.total || page.items.length === 0;
    } finally {
      moreBtn.disabled = false;
      // Observing afresh reports at once if the end is still in reach.
      if (watcher && !more.hidden) { watcher.unobserve(more); watcher.observe(more); }
    }
  });

  const set = (changes) => {
    const p = new URLSearchParams({ sort, order });
    if (unwatched) p.set("unwatched", "1");
    if (resolution) p.set("resolution", resolution);
    if (genre) p.set("genre", genre);
    if (decade) p.set("decade", decade);
    for (const [k, v] of Object.entries(changes)) {
      if (v === "" || v === null) p.delete(k);
      else p.set(k, v);
    }
    go(`/library/${id}?${p}`);
  };
  const sortSel = h("select", { "aria-label": "Sort", onchange: (e) => {
    const [s, o] = e.target.value.split(":");
    set({ sort: s, order: o });
  } },
  [["title:asc", "Title A to Z"], ["title:desc", "Title Z to A"], ["added:desc", "Recently added"], ["year:desc", "Newest"], ["year:asc", "Oldest"]]
    .map(([v, label]) => h("option", { value: v, selected: v === `${sort}:${order}` }, label)));
  const resSel = h("select", { "aria-label": "Resolution", onchange: (e) => set({ resolution: e.target.value }) },
    [["", "Any resolution"], ["4K", "4K"], ["1080p", "1080p"], ["720p", "720p"], ["SD", "SD"]]
      .map(([v, label]) => h("option", { value: v, selected: v === resolution }, label)));
  const unwatchedBox = h("label", {}, h("input", { type: "checkbox", checked: unwatched, onchange: (e) => set({ unwatched: e.target.checked ? "1" : "" }) }), "Unwatched");
  const genreSel = filters && filters.genres.length ? h("select", { "aria-label": "Genre", onchange: (e) => set({ genre: e.target.value }) },
    h("option", { value: "" }, "Any genre"),
    filters.genres.map((g) => h("option", { value: g.value, selected: g.value === genre }, `${g.value} (${g.count})`))) : null;
  const decadeSel = filters && filters.decades.length ? h("select", { "aria-label": "Decade", onchange: (e) => set({ decade: e.target.value }) },
    h("option", { value: "" }, "Any decade"),
    filters.decades.map((d) => {
      const v = d.value.replace(/s$/, "");
      return h("option", { value: v, selected: v === decade }, `${d.value} (${d.count})`);
    })) : null;
  const colLink = cols.collections.length ? h("a", { class: "btn btn-small", href: `#/library/${id}/collections` }, `Collections (${cols.collections.length})`) : null;

  // The filters sit in the toolbar's line on a wide screen and scroll in one
  // row of their own on a phone, so the posters start near the top.
  const el = h("div", {},
    h("div", { class: "toolbar" }, h("h1", {}, lib ? lib.name : "Library"), count, colLink,
      h("div", { class: "filters" }, sortSel, genreSel, decadeSel, resSel, unwatchedBox)),
    first.total ? grid : h("div", { class: "empty" }, "No titles match these filters."),
    more);
  // The next page loads as the grid's end comes near; the button stays for
  // keyboards, and for browsers without IntersectionObserver.
  let watcher = null;
  if ("IntersectionObserver" in window) {
    watcher = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting) && !more.hidden && !moreBtn.disabled) moreBtn.click();
    }, { rootMargin: "800px 0px" });
    watcher.observe(more);
  }
  return { el, title: lib ? lib.name : "Library", cleanup: () => watcher && watcher.disconnect() };
}

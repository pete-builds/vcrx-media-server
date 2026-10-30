import { api } from "../api.js";
import { h, card } from "../dom.js";
import { state, go } from "../app.js";

const PAGE = 120;

// A library grid with sort, filters and "show more" paging. The choices live in
// the address, so back and reload keep them.
export async function libraryView({ match, params }) {
  const id = Number(match[1]);
  const lib = state.libraries.find((l) => l.id === id);
  const sort = params.get("sort") || "title";
  const order = params.get("order") || (sort === "title" ? "asc" : "desc");
  const unwatched = params.get("unwatched") === "1";
  const resolution = params.get("resolution") || "";
  const query = (offset) => {
    const q = new URLSearchParams({ sort, order, limit: String(PAGE), offset: String(offset) });
    if (unwatched) q.set("unwatched", "true");
    if (resolution) q.set("resolution", resolution);
    return `/libraries/${id}/items?${q}`;
  };
  const first = await api(query(0));
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
    }
  });

  const set = (changes) => {
    const p = new URLSearchParams({ sort, order });
    if (unwatched) p.set("unwatched", "1");
    if (resolution) p.set("resolution", resolution);
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

  const el = h("div", {},
    h("div", { class: "toolbar" }, h("h1", {}, lib ? lib.name : "Library"), count, sortSel, resSel, unwatchedBox),
    first.total ? grid : h("div", { class: "empty" }, "No titles match these filters."),
    more);
  return { el, title: lib ? lib.name : "Library" };
}

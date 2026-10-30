import { api } from "../api.js";
import { h, card } from "../dom.js";

// Home shows the server's rows: Continue Watching, then recently added per library.
export async function homeView() {
  const data = await api("/home");
  const rows = (data.rows || []).filter((r) => r.items && r.items.length);
  if (!rows.length) {
    return { el: h("div", { class: "empty" }, "Nothing here yet. Once a library has been scanned, its titles show up here."), title: "Home" };
  }
  const el = h("div", {}, rows.map((row) => {
    const landscape = row.id === "continue_watching" || row.items.every((i) => i.kind === "episode");
    const more = row.library_id ? h("a", { href: `#/library/${row.library_id}?sort=added` }, "See all") : null;
    return h("section", { class: "row" },
      h("div", { class: "row-head" }, h("h2", {}, row.title), more),
      h("div", { class: "strip" + (landscape ? " wide" : "") }, row.items.map((i) => card(i, { landscape }))));
  }));
  return { el, title: "Home" };
}

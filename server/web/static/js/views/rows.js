import { api } from "../api.js";
import { h, card } from "../dom.js";

// A Home row's See all: the same row, up to 100 titles, as a grid.
export async function rowView({ match }) {
  const id = decodeURIComponent(match[1]);
  const row = await api(`/rows/${encodeURIComponent(id)}`);
  const items = row.items || [];
  const landscape = items.length > 0 && items.every((i) => i.kind === "episode");
  const el = h("div", {},
    h("div", { class: "crumbs" }, h("a", { href: "#/" }, "Home")),
    h("div", { class: "toolbar" }, h("h1", {}, row.title), h("span", { class: "count" }, `${items.length} title${items.length === 1 ? "" : "s"}`)),
    items.length ? h("div", { class: "grid" + (landscape ? " wide" : "") }, items.map((i) => card(i, { landscape })))
      : h("div", { class: "empty" }, "Nothing in this row right now."));
  return { el, title: row.title };
}

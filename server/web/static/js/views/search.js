import { api } from "../api.js";
import { h, card } from "../dom.js";
import { musicResults } from "./music.js";

export async function searchView({ params }) {
  const q = (params.get("q") || "").trim();
  const input = document.querySelector("#search input");
  if (input && input.value !== q) input.value = q;
  if (!q) return { el: h("div", { class: "empty" }, "Type a title in the search box."), title: "Search" };
  const [data, music] = await Promise.all([api("/search?limit=100&q=" + encodeURIComponent(q)), musicResults(q)]);
  const items = data.items || [];
  const found = items.length + (music ? music.count : 0);
  const groups = [["Movies", "movie"], ["Shows", "show"], ["Episodes", "episode"]]
    .map(([label, kind]) => [label, items.filter((i) => i.kind === kind)])
    .filter(([, list]) => list.length);
  const el = h("div", {},
    h("div", { class: "toolbar" }, h("h1", {}, `Results for “${q}”`), h("span", { class: "count" }, `${found} found`)),
    found ? null : h("div", { class: "empty" }, "No titles match."),
    groups.map(([label, list]) => h("section", { class: "row" },
      h("div", { class: "row-head" }, h("h2", {}, label)),
      h("div", { class: "grid" + (label === "Episodes" ? " wide" : "") }, list.map((i) => card(i, { landscape: label === "Episodes" }))))),
    music ? music.el : null);
  return { el, title: `Search: ${q}` };
}

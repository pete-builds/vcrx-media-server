import { api, artUrl } from "../api.js";
import { h, card } from "../dom.js";
import { state } from "../app.js";

// A library's collections, such as film series, as a grid.
export async function libraryCollectionsView({ match }) {
  const id = Number(match[1]);
  const lib = state.libraries.find((l) => l.id === id);
  const data = await api(`/libraries/${id}/collections`);
  const el = h("div", {},
    h("div", { class: "toolbar" },
      h("h1", {}, `${lib ? lib.name : "Library"} collections`),
      h("span", { class: "count" }, `${data.collections.length}`),
      h("a", { class: "btn btn-small", href: `#/library/${id}` }, "All titles")),
    data.collections.length ? h("div", { class: "grid" }, data.collections.map(collectionCard))
      : h("div", { class: "empty" }, "No collections yet. Film series appear here once two or more of their films are in the library."));
  return { el, title: `${lib ? lib.name : "Library"} collections` };
}

function collectionCard(c) {
  const art = h("div", { class: "art" }, h("span", { class: "badge" }, c.item_count));
  const first = c.poster_item_ids[0];
  if (first) {
    // The clean poster: a member's rating badge would mislabel the collection.
    const img = h("img", { src: artUrl(first, "poster", 300) + "&overlays=0", alt: "", loading: "lazy" });
    img.addEventListener("error", () => { img.remove(); art.prepend(h("div", { class: "placeholder" }, c.name)); }, { once: true });
    art.prepend(img);
  } else {
    art.prepend(h("div", { class: "placeholder" }, c.name));
  }
  return h("a", { class: "card", href: `#/collection/${c.id}`, title: c.name },
    art, h("div", { class: "title" }, c.name), h("div", { class: "sub" }, `${c.item_count} titles`));
}

// One collection's titles, in their order (release order for a film series).
export async function collectionView({ match }) {
  const data = await api(`/collections/${Number(match[1])}`);
  const c = data.collection;
  const lib = state.libraries.find((l) => l.id === c.library_id);
  const el = h("div", {},
    h("div", { class: "crumbs" }, h("a", { href: `#/library/${c.library_id}/collections` }, `${lib ? lib.name : "Library"} collections`)),
    h("div", { class: "toolbar" }, h("h1", {}, c.name), h("span", { class: "count" }, `${data.items.length} titles`)),
    h("div", { class: "grid" }, data.items.map((i) => card(i))));
  return { el, title: c.name };
}

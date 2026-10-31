import { api } from "../api.js";
import { h, card, icon, toast } from "../dom.js";

// Home shows the server's rows: Continue Watching, then recently added per library.
export async function homeView() {
  const data = await api("/home");
  const rows = (data.rows || []).filter((r) => r.items && r.items.length);
  if (!rows.length) {
    return { el: h("div", { class: "empty" }, "Nothing here yet. Once a library has been scanned, its titles show up here."), title: "Home" };
  }
  const el = h("div", {}, rows.map((row) => {
    const continuing = row.id === "continue_watching";
    const landscape = continuing || row.items.every((i) => i.kind === "episode");
    const more = row.library_id ? h("a", { href: `#/library/${row.library_id}?sort=added` }, "See all") : null;
    const section = h("section", { class: "row", "data-row": row.id },
      h("div", { class: "row-head" }, h("h2", {}, row.title), more));
    const strip = h("div", { class: "strip" + (landscape ? " wide" : "") },
      row.items.map((i) => (continuing ? continueCard(i, section) : card(i, { landscape }))));
    section.append(strip);
    return section;
  }));
  return { el, title: "Home" };
}

// continueCard is a Continue Watching card with a Remove button beside the
// link, not inside it, so pressing it never opens the title.
function continueCard(item, section) {
  const label = item.kind === "episode" && item.show_title ? `${item.show_title}: ${item.title}` : item.title;
  const remove = h("button", { class: "cw-remove", type: "button", title: "Remove from Continue Watching",
    "aria-label": `Remove ${label} from Continue Watching` }, icon("close"), h("span", {}, "Remove"));
  const wrap = h("div", { class: "cw-card" }, card(item, { landscape: true }), remove);
  remove.addEventListener("click", async () => {
    remove.disabled = true;
    try {
      await api(`/items/${item.id}/progress`, { method: "DELETE" });
      // Keep keyboard focus in the row: on the next card's Remove, else the previous one.
      const next = wrap.nextElementSibling || wrap.previousElementSibling;
      wrap.remove();
      if (next) next.querySelector(".cw-remove").focus();
      else section.remove();
      toast(`Removed ${label} from Continue Watching`);
    } catch (e) {
      toast(e.message);
      remove.disabled = false;
    }
  });
  return wrap;
}

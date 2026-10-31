import { api } from "../api.js";
import { h, icon, toast, errorBox } from "../dom.js";
import { state } from "../app.js";

// Customize Home: which rows Home shows and in what order. The layout lives on
// the server (GET and PUT /layout/home), so the TV app and every browser show
// the same Home. Every change saves at once. Rows move with the Move up and
// Move down buttons (keyboard and screen readers) or by dragging (mouse).
export async function homeLayoutTab(body) {
  const admin = !!(state.me && state.me.is_admin);
  let layout;
  try {
    layout = await api("/layout/home");
  } catch (e) {
    body.replaceChildren(errorBox("Could not load your Home layout", e.message));
    return;
  }
  const list = h("ol", { class: "layout-list", "aria-label": "Home rows, top to bottom" });
  const source = h("p", { class: "muted layout-source", role: "status" });
  let dragKey = null;

  const save = async (rows, focus) => {
    try {
      layout = await api("/layout/home", { method: "PUT", body: { rows: rows.map((r) => ({ key: r.key, enabled: r.enabled })) } });
      draw(focus);
    } catch (e) {
      toast(e.message);
      draw(focus);
    }
  };
  const move = (key, by, focus) => {
    const rows = layout.rows.slice();
    const from = rows.findIndex((r) => r.key === key);
    const to = from + by;
    if (from < 0 || to < 0 || to >= rows.length) return;
    rows.splice(to, 0, rows.splice(from, 1)[0]);
    save(rows, focus);
  };

  // draw rebuilds the list and puts focus back where the user was, so moving
  // a row with the keyboard keeps the focus on that row's button.
  const draw = (focus) => {
    source.textContent = {
      user: "This is your own Home. Reset it to go back to the server's default.",
      server: "You are using the server's default Home. Changing anything makes it yours.",
      default: "You are using the standard Home. Changing anything makes it yours.",
    }[layout.source] || "";
    const n = layout.rows.length;
    list.replaceChildren(...layout.rows.map((r, i) => {
      const box = h("input", { type: "checkbox", id: `row-${r.key}`, checked: r.enabled });
      box.addEventListener("change", () => {
        const rows = layout.rows.map((x) => (x.key === r.key ? { ...x, enabled: box.checked } : x));
        save(rows, { key: r.key, what: "box" });
      });
      const up = h("button", { class: "icon-btn", type: "button", "data-move": "up", disabled: i === 0, "aria-label": `Move ${r.title} up`, title: "Move up" }, icon("up"));
      const down = h("button", { class: "icon-btn", type: "button", "data-move": "down", disabled: i === n - 1, "aria-label": `Move ${r.title} down`, title: "Move down" }, icon("down"));
      // At the top or bottom, the button just used is disabled; focus its partner.
      up.addEventListener("click", () => move(r.key, -1, { key: r.key, what: i - 1 === 0 ? "down" : "up" }));
      down.addEventListener("click", () => move(r.key, 1, { key: r.key, what: i + 1 === n - 1 ? "up" : "down" }));
      const li = h("li", { class: "layout-row" + (r.enabled ? "" : " off"), "data-key": r.key, draggable: "true" },
        h("span", { class: "layout-grip", "aria-hidden": "true", title: "Drag to move" }, icon("grip")),
        h("label", { for: `row-${r.key}` }, box,
          h("span", { class: "layout-text" }, h("span", { class: "layout-title" }, r.title), h("span", { class: "layout-desc" }, r.description))),
        h("span", { class: "layout-move" }, up, down));
      li.addEventListener("dragstart", (e) => {
        dragKey = r.key;
        li.classList.add("dragging");
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", r.key);
      });
      li.addEventListener("dragend", () => {
        dragKey = null;
        li.classList.remove("dragging");
        for (const x of list.children) x.classList.remove("drop-before", "drop-after");
      });
      li.addEventListener("dragover", (e) => {
        if (!dragKey || dragKey === r.key) return;
        e.preventDefault();
        const rect = li.getBoundingClientRect();
        const after = e.clientY > rect.top + rect.height / 2;
        li.classList.toggle("drop-after", after);
        li.classList.toggle("drop-before", !after);
      });
      li.addEventListener("dragleave", () => li.classList.remove("drop-before", "drop-after"));
      li.addEventListener("drop", (e) => {
        e.preventDefault();
        const after = li.classList.contains("drop-after");
        li.classList.remove("drop-before", "drop-after");
        if (!dragKey || dragKey === r.key) return;
        const rows = layout.rows.filter((x) => x.key !== dragKey);
        const moved = layout.rows.find((x) => x.key === dragKey);
        rows.splice(rows.findIndex((x) => x.key === r.key) + (after ? 1 : 0), 0, moved);
        save(rows, null);
      });
      return li;
    }));
    if (focus) {
      const li = list.querySelector(`[data-key="${CSS.escape(focus.key)}"]`);
      const target = li && (focus.what === "box" ? li.querySelector("input") : li.querySelector(`[data-move="${focus.what}"]`));
      if (target) target.focus();
    }
  };

  const reset = h("button", { class: "btn btn-small", type: "button" }, "Reset to default");
  reset.addEventListener("click", async () => {
    try {
      layout = await api("/layout/home", { method: "DELETE" });
      draw(null);
      toast("Home is back to the default");
    } catch (e) {
      toast(e.message);
    }
  });
  const actions = h("div", { class: "layout-actions" }, reset);
  if (admin) {
    const everyone = h("button", { class: "btn btn-small", type: "button" }, "Make this everyone's default");
    everyone.addEventListener("click", async () => {
      try {
        await api("/admin/layout/home", { method: "PUT", body: { rows: layout.rows.map((r) => ({ key: r.key, enabled: r.enabled })) } });
        toast("Saved as the default for everyone without their own Home");
      } catch (e) {
        toast(e.message);
      }
    });
    actions.append(everyone);
  }
  draw(null);
  body.replaceChildren(
    h("h2", { class: "layout-heading" }, "Customize Home"),
    h("p", { class: "muted" }, "Choose the rows Home shows and their order. Changes save as you make them, and the TV app shows the same Home. A row with fewer than four titles stays hidden until it fills up."),
    source, list, actions);
}

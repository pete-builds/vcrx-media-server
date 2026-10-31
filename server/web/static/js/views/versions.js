import { api } from "../api.js";
import { h, size, toast } from "../dom.js";
import { go } from "../app.js";

// Version tools on a movie's page, for admins: split one file off into a
// movie of its own, or merge other movies of the same library into this one.
// The server keeps both through rescans, so a choice here is made once.
export function versionTools(item) {
  const versions = item.versions || [];
  const rows = versions.map((v) => h("div", { class: "version-row" },
    h("div", { class: "version-name" },
      h("strong", {}, versionName(v)),
      h("div", { class: "faint" }, [v.file_name, size(v.size)].filter(Boolean).join(" · "))),
    versions.length > 1 ? confirmButton("Split this version", "Split it off", async () => {
      const res = await api(`/admin/versions/${v.id}/split`, { method: "POST" });
      toast("Split into a movie of its own");
      go(`/item/${res.item_id}`);
    }) : null));
  return h("section", { class: "section versions-admin" },
    h("h2", {}, "Versions"),
    versions.length > 1 ? h("p", { class: "muted" }, "Split a file that is a different film or cut. Rescans and TMDb matching keep it apart.") : null,
    rows,
    mergePicker(item));
}

// versionName is the label the scanner composed ("4K HDR REMUX"), with the
// resolution in front when the label is only a name ("Workprint").
function versionName(v) {
  if (v.label && v.resolution && v.label.includes(v.resolution)) return v.label;
  return [v.resolution, v.hdr_type ? v.hdr_type.toUpperCase() : "", v.label].filter(Boolean).join(" ") || "Version";
}

// confirmButton asks for a second click before it acts, so one stray tap
// changes nothing.
function confirmButton(label, confirmLabel, act) {
  const btn = h("button", { class: "btn btn-small", type: "button" }, label);
  let armed = false;
  btn.addEventListener("click", async () => {
    if (!armed) {
      armed = true;
      btn.textContent = confirmLabel;
      btn.classList.add("btn-primary");
      return;
    }
    btn.disabled = true;
    try {
      await act();
    } catch (e) {
      toast(e.message);
      btn.disabled = false;
    }
  });
  return btn;
}

function mergePicker(item) {
  const results = h("div", { class: "fix-results" });
  const query = h("input", { name: "query", value: item.title, "aria-label": "Movie to merge with" });
  const form = h("form", { class: "fix-form" }, query, h("button", { class: "btn btn-primary btn-small", type: "submit" }, "Search"));
  const panel = h("div", { class: "panel merge-panel", hidden: true },
    h("h3", {}, "Merge with another movie"),
    h("p", { class: "muted" }, `Its files become versions of “${item.title}”, and its watch history comes along. This movie keeps its title and match.`),
    form, results);
  const search = async () => {
    const q = query.value.trim();
    if (!q) return;
    results.replaceChildren(h("p", { class: "muted" }, "Searching"));
    try {
      const data = await api(`/search?limit=50&q=${encodeURIComponent(q)}`);
      const found = (data.items || []).filter((m) => m.kind === "movie" && m.library_id === item.library_id && m.id !== item.id);
      if (!found.length) {
        results.replaceChildren(h("p", { class: "muted" }, "No other movie in this library matches."));
        return;
      }
      results.replaceChildren(...found.map((m) => h("div", { class: "fix-choice" },
        h("div", {}, h("a", { class: "link", href: `#/item/${m.id}` }, m.title), m.year ? h("span", { class: "muted" }, ` ${m.year}`) : null,
          m.resolution ? h("span", { class: "faint" }, ` · ${m.resolution}`) : null),
        confirmButton("Merge", "Merge into this one", async () => {
          await api(`/admin/items/${item.id}/merge`, { method: "POST", body: { item_ids: [m.id] } });
          toast(`Merged ${m.title} into this movie`);
          go(`/item/${item.id}`);
        }))));
    } catch (e) {
      results.replaceChildren(h("p", { class: "muted" }, e.message));
    }
  };
  form.addEventListener("submit", (e) => { e.preventDefault(); search(); });
  const toggle = h("button", { class: "btn btn-small", type: "button" }, "Merge with…");
  toggle.addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    if (!panel.hidden && !results.childNodes.length) search();
  });
  return h("div", { class: "merge" }, h("div", { class: "fix-toggle" }, toggle), panel);
}

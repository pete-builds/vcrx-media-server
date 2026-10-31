import { api } from "../api.js";
import { h, when, toast, errorBox } from "../dom.js";
import { state } from "../app.js";

// Server management for admins, like Plex's library and user settings. The
// server allows these routes only to an admin on the local network.

// Libraries: scan now, last scan, history.
export async function librariesTab(body) {
  let timer = null;
  const draw = async () => {
    let libs, scans, unmatched;
    try {
      [libs, scans, unmatched] = await Promise.all([api("/admin/libraries"), api("/admin/scans?limit=15"),
        api("/admin/metadata/unmatched").catch(() => null)]);
    } catch (e) {
      body.replaceChildren(errorBox("Could not load libraries", e.message));
      return;
    }
    const running = libs.libraries.some((l) => l.last_scan && l.last_scan.status === "running");
    clearTimeout(timer);
    if (running) timer = setTimeout(draw, 3000);
    const names = Object.fromEntries(libs.libraries.map((l) => [l.id, l.name]));
    // replaceChildren writes null as the text "null"; drop the empty sections.
    body.replaceChildren(...[
      h("table", { class: "list" },
        h("thead", {}, h("tr", {}, ["Library", "Type", "Titles", "Folder", "Last scan", ""].map((c, i) => h("th", { class: i === 2 ? "num" : "" }, c)))),
        h("tbody", {}, libs.libraries.map((l) => {
          const btn = h("button", { class: "btn btn-small", type: "button", disabled: l.last_scan && l.last_scan.status === "running" }, "Scan now");
          btn.addEventListener("click", async () => {
            btn.disabled = true;
            try {
              await api(`/admin/libraries/${l.id}/scan`, { method: "POST" });
              toast(`Scanning ${l.name}`);
            } catch (e) {
              toast(e.message);
            }
            draw();
          });
          return h("tr", {},
            h("td", {}, h("a", { href: `#/library/${l.id}` }, l.name)),
            h("td", {}, l.kind === "movie" ? "Movies" : "TV"),
            h("td", { class: "num" }, l.item_count),
            h("td", { class: "faint" }, l.root_path),
            h("td", {}, scanSummary(l.last_scan)),
            h("td", {}, btn));
        }))),
      unmatchedSection(unmatched),
      h("section", { class: "section" }, h("h2", {}, "Recent scans"),
        scans.scans.length ? h("table", { class: "list" },
          h("thead", {}, h("tr", {}, ["When", "Library", "Started by", "Result", "Seen", "Added", "Changed", "Missing", "Failed"]
            .map((c, i) => h("th", { class: i >= 4 ? "num" : "" }, c)))),
          h("tbody", {}, scans.scans.map((s) => h("tr", {},
            h("td", {}, when(s.started_at)),
            h("td", {}, names[s.library_id] || s.library_id),
            h("td", {}, s.trigger),
            h("td", { title: s.error || "" }, s.status === "ok" ? "Done" : s.status),
            [s.files_seen, s.files_added, s.files_changed, s.files_missing, s.files_failed].map((n) => h("td", { class: "num" }, n)))))) :
          h("p", { class: "muted" }, "No scans yet."))].filter(Boolean));
  };
  await draw();
  return () => clearTimeout(timer);
}

// Titles TMDb could not match, each a link to its Fix Match panel. Nothing is
// shown when TMDb is off (null) or everything matched.
function unmatchedSection(data) {
  if (!data || !data.unmatched.length) return null;
  return h("section", { class: "section" }, h("h2", {}, `Unmatched titles (${data.unmatched.length})`),
    h("p", { class: "muted" }, "TMDb found no confident match for these, so they have no plot or poster from it. Open one to pick the right title."),
    h("table", { class: "list" },
      h("thead", {}, h("tr", {}, ["Title", "Type", "Why"].map((c) => h("th", {}, c)))),
      h("tbody", {}, data.unmatched.map((u) => h("tr", {},
        h("td", {}, h("a", { class: "link", href: `#/item/${u.id}?fix=1` }, u.year ? `${u.title} (${u.year})` : u.title)),
        h("td", {}, u.kind === "movie" ? "Movie" : "Show"),
        h("td", { class: "muted" }, u.reason))))));
}

function scanSummary(s) {
  if (!s) return h("span", { class: "muted" }, "Never");
  if (s.status === "running") return h("span", { class: "state playing" }, `Scanning since ${when(s.started_at)}`);
  const counts = [s.files_added && `${s.files_added} added`, s.files_changed && `${s.files_changed} changed`,
    s.files_missing && `${s.files_missing} missing`, s.files_failed && `${s.files_failed} failed`].filter(Boolean).join(", ");
  return h("span", { title: s.error || "" }, `${s.status === "ok" ? "" : s.status + ", "}${when(s.finished_at || s.started_at)}${counts ? " · " + counts : ""}`);
}

// Users: who can see what, and add a user. The server enforces every limit
// here on every request; this page only edits them.
export async function usersTab(body) {
  const draw = async () => {
    const [users, libs, labels] = await Promise.all([api("/admin/users"), api("/admin/libraries"), api("/admin/labels")]);
    body.replaceChildren(
      h("div", { class: "user-list" }, users.users.map((u) => userRow(u, libs.libraries, labels.labels, draw))),
      addUserForm(libs.libraries, draw));
  };
  await draw();
}

// Content-rating ceilings: each also admits the TV ratings for the same audience.
const RATINGS = [["", "Any rating"], ["G", "G and TV-G"], ["PG", "Up to PG and TV-PG"], ["PG-13", "Up to PG-13 and TV-14"],
  ["R", "Up to R and TV-MA"], ["NC-17", "Up to NC-17"]];
const RESOLUTIONS = [["", "Any resolution"], ["4K", "Up to 4K"], ["1080p", "Up to 1080p"], ["720p", "Up to 720p"], ["SD", "SD only"]];

function select(label, options, value) {
  return h("select", { "aria-label": label },
    options.map(([v, text]) => h("option", { value: v, selected: (value || "") === v }, text)));
}

// labelChecks offers every label on a title, plus any the policy names that no
// title carries any more, so saving never drops one silently.
function labelChecks(all, chosen) {
  const names = all.map((l) => l.name);
  for (const c of chosen || []) if (!names.some((n) => n.toLowerCase() === c.toLowerCase())) names.push(c);
  const on = new Set((chosen || []).map((c) => c.toLowerCase()));
  return names.map((n) => h("label", { class: "check" }, h("input", { type: "checkbox", value: n, checked: on.has(n.toLowerCase()) }), n));
}

function checked(boxes) {
  return boxes.map((b) => b.querySelector("input")).filter((i) => i.checked).map((i) => i.value);
}

function userRow(u, libraries, labels, redraw) {
  const p = u.policy || {};
  const head = h("div", { class: "user-head" },
    h("strong", {}, u.display_name || u.username),
    u.display_name ? h("span", { class: "muted" }, ` ${u.username}`) : null,
    u.is_admin ? h("span", { class: "pill" }, "admin") : null,
    u.disabled ? h("span", { class: "pill" }, "disabled") : null,
    h("span", { class: "faint" }, ` · since ${new Date(u.created_at).toLocaleDateString()}`));
  const summary = u.summary ? h("p", { class: "user-summary" }, u.summary) : null;
  if (u.is_admin) {
    return h("div", { class: "panel user" }, head, summary);
  }
  const boxes = libraries.map((l) => h("label", { class: "check" },
    h("input", { type: "checkbox", value: String(l.id), checked: u.library_ids.includes(l.id) }), l.name));
  const res = select("Highest resolution", RESOLUTIONS, p.max_resolution);
  const rating = select("Highest content rating", RATINGS, p.max_content_rating);
  const hint = h("p", { class: "faint hint", hidden: !rating.value }, "Titles with no rating are hidden while a rating limit is set.");
  rating.addEventListener("change", () => { hint.hidden = !rating.value; });
  const conv = h("label", { class: "check" }, h("input", { type: "checkbox", checked: p.allow_transcode }), "May use converted streams");
  const conv4k = h("label", { class: "check" }, h("input", { type: "checkbox", checked: p.allow_4k_transcode }), "Including 4K");
  const deny = labelChecks(labels, p.deny_labels);
  const allow = labelChecks(labels, p.allow_labels);
  const labelsBlock = labels.length || deny.length
    ? [h("div", { class: "k" }, "Never show titles labelled"), h("div", { class: "checks" }, deny),
      h("div", { class: "k k-gap" }, "Only show titles labelled (none ticked: no limit)"), h("div", { class: "checks" }, allow)]
    : [h("div", { class: "k" }, "Labels"), h("p", { class: "muted" }, "No labels yet. Tags in your .nfo files become labels at the next scan.")];
  const save = h("button", { class: "btn btn-small btn-primary", type: "button" }, "Save");
  save.addEventListener("click", async () => {
    save.disabled = true;
    try {
      const ids = checked(boxes).map(Number);
      await api(`/admin/users/${u.id}/grants`, { method: "PUT", body: { library_ids: ids } });
      await api(`/admin/users/${u.id}/policy`, { method: "PUT", body: {
        max_resolution: res.value || null, max_content_rating: rating.value || null,
        allow_labels: checked(allow), deny_labels: checked(deny),
        allow_transcode: conv.querySelector("input").checked, allow_4k_transcode: conv4k.querySelector("input").checked,
      } });
      toast(`Saved ${u.username}`);
      redraw();
    } catch (e) {
      toast(e.message);
      save.disabled = false;
    }
  });
  return h("div", { class: "panel user" }, head, summary,
    h("div", { class: "user-grid" },
      h("div", {}, h("div", { class: "k" }, "Libraries"), h("div", { class: "checks" }, boxes)),
      h("div", {}, h("div", { class: "k" }, "Limits"), h("div", { class: "checks" }, res, rating),
        hint,
        h("div", { class: "checks k-gap" }, conv, conv4k)),
      h("div", {}, labelsBlock)),
    h("div", { class: "actions" }, save));
}

function addUserForm(libraries, redraw) {
  const err = h("div", { class: "form-error", role: "alert" });
  const form = h("form", { class: "panel add-user" },
    h("h3", {}, "Add a user"),
    h("div", { class: "user-grid" },
      h("label", { class: "field" }, "Username", h("input", { name: "username", required: true, maxlength: "64", autocomplete: "off" })),
      h("label", { class: "field" }, "Display name", h("input", { name: "display_name", maxlength: "128", autocomplete: "off" })),
      h("label", { class: "field" }, "Password (8 or more characters)", h("input", { name: "password", type: "password", required: true, minlength: "8", autocomplete: "new-password" })),
      h("div", {}, h("div", { class: "k" }, "Libraries"), h("div", { class: "checks" }, libraries.map((l) =>
        h("label", { class: "check" }, h("input", { type: "checkbox", name: "lib", value: String(l.id), checked: true }), l.name))))),
    h("label", { class: "check" }, h("input", { type: "checkbox", name: "is_admin" }), "Admin (sees everything, manages the server)"),
    err,
    h("div", { class: "actions" }, h("button", { class: "btn btn-primary", type: "submit" }, "Add user")));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    const f = form.elements;
    try {
      await api("/admin/users", { method: "POST", body: {
        username: f.username.value.trim(), password: f.password.value, display_name: f.display_name.value.trim() || undefined,
        is_admin: f.is_admin.checked, library_ids: [...form.querySelectorAll('input[name="lib"]:checked')].map((i) => Number(i.value)),
      } });
      toast(`Added ${f.username.value.trim()}`);
      redraw();
    } catch (e2) {
      err.textContent = e2.message;
    }
  });
  return form;
}

export function isAdmin() {
  return !!(state.me && state.me.is_admin);
}

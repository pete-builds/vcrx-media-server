import { api } from "../api.js";
import { h, clock, toast, errorBox, episodeCode } from "../dom.js";
import { go } from "../app.js";

// Intro and credits markers outside the player: the admin editor on an
// episode's page, each person's Skip intro choice, and detection's progress
// for admins.

const SOURCES = { detected: "found by matching the season's audio", chapter: "from a chapter title", nfo: "from the NFO", manual: "set by hand" };

// "1:02.5" or "62.5" or "1:02" to milliseconds; null when it cannot be read.
function parseClock(text) {
  const t = String(text).trim();
  if (!/^\d+(:\d{1,2}){0,2}(\.\d+)?$/.test(t)) return null;
  return Math.round(t.split(":").reduce((acc, p) => acc * 60 + Number(p), 0) * 1000);
}

function fmt(ms) {
  const tenths = Math.floor((ms % 1000) / 100);
  return `${clock(ms - (ms % 1000))}${tenths ? "." + tenths : ""}`;
}

// markerEditor is the admin panel on an episode page: set or clear each
// marker, copy one to the whole season by its audio, or detect the season
// again.
export function markerEditor(item) {
  const list = item.markers || [];
  const body = h("div", {});
  const rows = ["intro", "credits"].map((kind) => {
    const m = list.find((x) => x.kind === kind);
    const start = h("input", { value: m ? fmt(m.start_ms) : "", placeholder: "start", "aria-label": `${kind} start` });
    const end = h("input", { value: m ? fmt(m.end_ms) : "", placeholder: "end", "aria-label": `${kind} end` });
    const save = h("button", { class: "btn btn-small btn-primary", type: "button" }, "Save");
    const clear = h("button", { class: "btn btn-small", type: "button", disabled: !m }, "Clear");
    const copy = h("button", { class: "btn btn-small", type: "button", disabled: !m, title: "Find this stretch in the other episodes by its audio" }, "Apply to season");
    const act = async (btn, fn, done) => {
      btn.disabled = true;
      try {
        await fn();
        toast(done);
        go(`/item/${item.id}`);
      } catch (e) {
        toast(e.message);
        btn.disabled = false;
      }
    };
    save.addEventListener("click", () => {
      const s = parseClock(start.value), e = parseClock(end.value);
      if (s === null || e === null || e <= s) { toast("Times look like 1:05 or 1:05.5, with the end after the start."); return; }
      act(save, () => api(`/admin/items/${item.id}/markers/${kind}`, { method: "PUT", body: { start_ms: s, end_ms: e } }), `Saved the ${kind}`);
    });
    clear.addEventListener("click", () => act(clear, () => api(`/admin/items/${item.id}/markers/${kind}`, { method: "DELETE" }), `Cleared the ${kind}; detection will leave it alone`));
    copy.addEventListener("click", () => act(copy, () => api(`/admin/items/${item.id}/markers/${kind}/apply-to-season`, { method: "POST" }),
      "Copying to the season; reload the other episodes in a minute"));
    const note = m ? `${SOURCES[m.source] || m.source}${m.source === "detected" ? `, confidence ${Math.round(m.confidence * 100)}%` : ""}` : "None";
    return h("div", { class: "marker-row" }, h("strong", {}, kind === "intro" ? "Intro" : "Credits"), start, "to", end, save, clear, copy,
      h("span", { class: "faint" }, note));
  });
  const again = h("button", { class: "btn btn-small", type: "button" }, "Detect the season again");
  again.addEventListener("click", async () => {
    again.disabled = true;
    try {
      await api(`/admin/items/${item.id}/markers/detect`, { method: "POST" });
      toast("Detecting now; markers you set or cleared stay");
    } catch (e) {
      toast(e.message);
      again.disabled = false;
    }
  });
  body.append(...rows, h("div", { class: "actions" }, again));
  return h("section", { class: "section panel markers-edit" }, h("h3", {}, "Skip markers"),
    h("p", { class: "muted" }, "Times from the start of the file. What you set or clear here is never replaced by detection."), body);
}

// skipSetting is the account-page control for this person's own choice.
export function skipSetting() {
  const sel = h("select", { "aria-label": "Skip intros" },
    h("option", { value: "button" }, "Show a Skip button"),
    h("option", { value: "auto" }, "Skip automatically"),
    h("option", { value: "off" }, "Off"));
  sel.disabled = true;
  api("/me/playback-settings").then((s) => { sel.value = s.skip_markers; sel.disabled = false; }).catch((e) => toast(e.message));
  sel.addEventListener("change", async () => {
    try {
      await api("/me/playback-settings", { method: "PUT", body: { skip_markers: sel.value } });
      toast("Saved");
    } catch (e) {
      toast(e.message);
    }
  });
  return h("section", { class: "section" }, h("h2", {}, "Skip intros"), sel,
    h("p", { class: "muted" }, "Automatic skipping acts only when VCRX is sure where the intro is; otherwise the button shows. Credits always wait for you, so a scene after them is never missed. The TV app uses the same choice."));
}

// markersTab is detection's progress for admins: seasons done, show by show,
// and files it could not read.
export async function markersTab(body) {
  let st;
  try {
    st = await api("/admin/markers");
  } catch (e) {
    body.replaceChildren(errorBox("Could not load detection progress", e.message));
    return null;
  }
  const pct = st.seasons_total ? Math.round((100 * st.seasons_done) / st.seasons_total) : 100;
  const state = !st.enabled ? "Off (detect_markers: false)" : st.in_window ? `Running now (hours ${st.hours})` : `Waiting for its hours (${st.hours})`;
  const parts = [
    h("dl", {},
      h("dt", {}, "Detection"), h("dd", {}, state),
      h("dt", {}, "Seasons done"), h("dd", {}, `${st.seasons_done} of ${st.seasons_total} (${pct}%)`),
      h("dt", {}, "Episodes looked at"), h("dd", {}, `${st.episodes_done} of ${st.episodes_total}`),
      h("dt", {}, "Found"), h("dd", {}, `${st.intros} intros, ${st.credits} credits`),
      h("dt", {}, "Files it could not read"), h("dd", {}, String(st.errors))),
    h("table", { class: "list" },
      h("thead", {}, h("tr", {}, ["Show", "Seasons", "Episodes", "Intros", "Credits", "Errors"].map((c, i) => h("th", { class: i ? "num" : "" }, c)))),
      h("tbody", {}, st.shows.map((s) => h("tr", {},
        h("td", {}, h("a", { class: "link", href: `#/item/${s.show_id}` }, s.title)),
        h("td", { class: "num" }, `${s.seasons_done}/${s.seasons_total}`),
        h("td", { class: "num" }, `${s.episodes_done}/${s.episodes_total}`),
        h("td", { class: "num" }, String(s.intros)),
        h("td", { class: "num" }, String(s.credits)),
        h("td", { class: "num" }, String(s.errors)))))),
  ];
  if (st.recent_errors.length) {
    parts.push(h("h2", {}, "Latest errors"), h("ul", {}, st.recent_errors.map((e) =>
      h("li", {}, h("a", { class: "link", href: `#/item/${e.item_id}` }, `${e.show_title} ${episodeCode(e)}`), ": ", e.error))));
  }
  body.replaceChildren(...parts);
  return null;
}

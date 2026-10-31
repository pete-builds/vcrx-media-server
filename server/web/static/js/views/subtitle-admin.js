import { api } from "../api.js";
import { h, toast, when } from "../dom.js";

// For admins on a movie or episode page: the subtitles downloaded from
// OpenSubtitles for its files, with a Delete button each, and the account's
// quota. Hidden when there is nothing to show or the admin is not on the LAN.
export function downloadedSubtitles(item) {
  const section = h("section", { class: "section sub-admin", hidden: true });
  const versions = item.versions || [];
  const draw = async () => {
    let data;
    try {
      data = await api(`/admin/items/${item.id}/subtitles`);
    } catch {
      section.hidden = true;
      return;
    }
    const list = data.subtitles || [];
    if (!data.enabled && !list.length) {
      section.hidden = true;
      return;
    }
    const rows = list.map((s) => {
      const del = h("button", { class: "btn btn-small", type: "button" }, "Delete");
      del.addEventListener("click", async () => {
        del.disabled = true;
        try {
          await api(`/admin/subtitles/${s.stream_id}`, { method: "DELETE" });
          toast("Subtitle deleted");
          draw();
        } catch (e) {
          toast(e.message);
          del.disabled = false;
        }
      });
      const n = versions.findIndex((v) => v.id === s.version_id);
      const facts = [
        s.language ? s.language.toUpperCase() : "Unknown language",
        versions.length > 1 && n >= 0 ? `version ${n + 1}` : "",
        s.hash_match ? "matches the file" : "",
        s.hearing_impaired ? "SDH" : "",
        s.downloaded_by ? `by ${s.downloaded_by}` : "automatic",
        when(s.created_at),
      ].filter(Boolean).join(" · ");
      return h("div", { class: "sub-admin-row" },
        h("div", {}, h("div", { class: "sub-release" }, s.release || "Downloaded subtitle"), h("div", { class: "faint" }, facts)), del);
    });
    const q = data.quota;
    section.replaceChildren(
      h("h2", {}, "Downloaded subtitles"),
      q && q.known ? h("p", { class: "faint" }, `OpenSubtitles downloads left today: ${q.remaining}${q.allowed ? ` of ${q.allowed}` : ""}`) : null,
      rows.length ? h("div", { class: "sub-admin-list" }, rows) : h("p", { class: "muted" }, "None yet. Search from the player's subtitle menu."));
    section.hidden = false;
  };
  draw();
  return section;
}

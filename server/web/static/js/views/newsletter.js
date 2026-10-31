import { api } from "../api.js";
import { h, when, toast, errorBox } from "../dom.js";

// The weekly newsletter, Tautulli style: what was added that week, posted to
// the webhooks (Discord gets posters). Admin only; the server enforces it too.

const days = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export async function newsletterTab(body) {
  let st;
  try {
    st = await api("/admin/newsletter");
  } catch (e) {
    body.replaceChildren(errorBox("Could not load the newsletter settings", e.message));
    return;
  }
  const draw = () => {
    const enabled = h("input", { type: "checkbox", name: "enabled", checked: st.enabled });
    const weekday = h("select", { name: "weekday", "aria-label": "Day" },
      days.map((d, i) => h("option", { value: i, selected: i === st.weekday }, d)));
    const hour = h("select", { name: "hour", "aria-label": "Hour" },
      Array.from({ length: 24 }, (_, i) => h("option", { value: i, selected: i === st.hour }, `${String(i).padStart(2, "0")}:00`)));
    const form = h("form", { class: "newsletter-form" },
      h("label", { class: "check" }, enabled, "Send the newsletter every week"),
      h("div", { class: "when" }, h("span", {}, "On"), weekday, h("span", {}, "at"), hour,
        h("span", { class: "muted" }, st.time_zone ? `(${st.time_zone})` : "")),
      h("div", {}, h("button", { class: "btn btn-primary", type: "submit" }, "Save")));
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      try {
        st = await api("/admin/newsletter", { method: "PUT",
          body: { enabled: enabled.checked, weekday: Number(weekday.value), hour: Number(hour.value) } });
        toast(st.enabled ? "Newsletter on" : "Newsletter off");
        draw();
      } catch (err) {
        toast(err.message);
      }
    });
    const preview = h("button", { class: "btn", type: "button", disabled: !st.webhooks }, "Send a preview now");
    preview.addEventListener("click", async () => {
      preview.disabled = true;
      try {
        const r = await api("/admin/newsletter/preview", { method: "POST" });
        toast(`Preview sent: ${r.movies} ${r.movies === 1 ? "movie" : "movies"}, ${r.episodes} ${r.episodes === 1 ? "episode" : "episodes"}`);
      } catch (err) {
        toast(err.message);
      }
      preview.disabled = false;
    });
    body.replaceChildren(h("section", { class: "section newsletter" },
      h("h2", {}, "Weekly newsletter"),
      h("p", { class: "muted" }, "Once a week VCRX posts what was added in the last seven days, new movies and new episodes by show, ",
        "to your webhooks. Discord gets a card with a poster for each. Nothing is sent in a week when nothing was added."),
      st.webhooks ? null : h("p", { class: "notice" }, "No webhook is set up yet. Add a Discord or other webhook URL with ",
        h("code", {}, "VCRX_WEBHOOK_URLS"), ", then come back to switch the newsletter on."),
      form,
      h("dl", {},
        h("dt", {}, "Next"), h("dd", {}, st.next_send ? when(st.next_send) : "Off"),
        h("dt", {}, "Last sent"), h("dd", {}, st.last_sent ? when(st.last_sent) : "Never"),
        h("dt", {}, "Webhooks"), h("dd", {}, String(st.webhooks))),
      h("div", { class: "preview" }, preview,
        h("p", { class: "muted" }, "Sends the last seven days now, marked as a preview. It works while the newsletter is off."))));
  };
  draw();
}

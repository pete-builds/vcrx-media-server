import { api } from "../api.js";
import { h, when, toast, errorBox } from "../dom.js";

// The weekly newsletter, Tautulli style: what was added that week, posted to
// the webhooks (Discord gets posters). Admin only; the server enforces it too.

const days = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export async function newsletterTab(body) {
  let st, friends;
  try {
    const [n, u] = await Promise.all([api("/admin/newsletter"), api("/admin/users")]);
    st = n;
    // Admins see every library, so only friend accounts can be the audience.
    friends = u.users.filter((x) => !x.is_admin && !x.disabled);
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
    const audience = h("select", { name: "audience", "aria-label": "Who it is for" },
      h("option", { value: 0, selected: !st.audience_user_id }, "Everyone: only titles every friend can see"),
      friends.map((u) => h("option", { value: u.id, selected: u.id === st.audience_user_id }, `What ${u.username} can see`)));
    const form = h("form", { class: "newsletter-form" },
      h("label", { class: "check" }, enabled, "Send the newsletter every week"),
      h("div", { class: "when" }, h("span", {}, "Lists"), audience),
      h("div", { class: "when" }, h("span", {}, "On"), weekday, h("span", {}, "at"), hour,
        h("span", { class: "muted" }, st.time_zone ? `(${st.time_zone})` : "")),
      h("div", {}, h("button", { class: "btn btn-primary", type: "submit" }, "Save")));
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      try {
        st = await api("/admin/newsletter", { method: "PUT",
          body: { enabled: enabled.checked, weekday: Number(weekday.value), hour: Number(hour.value), audience_user_id: Number(audience.value) } });
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
      h("p", { class: "muted" }, "It lists only what its audience may see, so a library some friends are not granted, ",
        "or a title with a label they are denied, stays out."),
      st.webhooks ? null : h("p", { class: "notice" }, "No webhook takes the newsletter yet. Add a Discord or other webhook URL with ",
        h("code", {}, "VCRX_WEBHOOK_URLS"), ", and if you set ", h("code", {}, "webhook_events"), ", include ", h("code", {}, "newsletter"), "."),
      form,
      h("dl", {},
        h("dt", {}, "Audience"), h("dd", {}, st.audience),
        h("dt", {}, "Next"), h("dd", {}, st.next_send ? when(st.next_send) : "Off"),
        h("dt", {}, "Last sent"), h("dd", {}, st.last_sent ? when(st.last_sent) : "Never"),
        h("dt", {}, "Webhooks"), h("dd", {}, String(st.webhooks))),
      h("div", { class: "preview" }, preview,
        h("p", { class: "muted" }, "Sends the last seven days now, marked as a preview. It works while the newsletter is off."))));
  };
  draw();
}

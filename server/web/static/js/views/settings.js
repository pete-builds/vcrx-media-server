import { signOut } from "../api.js";
import { h, toast } from "../dom.js";
import { state, go } from "../app.js";
import { librariesTab, usersTab } from "./manage.js";
import { newsletterTab } from "./newsletter.js";
import { householdPanel } from "./household.js";
import { devicesTab } from "./devices.js";

export async function settingsView({ match }) {
  const tab = (match && match[1]) || "account";
  const admin = !!(state.me && state.me.is_admin);
  if (admin && tab !== "account") {
    const body = h("div", {});
    const cleanup = await ({ libraries: librariesTab, devices: devicesTab, newsletter: newsletterTab }[tab] || usersTab)(body);
    const household = tab === "users" ? householdPanel(() => usersTab(body)) : null;
    return { el: h("div", { class: "settings wide-settings" }, h("h1", {}, "Settings"), tabs(tab, admin), body, household), title: "Settings", cleanup };
  }
  const me = state.me || {};
  const server = state.server || {};
  const invited = me.kind === "token";
  const out = h("button", { class: "btn", type: "button" }, invited ? "Leave and give up this invite" : "Sign out of this browser");
  out.addEventListener("click", async () => {
    if (invited && !confirm("Leave? This device was invited, so it needs a new invite to come back.")) return;
    try {
      await signOut();
    } catch (e) {
      toast(e.message);
    }
    state.me = null;
    go("/"); // the sign-in form, or "Who's watching?" where the server shows it
  });
  const note = invited ? "This device was invited: it sees only what it was given, and the server's admin can revoke it."
    : server.auto_sign_in ? "This network signs in without a password, so the next visit signs straight back in."
      : "";
  return {
    el: h("div", { class: "settings" },
      h("h1", {}, "Settings"),
      tabs("account", admin),
      h("section", { class: "section" },
        h("dl", {},
          h("dt", {}, "Signed in as"), h("dd", {}, invited ? `${me.display_name || "This device"} (invited)` : `${me.username || ""}${me.is_admin ? " (admin)" : ""}`),
          invited && me.device_expires_at ? [h("dt", {}, "Invite ends"), h("dd", {}, new Date(me.device_expires_at).toLocaleDateString())] : null,
          h("dt", {}, "Server"), h("dd", {}, server.name || "VCRX"),
          h("dt", {}, "Version"), h("dd", {}, server.version || ""),
          h("dt", {}, "API"), h("dd", {}, h("a", { href: "/api/v1/openapi.yaml" }, "OpenAPI description")))),
      h("section", { class: "section" }, out, note ? h("p", { class: "muted" }, note) : null),
      h("section", { class: "section faint" },
        h("p", {}, "VCRX Media Server is free software under the AGPL. Fonts: Inter and Rajdhani, SIL Open Font License 1.1 (",
          h("a", { href: "fonts/licenses/FONTS.txt" }, "details"), ")."))),
    title: "Settings",
  };
}

function tabs(active, admin) {
  if (!admin) return null;
  return h("nav", { class: "tabs", "aria-label": "Settings" },
    [["account", "Account"], ["libraries", "Libraries"], ["users", "Users"], ["devices", "Devices"], ["newsletter", "Newsletter"]]
      .map(([k, label]) => h("a", { href: `#/settings/${k}`, class: k === active ? "active" : "" }, label)));
}

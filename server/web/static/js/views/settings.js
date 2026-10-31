import { signOut } from "../api.js";
import { h, toast } from "../dom.js";
import { state, go } from "../app.js";
import { librariesTab, usersTab } from "./manage.js";
import { homeLayoutTab } from "./homelayout.js";
import { newsletterTab } from "./newsletter.js";
import { householdPanel } from "./household.js";
import { markersTab, skipSetting } from "./markers.js";

export async function settingsView({ match }) {
  const tab = (match && match[1]) || "account";
  const admin = !!(state.me && state.me.is_admin);
  if (tab === "home") {
    const body = h("div", {});
    await homeLayoutTab(body);
    return { el: h("div", { class: "settings" }, h("h1", {}, "Settings"), tabs(tab, admin), body), title: "Customize Home" };
  }
  if (admin && tab !== "account") {
    const body = h("div", {});
    const cleanup = await ({ libraries: librariesTab, newsletter: newsletterTab, markers: markersTab }[tab] || usersTab)(body);
    const household = tab === "users" ? householdPanel(() => usersTab(body)) : null;
    return { el: h("div", { class: "settings wide-settings" }, h("h1", {}, "Settings"), tabs(tab, admin), body, household), title: "Settings", cleanup };
  }
  const me = state.me || {};
  const server = state.server || {};
  const out = h("button", { class: "btn", type: "button" }, "Sign out of this browser");
  out.addEventListener("click", async () => {
    try {
      await signOut();
    } catch (e) {
      toast(e.message);
    }
    state.me = null;
    go("/"); // the sign-in form, or "Who's watching?" where the server shows it
  });
  const note = server.auto_sign_in
    ? "This network signs in without a password, so the next visit signs straight back in."
    : "";
  return {
    el: h("div", { class: "settings" },
      h("h1", {}, "Settings"),
      tabs("account", admin),
      h("section", { class: "section" },
        h("dl", {},
          h("dt", {}, "Signed in as"), h("dd", {}, `${me.username || ""}${me.is_admin ? " (admin)" : ""}`),
          h("dt", {}, "Server"), h("dd", {}, server.name || "VCRX"),
          h("dt", {}, "Version"), h("dd", {}, server.version || ""),
          h("dt", {}, "API"), h("dd", {}, h("a", { href: "/api/v1/openapi.yaml" }, "OpenAPI description")))),
      skipSetting(),
      h("section", { class: "section" }, out, note ? h("p", { class: "muted" }, note) : null),
      h("section", { class: "section faint" },
        h("p", {}, "VCRX Media Server is free software under the AGPL. Fonts: Inter and Rajdhani, SIL Open Font License 1.1 (",
          h("a", { href: "fonts/licenses/FONTS.txt" }, "details"), ")."))),
    title: "Settings",
  };
}

// Everyone has Account and Home; admins also manage libraries, users and the newsletter.
function tabs(active, admin) {
  const all = [["account", "Account"], ["home", "Home"]];
  if (admin) all.push(["libraries", "Libraries"], ["users", "Users"], ["newsletter", "Newsletter"], ["markers", "Intros"]);
  return h("nav", { class: "tabs", "aria-label": "Settings" },
    all.map(([k, label]) => h("a", { href: `#/settings/${k}`, class: k === active ? "active" : "", "aria-current": k === active ? "page" : null }, label)));
}

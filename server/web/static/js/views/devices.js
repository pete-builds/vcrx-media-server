// Settings > Devices and Invites: the invite is the account. An admin invites a
// device with a link or QR code, or approves a TV that shows a pairing code,
// each with a scope (libraries, highest resolution, converting, expiry), and
// can revoke any device on its own. Also the two pages those links open:
// #/invite/<secret> on the invited device, #/pair/<code> on the admin's phone.
// The server enforces every scope; this page only asks for it.

import { api, device, ApiError } from "../api.js";
import { h, when, toast, errorBox } from "../dom.js";
import { state, signedIn, go } from "../app.js";

const RESOLUTIONS = [["", "Any resolution"], ["4K", "Up to 4K"], ["1080p", "Up to 1080p"], ["720p", "Up to 720p"], ["SD", "SD only"]];
const EXPIRY = [[1, "1 day"], [7, "1 week"], [30, "30 days"], [90, "90 days"], [365, "1 year"]];

// scopeForm asks what a device may see and for how long. onSubmit gets the
// scope body the server takes and returns a promise; the form shows its error.
export function scopeForm({ name = "", nameLabel = "Device name", submit = "Create invite", onSubmit }) {
  const err = h("div", { class: "form-error", role: "alert" });
  const libs = (state.libraries || []).map((l) => h("label", { class: "check" },
    h("input", { type: "checkbox", value: String(l.id), checked: true }), l.name));
  const res = h("select", { "aria-label": "Highest resolution" }, RESOLUTIONS.map(([v, t]) => h("option", { value: v, selected: v === "1080p" }, t)));
  const conv = h("input", { type: "checkbox" });
  const expiry = h("select", { "aria-label": "Expires after" }, EXPIRY.map(([v, t]) => h("option", { value: String(v), selected: v === 30 }, t)));
  const button = h("button", { class: "btn btn-primary", type: "submit" }, submit);
  const form = h("form", { class: "scope-form" },
    h("label", { class: "field" }, nameLabel, h("input", { name: "name", maxlength: "64", autocomplete: "off", value: name,
      placeholder: "Mark's TV", required: nameLabel === "Device name" })),
    h("div", {}, h("div", { class: "k" }, "Libraries"), h("div", { class: "checks" }, libs)),
    h("div", {}, h("div", { class: "k" }, "Limits"), h("div", { class: "checks" }, res,
      h("label", { class: "check" }, conv, "May convert files it cannot play"))),
    h("div", {}, h("div", { class: "k" }, "Expires after"), h("div", { class: "checks" }, expiry)),
    err,
    h("div", { class: "actions" }, button));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    const ids = libs.map((b) => b.querySelector("input")).filter((i) => i.checked).map((i) => Number(i.value));
    if (!ids.length) {
      err.textContent = "Choose at least one library.";
      return;
    }
    button.disabled = true;
    try {
      await onSubmit({ name: form.elements.name.value.trim(), library_ids: ids, max_resolution: res.value,
        allow_transcode: conv.checked, expires_in_days: Number(expiry.value) });
    } catch (e2) {
      err.textContent = e2.message;
    }
    button.disabled = false;
  });
  return form;
}

function qrImage(b64, label) {
  return h("img", { class: "qr", src: `data:image/png;base64,${b64}`, alt: label, width: "220", height: "220" });
}

// The invite, shown once: the server keeps only a hash of it.
function inviteCard(out, onDone) {
  const link = h("input", { class: "invite-link", readonly: true, value: out.url, "aria-label": "Invite link" });
  link.addEventListener("focus", () => link.select());
  const copy = h("button", { class: "btn btn-small", type: "button" }, "Copy link");
  copy.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(out.url);
      toast("Link copied");
    } catch {
      link.select();
      toast("Select the link and copy it");
    }
  });
  const done = h("button", { class: "btn btn-small", type: "button" }, "Done");
  done.addEventListener("click", onDone);
  return h("div", { class: "panel invite-card" },
    h("h3", {}, `Invite for ${out.device.name}`),
    h("p", { class: "muted" }, "Scan the code with the device's camera, or send it the link. It works once, on one device. ",
      "This is the only time it is shown: VCRX keeps no copy."),
    qrImage(out.qr_png, `QR code of the invite for ${out.device.name}`),
    h("div", { class: "invite-row" }, link, copy),
    h("p", { class: "faint" }, out.device.summary, ` Expires ${new Date(out.device.scope.expires_at).toLocaleDateString()}.`),
    h("div", { class: "actions" }, done));
}

const stateNames = { pending: "Not used yet", active: "Active", revoked: "Revoked", expired: "Expired" };

function deviceRow(d, redraw) {
  const live = d.state === "pending" || d.state === "active";
  const revoke = live ? h("button", { class: "btn btn-small btn-danger", type: "button" }, d.state === "pending" ? "Cancel invite" : "Revoke") : null;
  if (revoke) {
    revoke.addEventListener("click", async () => {
      if (!confirm(`${d.state === "pending" ? "Cancel the invite for" : "Revoke"} ${d.name}? It stops working at once.`)) return;
      try {
        const r = await api(`/admin/devices/${d.id}`, { method: "DELETE" });
        toast(r.streams_stopped ? `Revoked ${d.name} and stopped its stream` : `Revoked ${d.name}`);
      } catch (e) {
        toast(e.message);
      }
      redraw();
    });
  }
  const what = [d.device_name, d.platform && d.platform.replace(/_/g, " ")].filter(Boolean).join(" · ");
  const seen = d.last_seen_at ? `Last seen ${when(d.last_seen_at)}${d.last_ip ? " from " + d.last_ip : ""}` : "Never seen";
  return h("div", { class: "panel device-row", "data-id": String(d.id), "data-state": d.state },
    h("div", { class: "device-head" },
      h("strong", {}, d.name),
      h("span", { class: `pill device-state ${d.state}` }, stateNames[d.state] || d.state),
      h("span", { class: "faint" }, d.source === "pairing" ? "Paired TV" : "Invite link"),
      revoke),
    h("p", { class: "user-summary" }, d.summary),
    h("p", { class: "faint" }, [what, live ? seen : null,
      d.state === "revoked" ? `Revoked ${when(d.revoked_at)}${d.revoked_by ? " by " + d.revoked_by : ""}` : `Expires ${when(d.scope.expires_at)}`]
      .filter(Boolean).join(" · ")));
}

// devicesTab is the Settings tab.
export async function devicesTab(body) {
  const list = h("div", { class: "device-list" });
  const minted = h("div", {});
  const drawList = async () => {
    try {
      const data = await api("/admin/devices");
      list.replaceChildren(data.devices.length ? h("div", { class: "device-rows" }, data.devices.map((d) => deviceRow(d, drawList)))
        : h("p", { class: "muted" }, "No devices invited yet."));
    } catch (e) {
      list.replaceChildren(errorBox("Could not load devices", e.message));
    }
  };
  const form = scopeForm({
    onSubmit: async (scope) => {
      const out = await api("/admin/devices", { method: "POST", body: scope });
      minted.replaceChildren(inviteCard(out, () => minted.replaceChildren()));
      form.reset();
      minted.scrollIntoView({ block: "nearest" });
      drawList();
    },
  });
  const code = h("input", { name: "code", placeholder: "BCDF-GHJK", maxlength: "12", autocomplete: "off", autocapitalize: "characters",
    "aria-label": "Pairing code on the TV", class: "pair-code-input" });
  const pair = h("form", { class: "pair-form" }, code, h("button", { class: "btn", type: "submit" }, "Find TV"));
  pair.addEventListener("submit", (e) => {
    e.preventDefault();
    const c = code.value.replace(/[^a-z]/gi, "").toUpperCase();
    if (c) go(`/pair/${c}`);
  });
  body.replaceChildren(
    h("section", { class: "section devices" },
      h("h2", {}, "Invite a device"),
      h("p", { class: "muted" }, "An invite is the account: the device that opens it signs in with no username or password, ",
        "sees only what you choose here, never the household's profiles, and can be revoked on its own. It streams; it cannot download."),
      h("div", { class: "panel" }, form),
      minted),
    h("section", { class: "section" },
      h("h2", {}, "Pair a TV"),
      h("p", { class: "muted" }, "On the TV, choose “Pair with a phone” and scan its QR code with this phone, or type the code it shows."),
      pair),
    h("section", { class: "section" }, h("h2", {}, "Devices"), list));
  await drawList();
}

// #/pair/<code>: the admin's phone approving a TV.
export async function pairView({ match }) {
  const code = match[1];
  if (!state.me || !state.me.is_admin) {
    return { el: errorBox("Admins only", "Only an admin, on the local network, can approve a TV."), title: "Pair a TV" };
  }
  const root = h("div", { class: "settings pair-page" });
  let p;
  try {
    p = await api(`/admin/pairings/${encodeURIComponent(code)}`);
  } catch (e) {
    root.replaceChildren(h("h1", {}, "Pair a TV"), errorBox("No TV is waiting with that code", e.message,
      h("a", { class: "btn", href: "#/settings/devices" }, "Devices")));
    return { el: root, title: "Pair a TV" };
  }
  const tvName = p.device_name || "A TV";
  const done = (msg) => root.replaceChildren(h("h1", {}, "Pair a TV"), h("div", { class: "panel" }, h("p", {}, msg)),
    h("div", { class: "actions" }, h("a", { class: "btn btn-primary", href: "#/settings/devices" }, "Devices")));
  const deny = h("button", { class: "btn", type: "button" }, "Turn it away");
  deny.addEventListener("click", async () => {
    try {
      await api(`/admin/pairings/${encodeURIComponent(code)}`, { method: "DELETE" });
      done(`${tvName} was turned away.`);
    } catch (e) {
      toast(e.message);
    }
  });
  root.replaceChildren(
    h("h1", {}, "Pair a TV"),
    h("div", { class: "panel pair-who" },
      h("p", { class: "pair-code" }, p.user_code),
      h("p", {}, h("strong", {}, tvName), [p.device_model, p.client].filter(Boolean).length ? ` (${[p.device_model, p.client].filter(Boolean).join(", ")})` : ""),
      h("p", { class: "faint" }, `Asked ${when(p.created_at)} from ${p.ip}${p.location ? " (" + p.location + ")" : ""}. `,
        "Check the code matches the one on the TV before you approve.")),
    h("div", { class: "panel" }, scopeForm({ name: p.device_name || "", nameLabel: "Name it", submit: "Approve this TV",
      onSubmit: async (scope) => {
        const out = await api(`/admin/pairings/${encodeURIComponent(code)}/approve`, { method: "POST", body: scope });
        done(`${out.device.name} is paired. It signs in on its own within a few seconds.`);
      } })),
    h("div", { class: "actions" }, deny));
  return { el: root, title: "Pair a TV" };
}

// #/invite/<secret>: the invited device. Joining replaces whatever this
// browser was signed in as, so it asks first; a link preview never spends it.
export async function inviteView({ match }) {
  const secret = match[1];
  const server = state.server || {};
  const root = h("div", { class: "picker invite-page" });
  const err = h("p", { class: "form-error", role: "alert" });
  const join = h("button", { class: "btn btn-primary", type: "button" }, "Join");
  join.addEventListener("click", async () => {
    join.disabled = true;
    err.textContent = "";
    try {
      await api("/invites/redeem", { method: "POST", body: { invite: secret, device: device(), cookie: true } });
      // The secret is spent; keep it out of history and the address bar.
      history.replaceState(null, "", location.pathname + "#/");
      await signedIn();
      go("/");
    } catch (e) {
      err.textContent = e instanceof ApiError ? e.message : "Could not reach the server.";
      join.disabled = false;
    }
  });
  root.replaceChildren(
    h("img", { class: "picker-mark", src: "img/wordmark.png", alt: "VCRX", width: "160", height: "36" }),
    h("h1", {}, "You're invited"),
    h("p", { class: "muted" }, `${server.name || "A VCRX server"} has invited this device. Join to watch what it shares with you: `,
      "no account, no password. The invite works once."),
    state.me ? h("p", { class: "faint" }, `This browser is signed in as ${state.me.display_name || state.me.username}. Joining signs it in as the invited device instead.`) : null,
    join, err);
  return { el: root, title: "Invite" };
}

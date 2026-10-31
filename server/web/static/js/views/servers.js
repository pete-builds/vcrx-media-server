// Settings > Friends: linking with friends' servers (milestone 9, item 3).
// Two directions, each revocable from either end:
// - Watch a friend's server: paste the server invite link their admin sent.
//   Their titles then show on Home, badged with where they live, and play
//   from their server through this one. Nothing is copied.
// - Share with a friend's server: mint a server invite with a scope, like a
//   device's, plus a bandwidth cap for everything that server streams at once.
// The servers enforce all of it; this page only asks.

import { api } from "../api.js";
import { h, when, toast, errorBox } from "../dom.js";
import { scopeForm } from "./devices.js";

const BANDWIDTH = [[0, "No bandwidth cap"], [2000, "At most 2 Mbit/s"], [5000, "At most 5 Mbit/s"], [10000, "At most 10 Mbit/s"],
  [20000, "At most 20 Mbit/s"], [40000, "At most 40 Mbit/s"]];
const STATES = { online: "Online", offline: "Offline", bad_answer: "Answering badly", unlinked: "Ended the link", unknown: "Not checked yet" };
const DEVICE_STATES = { pending: "Not linked yet", active: "Linked", revoked: "Revoked", expired: "Expired" };

function peerRow(p, redraw) {
  const unlink = h("button", { class: "btn btn-small btn-danger", type: "button" }, "Unlink");
  unlink.addEventListener("click", async () => {
    if (!confirm(`Unlink ${p.name}? Its titles leave Home and anything playing from it stops now.`)) return;
    try {
      const r = await api(`/admin/peers/${p.id}`, { method: "DELETE" });
      toast(r.peer_notified ? `Unlinked ${p.name}` : `Unlinked ${p.name}; it could not be told, so its admin may still list the link`);
    } catch (e) {
      toast(e.message);
    }
    redraw();
  });
  const seen = p.last_ok_at ? `Last answered ${when(p.last_ok_at)}` : "Has not answered yet";
  return h("div", { class: "panel device-row peer-row", "data-id": String(p.id), "data-state": p.state },
    h("div", { class: "device-head" },
      h("strong", {}, p.name),
      h("span", { class: `pill device-state peer-${p.state}` }, STATES[p.state] || p.state),
      unlink),
    h("p", { class: "faint" }, [p.address, p.remote_name && p.remote_name !== p.name ? `calls itself ${p.remote_name}` : null, seen,
      `Link ends ${when(p.expires_at)}`].filter(Boolean).join(" · ")),
    p.last_error && p.state !== "online" ? h("p", { class: "muted" }, p.last_error) : null);
}

function linkedRow(d, redraw) {
  const live = d.state === "pending" || d.state === "active";
  const revoke = live ? h("button", { class: "btn btn-small btn-danger", type: "button" }, d.state === "pending" ? "Cancel invite" : "Revoke") : null;
  if (revoke) {
    revoke.addEventListener("click", async () => {
      if (!confirm(`${d.state === "pending" ? "Cancel the server invite for" : "Revoke"} ${d.name}? It stops working at once.`)) return;
      try {
        const r = await api(`/admin/devices/${d.id}`, { method: "DELETE" });
        toast(r.streams_stopped ? `Revoked ${d.name} and stopped its streams` : `Revoked ${d.name}`);
      } catch (e) {
        toast(e.message);
      }
      redraw();
    });
  }
  const seen = d.last_seen_at ? `Last seen ${when(d.last_seen_at)}${d.last_ip ? " from " + d.last_ip : ""}` : "Never seen";
  return h("div", { class: "panel device-row", "data-id": String(d.id), "data-state": d.state },
    h("div", { class: "device-head" },
      h("strong", {}, d.name),
      h("span", { class: `pill device-state ${d.state}` }, DEVICE_STATES[d.state] || d.state),
      revoke),
    h("p", { class: "user-summary" }, d.summary),
    h("p", { class: "faint" }, [d.device_name ? `Calls itself ${d.device_name}` : null, live ? seen : null].filter(Boolean).join(" · ")));
}

// The server invite, shown once: the server keeps only a hash of it.
function inviteCard(out, onDone) {
  const link = h("input", { class: "invite-link", readonly: true, value: out.url, "aria-label": "Server invite link" });
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
    h("h3", {}, `Server invite for ${out.device.name}`),
    h("p", { class: "muted" }, "Send this link to your friend. Their admin pastes it into Settings, Friends on their VCRX. ",
      "It works once, for one server. This is the only time it is shown: VCRX keeps no copy."),
    h("div", { class: "invite-row" }, link, copy),
    h("p", { class: "faint" }, out.device.summary, ` Expires ${new Date(out.device.scope.expires_at).toLocaleDateString()}.`),
    h("div", { class: "actions" }, done));
}

export async function friendsTab(body) {
  const watching = h("div", { class: "device-list" });
  const sharing = h("div", { class: "device-list" });
  const minted = h("div", {});
  const draw = async () => {
    try {
      const data = await api("/admin/peers");
      watching.replaceChildren(data.peers.length ? h("div", { class: "device-rows" }, data.peers.map((p) => peerRow(p, draw)))
        : h("p", { class: "muted" }, "No friends' servers linked yet."));
      sharing.replaceChildren(data.linked_servers.length ? h("div", { class: "device-rows" }, data.linked_servers.map((d) => linkedRow(d, draw)))
        : h("p", { class: "muted" }, "No friend's server is linked to this one."));
    } catch (e) {
      watching.replaceChildren(errorBox("Could not load friends' servers", e.message));
      sharing.replaceChildren();
    }
  };

  // Watch: paste their invite.
  const err = h("div", { class: "form-error", role: "alert" });
  const invite = h("input", { name: "invite", required: true, autocomplete: "off", placeholder: "http://192.168.1.20:3750/#/link/…",
    "aria-label": "Server invite link" });
  const address = h("input", { name: "address", autocomplete: "off", placeholder: "Optional: their address as this server reaches it",
    "aria-label": "Their address" });
  const name = h("input", { name: "name", maxlength: "64", autocomplete: "off", placeholder: "Optional: Mark's server", "aria-label": "Name" });
  const linkBtn = h("button", { class: "btn btn-primary", type: "submit" }, "Link");
  const linkForm = h("form", { class: "scope-form link-form" },
    h("label", { class: "field" }, "Server invite link", invite),
    h("label", { class: "field" }, "Address", address),
    h("label", { class: "field" }, "Call it", name),
    err, h("div", { class: "actions" }, linkBtn));
  linkForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    linkBtn.disabled = true;
    try {
      const out = await api("/admin/peers", { method: "POST", body: { invite: invite.value.trim(), address: address.value.trim(), name: name.value.trim() } });
      toast(`Linked ${out.peer.name}. Its titles show on Home.`);
      linkForm.reset();
      draw();
    } catch (e2) {
      err.textContent = e2.message;
    }
    linkBtn.disabled = false;
  });

  // Share: mint a server invite.
  const cap = h("select", { "aria-label": "Bandwidth cap" }, BANDWIDTH.map(([v, t]) => h("option", { value: String(v), selected: v === 10000 }, t)));
  const myAddress = h("input", { autocomplete: "off", placeholder: `Optional, if not ${location.host}` });
  const form = scopeForm({
    nameLabel: "Their server's name", submit: "Create server invite",
    onSubmit: async (scope) => {
      const out = await api("/admin/peers/invites", { method: "POST",
        body: { ...scope, name: scope.name || "Friend's server", bandwidth_kbps: Number(cap.value), address: myAddress.value.trim() } });
      minted.replaceChildren(inviteCard(out, () => minted.replaceChildren()));
      form.reset();
      minted.scrollIntoView({ block: "nearest" });
      draw();
    },
  });
  const errBox = form.querySelector(".form-error");
  form.insertBefore(h("div", {}, h("div", { class: "k" }, "Bandwidth"), h("div", { class: "checks" }, cap)), errBox);
  form.insertBefore(h("label", { class: "field address-field" }, "This server's address for them", myAddress), errBox);

  body.replaceChildren(
    h("section", { class: "section devices" },
      h("h2", {}, "Watch a friend's server"),
      h("p", { class: "muted" }, "Paste the server invite link a friend sent you. Their titles show on Home with a badge saying whose they are, ",
        "and play from their server through this one: nothing is copied here. Only this server's household sees them. ",
        "Their server must be reachable from this one, on the LAN or over Tailscale."),
      h("div", { class: "panel" }, linkForm)),
    h("section", { class: "section" }, h("h2", {}, "Servers you watch"), watching),
    h("section", { class: "section" },
      h("h2", {}, "Share with a friend's server"),
      h("p", { class: "muted" }, "A server invite is like a device's: their server sees only what you choose here, streams and never downloads, ",
        "and you can revoke it on its own. The bandwidth cap covers everything it streams at once."),
      h("div", { class: "panel" }, form),
      minted),
    h("section", { class: "section" }, h("h2", {}, "Servers watching yours"), sharing));
  await draw();
}

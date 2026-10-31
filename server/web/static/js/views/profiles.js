// "Who's watching?": the household profile picker, full screen, with a PIN pad.
// The server decides who may switch to whom (a PIN, or an admin's password
// from a restricted profile); each profile's `needs` says what to ask for.
// Switching replaces this browser's session cookie with the new profile's.

import { api, device, ApiError } from "../api.js";
import { h } from "../dom.js";
import { state, signedIn, go } from "../app.js";

export function initials(name) {
  const words = String(name || "?").trim().split(/\s+/).filter(Boolean);
  const letters = words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] || "?").slice(0, 2);
  return letters.toUpperCase();
}

// avatar draws a profile's picture, or its initials on its colour.
export function avatar(p, cls = "avatar") {
  const el = h("span", { class: cls, style: { background: p.color || "#333" }, "aria-hidden": "true" }, initials(p.name));
  if (p.avatar_url) {
    const img = h("img", { src: p.avatar_url, alt: "", decoding: "async" });
    img.addEventListener("error", () => img.remove(), { once: true });
    el.append(img);
  }
  return el;
}

async function switchTo(p, cred) {
  await api("/auth/switch", { method: "POST", body: { user_id: p.id, ...cred, device: device(), cookie: true } });
  await signedIn();
}

export async function pickerView({ params }) {
  const next = params.get("next") || "/";
  const data = await api("/profiles");
  const signedInNow = !!state.me;
  const root = h("div", { class: "picker" });
  const server = state.server || {};

  const grid = () => {
    const buttons = data.profiles.map((p) => {
      const b = h("button", { class: "profile" + (p.id === data.current_id ? " current" : ""), type: "button", "data-id": String(p.id) },
        avatar(p, "avatar avatar-big"),
        h("span", { class: "profile-name" }, p.name),
        p.kid || p.needs !== "none" ? h("span", { class: "profile-tags" },
          p.kid ? h("span", { class: "profile-tag" }, "Kids") : null,
          p.needs !== "none" ? h("span", { class: "profile-lock", title: "Needs a PIN or password" }, p.needs === "pin" ? "PIN" : "Password") : null) : null);
      b.addEventListener("click", () => choose(p));
      return b;
    });
    root.replaceChildren(
      h("img", { class: "picker-mark", src: "img/wordmark.png", alt: "VCRX", width: "160", height: "36" }),
      h("h1", {}, "Who's watching?"),
      h("div", { class: "profiles" }, buttons),
      signedInNow ? h("div", { class: "picker-actions" }, h("a", { class: "btn", href: "#" + next }, "Cancel")) :
        h("p", { class: "muted picker-note" }, `${server.name || "VCRX"} on this network`));
    const focus = buttons.find((b) => Number(b.dataset.id) === data.focus_id) || buttons[0];
    if (focus) focus.focus();
  };

  const done = () => go(next === "/profiles" ? "/" : next);

  const choose = async (p) => {
    if (p.needs === "none") {
      try {
        await switchTo(p, {});
        done();
      } catch (e) {
        if (e instanceof ApiError && e.code === "pin_required") showPad(p, "pin");
        else if (e instanceof ApiError && e.code === "credential_required") showPad(p, "password");
        else showError(e);
      }
      return;
    }
    showPad(p, p.needs);
  };

  const showError = (e) => {
    root.append(h("p", { class: "form-error", role: "alert" }, e instanceof ApiError ? e.message : "The server could not be reached."));
  };

  const showPad = (p, mode) => {
    root.replaceChildren(pinPad(p, mode, {
      submit: async (cred) => {
        await switchTo(p, cred);
        done();
      },
      back: grid,
    }));
  };

  grid();
  return { el: root, title: "Who's watching?" };
}

// pinPad asks for a profile's PIN on an on-screen pad (digits work from the
// keyboard too), or for a password when that is all the profile takes.
function pinPad(p, mode, { submit, back }) {
  let pin = "";
  const err = h("p", { class: "form-error pin-error", role: "alert" });
  const dots = h("div", { class: "pin-dots", "aria-live": "polite", "aria-label": "PIN digits entered" });
  const drawDots = () => {
    dots.replaceChildren(...Array.from({ length: Math.max(4, pin.length) }, (_, i) => h("i", { class: i < pin.length ? "on" : "" })));
  };
  let busy = false;
  const send = async (cred) => {
    if (busy) return;
    busy = true;
    err.textContent = "";
    try {
      await submit(cred);
    } catch (e) {
      busy = false;
      pin = "";
      drawDots();
      if (pwField) pwField.value = "";
      err.textContent = e instanceof ApiError ? e.message : "The server could not be reached.";
    }
  };
  const press = (d) => {
    err.textContent = "";
    if (d === "back") pin = pin.slice(0, -1);
    else if (pin.length < 8) pin += d;
    drawDots();
    if (pin.length === 8) send({ pin });
  };

  let pwField = null;
  let body;
  if (mode === "password") {
    pwField = h("input", { type: "password", name: "password", autocomplete: "current-password", "aria-label": `Password for ${p.name}`, required: true });
    body = h("form", { class: "pin-password" },
      h("label", { class: "field" }, "Password", pwField),
      h("button", { class: "btn btn-primary", type: "submit" }, "Continue"));
    body.addEventListener("submit", (e) => {
      e.preventDefault();
      if (pwField.value) send({ password: pwField.value });
    });
    setTimeout(() => pwField.focus(), 0);
  } else {
    const keys = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "back", "0", "ok"].map((k) => {
      const label = k === "back" ? "Delete" : k === "ok" ? "OK" : k;
      const b = h("button", { class: "pin-key" + (k === "ok" ? " pin-ok" : ""), type: "button", "data-key": k, "aria-label": label }, k === "back" ? "⌫" : label);
      b.addEventListener("click", () => (k === "ok" ? pin.length >= 4 && send({ pin }) : press(k)));
      return b;
    });
    body = h("div", { class: "pin-keys" }, keys);
    drawDots();
  }
  const el = h("div", { class: "pin-pad", tabindex: "-1" },
    avatar(p, "avatar avatar-big"),
    h("h2", {}, p.name),
    h("p", { class: "muted" }, mode === "password" ? `Enter ${p.name}'s password` : `Enter ${p.name}'s PIN`),
    mode === "password" ? null : dots,
    err,
    body,
    h("button", { class: "btn picker-back", type: "button" }, "Back"));
  el.querySelector(".picker-back").addEventListener("click", back);
  if (mode !== "password") {
    el.addEventListener("keydown", (e) => {
      if (/^[0-9]$/.test(e.key)) press(e.key);
      else if (e.key === "Backspace") press("back");
      else if (e.key === "Enter" && pin.length >= 4) send({ pin });
      else if (e.key === "Escape") back();
      else return;
      e.preventDefault();
    });
    setTimeout(() => el.focus(), 0);
  }
  return el;
}

// drawProfileButton shows the top bar's "Switch profile" button for household
// members: the signed-in profile's avatar, linking to the picker.
export async function drawProfileButton() {
  const btn = document.getElementById("nav-profile");
  if (!btn) return;
  btn.hidden = true;
  // An invited device is its own principal: no profiles to switch to.
  if (!state.me || state.me.kind === "token") return;
  let data;
  try {
    data = await api("/profiles");
  } catch {
    return; // not in the household (403), or an older server
  }
  const me = data.profiles.find((p) => p.id === data.current_id);
  if (!me || data.profiles.length < 2) return;
  btn.replaceChildren(avatar(me, "avatar avatar-small"));
  btn.title = `${me.name}: switch profile`;
  btn.hidden = false;
}

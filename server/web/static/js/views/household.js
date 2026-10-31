// Settings > Users > Household: who appears in "Who's watching?", their PINs,
// kids' profiles and their rating ceilings, and profile pictures. Admin only;
// the server enforces every one of these on every request.

import { api, ApiError } from "../api.js";
import { h, toast, errorBox } from "../dom.js";
import { avatar } from "./profiles.js";

// Rating ceilings a profile may have; each admits the TV ratings for the same audience.
const RATINGS = [["", "Any rating"], ["G", "G and TV-G"], ["PG", "Up to PG and TV-PG"], ["PG-13", "Up to PG-13 and TV-14"],
  ["R", "Up to R and TV-MA"], ["NC-17", "Up to NC-17"]];

function ratingSelect(value, label) {
  return h("select", { "aria-label": label }, RATINGS.map(([v, text]) => h("option", { value: v, selected: (value || "") === v }, text)));
}

// householdPanel draws the section; onChange redraws the user list above it,
// which a new profile joins.
export function householdPanel(onChange) {
  const el = h("section", { class: "section household" });
  const draw = async () => {
    let data;
    try {
      data = await api("/admin/profiles");
    } catch (e) {
      el.replaceChildren(errorBox("Could not load profiles", e.message));
      return;
    }
    const redraw = async () => {
      await draw();
      if (onChange) await onChange();
    };
    el.replaceChildren(
      h("h2", {}, "Household"),
      h("p", { class: "muted" }, "Household members share this server's screens. Each has their own Continue Watching and watched state, ",
        "and anyone in the household can switch to their profile from “Who's watching?”, with its PIN if it has one. ",
        "An admin's profile always asks for its PIN or password when reached from someone else's."),
      h("div", { class: "profile-rows" }, data.profiles.filter((p) => !p.disabled).map((p) => profileRow(p, redraw))),
      newProfileForm(redraw));
  };
  draw();
  return el;
}

function profileRow(p, redraw) {
  const member = h("input", { type: "checkbox", checked: p.household });
  member.addEventListener("change", () => save({ household: member.checked }, member.checked ? `${p.name} joined the household` : `${p.name} left the household`));
  const save = async (body, msg) => {
    try {
      await api(`/admin/profiles/${p.id}`, { method: "PUT", body });
      toast(msg);
    } catch (e) {
      toast(e.message);
    }
    redraw();
  };

  const pinInput = h("input", { type: "password", inputmode: "numeric", pattern: "[0-9]{4,8}", maxlength: "8", autocomplete: "new-password",
    placeholder: p.has_pin ? "New PIN" : "4 to 8 digits", "aria-label": `PIN for ${p.name}`, class: "pin-input" });
  const setPin = h("button", { class: "btn btn-small", type: "button" }, p.has_pin ? "Change PIN" : "Set PIN");
  setPin.addEventListener("click", async () => {
    try {
      await api(`/admin/profiles/${p.id}/pin`, { method: "PUT", body: { pin: pinInput.value } });
      toast(`PIN set for ${p.name}`);
      redraw();
    } catch (e) {
      toast(e.message);
    }
  });
  const clearPin = p.has_pin ? h("button", { class: "btn btn-small", type: "button" }, "Remove PIN") : null;
  if (clearPin) {
    clearPin.addEventListener("click", async () => {
      await api(`/admin/profiles/${p.id}/pin`, { method: "DELETE" }).catch((e) => toast(e.message));
      redraw();
    });
  }

  let ratingBlock = null;
  if (!p.is_admin) {
    const kid = h("input", { type: "checkbox", checked: p.kid });
    kid.addEventListener("change", () => save({ kid: kid.checked }, kid.checked ? `${p.name} is a kid's profile` : `${p.name} is no longer a kid's profile`));
    const rating = ratingSelect(p.max_content_rating, `Highest content rating for ${p.name}`);
    rating.addEventListener("change", () => save({ max_content_rating: rating.value }, `Saved ${p.name}'s rating limit`));
    ratingBlock = h("div", { class: "checks" }, h("label", { class: "check" }, kid, "Kid's profile"), rating);
  }

  const file = h("input", { type: "file", accept: "image/jpeg,image/png,image/gif", hidden: true });
  const pick = h("button", { class: "btn btn-small", type: "button" }, p.avatar_url ? "Change picture" : "Add picture");
  pick.addEventListener("click", () => file.click());
  file.addEventListener("change", async () => {
    const f = file.files && file.files[0];
    if (!f) return;
    try {
      // A raw image body, not JSON; the header is the CSRF rule's.
      const r = await fetch(`/api/v1/admin/profiles/${p.id}/avatar`, { method: "PUT", body: f, credentials: "same-origin",
        headers: { "X-VCRX-Client": "web", "Content-Type": f.type || "application/octet-stream" } });
      if (!r.ok) {
        const data = await r.json().catch(() => null);
        throw new ApiError(r.status, data && data.error, data && data.message);
      }
      toast(`Picture saved for ${p.name}`);
    } catch (e) {
      toast(e.message);
    }
    redraw();
  });
  const drop = p.avatar_url ? h("button", { class: "btn btn-small", type: "button" }, "Remove picture") : null;
  if (drop) {
    drop.addEventListener("click", async () => {
      await api(`/admin/profiles/${p.id}/avatar`, { method: "DELETE" }).catch((e) => toast(e.message));
      redraw();
    });
  }

  const tags = [p.is_admin && "admin", p.kid && "kid", p.managed && "no password", p.has_pin && "PIN"].filter(Boolean);
  return h("div", { class: "panel profile-row" },
    h("div", { class: "profile-row-head" },
      avatar(p),
      h("div", {}, h("strong", {}, p.name), tags.length ? h("div", { class: "faint" }, tags.join(" · ")) : null),
      h("label", { class: "check profile-member" }, member, "In the household")),
    p.household ? h("div", { class: "profile-row-body" },
      h("div", { class: "checks" }, pinInput, setPin, clearPin),
      ratingBlock,
      h("div", { class: "checks" }, pick, drop, file)) : null);
}

function newProfileForm(redraw) {
  const err = h("div", { class: "form-error", role: "alert" });
  const rating = ratingSelect("PG", "Highest content rating");
  const kid = h("input", { type: "checkbox", name: "kid", checked: true });
  kid.addEventListener("change", () => { if (!kid.checked && rating.value === "PG") rating.value = ""; });
  const form = h("form", { class: "panel add-profile" },
    h("h3", {}, "Add a profile"),
    h("p", { class: "muted" }, "A profile with no password, for someone who only ever watches here. It sees every library, up to its rating limit."),
    h("div", { class: "user-grid" },
      h("label", { class: "field" }, "Name", h("input", { name: "name", required: true, maxlength: "32", autocomplete: "off" })),
      h("label", { class: "field" }, "PIN (optional)", h("input", { name: "pin", type: "password", inputmode: "numeric", pattern: "[0-9]{4,8}", maxlength: "8", autocomplete: "new-password" })),
      h("div", {}, h("div", { class: "k" }, "Limits"), h("div", { class: "checks" }, h("label", { class: "check" }, kid, "Kid's profile"), rating))),
    err,
    h("div", { class: "actions" }, h("button", { class: "btn btn-primary", type: "submit" }, "Add profile")));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    const name = form.elements.name.value.trim();
    try {
      await api("/admin/profiles", { method: "POST", body: {
        name, kid: kid.checked, max_content_rating: rating.value, pin: form.elements.pin.value || undefined,
      } });
      toast(`Added ${name}`);
      redraw();
    } catch (e2) {
      err.textContent = e2.message;
    }
  });
  return form;
}

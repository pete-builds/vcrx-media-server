import { setUp, api, ApiError } from "../api.js";
import { h } from "../dom.js";
import { state, signedIn, go } from "../app.js";

// First-run setup: a new server with no account yet. The server offers it
// (GET /server says setup_needed) only on its local network; whoever opens it
// first makes the admin and is signed in as them.
export async function setupView() {
  if (state.me) {
    go("/");
    return { el: h("div", {}) };
  }
  if (!state.server || !state.server.setup_needed) {
    go("/login");
    return { el: h("div", {}) };
  }
  const err = h("div", { class: "form-error", role: "alert" });
  const form = h("form", {},
    h("label", { class: "field" }, "Your name", h("input", { name: "display_name", autocomplete: "name", maxlength: "128", autofocus: true })),
    h("label", { class: "field" }, "Username", h("input", { name: "username", autocomplete: "username", required: true })),
    h("label", { class: "field" }, "Password", h("input", { name: "password", type: "password", autocomplete: "new-password", minlength: "8", required: true })),
    h("label", { class: "field" }, "Password again", h("input", { name: "confirm", type: "password", autocomplete: "new-password", minlength: "8", required: true })),
    err,
    h("button", { class: "btn btn-primary", type: "submit" }, "Create admin account"));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    const f = form.elements;
    if (f.password.value !== f.confirm.value) {
      err.textContent = "The two passwords do not match.";
      return;
    }
    const btn = form.querySelector("button");
    btn.disabled = true;
    try {
      await setUp(f.username.value.trim(), f.password.value, f.display_name.value.trim());
      state.server = await api("/server").catch(() => ({ ...state.server, setup_needed: false }));
      await signedIn();
      go("/settings/libraries");
    } catch (e2) {
      if (e2 instanceof ApiError && e2.code === "setup_done") {
        // Someone else finished setup first: this browser signs in like anyone.
        state.server = { ...state.server, setup_needed: false };
        go("/login");
        return;
      }
      err.textContent = e2 instanceof ApiError ? e2.message : "The server could not be reached.";
      btn.disabled = false;
    }
  });
  const server = state.server || {};
  return {
    el: h("div", { class: "login" },
      h("img", { src: "img/wordmark.png", alt: "VCRX", width: "220", height: "44" }),
      h("p", { class: "muted" }, `Welcome to ${server.name || "VCRX"}. Create the admin account to finish setting up this server.`),
      form),
    title: "Set up",
  };
}

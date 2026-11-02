import { signIn, ApiError } from "../api.js";
import { h } from "../dom.js";
import { state, signedIn, go } from "../app.js";

export async function loginView({ params }) {
  const next = params.get("next") || "/";
  if (state.me) {
    go(next);
    return { el: h("div", {}) };
  }
  const err = h("div", { class: "form-error", role: "alert" });
  const form = h("form", {},
    h("label", { class: "field" }, "Username", h("input", { name: "username", autocomplete: "username", required: true, autofocus: true })),
    h("label", { class: "field" }, "Password", h("input", { name: "password", type: "password", autocomplete: "current-password", required: true })),
    err,
    h("button", { class: "btn btn-primary", type: "submit" }, "Sign in"));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    const btn = form.querySelector("button");
    btn.disabled = true;
    try {
      await signIn(form.elements.username.value.trim(), form.elements.password.value);
      await signedIn();
      go(next === "/login" ? "/" : next);
    } catch (e2) {
      err.textContent = e2 instanceof ApiError ? e2.message : "The server could not be reached.";
      btn.disabled = false;
    }
  });
  const server = state.server || {};
  return {
    el: h("div", { class: "login" },
      h("img", { src: "img/wordmark.png", alt: "VCRX", width: "220", height: "44" }),
      h("p", { class: "muted" }, `Sign in to ${server.name || "VCRX"}`),
      form),
    title: "Sign in",
  };
}


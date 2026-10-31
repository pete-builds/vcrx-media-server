// App shell: signs in (without a password on trusted networks), draws the top
// bar, and routes #/paths to views. Each view returns an optional cleanup
// function that runs when the user leaves it.

import { api, autoSignIn, whenSignedOut, ApiError } from "./api.js";
import { h, errorBox } from "./dom.js";
import { homeView } from "./views/home.js";
import { libraryView } from "./views/library.js";
import { libraryCollectionsView, collectionView } from "./views/collections.js";
import { itemView } from "./views/item.js";
import { searchView } from "./views/search.js";
import { playerView } from "./views/player.js";
import { activityView } from "./views/activity.js";
import { settingsView } from "./views/settings.js";
import { loginView } from "./views/login.js";

export const state = { me: null, server: null, libraries: [] };

const routes = [
  [/^\/$/, () => homeView],
  [/^\/library\/(\d+)$/, () => libraryView],
  [/^\/library\/(\d+)\/collections$/, () => libraryCollectionsView],
  [/^\/collection\/(\d+)$/, () => collectionView],
  [/^\/item\/(\d+)$/, () => itemView],
  [/^\/search$/, () => searchView],
  [/^\/play\/(\d+)$/, () => playerView],
  [/^\/activity(?:\/(now|history|stats|users))?$/, () => activityView],
  [/^\/settings(?:\/(account|libraries|users))?$/, () => settingsView],
  [/^\/login$/, () => loginView],
];

let cleanup = null;
let renderSeq = 0;

function parseHash() {
  const raw = location.hash.replace(/^#/, "") || "/";
  const [path, query = ""] = raw.split("?");
  return { path, params: new URLSearchParams(query) };
}

export function go(path) {
  if (location.hash === "#" + path) render();
  else location.hash = path;
}

async function render() {
  const seq = ++renderSeq;
  if (cleanup) {
    try { cleanup(); } catch (e) { console.error(e); }
    cleanup = null;
  }
  const { path, params } = parseHash();
  const view = document.getElementById("view");
  if (!state.me && path !== "/login") {
    go("/login?next=" + encodeURIComponent(path));
    return;
  }
  let match = null, viewFn = null;
  for (const [re, fn] of routes) {
    match = path.match(re);
    if (match) { viewFn = fn(); break; }
  }
  document.getElementById("topbar").hidden = !state.me || path.startsWith("/play/");
  markNav(path);
  view.replaceChildren(h("div", { class: "loading", "aria-live": "polite" }, "Loading"));
  if (!viewFn) {
    view.replaceChildren(errorBox("Not found", "There is nothing at this address.", h("a", { class: "btn", href: "#/" }, "Home")));
    return;
  }
  try {
    const out = await viewFn({ match, params, path });
    if (seq !== renderSeq) {
      if (out && out.cleanup) out.cleanup();
      return;
    }
    view.replaceChildren(out.el);
    cleanup = out.cleanup || null;
    if (out.title) document.title = `${out.title} · VCRX`;
    else document.title = "VCRX";
    if (!out.keepScroll) window.scrollTo(0, 0);
  } catch (err) {
    if (seq !== renderSeq) return;
    console.error(err);
    const msg = err instanceof ApiError ? err.message : "Something went wrong in the browser.";
    const title = err instanceof ApiError && err.status === 404 ? "Not found" : "Could not load this page";
    view.replaceChildren(errorBox(title, msg, h("a", { class: "btn", href: "#/" }, "Home")));
  }
}

function markNav(path) {
  for (const a of document.querySelectorAll("#nav a, #nav-user")) {
    const target = a.getAttribute("href").slice(1);
    a.classList.toggle("active", target === "/" ? path === "/" : path.startsWith(target));
  }
}

export function drawNav() {
  const nav = document.getElementById("nav");
  const links = [h("a", { class: "nav-link", href: "#/" }, "Home")];
  for (const lib of state.libraries) links.push(h("a", { class: "nav-link", href: `#/library/${lib.id}` }, lib.name));
  if (state.me && state.me.is_admin) links.push(h("a", { class: "nav-link", href: "#/activity" }, "Activity"));
  nav.replaceChildren(...links);
}

// signedIn loads who we are and the libraries, then draws the shell.
export async function signedIn() {
  state.me = await api("/me");
  const libs = await api("/libraries");
  state.libraries = libs.libraries || [];
  drawNav();
}

async function boot() {
  whenSignedOut(() => {
    if (!state.me) return;
    state.me = null;
    go("/login?next=" + encodeURIComponent(parseHash().path));
  });
  document.getElementById("search").addEventListener("submit", (e) => {
    e.preventDefault();
    const q = new FormData(e.target).get("q").toString().trim();
    if (q) go("/search?q=" + encodeURIComponent(q));
  });
  try {
    state.server = await api("/server");
  } catch {
    state.server = { name: "VCRX" };
  }
  try {
    await signedIn();
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 401) throw err;
    if (state.server.auto_sign_in) {
      try {
        await autoSignIn();
        await signedIn();
      } catch (e) {
        console.warn("automatic sign-in failed", e);
      }
    }
  }
  window.addEventListener("hashchange", render);
  render();
}

boot().catch((err) => {
  console.error(err);
  document.getElementById("view").replaceChildren(errorBox("VCRX is not answering", "The server could not be reached. Check that it is running, then reload."));
});

// App shell: signs in (without a password on trusted networks), draws the top
// bar, and routes #/paths to views. Each view returns an optional cleanup
// function that runs when the user leaves it.

import { api, autoSignIn, whenSignedOut, ApiError } from "./api.js";
import { h, errorBox, skeleton } from "./dom.js";
import { homeView } from "./views/home.js";
import { libraryView } from "./views/library.js";
import { libraryCollectionsView, collectionView } from "./views/collections.js";
import { rowView } from "./views/rows.js";
import { itemView } from "./views/item.js";
import { searchView } from "./views/search.js";
import { playerView } from "./views/player.js";
import { activityView } from "./views/activity.js";
import { settingsView } from "./views/settings.js";
import { loginView } from "./views/login.js";
import { searchSuggest } from "./suggest.js";
import { installShortcuts } from "./shortcuts.js";
import { pickerView, drawProfileButton } from "./views/profiles.js";

export const state = { me: null, server: null, libraries: [] };

const routes = [
  [/^\/$/, () => homeView],
  [/^\/library\/(\d+)$/, () => libraryView],
  [/^\/library\/(\d+)\/collections$/, () => libraryCollectionsView],
  [/^\/collection\/(\d+)$/, () => collectionView],
  [/^\/row\/([^/]+)$/, () => rowView],
  [/^\/item\/(\d+)$/, () => itemView],
  [/^\/search$/, () => searchView],
  [/^\/play\/(\d+)$/, () => playerView],
  [/^\/activity(?:\/(now|history|stats|users))?$/, () => activityView],
  [/^\/settings(?:\/(account|home|libraries|users|newsletter|markers))?$/, () => settingsView],
  [/^\/login$/, () => loginView],
  [/^\/profiles$/, () => pickerView],
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
  if (!state.me && path !== "/login" && path !== "/profiles") {
    go(signInPath() + "?next=" + encodeURIComponent(path));
    return;
  }
  let match = null, viewFn = null;
  for (const [re, fn] of routes) {
    match = path.match(re);
    if (match) { viewFn = fn(); break; }
  }
  document.getElementById("topbar").hidden = !state.me || path.startsWith("/play/") || path === "/profiles";
  markNav(path);
  setMenu(false);
  // While a page with a hero loads, its skeleton already sits under the
  // translucent top bar, so the bar does not flash solid and back.
  setHero(path === "/" || path.startsWith("/item/"));
  view.replaceChildren(skeleton(path));
  if (!viewFn) {
    setHero(false);
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
    setHero(!!out.hero);
    cleanup = out.cleanup || null;
    if (out.title) document.title = `${out.title} · VCRX`;
    else document.title = "VCRX";
    if (!out.keepScroll) window.scrollTo(0, 0);
  } catch (err) {
    if (seq !== renderSeq) return;
    console.error(err);
    const msg = err instanceof ApiError ? err.message : "Something went wrong in the browser.";
    const title = err instanceof ApiError && err.status === 404 ? "Not found" : "Could not load this page";
    setHero(false);
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
  // On a phone the top bar has no room for Settings, so the menu carries it.
  links.push(h("a", { class: "nav-link nav-phone-only", href: "#/settings" }, "Settings"));
  nav.replaceChildren(...links);
}

// The top bar is see-through over a page's hero art until the page scrolls.
function setHero(on) {
  document.body.classList.toggle("over-hero", on);
}

// The phone menu: the library links drop down under the menu button.
function setMenu(open) {
  const btn = document.getElementById("menu-btn");
  document.getElementById("topbar").classList.toggle("menu-open", open);
  btn.setAttribute("aria-expanded", open ? "true" : "false");
}

function installChrome() {
  const bar = document.getElementById("topbar");
  const btn = document.getElementById("menu-btn");
  const onScroll = () => document.body.classList.toggle("scrolled", window.scrollY > 8);
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();
  btn.addEventListener("click", () => {
    const open = !bar.classList.contains("menu-open");
    setMenu(open);
    if (open) document.querySelector("#nav a").focus();
  });
  bar.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && bar.classList.contains("menu-open")) {
      setMenu(false);
      btn.focus();
    }
  });
  document.addEventListener("click", (e) => {
    if (bar.classList.contains("menu-open") && !bar.contains(e.target)) setMenu(false);
  });
  // The skip link moves focus to the page without touching the hash, which is the route.
  document.querySelector(".skip-link").addEventListener("click", (e) => {
    e.preventDefault();
    document.getElementById("view").focus();
  });
}

// signedIn loads who we are and the libraries, then draws the shell.
export async function signedIn() {
  state.me = await api("/me");
  const libs = await api("/libraries");
  state.libraries = libs.libraries || [];
  drawNav();
  drawProfileButton();
}

// signInPath is where a signed-out browser goes: the "Who's watching?" picker
// when the server shows one on this network, else the sign-in form.
function signInPath() {
  return state.server && state.server.profile_picker ? "/profiles" : "/login";
}

async function boot() {
  whenSignedOut(() => {
    if (!state.me) return;
    state.me = null;
    go(signInPath() + "?next=" + encodeURIComponent(parseHash().path));
  });
  document.getElementById("search").addEventListener("submit", (e) => {
    e.preventDefault();
    const q = new FormData(e.target).get("q").toString().trim();
    if (q) go("/search?q=" + encodeURIComponent(q));
  });
  searchSuggest(document.getElementById("search"));
  installShortcuts();
  installChrome();
  try {
    state.server = await api("/server");
  } catch {
    state.server = { name: "VCRX" };
  }
  try {
    await signedIn();
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 401) throw err;
    if (state.server.auto_sign_in && !state.server.profile_picker) {
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

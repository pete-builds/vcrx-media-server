// App shell: signs in (without a password on trusted networks), draws the top
// bar, and routes #/paths to views. Each view returns an optional cleanup
// function that runs when the user leaves it.

import { api, autoSignIn, whenSignedOut, whenRewound, ApiError } from "./api.js";
import { h, icon, errorBox, skeleton, loadBar } from "./dom.js";
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
import { musicLibraryView, musicArtistView, musicAlbumView } from "./views/music.js";
import { installMusicPlayer, clear as clearMusic } from "./music/player.js";
import { pickerView, drawProfileButton } from "./views/profiles.js";
import { pairView, inviteView } from "./views/devices.js";
import { lendView, rewindView } from "./views/lend.js";
import { peerItemView } from "./views/peer.js";
import { liveView, livePlayerView } from "./views/live.js";

// rewind is set once a lent tape's loan is over: {title, message}.
export const state = { me: null, server: null, libraries: [], rewind: null };

const routes = [
  [/^\/$/, () => homeView],
  [/^\/library\/(\d+)$/, () => libraryView],
  [/^\/library\/(\d+)\/collections$/, () => libraryCollectionsView],
  [/^\/collection\/(\d+)$/, () => collectionView],
  [/^\/row\/([^/]+)$/, () => rowView],
  [/^\/item\/(\d+)$/, () => itemView],
  [/^\/search$/, () => searchView],
  [/^\/play\/(\d+)$/, () => playerView],
  [/^\/peer\/(\d+)\/item\/(\d+)$/, () => peerItemView],
  [/^\/peer\/(\d+)\/play\/(\d+)$/, () => playerView],
  [/^\/activity(?:\/(now|history|stats|users|loans))?$/, () => activityView],
  [/^\/settings(?:\/(account|home|libraries|users|devices|friends|newsletter|markers))?$/, () => settingsView],
  [/^\/login$/, () => loginView],
  [/^\/music\/(\d+)$/, () => musicLibraryView],
  [/^\/music\/artist\/(\d+)$/, () => musicArtistView],
  [/^\/music\/album\/(\d+)$/, () => musicAlbumView],
  [/^\/profiles$/, () => pickerView],
  [/^\/pair\/([A-Za-z-]{1,16})$/, () => pairView],
  [/^\/invite\/([A-Za-z2-7-]{16,64})$/, () => inviteView],
  [/^\/lend\/(\d+)$/, () => lendView],
  [/^\/rewind$/, () => rewindView],
  [/^\/live$/, () => liveView],
  [/^\/live\/(\d+)$/, () => livePlayerView],
];

// An invite link opens on a device that has no session yet.
const isInvite = (path) => path.startsWith("/invite/");

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
  if (!state.me && state.rewind && path !== "/rewind" && !isInvite(path)) {
    go("/rewind");
    return;
  }
  if (!state.me && path !== "/login" && path !== "/profiles" && path !== "/rewind" && !isInvite(path)) {
    go(signInPath() + "?next=" + encodeURIComponent(path));
    return;
  }
  let match = null, viewFn = null;
  for (const [re, fn] of routes) {
    match = path.match(re);
    if (match) { viewFn = fn(); break; }
  }
  const playing = /^\/(peer\/\d+\/)?play\//.test(path) || /^\/live\/\d+$/.test(path);
  const chrome = !!state.me && !playing && path !== "/profiles" && path !== "/rewind" && !isInvite(path);
  document.getElementById("topbar").hidden = !chrome;
  document.getElementById("tabbar").hidden = !chrome;
  document.body.classList.toggle("has-tabbar", chrome);
  document.body.classList.toggle("route-search", path === "/search");
  document.getElementById("back-btn").hidden = path === "/";
  markNav(path);
  setMenu(false);
  setSheet(false);
  // While a page with a hero loads, its skeleton already sits under the
  // translucent top bar, so the bar does not flash solid and back.
  setHero(path === "/" || path.startsWith("/item/") || /^\/peer\/\d+\/item\//.test(path));
  view.replaceChildren(skeleton(path));
  if (!viewFn) {
    setHero(false);
    view.replaceChildren(errorBox("Not found", "There is nothing at this address.", h("a", { class: "btn", href: "#/" }, "Home")));
    return;
  }
  loadBar.show();
  try {
    const out = await viewFn({ match, params, path });
    if (seq !== renderSeq) {
      if (out && out.cleanup) out.cleanup();
      return;
    }
    view.replaceChildren(out.el);
    // Replacing the page drops keyboard focus to the body, so the next Tab
    // would start from the top bar again. Unless the view put focus somewhere
    // itself, it goes to the new page (without scrolling).
    if (!document.activeElement || document.activeElement === document.body) view.focus({ preventScroll: true });
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
  } finally {
    loadBar.hide();
  }
}

function markNav(path) {
  for (const a of document.querySelectorAll("#nav a, #nav-user, #tabbar a, #more-sheet a")) {
    const target = a.getAttribute("href").slice(1);
    const on = target === "/" ? path === "/" : path === target || path.startsWith(target + "/") || path.startsWith(target + "?");
    a.classList.toggle("active", on);
    if (a.closest("#tabbar")) {
      if (on) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    }
  }
  // More is lit while one of its pages is open.
  const more = document.getElementById("more-btn");
  if (more) more.classList.toggle("active", !!document.querySelector("#more-sheet a.active"));
}

export function drawNav() {
  const nav = document.getElementById("nav");
  const links = [h("a", { class: "nav-link", href: "#/" }, "Home")];
  for (const lib of state.libraries) links.push(h("a", { class: "nav-link", href: libraryHref(lib) }, lib.name));
  if (state.me && state.me.live_tv) links.push(h("a", { class: "nav-link", href: "#/live" }, "Live TV"));
  if (state.me && state.me.is_admin) links.push(h("a", { class: "nav-link", href: "#/activity" }, "Activity"));
  // On a phone the top bar has no room for Settings, so the menu carries it.
  links.push(h("a", { class: "nav-link nav-phone-only", href: "#/settings" }, "Settings"));
  nav.replaceChildren(...links);
  drawTabs();
}

// drawTabs fills the phone's tab bar: Home, the first movie and the first TV
// library (else the first two libraries), Search, and More, a sheet with every
// other place. Five at most, so each tab stays a comfortable thumb's width.
function drawTabs() {
  const libs = state.libraries;
  const picked = [libs.find((l) => l.kind === "movie"), libs.find((l) => l.kind === "show")].filter(Boolean);
  for (const l of libs) if (picked.length < 2 && !picked.includes(l)) picked.push(l);
  const libIcon = (l) => icon(l.kind === "show" ? "tv" : l.kind === "music" ? "music" : "movie");
  const tab = (href, ic, label) => h("a", { class: "tab", href }, ic, h("span", {}, label));
  const search = tab("#/search", icon("search"), "Search");
  // Focus the box inside the tap itself: an iPhone opens the keyboard only for
  // focus given during the tap, not after the page changes.
  search.addEventListener("click", () => {
    document.body.classList.add("route-search");
    document.querySelector("#search input").focus();
  });
  const more = h("button", { class: "tab", id: "more-btn", type: "button", "aria-expanded": "false", "aria-controls": "more-sheet" },
    icon("more"), h("span", {}, "More"));
  more.addEventListener("click", () => setSheet(document.getElementById("more-sheet").hidden));
  document.getElementById("tabbar").replaceChildren(tab("#/", icon("home"), "Home"),
    ...picked.map((l) => tab(libraryHref(l), libIcon(l), l.name)), search, more);
  const rest = libs.filter((l) => !picked.includes(l)).map((l) => h("a", { class: "sheet-link", href: libraryHref(l) }, libIcon(l), l.name));
  if (state.me && state.me.live_tv) rest.push(h("a", { class: "sheet-link", href: "#/live" }, icon("tv"), "Live TV"));
  if (state.me && state.me.is_admin) rest.push(h("a", { class: "sheet-link", href: "#/activity" }, icon("activity"), "Activity"));
  rest.push(h("a", { class: "sheet-link", href: "#/settings" }, icon("settings"), "Settings"));
  document.querySelector("#more-sheet nav").replaceChildren(...rest);
}

// The phone's More sheet rises above the tab bar.
function setSheet(open) {
  const sheet = document.getElementById("more-sheet");
  const btn = document.getElementById("more-btn");
  sheet.hidden = !open;
  if (btn) btn.setAttribute("aria-expanded", open ? "true" : "false");
  if (open) {
    const first = sheet.querySelector("a");
    if (first) first.focus();
  }
}

// A music library has its own pages.
export function libraryHref(lib) {
  return lib.kind === "music" ? `#/music/${lib.id}` : `#/library/${lib.id}`;
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
    const sheet = document.getElementById("more-sheet");
    if (!sheet.hidden && !sheet.contains(e.target) && !e.target.closest("#more-btn")) setSheet(false);
  });
  document.addEventListener("keydown", (e) => {
    const sheet = document.getElementById("more-sheet");
    if (e.key === "Escape" && !sheet.hidden) {
      setSheet(false);
      document.getElementById("more-btn").focus();
    }
  });
  // Back: an app on a Home Screen has no browser back button. It steps back
  // through VCRX's own pages, and from a page opened directly (a shared link,
  // a fresh launch) goes Home rather than out of the app.
  document.getElementById("back-btn").addEventListener("click", () => {
    if (navDepth() > 0) history.back();
    else go("/");
  });
  // The skip link moves focus to the page without touching the hash, which is the route.
  document.querySelector(".skip-link").addEventListener("click", (e) => {
    e.preventDefault();
    document.getElementById("view").focus();
  });
}

// navDepth counts VCRX pages behind this one in the tab's history. Each entry
// carries its depth in history.state: an entry without one is new (a link or
// go()), one above its predecessor; an entry with one was returned to by Back
// or Forward and keeps it.
let depth = 0;
function trackDepth() {
  const st = history.state;
  if (st && typeof st.vcrxDepth === "number") depth = st.vcrxDepth;
  else {
    depth += 1;
    history.replaceState({ ...(st || {}), vcrxDepth: depth }, "");
  }
}
function navDepth() { return depth; }

// signedOut forgets who was signed in: their libraries, and their music,
// which would otherwise play on with their queue on the bar for whoever
// uses the device next (sign-out, an expired session, an ended loan).
export function signedOut() {
  state.me = null;
  state.libraries = [];
  clearMusic();
}

// signedIn loads who we are and the libraries, then draws the shell. A
// switch to another profile stops the last one's music, as signing out does.
export async function signedIn() {
  const before = state.me && state.me.id;
  state.me = await api("/me");
  if (before && before !== state.me.id) clearMusic();
  state.rewind = null;
  const libs = await api("/libraries");
  state.libraries = libs.libraries || [];
  drawNav();
  drawProfileButton();
  watchLoan();
}

// A borrower's browser asks again when its loan is due, so the "Be kind,
// rewind." page comes up on time even on a page that makes no requests.
let loanTimer = 0;
function watchLoan() {
  clearTimeout(loanTimer);
  const loan = state.me && state.me.loan;
  if (!loan) return;
  const wait = Math.min(Math.max(0, loan.due_at - Date.now()) + 2000, 2 ** 31 - 1);
  loanTimer = setTimeout(() => api("/me").catch(() => {}), wait);
}

// signInPath is where a signed-out browser goes: the "Who's watching?" picker
// when the server shows one on this network, else the sign-in form.
function signInPath() {
  return state.server && state.server.profile_picker ? "/profiles" : "/login";
}

async function boot() {
  if (!history.state || typeof history.state.vcrxDepth !== "number") history.replaceState({ ...(history.state || {}), vcrxDepth: 0 }, "");
  else depth = history.state.vcrxDepth;
  window.addEventListener("hashchange", trackDepth);
  whenSignedOut(() => {
    if (!state.me) return;
    signedOut();
    go(signInPath() + "?next=" + encodeURIComponent(parseHash().path));
  });
  whenRewound((data) => {
    const first = !state.rewind;
    signedOut();
    state.rewind = { title: data.title || "", message: data.message || "" };
    if (first || parseHash().path !== "/rewind") go("/rewind");
  });
  document.getElementById("search").addEventListener("submit", (e) => {
    e.preventDefault();
    const q = new FormData(e.target).get("q").toString().trim();
    if (q) go("/search?q=" + encodeURIComponent(q));
  });
  searchSuggest(document.getElementById("search"));
  installShortcuts();
  installMusicPlayer();
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
    if (err.code === "loan_ended") {
      // whenRewound has the page; nothing to sign in to.
      window.addEventListener("hashchange", render);
      render();
      return;
    }
    // An invite link joins as the invited device: signing this browser in as
    // the household first would only be undone.
    if (state.server.auto_sign_in && !state.server.profile_picker && !isInvite(parseHash().path)) {
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

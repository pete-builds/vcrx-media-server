#!/usr/bin/env node
// Drives the web app in headless Chrome and fails on anything a person would
// notice: a page that throws, a page showing "null" or "undefined", a page
// missing its main content, a video that does not play, search as you type that
// lists nothing, a Continue Watching Remove control that does not remove, a
// keyboard shortcuts list that does not open, a Home feature whose Play does
// not play, season tabs that do not switch, a phone menu that does not open,
// Home rows missing (the Halloween row, with the dev server's fixed date),
// a Customize Home editor that does not hide or move a row, or an invited
// device that is not signed in by its invite, sees past its scope, reaches
// the profile picker, or survives being revoked; and, given a second dev
// server, a friend's server that does not link, badge its titles on Home,
// play through this one, or leave Home when unlinked.
//
//   node scripts/web-smoke.mjs [base-url]      (default http://127.0.0.1:3751)
//   VCRX_SMOKE_PEER=http://127.0.0.1:3752 node scripts/web-smoke.mjs ...
//
// VCRX_SMOKE_PEER names a second ./scripts/dev-server.sh (its own VCRX_DEV_DIR
// and VCRX_PORT) to link to as a friend's server.
//
// Run it against ./scripts/dev-server.sh, which signs the local browser in and
// has the sample clips this expects (one plays directly, one has its audio
// converted, one has its video transcoded).
//
// Safari gets converted files as HLS. Chrome plays HLS natively too, so the
// player's HLS path runs here with Chrome telling the app it is Safari. In a
// Chrome without native HLS (or with VCRX_SMOKE_HLS=mse) the HLS stream is
// played through Media Source Extensions instead: the same segments, decoded
// by the same browser, without the player.
// CHROME may name the browser binary. Talks to Chrome over --remote-debugging-pipe,
// so it needs nothing beyond Node 20 and Chrome.

import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const base = (process.argv[2] || "http://127.0.0.1:3751").replace(/\/$/, "");
const peer = (process.env.VCRX_SMOKE_PEER || "").replace(/\/$/, "");
const chrome = process.env.CHROME || [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser",
].find((p) => existsSync(p));
if (!chrome) {
  console.error("web-smoke: no Chrome found; set CHROME");
  process.exit(2);
}

const profile = mkdtempSync(join(tmpdir(), "vcrx-smoke-"));
const args = ["--headless=new", "--remote-debugging-pipe", `--user-data-dir=${profile}`, "--no-first-run",
  "--autoplay-policy=no-user-gesture-required", "--window-size=1440,900", "--mute-audio", "about:blank"];
if (process.env.CI) args.unshift("--no-sandbox");
const proc = spawn(chrome, args, { stdio: ["ignore", "ignore", "pipe", "pipe", "pipe"] });
proc.stderr.on("data", () => {});

// The pipe protocol: JSON messages separated by NUL, written to fd 3, read from fd 4.
let nextId = 0;
const pending = new Map();
const listeners = [];
let buf = "";
proc.stdio[4].on("data", (chunk) => {
  buf += chunk.toString("utf8");
  let i;
  while ((i = buf.indexOf("\0")) >= 0) {
    const msg = JSON.parse(buf.slice(0, i));
    buf = buf.slice(i + 1);
    if (msg.id && pending.has(msg.id)) {
      pending.get(msg.id)(msg);
      pending.delete(msg.id);
    } else if (msg.method) {
      for (const l of listeners) l(msg);
    }
  }
});
function send(method, params = {}, sessionId) {
  const id = ++nextId;
  return new Promise((resolve, reject) => {
    pending.set(id, (m) => (m.error ? reject(new Error(`${method}: ${m.error.message}`)) : resolve(m.result)));
    proc.stdio[3].write(JSON.stringify({ id, method, params, sessionId }) + "\0");
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const failures = [];
const fail = (where, what) => {
  failures.push(`${where}: ${what}`);
  console.log(`  FAIL ${what}`);
};

async function main() {
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => send(m, p, sessionId);
  let exceptions = [];
  listeners.push((m) => {
    if (m.sessionId !== sessionId) return;
    if (m.method === "Runtime.exceptionThrown") {
      const d = m.params.exceptionDetails;
      exceptions.push((d.exception && d.exception.description) || d.text);
    }
  });
  await s("Runtime.enable");
  await s("Page.enable");
  const evaluate = async (expr) => {
    const r = await s("Runtime.evaluate", { expression: `(async () => { ${expr} })()`, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  };

  await s("Page.navigate", { url: `${base}/#/` });
  await sleep(2500);
  // Discover ids through the app's own session.
  const ids = await evaluate(`
    const j = (p) => fetch("/api/v1" + p).then((r) => r.json());
    const libs = (await j("/libraries")).libraries;
    const out = { libs: libs.map((l) => l.id), movie: null, show: null, direct: null, converted: null, transcoded: null };
    for (const l of libs) {
      const items = (await j("/libraries/" + l.id + "/items?limit=50")).items;
      if (l.kind === "movie") {
        out.movie = items[0] && items[0].id;
        const by = (t) => items.find((i) => i.title === t);
        out.direct = by("Copper Valley") ? by("Copper Valley").id : null;
        out.converted = by("River Line") ? by("River Line").id : null;
        out.transcoded = by("Tin Kettle Road") ? by("Tin Kettle Road").id : null;
        out.cast = by("Harbor Lights") ? by("Harbor Lights").id : null;
      } else {
        // Quiet Street by name: it has the two seasons and the theme music the
        // checks below need, and Lantern Bay (Skip Intro) sorts before it.
        const qs = items.find((i) => i.title === "Quiet Street") || items[0];
        out.show = qs && qs.id;
      }
    }
    return out;`);

  // Put a movie in Continue Watching (a session stopped partway through), so
  // Home draws that row with its Remove controls on every pass below.
  const seeded = await evaluate(`
    const { api } = await import("/js/api.js");
    const { capabilities } = await import("/js/caps.js");
    const id = ${JSON.stringify(ids.direct || ids.movie)};
    const item = await api("/items/" + id);
    const dur = (item.versions[0] && item.versions[0].duration_ms) || 60000;
    const sess = await api("/playback/sessions", { method: "POST", body: { item_id: id, capabilities: capabilities() } });
    const ev = (type, position_ms) => api("/playback/sessions/" + sess.session_id + "/events",
      { method: "POST", body: { type, position_ms, duration_ms: dur } });
    await ev("start", 0);
    await ev("stop", Math.round(dur * 0.4));
    const row = ((await api("/home")).rows || []).find((r) => r.id === "continue_watching");
    return { id, inRow: !!(row && row.items.some((i) => i.id === id)) };`);
  const wait = (cond) => `for (let i = 0; i < 40 && !(${cond}); i++) await new Promise((r) => setTimeout(r, 150));`;
  const press = async (key, code, vk, text, modifiers = 0) => {
    await s("Input.dispatchKeyEvent", { type: text ? "keyDown" : "rawKeyDown", key, code, windowsVirtualKeyCode: vk, text, modifiers });
    await s("Input.dispatchKeyEvent", { type: "keyUp", key, code, windowsVirtualKeyCode: vk, modifiers });
  };
  // Types into the top bar search one character at a time, as a person would,
  // then waits for the suggestions to show.
  const typeSearch = async (text) => {
    await s("Page.navigate", { url: `${base}/#/` });
    await evaluate(`${wait(`document.querySelector("#view .row")`)}
      const i = document.querySelector("#search input");
      i.value = "";
      i.focus();`);
    for (const ch of text) {
      await s("Input.insertText", { text: ch });
      await sleep(60);
    }
    return evaluate(`${wait(`document.querySelector("#search-suggest [role=option], #search-suggest .suggest-empty")`)}
      const list = document.getElementById("search-suggest");
      if (!list) return { n: 0, text: "(no list under the search box)", hidden: true, expanded: null, right: 0, scroll: 0, width: screen.width };
      const r = list.getBoundingClientRect();
      return { n: list.querySelectorAll("[role=option]").length, text: list.innerText, hidden: list.hidden,
        expanded: document.querySelector("#search input").getAttribute("aria-expanded"),
        right: r.right, scroll: document.documentElement.scrollWidth, width: screen.width };`);
  };

  // Each page: what it must show.
  const pages = [
    ["/", ".row .card"],
    ...ids.libs.map((id) => [`/library/${id}`, ".grid .card"]),
    ...ids.libs.map((id) => [`/library/${id}/collections`, ".toolbar h1"]),
    [`/item/${ids.movie}`, ".versions-admin"], // the admin version tools render too
    // Its NFO names an invented cast.
    [`/item/${ids.cast}`, ".cast-list .cast-member .cast-role"],
    [`/item/${ids.show}`, ".hero h1"],
    ["/search?q=a", ".toolbar h1"],
    ["/activity/now", ".tiles"],
    ["/activity/history", ".tabs"],
    ["/activity/stats?days=7", ".chart-box svg"],
    ["/activity/users", "table.list"],
    ["/settings/users", ".household .profile-row"],
    ["/profiles", ".picker .profile"],
    ["/settings", ".settings h1"],
    ["/settings/libraries", "table.list"],
    ["/settings/users", ".user-summary"],
    ["/settings/home", ".layout-list li"],
    ["/row/trending", ".grid .card"],
    ["/settings/newsletter", ".newsletter-form"],
    ["/settings/devices", ".scope-form"],
    ["/settings/friends", ".link-form"],
    ["/settings/markers", "table.list"],
  ];
  for (const [path, selector] of pages) {
    console.log(`page #${path}`);
    exceptions = [];
    await s("Page.navigate", { url: `${base}/#${path}` });
    const res = await evaluate(`
      for (let i = 0; i < 40; i++) {
        if (document.querySelector(${JSON.stringify(selector)})) break;
        await new Promise((r) => setTimeout(r, 150));
      }
      const view = document.getElementById("view");
      const text = view ? view.innerText : "";
      return { found: !!document.querySelector(${JSON.stringify(selector)}), text,
        error: !!document.querySelector("#view .error-box") };`);
    if (!res.found) fail(`#${path}`, `no ${selector}`);
    if (res.error) fail(`#${path}`, `shows an error box: ${res.text.slice(0, 160).replace(/\n/g, " ")}`);
    const junk = res.text.match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
    if (junk) {
      const at = res.text.slice(Math.max(0, junk.index - 60), junk.index + 20).replace(/\s+/g, " ");
      fail(`#${path}`, `shows "${junk[0]}" in "...${at}..."`);
    }
    if (exceptions.length) fail(`#${path}`, `threw: ${exceptions[0]}`);
  }

  // The same pages on a phone: nothing may be wider than the screen.
  await s("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
  for (const [path, selector] of pages) {
    await s("Page.navigate", { url: `${base}/#${path}` });
    const res = await evaluate(`
      for (let i = 0; i < 40 && !document.querySelector(${JSON.stringify(selector)}); i++) await new Promise((r) => setTimeout(r, 150));
      await new Promise((r) => setTimeout(r, 200));
      // Compare with the device, not innerWidth: a phone widens its layout
      // viewport to fit overflowing content, so innerWidth always "fits".
      return { scroll: document.documentElement.scrollWidth, width: screen.width };`);
    if (res.scroll > res.width + 1) fail(`#${path} on a phone`, `page is ${res.scroll}px wide on a ${res.width}px screen`);
  }
  console.log(`phone width: ${pages.length} pages checked`);
  // The phone top bar folds the library links into a menu: they must be out
  // of the way until the menu button opens them, and reachable once it does.
  console.log("phone menu");
  exceptions = [];
  await s("Page.navigate", { url: `${base}/#/` });
  const menu = await evaluate(`${wait(`document.querySelector("#view .row")`)}
    const btn = document.getElementById("menu-btn");
    const shown = (el) => !!el && el.getClientRects().length > 0;
    const link = () => document.querySelector('#nav a[href="#/settings"]');
    const before = { btn: shown(btn), nav: shown(link()) };
    if (btn) btn.click();
    await new Promise((r) => setTimeout(r, 100));
    const after = { nav: shown(link()), expanded: btn && btn.getAttribute("aria-expanded") };
    if (link()) link().click();
    ${wait(`location.hash === "#/settings" && !shown(link())`)}
    return { before, after, hash: location.hash, closed: !shown(link()) };`);
  if (!menu.before.btn) fail("phone menu", "no menu button in the top bar");
  if (menu.before.nav) fail("phone menu", "the library links show before the menu is opened");
  if (!menu.after.nav || menu.after.expanded !== "true") fail("phone menu", "the menu button did not open the links");
  if (menu.hash !== "#/settings") fail("phone menu", `Settings in the menu went to ${menu.hash}`);
  if (!menu.closed) fail("phone menu", "the menu stayed open after choosing a page");
  if (exceptions.length) fail("phone menu", `threw: ${exceptions[0]}`);
  const phoneSuggest = await typeSearch("riv");
  if (!phoneSuggest.n) fail("search as you type on a phone", "no suggestions");
  if (phoneSuggest.right > phoneSuggest.width + 1 || phoneSuggest.scroll > phoneSuggest.width + 1) {
    fail("search as you type on a phone", `suggestions reach ${Math.round(phoneSuggest.right)}px on a ${phoneSuggest.width}px screen`);
  }
  await press("Escape", "Escape", 27);
  await s("Emulation.clearDeviceMetricsOverride");

  // Continue Watching: the details page offers removal, and Home's Remove
  // control, pressed with a real mouse click, takes the card out without
  // opening the title or marking it watched.
  console.log(`continue watching: remove #/item/${seeded.id}`);
  exceptions = [];
  if (!seeded.inRow) {
    fail("continue watching", "a stopped session did not put the movie in Continue Watching");
  } else {
    await s("Page.navigate", { url: `${base}/#/item/${seeded.id}` });
    const offered = await evaluate(`${wait(`document.querySelector(".hero .actions")`)}
      return [...document.querySelectorAll(".hero .actions button")].some((b) => b.textContent.includes("Remove from Continue Watching"));`);
    if (!offered) fail("continue watching", "the details page has no Remove from Continue Watching button");
    await s("Page.navigate", { url: `${base}/#/` });
    const cardSel = `[data-row="continue_watching"] a.card[href="#/item/${seeded.id}"]`;
    const at = await evaluate(`${wait(`document.querySelector(${JSON.stringify(cardSel)})`)}
      const c = document.querySelector(${JSON.stringify(cardSel)});
      const b = c && c.closest(".cw-card").querySelector("button.cw-remove");
      if (!b) return null;
      b.scrollIntoView({ block: "center" });
      const r = b.getBoundingClientRect();
      const x = r.left + r.width / 2, y = r.top + r.height / 2;
      return { x, y, hit: document.elementFromPoint(x, y)?.closest("button") === b };`);
    if (!at) fail("continue watching", "no Remove control on the card");
    else {
      if (!at.hit) fail("continue watching", "the Remove control is covered by something else");
      await s("Input.dispatchMouseEvent", { type: "mouseMoved", x: at.x, y: at.y });
      await s("Input.dispatchMouseEvent", { type: "mousePressed", x: at.x, y: at.y, button: "left", clickCount: 1 });
      await s("Input.dispatchMouseEvent", { type: "mouseReleased", x: at.x, y: at.y, button: "left", clickCount: 1 });
      const res = await evaluate(`${wait(`!document.querySelector(${JSON.stringify(cardSel)})`)}
        const { api } = await import("/js/api.js");
        const item = await api("/items/${seeded.id}");
        const row = ((await api("/home")).rows || []).find((r) => r.id === "continue_watching");
        return { gone: !document.querySelector(${JSON.stringify(cardSel)}), hash: location.hash, played: item.watch.played,
          pos: item.watch.position_ms, inRow: !!(row && row.items.some((i) => i.id === ${seeded.id})) };`);
      if (!res.gone) fail("continue watching", "the card is still on Home after Remove");
      if (res.hash !== "#/") fail("continue watching", `Remove navigated to ${res.hash}`);
      if (res.inRow) fail("continue watching", "the server still lists it in Continue Watching");
      if (res.played || res.pos !== 0) fail("continue watching", `after Remove: played ${res.played}, position ${res.pos}`);
    }
  }
  if (exceptions.length) fail("continue watching", `threw: ${exceptions[0]}`);

  // Home's rows: the dev server pins Home's date to Oct 31 and seeds plays,
  // so Halloween, Trending and the others must all be there, each with at
  // least four titles, and See all must open a row's grid.
  console.log("home rows");
  exceptions = [];
  const homeRows = async () => {
    await s("Page.navigate", { url: `${base}/#/` });
    return evaluate(`${wait(`document.querySelectorAll("#view .row").length > 3`)}
      return [...document.querySelectorAll("#view .row")].map((r) => ({ id: r.dataset.row, title: r.querySelector("h2").innerText,
        cards: r.querySelectorAll("a.card").length, notes: [...r.querySelectorAll(".sub.note")].map((n) => n.innerText),
        titles: [...r.querySelectorAll("a.card .title")].map((t) => t.innerText),
        more: r.querySelector(".row-head a") ? r.querySelector(".row-head a").getAttribute("href") : null }));`);
  };
  const rowsBefore = await homeRows();
  const ids0 = rowsBefore.map((r) => r.id);
  for (const want of ["seasonal:halloween", "trending", "top_rated_movies", "most_watched", "recently_released", "unwatched_gems"]) {
    if (!ids0.includes(want)) fail("home rows", `no ${want} row; rows: ${ids0.join(" ")}`);
  }
  if (!ids0.some((id) => id.startsWith("because_you_watched:"))) fail("home rows", `no Because You Watched row; rows: ${ids0.join(" ")}`);
  for (const r of rowsBefore) {
    if (r.id !== "continue_watching" && r.cards < 4) fail("home rows", `${r.id} shows ${r.cards} titles, fewer than four`);
  }
  const halloween = rowsBefore.find((r) => r.id === "seasonal:halloween");
  if (halloween && !halloween.titles.some((t) => /Hollow Pines|Lantern Hour|Grave Weather/.test(t))) {
    fail("home rows", `the Halloween row has no horror: ${halloween.titles.join(", ")}`);
  }
  const tvRow = rowsBefore.find((r) => r.id.startsWith("recently_added_tv:"));
  if (!tvRow || !tvRow.notes.some((n) => /new episode/.test(n))) fail("home rows", `Recently Added TV has no "new episodes" note: ${JSON.stringify(tvRow)}`);
  const trendingRow = rowsBefore.find((r) => r.id === "trending");
  if (trendingRow) {
    if (trendingRow.more !== "#/row/trending") fail("home rows", `Trending's See all goes to ${trendingRow.more}`);
    await s("Page.navigate", { url: `${base}/${trendingRow.more}` });
    const grid = await evaluate(`${wait(`document.querySelectorAll(".grid .card").length`)}
      return { n: document.querySelectorAll(".grid .card").length, h1: document.querySelector(".toolbar h1")?.innerText };`);
    if (grid.n < trendingRow.cards || grid.h1 !== trendingRow.title) fail("home rows", `See all for Trending: ${JSON.stringify(grid)}`);
  }
  if (exceptions.length) fail("home rows", `threw: ${exceptions[0]}`);

  // Customize Home, by keyboard: Space on a row's checkbox hides it, Enter on
  // Move up moves it and keeps the focus on it, and Reset brings Home back.
  console.log("customize home");
  exceptions = [];
  const editor = async () => {
    await s("Page.navigate", { url: `${base}/#/settings/home` });
    return evaluate(`${wait(`document.querySelectorAll(".layout-list li").length > 5`)}
      return [...document.querySelectorAll(".layout-list li")].map((li) => ({ key: li.dataset.key, on: li.querySelector("input").checked }));`);
  };
  await editor();
  await evaluate(`document.querySelector('.layout-list li[data-key="seasonal"] input').focus();`);
  await press(" ", "Space", 32, " ");
  const hidden = await evaluate(`${wait(`document.querySelector(".layout-source").innerText.includes("your own")`)}
    return document.querySelector('.layout-list li[data-key="seasonal"] input').checked;`);
  if (hidden) fail("customize home", "Space did not turn the seasonal row off");
  const afterHide = (await homeRows()).map((r) => r.id);
  if (afterHide.some((id) => id.startsWith("seasonal:"))) fail("customize home", `a hidden row still shows: ${afterHide.join(" ")}`);
  const order0 = await editor();
  const at = order0.findIndex((r) => r.key === "trending");
  const above = order0[at - 1].key;
  await evaluate(`document.querySelector('.layout-list li[data-key="trending"] [data-move="up"]').focus();`);
  await press("Enter", "Enter", 13, "\r");
  const moved = await evaluate(`${wait(`[...document.querySelectorAll(".layout-list li")].findIndex((li) => li.dataset.key === "trending") === ${at - 1}`)}
    const keys = [...document.querySelectorAll(".layout-list li")].map((li) => li.dataset.key);
    const f = document.activeElement;
    return { at: keys.indexOf("trending"), focus: f && f.closest("li") ? f.closest("li").dataset.key + ":" + f.dataset.move : String(f && f.tagName) };`);
  if (moved.at !== at - 1) fail("customize home", `Move up left Trending at ${moved.at}, from ${at}`);
  if (moved.focus !== "trending:up") fail("customize home", `focus went to ${moved.focus} after moving`);
  const kindOf = (id) => id.split(":")[0].replace(/^(genre|collection|best_of_decade)$/, "x");
  const order1 = (await homeRows()).map((r) => kindOf(r.id));
  const ti = order1.indexOf("trending");
  const ai = order1.indexOf(kindOf(above));
  if (ti < 0 || (ai >= 0 && ti > ai)) fail("customize home", `Home did not follow the new order: ${order1.join(" ")}`);
  await editor();
  await evaluate(`[...document.querySelectorAll(".layout-actions button")].find((b) => b.innerText.includes("Reset")).click();`);
  await evaluate(`${wait(`!document.querySelector(".layout-source").innerText.includes("your own")`)}`);
  const restored = (await homeRows()).map((r) => r.id);
  if (!restored.includes("seasonal:halloween")) fail("customize home", `Reset did not bring the Halloween row back: ${restored.join(" ")}`);
  if (exceptions.length) fail("customize home", `threw: ${exceptions[0]}`);

  // Search as you type: suggestions after two characters, arrow keys and Enter
  // open one, Escape closes, Enter with none highlighted goes to the results.
  console.log("search as you type");
  exceptions = [];
  const sug = await typeSearch("riv");
  if (!sug.n || sug.hidden) fail("search as you type", `no suggestions: ${sug.text.slice(0, 120)}`);
  if (sug.n > 8) fail("search as you type", `${sug.n} suggestions, at most 8 expected`);
  if (!/River/.test(sug.text)) fail("search as you type", `no River title in: ${sug.text.slice(0, 160).replace(/\n/g, " ")}`);
  if (sug.expanded !== "true") fail("search as you type", "the input does not say the list is open (aria-expanded)");
  const junk = sug.text.match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
  if (junk) fail("search as you type", `shows "${junk[0]}"`);
  await press("ArrowDown", "ArrowDown", 40);
  await press("Enter", "Enter", 13, "\r");
  const picked = await evaluate(`${wait(`location.hash.startsWith("#/item/") && document.querySelector(".hero h1")`)}
    const l = document.getElementById("search-suggest");
    return { hash: location.hash, open: !!l && !l.hidden };`);
  if (!picked.hash.startsWith("#/item/")) fail("search as you type", `ArrowDown and Enter went to ${picked.hash}`);
  if (picked.open) fail("search as you type", "the list stayed open after picking a title");
  await typeSearch("riv");
  await press("Escape", "Escape", 27);
  const closed = await evaluate(`const l = document.getElementById("search-suggest"); return !l || l.hidden;`);
  if (!closed) fail("search as you type", "Escape did not close the list");
  await press("Enter", "Enter", 13, "\r");
  const full = await evaluate(`${wait(`location.hash.startsWith("#/search")`)} return location.hash;`);
  if (full !== "#/search?q=riv") fail("search as you type", `Enter with nothing highlighted went to ${full}`);
  if (exceptions.length) fail("search as you type", `threw: ${exceptions[0]}`);

  // The shortcuts list: ? opens it outside a text field, Escape closes it.
  console.log("keyboard shortcuts");
  exceptions = [];
  await s("Page.navigate", { url: `${base}/#/` });
  await evaluate(`${wait(`document.querySelector("#view .row")`)} document.activeElement && document.activeElement.blur();`);
  await press("?", "Slash", 191, "?", 8);
  const help = await evaluate(`const o = document.getElementById("shortcuts"); return { open: !!o && !o.hidden, text: o ? o.innerText : "" };`);
  if (!help.open) fail("keyboard shortcuts", "? did not open the list");
  else {
    if (!/Play or pause/.test(help.text)) fail("keyboard shortcuts", "the list does not show the player's keys");
    await press("Escape", "Escape", 27);
    if (await evaluate(`return !document.getElementById("shortcuts").hidden;`)) fail("keyboard shortcuts", "Escape did not close the list");
  }
  if (exceptions.length) fail("keyboard shortcuts", `threw: ${exceptions[0]}`);

  // Home's featured title: a title, and a Play button that, clicked with a
  // real mouse, opens the player.
  console.log("home feature");
  exceptions = [];
  await s("Page.navigate", { url: `${base}/#/` });
  const feat = await evaluate(`${wait(`document.querySelector(".feature .actions button")`)}
    const f = document.querySelector(".feature");
    const b = f && f.querySelector(".actions button.btn-primary");
    if (!b) return { title: f ? f.querySelector("h1")?.innerText : null, text: f ? f.innerText : "" };
    const r = b.getBoundingClientRect();
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    return { title: f.querySelector("h1")?.innerText || "", text: f.innerText, label: b.innerText, x, y,
      hit: document.elementFromPoint(x, y)?.closest("button") === b };`);
  if (!feat.title) fail("home feature", "no featured title at the top of Home");
  const featJunk = (feat.text || "").match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
  if (featJunk) fail("home feature", `shows "${featJunk[0]}"`);
  if (!feat.label) fail("home feature", "no Play button");
  else {
    if (!feat.hit) fail("home feature", "the Play button is covered by something else");
    await s("Input.dispatchMouseEvent", { type: "mouseMoved", x: feat.x, y: feat.y });
    await s("Input.dispatchMouseEvent", { type: "mousePressed", x: feat.x, y: feat.y, button: "left", clickCount: 1 });
    await s("Input.dispatchMouseEvent", { type: "mouseReleased", x: feat.x, y: feat.y, button: "left", clickCount: 1 });
    const went = await evaluate(`${wait(`location.hash.startsWith("#/play/") && document.querySelector(".player")`)} return location.hash;`);
    if (!/^#\/play\/\d+/.test(went)) fail("home feature", `Play went to ${went}`);
  }
  if (exceptions.length) fail("home feature", `threw: ${exceptions[0]}`);

  // A show's seasons are tabs: the open one lists its episodes, and the arrow
  // keys move to the next season and list that one's.
  console.log(`season tabs (#/item/${ids.show})`);
  exceptions = [];
  await s("Page.navigate", { url: `${base}/#/item/${ids.show}` });
  const tabs = await evaluate(`${wait(`document.querySelector(".season-panel .ep-card")`)}
    const tabs = [...document.querySelectorAll('.season-tabs [role="tab"]')];
    const seasons = (await (await fetch("/api/v1/items/${ids.show}/children")).json()).items;
    const sel = () => tabs.findIndex((t) => t.getAttribute("aria-selected") === "true");
    const eps = () => [...document.querySelectorAll(".season-panel .ep-card a")].map((a) => a.getAttribute("href"));
    const first = { tabs: tabs.length, seasons: seasons.length, sel: sel(), eps: eps() };
    if (tabs.length < 2) return { first };
    tabs[sel()].focus();
    return { first, seasonIds: seasons.map((x) => x.id) };`);
  if (!tabs.first.tabs) fail("season tabs", "the show page has no season tabs");
  else if (tabs.first.tabs !== tabs.first.seasons) fail("season tabs", `${tabs.first.tabs} tabs for ${tabs.first.seasons} seasons`);
  if (!tabs.first.eps.length) fail("season tabs", "the open season lists no episodes");
  if (tabs.seasonIds) {
    await press("ArrowRight", "ArrowRight", 39);
    const moved = await evaluate(`
      const tabs = [...document.querySelectorAll('.season-tabs [role="tab"]')];
      const sel = tabs.findIndex((t) => t.getAttribute("aria-selected") === "true");
      const want = (await (await fetch("/api/v1/items/" + ${JSON.stringify(tabs.seasonIds)}[sel] + "/children")).json()).items.map((e) => "#/item/" + e.id);
      ${wait(`[...document.querySelectorAll(".season-panel .ep-card a")].length && [...document.querySelectorAll(".season-panel .ep-card a")][0].getAttribute("href") === want[0]`)}
      const got = [...document.querySelectorAll(".season-panel .ep-card a")].map((a) => a.getAttribute("href"));
      return { sel, focused: document.activeElement === tabs[sel], same: JSON.stringify(got) === JSON.stringify(want), got };`);
    if (moved.sel !== (tabs.first.sel + 1) % tabs.first.tabs) fail("season tabs", `ArrowRight selected tab ${moved.sel}, from ${tabs.first.sel}`);
    if (!moved.focused) fail("season tabs", "focus did not follow the selected tab");
    if (!moved.same) fail("season tabs", `the panel did not switch to that season's episodes: ${moved.got.join(" ")}`);
  }
  if (exceptions.length) fail("season tabs", `threw: ${exceptions[0]}`);

  // Extras: Harbor Lights has a -trailer file and two extras folders, and Quiet
  // Street a theme.mp3 (scripts/dev-server.sh). None may become a title of its
  // own. The film's page shows an Extras row and a Trailer button; the trailer,
  // clicked with a real mouse, plays in the VCRX player as a direct stream.
  console.log(`trailer and extras (#/item/${ids.cast})`);
  exceptions = [];
  const indexed = await evaluate(`
    const j = (p) => fetch("/api/v1" + p).then((r) => r.json());
    const titles = [];
    for (const l of (await j("/libraries")).libraries) titles.push(...(await j("/libraries/" + l.id + "/items?limit=200")).items.map((i) => i.title));
    const files = (await j("/items/${ids.cast}")).versions.map((v) => v.file_name);
    return { titles, files };`);
  const leaked = indexed.titles.filter((t) => /trailer|Raising the Lighthouse|The Long Night|theme/i.test(t));
  if (leaked.length) fail("extras", `extras became titles: ${leaked.join(", ")}`);
  // With title_year grouping, an extra read as a film would join Harbor Lights as a version instead.
  if (indexed.files.length !== 1) fail("extras", `Harbor Lights has ${indexed.files.length} versions: ${indexed.files.join(", ")}`);
  await s("Page.navigate", { url: `${base}/#/item/${ids.cast}` });
  const ex = await evaluate(`${wait(`document.querySelector(".extras .extra") && document.querySelector(".hero .trailer-btn")`)}
    const b = document.querySelector(".hero .trailer-btn");
    const row = document.querySelector(".extras");
    if (!b) return { tiles: row ? row.querySelectorAll(".extra").length : 0, text: row ? row.innerText : "" };
    b.scrollIntoView({ block: "center" });
    const r = b.getBoundingClientRect();
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    return { tiles: row ? row.querySelectorAll(".extra").length : 0, text: row ? row.innerText : "", x, y,
      hit: document.elementFromPoint(x, y)?.closest("button") === b };`);
  if (ex.tiles < 3) fail("extras", `the Extras row shows ${ex.tiles} extras, want the trailer, a featurette and a deleted scene`);
  if (!/Raising the Lighthouse/.test(ex.text) || !/Deleted scene/.test(ex.text)) fail("extras", `Extras row: ${ex.text.replace(/\n/g, " ").slice(0, 160)}`);
  const exJunk = ex.text.match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
  if (exJunk) fail("extras", `shows "${exJunk[0]}"`);
  if (ex.x === undefined) fail("trailer", "no Trailer button on the film's page");
  else {
    if (!ex.hit) fail("trailer", "the Trailer button is covered by something else");
    await evaluate(`document.querySelectorAll("video").forEach((v) => { v.dataset.stale = "1"; v.pause(); });`);
    await s("Input.dispatchMouseEvent", { type: "mouseMoved", x: ex.x, y: ex.y });
    await s("Input.dispatchMouseEvent", { type: "mousePressed", x: ex.x, y: ex.y, button: "left", clickCount: 1 });
    await s("Input.dispatchMouseEvent", { type: "mouseReleased", x: ex.x, y: ex.y, button: "left", clickCount: 1 });
    const tr = await evaluate(`
      const fresh = () => document.querySelector("video:not([data-stale])");
      for (let i = 0; i < 60 && !fresh(); i++) await new Promise((r) => setTimeout(r, 150));
      const v = fresh();
      if (!v) return { ok: false, hash: location.hash, why: document.getElementById("view").innerText.slice(0, 200) };
      v.muted = true;
      v.play().catch(() => {});
      for (let i = 0; i < 80 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
      return { ok: v.currentTime > 1.5, t: v.currentTime, err: v.error && v.error.code, hash: location.hash, src: v.currentSrc,
        title: (document.querySelector(".player .ptitle") || {}).innerText || "" };`);
    if (!/^#\/play\/\d+\?extra=\d+$/.test(tr.hash)) fail("trailer", `the Trailer button went to ${tr.hash}`);
    if (!tr.ok) fail("trailer", `trailer did not play (t=${tr.t}, media error ${tr.err}) ${tr.why || ""}`);
    if (!/\/api\/v1\/extras\/\d+\/file\?session=/.test(tr.src || "")) fail("trailer", `played ${tr.src}, not the extra's file`);
    if (!/Harbor Lights · Trailer/.test(tr.title)) fail("trailer", `player title "${tr.title}"`);
  }
  // Theme music: a button on the show's page, which plays only when pressed.
  await s("Page.navigate", { url: `${base}/#/item/${ids.show}` });
  const theme = await evaluate(`${wait(`document.querySelector(".hero .theme-btn")`)}
    const b = document.querySelector(".hero .theme-btn");
    if (!b) return { found: false };
    const before = b.getAttribute("aria-pressed");
    b.click();
    ${wait(`b.getAttribute("aria-pressed") === "true"`)}
    return { found: true, before, after: b.getAttribute("aria-pressed") };`);
  if (!theme.found) fail("theme music", "no Theme music button on the show's page");
  else if (theme.before !== "false" || theme.after !== "true") fail("theme music", `played before a click or not after one (${theme.before} then ${theme.after})`);
  if (exceptions.length) fail("trailer and extras", `threw: ${exceptions[0]}`);

  // Playback: a file the browser plays directly, one whose audio it needs
  // converted, and one whose video it cannot decode at all. The pill says
  // which conversion the server chose.
  for (const [label, id, wantDecision, wantPill] of [["direct play", ids.direct, "direct", null],
    ["converted", ids.converted, "converted", "Audio converted"], ["transcoded", ids.transcoded, "converted", "Video converted"]]) {
    if (!id) {
      fail(`play ${label}`, "sample clip not found (run against scripts/dev-server.sh)");
      continue;
    }
    console.log(`play ${label} (#/play/${id})`);
    exceptions = [];
    // Page.navigate returns before the app handles hashchange, so the previous
    // clip's player can still be on the page: mark its video so this clip is
    // never judged by the last one's.
    await evaluate(`document.querySelectorAll("video").forEach((v) => { v.dataset.stale = "1"; v.pause(); });`);
    await s("Page.navigate", { url: `${base}/#/play/${id}?start=0` });
    const res = await evaluate(`
      const fresh = () => document.querySelector("video:not([data-stale])");
      for (let i = 0; i < 60 && !fresh(); i++) await new Promise((r) => setTimeout(r, 150));
      const v = fresh();
      if (!v) return { ok: false, why: document.getElementById("view").innerText.slice(0, 200) };
      v.muted = true;
      v.play().catch(() => {});
      for (let i = 0; i < 80 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
      const pill = document.querySelector(".player .pill");
      // All the player's text, hidden controls included: they show on the next
      // mouse move, so a "null" there is one the viewer will see.
      const shell = v.closest(".player");
      return { ok: v.currentTime > 1.5, t: v.currentTime, err: v.error && v.error.code, converted: !v.controls, pill: pill ? pill.textContent : null,
        text: shell ? shell.textContent : "",
        why: document.querySelector(".player .overlay:not([hidden])") ? document.querySelector(".player .overlay").innerText : "" };`);
    if (!res.ok) fail(`play ${label}`, `video did not advance (t=${res.t}, media error ${res.err}) ${res.why || ""}`);
    const playerJunk = (res.text || "").match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
    if (playerJunk) fail(`play ${label}`, `the player shows "${playerJunk[0]}": ${res.text.slice(0, 160).replace(/\s+/g, " ")}`);
    if (res.converted !== (wantDecision === "converted")) fail(`play ${label}`, `expected a ${wantDecision} stream`);
    if (wantPill && res.pill !== wantPill) fail(`play ${label}`, `labelled "${res.pill}", expected "${wantPill}"`);
    if (exceptions.length) fail(`play ${label}`, `threw: ${exceptions[0]}`);
  }

  // Safari: the same two conversions as HLS, then a seek, which starts a new
  // stream at the new position.
  const nativeHLS = process.env.VCRX_SMOKE_HLS !== "mse" &&
    (await evaluate(`return document.createElement("video").canPlayType("application/vnd.apple.mpegurl") !== ""`));
  if (nativeHLS) await hlsPlayer(s, evaluate, ids);
  else await hlsMSE(evaluate, ids);

  // Subtitles: "Search for subtitles…" (the dev server's offline stub, never
  // OpenSubtitles), download and switch in one click, then the timing control,
  // which must change the cues the server serves, and Delete on the item page.
  if (ids.direct) {
    console.log(`subtitles: search, download, timing (#/play/${ids.direct})`);
    exceptions = [];
    await s("Page.navigate", { url: `${base}/#/play/${ids.direct}?start=0` });
    const res = await evaluate(`
      const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
      const wait = async (fn, n = 80) => { for (let i = 0; i < n; i++) { const v = await fn(); if (v) return v; await sleep(150); } return null; };
      const sel = await wait(() => document.querySelector('.player select[aria-label="Subtitles"]'));
      if (!sel) return { why: "no subtitle menu" };
      if (![...sel.options].some((o) => o.value === "search")) return { why: "no Search for subtitles entry in the menu" };
      sel.value = "search";
      sel.dispatchEvent(new Event("change"));
      const btn = await wait(() => document.querySelector(".sub-search:not([hidden]) .sub-result button:not([disabled])"));
      if (!btn) return { why: "no search results: " + ((document.querySelector(".sub-search") || {}).innerText || "no panel") };
      const row = btn.closest(".sub-result").innerText;
      if (!/Matches this file/.test(row)) return { why: "first result has no hash-match badge: " + row };
      btn.click();
      const picked = await wait(() => sel.selectedOptions[0] && /downloaded/.test(sel.selectedOptions[0].textContent));
      if (!picked) return { why: "the downloaded subtitle was not selected" };
      const v = document.querySelector("video");
      const firstCue = () => { const t = [...v.textTracks].find((t) => t.mode === "showing"); return t && t.cues && t.cues.length ? t.cues[0] : null; };
      const c0 = await wait(firstCue);
      if (!c0 || !/Downloaded subtitle/.test(c0.text)) return { why: "downloaded cues not shown: " + (c0 && c0.text) };
      const start0 = c0.startTime;
      const item = await (await fetch("/api/v1/items/${ids.direct}")).json();
      const track = item.versions.flatMap((x) => x.subtitle_tracks).find((t) => t.is_downloaded);
      if (!track) return { why: "the item lists no downloaded track" };
      const served = async () => (await (await fetch("/api/v1/subtitles/" + track.id + "?format=vtt")).text()).match(/^\\S+ --> \\S+/m)[0];
      const before = await served();
      const plus = document.querySelector(".sub-timing-plus");
      if (!plus || plus.closest("[hidden]")) return { why: "no subtitle timing control" };
      plus.click();
      plus.click();
      const shown = await wait(() => { const c = firstCue(); return c && Math.abs(c.startTime - start0 - 0.5) < 0.01; });
      const after = await served();
      document.querySelector(".sub-timing-reset").click();
      await wait(async () => (await served()) === before);
      const reset = await served();
      return { ok: true, before, after, reset, shown: !!shown, label: document.querySelector(".sub-timing-value").textContent };`);
    if (!res.ok) fail("subtitles", res.why);
    else {
      console.log(`  served ${res.before}, then ${res.after} at +500 ms, then ${res.reset} after Reset`);
      // ffmpeg writes mm:ss.ttt and the shifted copy hh:mm:ss.ttt, so compare times.
      const ms = (line) => line.split(" --> ").map((t) => t.split(":").reduce((acc, p) => acc * 60 + Number(p), 0) * 1000);
      const [b0, b1] = ms(res.before), [a0, a1] = ms(res.after);
      if (b0 !== 2000 || b1 !== 5000) fail("subtitles", `unexpected first cue ${res.before}`);
      if (Math.round(a0 - b0) !== 500 || Math.round(a1 - b1) !== 500) fail("subtitles", `timing +500 ms served ${res.after}`);
      if (!res.shown) fail("subtitles", "the player did not show the shifted cue");
      if (res.reset !== res.before || res.label !== "0 ms") fail("subtitles", `Reset left ${res.reset} (${res.label})`);
    }
    if (exceptions.length) fail("subtitles", `threw: ${exceptions[0]}`);

    console.log(`subtitles: delete on the item page (#/item/${ids.direct})`);
    await s("Page.navigate", { url: `${base}/#/item/${ids.direct}` });
    const del = await evaluate(`
      const wait = async (fn, n = 60) => { for (let i = 0; i < n; i++) { const v = fn(); if (v) return v; await new Promise((r) => setTimeout(r, 150)); } return null; };
      const btn = await wait(() => document.querySelector(".sub-admin:not([hidden]) .sub-admin-row button"));
      if (!btn) return { why: "no downloaded subtitle listed for the admin" };
      btn.click();
      const gone = await wait(() => document.querySelector(".sub-admin:not([hidden])") && !document.querySelector(".sub-admin-row"));
      return gone ? { ok: true } : { why: "the subtitle was still listed after Delete" };`);
    if (!del.ok) fail("subtitles delete", del.why);
  }
  await profilePicker(s, evaluate, () => exceptions, (v) => { exceptions = v; });
  await inviteLoop(s, evaluate, () => exceptions, (v) => { exceptions = v; });
  if (peer) await friendsLoop(s, evaluate, () => exceptions, (v) => { exceptions = v; });
  else console.log("friends' servers: skipped (set VCRX_SMOKE_PEER to a second dev server)");
  await s("Page.navigate", { url: `${base}/#/` });
  await sleep(500);

  // History: the clip just played is listed on its details page for an admin.
  // The player reports stop as it unloads, so give that a moment to land.
  if (ids.direct) {
    const path = `/item/${ids.direct}`;
    console.log(`page #${path} (history)`);
    exceptions = [];
    let res = null;
    for (let attempt = 0; attempt < 10; attempt++) {
      await s("Page.navigate", { url: `${base}/#${path}?try=${attempt}` });
      res = await evaluate(`
        for (let i = 0; i < 40 && !document.querySelector(".hero h1"); i++) await new Promise((r) => setTimeout(r, 150));
        await new Promise((r) => setTimeout(r, 300));
        const sec = document.querySelector(".item-history");
        return { hero: !!document.querySelector(".hero h1"), rows: sec ? sec.querySelectorAll("tbody tr").length : 0,
          text: document.getElementById("view").innerText, error: !!document.querySelector("#view .error-box") };`);
      if (res.rows > 0) break;
      await sleep(500);
    }
    if (!res.hero) fail(`#${path}`, "no .hero h1 with history on the page");
    if (!res.rows) fail(`#${path}`, "no History rows after playing the clip");
    if (res.error) fail(`#${path}`, "shows an error box");
    const junk = res.text.match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
    if (junk) {
      const at = res.text.slice(Math.max(0, junk.index - 60), junk.index + 20).replace(/\s+/g, " ");
      fail(`#${path}`, `shows "${junk[0]}" in "...${at}..."`);
    }
    if (exceptions.length) fail(`#${path}`, `threw: ${exceptions[0]}`);
  }

  // Skip Intro and Next Episode on the sample season with a shared theme. The
  // server detects the markers in the background after its scans, so wait
  // for them first.
  console.log("skip intro (Lantern Bay)");
  const bay = await evaluate(`
    const j = (p) => fetch("/api/v1" + p).then((r) => r.json());
    const found = (await j("/search?q=Lantern%20Bay")).items || [];
    const show = found.find((i) => i.kind === "show" && i.title === "Lantern Bay");
    if (!show) return { why: "no Lantern Bay show in the library" };
    const season = (await j("/items/" + show.id + "/children")).items[0];
    const eps = (await j("/items/" + season.id + "/children")).items;
    const ep = eps[1];
    for (let i = 0; i < 180; i++) {
      const d = await j("/items/" + ep.id);
      const intro = d.markers.find((m) => m.kind === "intro");
      const credits = d.markers.find((m) => m.kind === "credits");
      if (intro && credits) return { id: ep.id, intro, credits };
      await new Promise((r) => setTimeout(r, 1000));
    }
    return { why: "no intro and credits markers after 3 minutes" };`);
  if (!bay.id) {
    fail("skip intro", bay.why);
  } else {
    exceptions = [];
    await s("Page.navigate", { url: `${base}/#/play/${bay.id}?start=${bay.intro.start_ms + 1000}` });
    const res = await evaluate(`
      for (let i = 0; i < 60 && !document.querySelector("video"); i++) await new Promise((r) => setTimeout(r, 150));
      const v = document.querySelector("video");
      if (!v) return { why: "no video" };
      v.muted = true;
      v.play().catch(() => {});
      const shown = () => { const b = document.querySelector(".player .skip-btn"); return b && !b.hidden ? b : null; };
      for (let i = 0; i < 60 && !shown(); i++) await new Promise((r) => setTimeout(r, 150));
      const b = shown();
      if (!b) return { why: "no Skip button inside the intro (t=" + v.currentTime + ")" };
      const label = b.textContent;
      b.click();
      for (let i = 0; i < 40 && v.currentTime * 1000 < ${bay.intro.end_ms} - 500; i++) await new Promise((r) => setTimeout(r, 150));
      return { label, t: v.currentTime, hidden: !shown() };`);
    if (res.why) fail("skip intro", res.why);
    else {
      if (res.label !== "Skip Intro") fail("skip intro", `button says "${res.label}"`);
      if (res.t * 1000 < bay.intro.end_ms - 500) fail("skip intro", `still at ${res.t} s, intro ends at ${bay.intro.end_ms / 1000} s`);
      if (!res.hidden) fail("skip intro", "button still showing after the skip");
    }
    // Credits that run to the end, with an episode after: Next Episode, and Up Next.
    await s("Page.navigate", { url: `${base}/#/play/${bay.id}?start=${bay.credits.start_ms + 1000}` });
    const next = await evaluate(`
      for (let i = 0; i < 60 && !document.querySelector("video"); i++) await new Promise((r) => setTimeout(r, 150));
      const v = document.querySelector("video");
      v.muted = true;
      v.play().catch(() => {});
      const shown = () => { const b = document.querySelector(".player .skip-btn"); return b && !b.hidden ? b : null; };
      for (let i = 0; i < 60 && !shown(); i++) await new Promise((r) => setTimeout(r, 150));
      const b = shown();
      if (!b) return { why: "no button inside the credits" };
      const label = b.textContent;
      b.click();
      for (let i = 0; i < 60 && !document.querySelector(".player .up-next"); i++) await new Promise((r) => setTimeout(r, 150));
      return { label, upNext: !!document.querySelector(".player .up-next") };`);
    if (next.why) fail("next episode", next.why);
    else {
      if (next.label !== "Next Episode") fail("next episode", `button says "${next.label}"`);
      if (!next.upNext) fail("next episode", "Up Next did not appear");
    }
    if (exceptions.length) fail("skip intro", `threw: ${exceptions[0]}`);
    // The admin's marker editor on the episode page, and detection's progress.
    for (const [path, selector] of [[`/item/${bay.id}`, ".markers-edit .marker-row"], ["/settings/markers", "table.list"]]) {
      await s("Page.navigate", { url: `${base}/#${path}` });
      const page = await evaluate(`
        for (let i = 0; i < 40 && !document.querySelector(${JSON.stringify(selector)}); i++) await new Promise((r) => setTimeout(r, 150));
        const text = document.getElementById("view").innerText;
        return { found: !!document.querySelector(${JSON.stringify(selector)}), junk: (text.match(/\\b(null|undefined|NaN)\\b/) || [])[0] || "" };`);
      if (!page.found) fail(`#${path}`, `no ${selector}`);
      if (page.junk) fail(`#${path}`, `shows "${page.junk}"`);
    }
  }
}

const safariUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15";

async function hlsPlayer(s, evaluate, ids) {
  const { userAgent } = await send("Browser.getVersion");
  await s("Emulation.setUserAgentOverride", { userAgent: safariUA });
  try {
    for (const [label, id, wantPill] of [["HLS converted", ids.converted, "Audio converted"], ["HLS transcoded", ids.transcoded, "Video converted"]]) {
      if (!id) {
        fail(`play ${label}`, "sample clip not found (run against scripts/dev-server.sh)");
        continue;
      }
      console.log(`play ${label} as Safari (#/play/${id})`);
      // As in the plays above: the previous clip's player can still be on the page
      // when this one is judged, so mark its video and read only a fresh one.
      await evaluate(`document.querySelectorAll("video").forEach((v) => { v.dataset.stale = "1"; v.pause(); });`);
      await s("Page.navigate", { url: `${base}/#/play/${id}?start=0` });
      const res = await evaluate(`
        const fresh = () => document.querySelector("video:not([data-stale])");
        for (let i = 0; i < 60 && !fresh(); i++) await new Promise((r) => setTimeout(r, 150));
        const v = fresh();
        if (!v) return { ok: false, why: document.getElementById("view").innerText.slice(0, 200) };
        const player = v.closest(".player") || document;
        v.muted = true;
        v.play().catch(() => {});
        const advanced = async () => {
          for (let i = 0; i < 100 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
          return v.currentTime > 1.5;
        };
        const ok = await advanced();
        const src = v.currentSrc;
        const pill = player.querySelector(".pill");
        // Seek 30 s on: a new stream, and the clock shows the new position.
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight" }));
        for (let i = 0; i < 60 && v.currentSrc === src; i++) await new Promise((r) => setTimeout(r, 100));
        const seeked = v.currentSrc !== src && await advanced();
        const clock = player.querySelector(".pc-time") ? player.querySelector(".pc-time").textContent : "";
        return { ok, seeked, t: v.currentTime, src, err: v.error && v.error.code, clock, converted: !v.controls,
          pill: pill ? pill.textContent : null, ua: navigator.userAgent.includes("Version/18.0 Safari"),
          why: player.querySelector(".overlay:not([hidden])") ? player.querySelector(".overlay").innerText : "" };`);
      if (!res.ua) fail(`play ${label}`, "the user agent override did not take");
      if (!res.ok) fail(`play ${label}`, `video did not advance (t=${res.t}, media error ${res.err}) ${res.why || ""}`);
      if (!/\/hls\/[0-9a-f]{16}\/index\.m3u8$/.test(res.src || "")) fail(`play ${label}`, `played ${res.src}, not an HLS playlist`);
      if (!res.converted) fail(`play ${label}`, "expected VCRX controls on a converted stream");
      if (res.pill !== wantPill) fail(`play ${label}`, `labelled "${res.pill}", expected "${wantPill}"`);
      const at = (res.clock.match(/^(\d+):(\d+)/) || []).slice(1).map(Number);
      if (!res.seeked || !(at[0] * 60 + at[1] >= 30)) fail(`play ${label}`, `seek 30 s on: new stream ${res.seeked}, clock "${res.clock}"`);
    }
  } finally {
    await s("Emulation.setUserAgentOverride", { userAgent });
  }
}

async function hlsMSE(evaluate, ids) {
  console.log("play HLS converted through Media Source Extensions (no native HLS in this Chrome)");
  if (!ids.converted) {
    fail("play HLS", "sample clip not found (run against scripts/dev-server.sh)");
    return;
  }
  const res = await evaluate(`
    const caps = { containers: ["mp4"], video_codecs: [{ codec: "h264" }], audio_codecs: [{ codec: "aac" }],
      hls_stream: true, default_audio_only: true };
    const json = (r) => r.json();
    const sess = await fetch("/api/v1/playback/sessions", { method: "POST", headers: { "Content-Type": "application/json", "X-VCRX-Client": "web" },
      body: JSON.stringify({ item_id: ${ids.converted}, start_position_ms: 0, capabilities: caps }) }).then(json);
    if (!sess.stream || sess.stream.protocol !== "hls") return { ok: false, why: "protocol " + JSON.stringify(sess.stream) };
    const at = await fetch(sess.stream.seek_url + "?position_ms=0").then(json);
    let pl = "";
    for (let i = 0; i < 100 && !pl.includes("#EXT-X-ENDLIST"); i++) {
      pl = await fetch(at.url).then((r) => r.text());
      await new Promise((r) => setTimeout(r, 200));
    }
    const dir = at.url.replace(/index\\.m3u8$/, "");
    const total = [...pl.matchAll(/#EXTINF:([0-9.]+)/g)].reduce((a, m) => a + Number(m[1]), 0);
    const files = [/#EXT-X-MAP:URI="([^"]+)"/.exec(pl)[1], ...pl.split("\\n").filter((l) => l && !l.startsWith("#"))];
    const v = document.createElement("video");
    v.muted = true;
    document.body.append(v);
    const ms = new MediaSource();
    v.src = URL.createObjectURL(ms);
    await new Promise((r) => ms.addEventListener("sourceopen", r, { once: true }));
    const sb = ms.addSourceBuffer('video/mp4; codecs="avc1.640028, mp4a.40.2"');
    for (const f of files) {
      const buf = await fetch(dir + f).then((r) => r.arrayBuffer());
      await new Promise((res, rej) => { sb.addEventListener("updateend", res, { once: true }); sb.addEventListener("error", rej, { once: true }); sb.appendBuffer(buf); });
    }
    ms.endOfStream();
    v.play().catch(() => {});
    for (let i = 0; i < 80 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
    await fetch("/api/v1/playback/sessions/" + sess.session_id + "/events", { method: "POST",
      headers: { "Content-Type": "application/json", "X-VCRX-Client": "web" }, body: JSON.stringify({ type: "stop", position_ms: 0 }) });
    return { ok: v.currentTime > 1.5, t: v.currentTime, total, duration: v.duration, err: v.error && v.error.code };`);
  if (!res.ok) fail("play HLS (MSE)", `video did not advance (t=${res.t}, media error ${res.err}) ${res.why || ""}`);
  if (!(Math.abs(res.total - 90) < 1 && Math.abs(res.duration - 90) < 1)) fail("play HLS (MSE)", `playlist ${res.total} s, media ${res.duration} s, want 90`);
}

// "Who's watching?" on a phone: a kid's profile with a PIN refuses a wrong
// PIN on the pad and opens with the right one; back on the admin, reached
// from the kid, it asks for the password. Leaves the browser as the admin.
async function profilePicker(s, evaluate, getExceptions, setExceptions) {
  console.log("profile picker (PIN pad, 390 px)");
  setExceptions([]);
  await s("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
  const helpers = `
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    const wait = async (sel) => { for (let i = 0; i < 40 && !document.querySelector(sel); i++) await sleep(150); return document.querySelector(sel); };
    const me = () => fetch("/api/v1/me").then((r) => r.json()).then((m) => m.username);
    const left = async () => { for (let i = 0; i < 40 && location.hash.startsWith("#/profiles"); i++) await sleep(150); };`;
  const ids = await evaluate(`
    const call = (p, method = "GET", body) => fetch("/api/v1" + p, { method, body: body && JSON.stringify(body),
      headers: { "X-VCRX-Client": "web", "Content-Type": "application/json" } }).then((r) => (r.status === 204 ? null : r.json()));
    const all = (await call("/admin/profiles")).profiles;
    const kid = all.find((p) => p.username === "Smoke Kid") || (await call("/admin/profiles", "POST", { name: "Smoke Kid", kid: true }));
    await call("/admin/profiles/" + kid.id + "/pin", "PUT", { pin: "2468" });
    return { kid: kid.id, admin: all.find((p) => p.username === "dev").id };`);
  await s("Page.navigate", { url: `${base}/#/profiles` });
  const kid = await evaluate(`${helpers}
    const card = await wait('.profile[data-id="${ids.kid}"]');
    if (!card) return { why: "the kid's profile is not in the picker: " + document.getElementById("view").innerText.slice(0, 160) };
    card.click();
    if (!(await wait(".pin-keys"))) return { why: "choosing a profile with a PIN showed no PIN pad" };
    const press = (k) => document.querySelector('.pin-key[data-key="' + k + '"]').click();
    for (const k of "1111") press(k);
    press("ok");
    let wrong = "";
    for (let i = 0; i < 40 && !wrong; i++) { wrong = document.querySelector(".pin-error").textContent; await sleep(150); }
    const width = document.documentElement.scrollWidth;
    for (const k of "2468") press(k);
    press("ok");
    await left();
    return { wrong, width, screen: screen.width, me: await me() };`);
  if (kid.why) fail("profile picker", kid.why);
  else {
    if (!kid.wrong) fail("profile picker", "a wrong PIN showed no error");
    if (kid.width > kid.screen + 1) fail("profile picker on a phone", `PIN pad is ${kid.width}px wide on a ${kid.screen}px screen`);
    if (kid.me !== "Smoke Kid") fail("profile picker", `the right PIN left the browser signed in as ${kid.me}`);
  }
  await s("Page.navigate", { url: `${base}/#/profiles` });
  const back = await evaluate(`${helpers}
    const card = await wait('.profile[data-id="${ids.admin}"]');
    if (!card) return { why: "no admin profile in the picker" };
    card.click();
    const pw = await wait(".pin-password input");
    if (!pw) return { why: "the admin, reached from a kid's profile, did not ask for a password" };
    pw.value = "devpassword";
    document.querySelector(".pin-password button").click();
    await left();
    return { me: await me() };`);
  if (back.why) fail("profile picker", back.why);
  else if (back.me !== "dev") fail("profile picker", `switching back left the browser as ${back.me}`);
  if (getExceptions().length) fail("profile picker", `threw: ${getExceptions()[0]}`);
  await s("Emulation.clearDeviceMetricsOverride");
}

// The invite is the account: the admin mints an invite for Movies only, a
// second browser (its own cookie jar, so not the admin) opens the link and
// joins with no password, sees Movies and nothing else, never the profile
// picker, and cannot replay the link; then the admin revokes it from Settings
// and its next request is refused. Then a TV's pairing code is approved from
// the admin's phone page and the TV's poll gets its session.
async function inviteLoop(s, evaluate, getExceptions, setExceptions) {
  console.log("invite: mint, redeem, scope, revoke; pair a TV");
  setExceptions([]);
  const helpers = `
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    const wait = async (sel, n = 40) => { for (let i = 0; i < n && !document.querySelector(sel); i++) await sleep(150); return document.querySelector(sel); };`;
  await s("Page.navigate", { url: `${base}/#/settings/devices` });
  const minted = await evaluate(`${helpers}
    const form = await wait(".scope-form");
    if (!form) return { why: "no invite form under Settings > Devices" };
    form.elements.name.value = "Smoke Phone";
    const libs = (await fetch("/api/v1/libraries").then((r) => r.json())).libraries;
    const movies = libs.find((l) => l.name === "Movies");
    for (const box of form.querySelectorAll(".checks input[type=checkbox][value]")) box.checked = box.value === String(movies.id);
    form.querySelector("button[type=submit]").click();
    const link = await wait(".invite-card .invite-link");
    const img = await wait(".invite-card img.qr");
    if (!link || !img) return { why: "minting showed no invite: " + document.getElementById("view").innerText.slice(0, 200) };
    for (let i = 0; i < 20 && !img.naturalWidth; i++) await sleep(100);
    let decoded = null;
    if ("BarcodeDetector" in window) {
      try { decoded = ((await new BarcodeDetector({ formats: ["qr_code"] }).detect(img))[0] || {}).rawValue || ""; } catch { decoded = null; }
    }
    return { url: link.value, qr: img.naturalWidth, decoded, card: document.querySelector(".invite-card").innerText };`);
  if (minted.why) return fail("invite", minted.why);
  if (!/#\/invite\/[a-z2-7]{32}$/.test(minted.url)) fail("invite", `the invite link looks wrong: ${minted.url}`);
  if (!minted.qr) fail("invite", "the QR code image did not load");
  if (minted.decoded !== null && minted.decoded !== minted.url) fail("invite", `the QR code reads "${minted.decoded}", not the link`);
  if (minted.decoded !== null) console.log("  QR code decodes to the invite link");
  // The invited device: a browser context of its own, at phone width.
  const { browserContextId } = await send("Target.createBrowserContext");
  const { targetId } = await send("Target.createTarget", { url: "about:blank", browserContextId });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const d = (m, p) => send(m, p, sessionId);
  const devErrors = [];
  listeners.push((m) => {
    if (m.sessionId === sessionId && m.method === "Runtime.exceptionThrown") devErrors.push(m.params.exceptionDetails.text);
  });
  await d("Runtime.enable");
  await d("Page.enable");
  await d("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
  const devEval = async (expr) => {
    const r = await d("Runtime.evaluate", { expression: `(async () => { ${helpers} ${expr} })()`, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  };
  const call = `const call = (p, method = "GET", body) => fetch("/api/v1" + p, { method, body: body && JSON.stringify(body),
      headers: { "X-VCRX-Client": "web", "Content-Type": "application/json" } }).then(async (r) => ({ status: r.status, body: await r.json().catch(() => null) }));`;
  try {
    await d("Page.navigate", { url: minted.url });
    const joined = await devEval(`${call}
      const join = await wait(".invite-page button");
      if (!join) return { why: "the invite link showed no Join button: " + document.getElementById("view").innerText.slice(0, 160) };
      const before = (await call("/me")).status;
      const width = document.documentElement.scrollWidth;
      join.click();
      for (let i = 0; i < 40 && location.hash.startsWith("#/invite/"); i++) await sleep(150);
      await wait(".row .card, .empty, .grid .card");
      const me = (await call("/me")).body || {};
      const libs = ((await call("/libraries")).body || {}).libraries || [];
      const picker = (await call("/profiles")).status;
      const switched = (await call("/auth/switch", "POST", { user_id: 1 })).status;
      const replay = (await call("/invites/redeem", "POST", { invite: ${JSON.stringify(minted.url.split("/").pop())} })).status;
      const avatarBtn = !document.getElementById("nav-profile").hidden;
      location.hash = "#/settings";
      await wait(".settings dl");
      return { before, width, screen: screen.width, hash: location.hash, me, libs: libs.map((l) => l.name), picker, switched, replay, avatarBtn,
        settings: document.querySelector(".settings dl").innerText };`);
    if (joined.why) fail("invite", joined.why);
    else {
      if (joined.before !== 401) fail("invite", `before joining, the new browser was already signed in (${joined.before})`);
      if (joined.width > joined.screen + 1) fail("invite on a phone", `the invite page is ${joined.width}px wide on a ${joined.screen}px screen`);
      if (joined.me.kind !== "token" || joined.me.display_name !== "Smoke Phone") fail("invite", `joined as ${JSON.stringify(joined.me)}, not the invited device`);
      if (joined.libs.join() !== "Movies") fail("invite", `the invited device sees ${joined.libs.join(", ") || "nothing"}, not Movies only`);
      if (joined.picker !== 403 || joined.switched !== 403) fail("invite", `the invited device reached the profile picker (${joined.picker}) or a switch (${joined.switched})`);
      if (joined.avatarBtn) fail("invite", "the invited device shows the Switch profile button");
      if (joined.replay !== 401) fail("invite", `replaying the spent invite answered ${joined.replay}, not 401`);
      if (!/Smoke Phone \(invited\)/.test(joined.settings)) fail("invite", `Settings does not say this is an invited device: ${joined.settings.slice(0, 120)}`);
    }
    // The admin revokes it from Settings > Devices (a fresh load of the list).
    await s("Page.navigate", { url: `${base}/#/settings/devices?after=join` });
    const revoked = await evaluate(`${helpers}
      window.confirm = () => true;
      let row = null;
      for (let i = 0; i < 40 && !row; i++) {
        row = [...document.querySelectorAll(".device-row")].find((r) => r.innerText.includes("Smoke Phone") && r.dataset.state === "active");
        if (!row) await sleep(150);
      }
      if (!row) return { why: "the redeemed device is not listed as active: " + document.getElementById("view").innerText.slice(0, 200) };
      const seen = row.innerText;
      row.querySelector("button").click();
      let state = "";
      for (let i = 0; i < 40 && state !== "revoked"; i++) {
        await sleep(150);
        const r = [...document.querySelectorAll(".device-row")].find((x) => x.innerText.includes("Smoke Phone"));
        state = r ? r.dataset.state : "";
      }
      return { state, seen };`);
    if (revoked.why) fail("invite", revoked.why);
    else {
      if (revoked.state !== "revoked") fail("invite", `after Revoke the device shows ${revoked.state}`);
      if (!/Last seen/.test(revoked.seen)) fail("invite", `the device row has no last seen: ${revoked.seen}`);
    }
    const after = await devEval(`${call}
      const me = (await call("/me")).status;
      location.hash = "#/";
      for (let i = 0; i < 40 && !location.hash.startsWith("#/login"); i++) await sleep(150);
      return { me, hash: location.hash };`);
    if (after.me !== 401) fail("invite", `after the revoke the device's next request answered ${after.me}, not 401`);
    if (!after.hash.startsWith("#/login")) fail("invite", `after the revoke the device stayed on ${after.hash}`);

    // A TV pairs: it asks for a code (here from the signed-out browser), the
    // admin approves it on #/pair/<code>, and the TV's poll gets its session.
    const tv = await devEval(`${call}
      const r = await call("/pair", "POST", { device: { id: "smoke-tv", name: "Smoke TV", platform: "android_tv", client: "vcrx-tv", client_version: "1" } });
      return r.body;`);
    if (!tv || !tv.user_code) fail("pair", `no pairing code: ${JSON.stringify(tv)}`);
    else {
      await s("Page.navigate", { url: `${base}/#/pair/${tv.user_code.replace("-", "")}` });
      const approved = await evaluate(`${helpers}
        const form = await wait(".pair-page .scope-form");
        if (!form) return { why: "the approval page has no scope form: " + document.getElementById("view").innerText.slice(0, 200) };
        const who = document.querySelector(".pair-who").innerText;
        form.querySelector("button[type=submit]").click();
        for (let i = 0; i < 40 && document.querySelector(".pair-page .scope-form"); i++) await sleep(150);
        return { who, text: document.getElementById("view").innerText };`);
      if (approved.why) fail("pair", approved.why);
      else {
        if (!approved.who.includes("Smoke TV") || !approved.who.includes(tv.user_code)) fail("pair", `the approval page does not name the TV and its code: ${approved.who}`);
        if (!/is paired/.test(approved.text)) fail("pair", `approving did not confirm: ${approved.text.slice(0, 160)}`);
      }
      const polled = await devEval(`${call}
        const r = await call("/pair/poll", "POST", { device_code: ${JSON.stringify(tv.device_code)} });
        const again = await call("/pair/poll", "POST", { device_code: ${JSON.stringify(tv.device_code)} });
        const me = r.body && r.body.token ? await fetch("/api/v1/me", { headers: { Authorization: "Bearer " + r.body.token } }).then((x) => x.json()) : null;
        return { status: r.body && r.body.status, me, again: again.status };`);
      if (polled.status !== "approved" || !polled.me || polled.me.kind !== "token") fail("pair", `the TV's poll after approval: ${JSON.stringify(polled)}`);
      if (polled.again !== 404) fail("pair", `a second poll answered ${polled.again}, not 404: the code is single-use`);
    }
    if (devErrors.length) fail("invite", `the invited device threw: ${devErrors[0]}`);
  } finally {
    await send("Target.closeTarget", { targetId }).catch(() => {});
    await send("Target.disposeBrowserContext", { browserContextId }).catch(() => {});
  }
  if (getExceptions().length) fail("invite", `threw: ${getExceptions()[0]}`);
}

// Friends' servers (milestone 9, item 3), against a second dev server: its
// admin mints a server invite (over its API: that server signs this machine
// in without a password), this server's admin pastes it into Settings >
// Friends, Home shows the friend's titles badged with where they live, one
// plays from the friend's server through this one, and once this side
// unlinks, the friend's titles leave Home.
async function friendsLoop(s, evaluate, getExceptions, setExceptions) {
  console.log(`friends' servers: link ${peer}, badge, play, unlink`);
  setExceptions([]);
  const helpers = `
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    const wait = async (sel, n = 40) => { for (let i = 0; i < n && !document.querySelector(sel); i++) await sleep(150); return document.querySelector(sel); };`;
  const json = { "Content-Type": "application/json" };
  const auto = await fetch(`${peer}/api/v1/auth/auto`, { method: "POST", headers: json,
    body: JSON.stringify({ device: { id: "smoke-peer-admin", name: "Smoke", platform: "web", client: "web-smoke" } }) }).then((r) => r.json());
  const auth = { ...json, Authorization: `Bearer ${auto.token}` };
  const libs = ((await fetch(`${peer}/api/v1/libraries`, { headers: auth }).then((r) => r.json())).libraries || []).map((l) => l.id);
  const minted = await fetch(`${peer}/api/v1/admin/peers/invites`, { method: "POST", headers: auth,
    body: JSON.stringify({ name: "Smoke server", library_ids: libs, max_resolution: "1080p", allow_transcode: true, expires_in_days: 1 }) })
    .then((r) => r.json());
  if (!/#\/link\/[a-z2-7]{32}$/.test(minted.url || "")) return fail("friends", `the friend's server minted no server invite: ${JSON.stringify(minted).slice(0, 200)}`);

  await s("Page.navigate", { url: `${base}/#/settings/friends` });
  const linked = await evaluate(`${helpers}
    const form = await wait(".link-form");
    if (!form) return { why: "no link form under Settings > Friends" };
    form.elements.invite.value = ${JSON.stringify(minted.url)};
    form.elements.name.value = "Smoke Friend";
    form.querySelector("button[type=submit]").click();
    let row = null;
    for (let i = 0; i < 60 && !row; i++) {
      row = [...document.querySelectorAll(".peer-row")].find((r) => r.innerText.includes("Smoke Friend"));
      if (!row) await sleep(150);
    }
    if (!row) return { why: "the friend's server is not listed after linking: " + form.querySelector(".form-error").textContent };
    return { id: Number(row.dataset.id), state: row.dataset.state };`);
  if (linked.why) return fail("friends", linked.why);
  try {
    if (linked.state !== "online") fail("friends", `the linked server shows ${linked.state}, not online`);
    await s("Page.navigate", { url: `${base}/#/?friends=1` });
    const home = await evaluate(`${helpers}
      if (!(await wait(".friends-row .card", 80))) return { why: "Home has no row from friends' servers: " + document.getElementById("view").innerText.slice(0, 200) };
      const cards = [...document.querySelectorAll(".friends-row .card")];
      const copper = cards.find((c) => c.title === "Copper Valley");
      return { badges: cards.map((c) => (c.querySelector(".source-badge") || {}).textContent || ""), href: copper && copper.getAttribute("href"),
        text: document.querySelector(".friends-row").innerText };`);
    if (home.why) return fail("friends", home.why);
    if (!home.badges.length || home.badges.some((b) => !b.startsWith("on Smoke Friend"))) fail("friends", `cards without a source badge: ${JSON.stringify(home.badges)}`);
    if (!home.badges.some((b) => b === "on Smoke Friend, 720p")) fail("friends", `no badge names the resolution: ${JSON.stringify(home.badges)}`);
    const junk = home.text.match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
    if (junk) fail("friends", `the friends row shows "${junk[0]}"`);
    if (!home.href) return fail("friends", "Copper Valley is not in the friends row");
    console.log(`  ${home.badges.length} titles badged "${home.badges[0]}"`);

    // The phone: the friends row fits.
    await s("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
    await s("Page.navigate", { url: `${base}/#/?friends=phone` });
    const phone = await evaluate(`${helpers}
      await wait(".friends-row .card", 80);
      await sleep(200);
      return { row: !!document.querySelector(".friends-row .source-badge"), scroll: document.documentElement.scrollWidth, width: screen.width };`);
    await s("Emulation.clearDeviceMetricsOverride");
    if (!phone.row) fail("friends on a phone", "no badged friends row");
    if (phone.scroll > phone.width + 1) fail("friends on a phone", `Home is ${phone.scroll}px wide on a ${phone.width}px screen`);

    // The title's page, then Play: it streams from the friend's server.
    await s("Page.navigate", { url: `${base}/${home.href}` });
    const page = await evaluate(`${helpers}
      const btn = await wait(".hero .actions .btn-primary", 60);
      if (!btn) return { why: "the friend's title page has no Play: " + document.getElementById("view").innerText.slice(0, 200) };
      const text = document.getElementById("view").innerText;
      document.querySelectorAll("video").forEach((v) => { v.dataset.stale = "1"; });
      btn.click();
      const fresh = () => document.querySelector("video:not([data-stale])");
      for (let i = 0; i < 60 && !fresh(); i++) await sleep(150);
      const v = fresh();
      if (!v) return { why: "Play showed no video: " + document.getElementById("view").innerText.slice(0, 200), text };
      v.muted = true;
      v.play().catch(() => {});
      for (let i = 0; i < 80 && !(v.currentTime > 1.5); i++) await sleep(150);
      return { text, t: v.currentTime, src: v.currentSrc, hash: location.hash, err: v.error && v.error.code,
        overlay: document.querySelector(".player .overlay:not([hidden])") ? document.querySelector(".player .overlay").innerText : "" };`);
    if (page.why) fail("friends", page.why);
    else {
      if (!/on Smoke Friend/i.test(page.text)) fail("friends", `the title page does not say where it lives: ${page.text.slice(0, 160)}`);
      const junk2 = page.text.match(/\b(null|undefined|NaN)\b|\[object \w+\]/);
      if (junk2) fail("friends", `the title page shows "${junk2[0]}"`);
      if (!(page.t > 1.5)) fail("friends", `the friend's title did not play (t=${page.t}, media error ${page.err}) ${page.overlay}`);
      if (!new RegExp(`/api/v1/peers/${linked.id}/playback/sessions/[0-9a-f]{32}/`).test(page.src)) fail("friends", `the video did not stream through this server: ${page.src}`);
      else console.log(`  played ${page.hash} through ${new URL(page.src).pathname.replace(/[0-9a-f]{32}/, "<session>")}`);
    }
    await s("Page.navigate", { url: `${base}/#/settings/friends?after=play` });
    await sleep(300);
  } finally {
    // Unlink from Settings > Friends; the friend's titles leave Home.
    const gone = await evaluate(`${helpers}
      window.confirm = () => true;
      let row = null;
      for (let i = 0; i < 40 && !row; i++) {
        row = document.querySelector('.peer-row[data-id="${linked.id}"]');
        if (!row) await sleep(150);
      }
      if (!row) return { why: "the linked server is not listed to unlink" };
      row.querySelector("button").click();
      for (let i = 0; i < 40 && document.querySelector('.peer-row[data-id="${linked.id}"]'); i++) await sleep(150);
      return { listed: !!document.querySelector('.peer-row[data-id="${linked.id}"]') };`);
    if (gone.why || gone.listed) fail("friends", gone.why || "Unlink left the server listed");
  }
  await s("Page.navigate", { url: `${base}/#/?friends=gone` });
  const after = await evaluate(`${helpers}
    await wait("#view .row", 40);
    await sleep(1500);
    return { row: !!document.querySelector(".friends-row .card") };`);
  if (after.row) fail("friends", "the friend's titles are still on Home after unlinking");
  const told = await fetch(`${peer}/api/v1/admin/peers`, { headers: auth }).then((r) => r.json());
  const mine = (told.linked_servers || []).find((d) => d.name === "Smoke server" && d.state === "active");
  if (mine) fail("friends", "the friend's server still lists the link as active after unlinking");
  if (getExceptions().length) fail("friends", `threw: ${getExceptions()[0]}`);
}

try {
  await main();
} catch (e) {
  failures.push(`harness: ${e.message}`);
} finally {
  proc.kill();
  await sleep(300);
  rmSync(profile, { recursive: true, force: true });
}
if (failures.length) {
  console.log(`\nweb-smoke: ${failures.length} failure(s)\n` + failures.map((f) => "  " + f).join("\n"));
  process.exit(1);
}
console.log("\nweb-smoke: every page, every playback path (HLS included), Continue Watching removal, search as you type, the shortcuts list, " +
  "the Home feature and rows, Customize Home, season tabs and the phone menu, the History section, the trailer, extras and theme music, " +
  "Skip Intro and Next Episode, the invite and pairing loop" +
  (peer ? ", and linking, badging, playing and unlinking a friend's server" : "") + " passed");

#!/usr/bin/env node
// Drives the web app in headless Chrome and fails on anything a person would
// notice: a page that throws, a page showing "null" or "undefined", a page
// missing its main content, a video that does not play, search as you type that
// lists nothing, a Continue Watching Remove control that does not remove, a
// keyboard shortcuts list that does not open, a Home feature whose Play does
// not play, season tabs that do not switch, a phone menu that does not open, or
// music that stops when the page changes.
//
//   node scripts/web-smoke.mjs [base-url]      (default http://127.0.0.1:3751)
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
    const out = { libs: libs.filter((l) => l.kind !== "music").map((l) => l.id), movie: null, show: null, direct: null, converted: null,
      transcoded: null, music: null, liveAlbum: null, alacAlbum: null, artist: null };
    for (const l of libs) {
      if (l.kind === "music") {
        out.music = l.id;
        const albums = (await j("/music/libraries/" + l.id + "/albums?limit=50")).albums;
        const live = albums.find((a) => a.live);
        out.liveAlbum = live ? live.id : null;
        out.artist = live ? live.artist_id : null;
        const alac = albums.find((a) => a.title === "Low Orbit");
        out.alacAlbum = alac ? alac.id : null;
        continue;
      }
      const items = (await j("/libraries/" + l.id + "/items?limit=50")).items;
      if (l.kind === "movie") {
        out.movie = items[0] && items[0].id;
        const by = (t) => items.find((i) => i.title === t);
        out.direct = by("Copper Valley") ? by("Copper Valley").id : null;
        out.converted = by("River Line") ? by("River Line").id : null;
        out.transcoded = by("Tin Kettle Road") ? by("Tin Kettle Road").id : null;
        out.cast = by("Harbor Lights") ? by("Harbor Lights").id : null;
      } else if (l.kind === "show") {
        out.show = items[0] && items[0].id;
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
    ["/settings", ".settings h1"],
    ["/settings/libraries", "table.list"],
    ["/settings/users", ".user-summary"],
    ["/settings/newsletter", ".newsletter-form"],
    // Music: artists, albums, live shows, an album's tracklist, an artist, and
    // music in search results.
    ...(ids.music ? [
      [`/music/${ids.music}`, ".music-grid .artist-card"],
      [`/music/${ids.music}?view=albums`, ".music-grid .music-card"],
      [`/music/${ids.music}?view=live`, ".music-grid .live-badge"],
      [`/music/album/${ids.liveAlbum}`, ".tracklist .track .track-btn"],
      [`/music/artist/${ids.artist}`, ".music-head h1"],
      ["/search?q=tide", ".music-results .tracklist .track"],
    ] : []),
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

  await music(s, evaluate, press, ids, { reset: () => { exceptions = []; }, first: () => exceptions[0] });

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
}

// Music: Play on a live album with a real mouse click; the bar keeps playing
// across a page change (it lives outside the routed view); the next track was
// loaded before the first ended, so the hand-over is near gapless; Space
// pauses and plays; the lock screen gets the track; the listen reaches
// Activity; a track Chrome cannot decode (ALAC) plays converted.
async function music(s, evaluate, press, ids, ex) {
  if (!ids.music || !ids.liveAlbum) {
    fail("music", "no music library with a live show (run against scripts/dev-server.sh)");
    return;
  }
  const wait = (cond, n = 60) => `for (let i = 0; i < ${n} && !(${cond}); i++) await new Promise((r) => setTimeout(r, 150));`;
  console.log(`music: play #/music/album/${ids.liveAlbum}`);
  ex.reset();
  await s("Page.navigate", { url: `${base}/#/music/album/${ids.liveAlbum}` });
  const btn = await evaluate(`${wait(`document.querySelector(".music-head .btn-primary")`)}
    const b = document.querySelector(".music-head .btn-primary");
    if (!b) return null;
    const r = b.getBoundingClientRect();
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    return { x, y, hit: document.elementFromPoint(x, y)?.closest("button") === b };`);
  if (!btn) {
    fail("music", "the album page has no Play button");
    return;
  }
  if (!btn.hit) fail("music", "the Play button is covered by something else");
  await s("Input.dispatchMouseEvent", { type: "mouseMoved", x: btn.x, y: btn.y });
  await s("Input.dispatchMouseEvent", { type: "mousePressed", x: btn.x, y: btn.y, button: "left", clickCount: 1 });
  await s("Input.dispatchMouseEvent", { type: "mouseReleased", x: btn.x, y: btn.y, button: "left", clickCount: 1 });
  const playing = `(() => { const a = [...document.querySelectorAll("audio.mb-audio")].find((e) => e.getAttribute("src") && !e.paused); return a ? a.currentTime : -1; })()`;
  const started = await evaluate(`${wait(`${playing} > 0.5`)}
    const bar = document.getElementById("music-bar");
    return { t: ${playing}, bar: !!bar && !bar.hidden, title: bar ? bar.querySelector(".mb-title").textContent : "" };`);
  if (!(started.t > 0.5)) fail("music", `the album did not start playing (t=${started.t})`);
  if (!started.bar) fail("music", "no player bar while music plays");

  // A page change: the router replaces #view, never the bar or the audio.
  await s("Page.navigate", { url: `${base}/#/library/${ids.libs[0]}` });
  const across = await evaluate(`${wait(`document.querySelector("#view .grid .card")`)}
    const before = ${playing};
    await new Promise((r) => setTimeout(r, 1200));
    const bar = document.getElementById("music-bar");
    return { before, after: ${playing}, bar: !!bar && !bar.hidden, hash: location.hash };`);
  if (!(across.after > across.before && across.before >= 0)) fail("music", `the music stopped on a page change (${across.before} then ${across.after})`);
  if (!across.bar) fail("music", "the player bar went away on a page change");

  // Into the second track: it was loaded while the first played.
  const handover = await evaluate(`
    const p = await import("/js/music/player.js");
    ${wait(`p.debug.transitions.length > 0 && p.debug.transitions[0].gapMs !== null`, 80)}
    const t = p.debug.transitions[0];
    const c = p.current();
    const ms = navigator.mediaSession && navigator.mediaSession.metadata;
    return { t: t || null, current: c ? c.title : "", session: ms ? ms.title : "", playing: p.isPlaying() };`);
  if (!handover.t) fail("music", "the first track never handed over to the second");
  else {
    if (!handover.t.preloaded) fail("music", "the next track was not loaded before the first ended (not gapless)");
    console.log(`  track change: next track preloaded ${handover.t.preloaded}, ${handover.t.gapMs} ms from ended to playing`);
  }
  if (handover.session !== handover.current) fail("music", `lock screen shows "${handover.session}", playing "${handover.current}"`);

  // Space pauses and plays again, from anywhere outside a text field.
  await evaluate(`document.activeElement && document.activeElement.blur && document.activeElement.blur();`);
  await press(" ", "Space", 32, " ");
  const paused = await evaluate(`${wait(`document.getElementById("music-bar").dataset.state === "paused"`, 20)}
    return document.getElementById("music-bar").dataset.state;`);
  if (paused !== "paused") fail("music", "Space did not pause the music");
  await press(" ", "Space", 32, " ");
  const resumed = await evaluate(`${wait(`document.getElementById("music-bar").dataset.state === "playing"`, 20)}
    return document.getElementById("music-bar").dataset.state;`);
  if (resumed !== "playing") fail("music", "Space did not start the music again");

  // The listens are in Activity, as tracks.
  const listens = await evaluate(`${wait(`false`, 8)}
    const h = await fetch("/api/v1/admin/activity/history?limit=50").then((r) => r.json());
    return JSON.stringify(h).includes("Glass Harbor · Low Tide");`);
  if (!listens) fail("music", "the listen is not in Activity history");

  // ALAC, which Chrome does not decode, plays as AAC made on the fly.
  if (ids.alacAlbum) {
    const conv = await evaluate(`
      const p = await import("/js/music/player.js");
      await p.playAlbum(${ids.alacAlbum});
      ${wait(`${playing} > 0.5`)}
      const c = p.current();
      return { t: ${playing}, protocol: c && c.stream ? c.stream.protocol : null };`);
    if (!(conv.t > 0.5)) fail("music", `the ALAC track did not play (t=${conv.t})`);
    if (conv.protocol !== "converted") fail("music", `ALAC came as ${conv.protocol}, expected a converted stream`);
  } else {
    fail("music", "no Low Orbit album (the ALAC sample)");
  }
  await evaluate(`const b = document.querySelector("#music-bar .mb-btn[aria-label^='Stop']"); if (b) b.click();`);
  if (ex.first()) fail("music", `threw: ${ex.first()}`);
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
  "the Home feature, season tabs and the phone menu, the History section, and music (playing across pages, gapless hand-over, Space) passed");

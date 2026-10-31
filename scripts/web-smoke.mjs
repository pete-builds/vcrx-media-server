#!/usr/bin/env node
// Drives the web app in headless Chrome and fails on anything a person would
// notice: a page that throws, a page showing "null" or "undefined", a page
// missing its main content, a video that does not play, search as you type that
// lists nothing, a Continue Watching Remove control that does not remove, or a
// keyboard shortcuts list that does not open.
//
//   node scripts/web-smoke.mjs [base-url]      (default http://127.0.0.1:3751)
//
// Run it against ./scripts/dev-server.sh, which signs the local browser in and
// has the sample clips this expects (one plays directly, one is converted).
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
    const out = { libs: libs.map((l) => l.id), movie: null, show: null, direct: null, converted: null };
    for (const l of libs) {
      const items = (await j("/libraries/" + l.id + "/items?limit=50")).items;
      if (l.kind === "movie") {
        out.movie = items[0] && items[0].id;
        const by = (t) => items.find((i) => i.title === t);
        out.direct = by("Copper Valley") ? by("Copper Valley").id : null;
        out.converted = by("River Line") ? by("River Line").id : null;
      } else {
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
    [`/item/${ids.movie}`, ".hero h1"],
    [`/item/${ids.show}`, ".hero h1"],
    ["/search?q=a", ".toolbar h1"],
    ["/activity/now", ".tiles"],
    ["/activity/history", ".tabs"],
    ["/activity/stats?days=7", ".chart-box svg"],
    ["/activity/users", "table.list"],
    ["/settings", ".settings h1"],
    ["/settings/libraries", "table.list"],
    ["/settings/users", ".add-user"],
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
    if (junk) fail(`#${path}`, `shows "${junk[0]}"`);
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

  // Playback: a file the browser plays directly, and one it needs converted.
  for (const [label, id, wantDecision] of [["direct play", ids.direct, "direct"], ["converted", ids.converted, "converted"]]) {
    if (!id) {
      fail(`play ${label}`, "sample clip not found (run against scripts/dev-server.sh)");
      continue;
    }
    console.log(`play ${label} (#/play/${id})`);
    exceptions = [];
    await s("Page.navigate", { url: `${base}/#/play/${id}?start=0` });
    const res = await evaluate(`
      for (let i = 0; i < 60 && !document.querySelector("video"); i++) await new Promise((r) => setTimeout(r, 150));
      const v = document.querySelector("video");
      if (!v) return { ok: false, why: document.getElementById("view").innerText.slice(0, 200) };
      v.muted = true;
      v.play().catch(() => {});
      for (let i = 0; i < 80 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
      return { ok: v.currentTime > 1.5, t: v.currentTime, err: v.error && v.error.code, converted: !v.controls,
        why: document.querySelector(".player .overlay:not([hidden])") ? document.querySelector(".player .overlay").innerText : "" };`);
    if (!res.ok) fail(`play ${label}`, `video did not advance (t=${res.t}, media error ${res.err}) ${res.why || ""}`);
    if (res.converted !== (wantDecision === "converted")) fail(`play ${label}`, `expected a ${wantDecision} stream`);
    if (exceptions.length) fail(`play ${label}`, `threw: ${exceptions[0]}`);
  }
  await s("Page.navigate", { url: `${base}/#/` });
  await sleep(500);
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
console.log("\nweb-smoke: every page, both playback paths, Continue Watching removal, search as you type and the shortcuts list passed");

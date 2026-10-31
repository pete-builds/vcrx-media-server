#!/usr/bin/env node
// Drives the web app in headless Chrome and fails on anything a person would
// notice: a page that throws, a page showing "null" or "undefined", a page
// missing its main content, or a video that does not play.
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
    const out = { libs: libs.map((l) => l.id), movie: null, show: null, direct: null, converted: null, transcoded: null };
    for (const l of libs) {
      const items = (await j("/libraries/" + l.id + "/items?limit=50")).items;
      if (l.kind === "movie") {
        out.movie = items[0] && items[0].id;
        const by = (t) => items.find((i) => i.title === t);
        out.direct = by("Copper Valley") ? by("Copper Valley").id : null;
        out.converted = by("River Line") ? by("River Line").id : null;
        out.transcoded = by("Tin Kettle Road") ? by("Tin Kettle Road").id : null;
      } else {
        out.show = items[0] && items[0].id;
      }
    }
    return out;`);

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
  await s("Emulation.clearDeviceMetricsOverride");

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
    await s("Page.navigate", { url: `${base}/#/play/${id}?start=0` });
    const res = await evaluate(`
      for (let i = 0; i < 60 && !document.querySelector("video"); i++) await new Promise((r) => setTimeout(r, 150));
      const v = document.querySelector("video");
      if (!v) return { ok: false, why: document.getElementById("view").innerText.slice(0, 200) };
      v.muted = true;
      v.play().catch(() => {});
      for (let i = 0; i < 80 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
      const pill = document.querySelector(".player .pill");
      return { ok: v.currentTime > 1.5, t: v.currentTime, err: v.error && v.error.code, converted: !v.controls, pill: pill ? pill.textContent : null,
        why: document.querySelector(".player .overlay:not([hidden])") ? document.querySelector(".player .overlay").innerText : "" };`);
    if (!res.ok) fail(`play ${label}`, `video did not advance (t=${res.t}, media error ${res.err}) ${res.why || ""}`);
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
  await s("Page.navigate", { url: `${base}/#/` });
  await sleep(500);
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
      await s("Page.navigate", { url: `${base}/#/play/${id}?start=0` });
      const res = await evaluate(`
        for (let i = 0; i < 60 && !document.querySelector("video"); i++) await new Promise((r) => setTimeout(r, 150));
        const v = document.querySelector("video");
        if (!v) return { ok: false, why: document.getElementById("view").innerText.slice(0, 200) };
        v.muted = true;
        v.play().catch(() => {});
        const advanced = async () => {
          for (let i = 0; i < 100 && !(v.currentTime > 1.5); i++) await new Promise((r) => setTimeout(r, 150));
          return v.currentTime > 1.5;
        };
        const ok = await advanced();
        const src = v.currentSrc;
        const pill = document.querySelector(".player .pill");
        // Seek 30 s on: a new stream, and the clock shows the new position.
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight" }));
        for (let i = 0; i < 60 && v.currentSrc === src; i++) await new Promise((r) => setTimeout(r, 100));
        const seeked = v.currentSrc !== src && await advanced();
        const clock = document.querySelector(".pc-time") ? document.querySelector(".pc-time").textContent : "";
        return { ok, seeked, t: v.currentTime, src, err: v.error && v.error.code, clock, converted: !v.controls,
          pill: pill ? pill.textContent : null, ua: navigator.userAgent.includes("Version/18.0 Safari"),
          why: document.querySelector(".player .overlay:not([hidden])") ? document.querySelector(".player .overlay").innerText : "" };`);
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
console.log("\nweb-smoke: every page and every playback path passed, HLS included");

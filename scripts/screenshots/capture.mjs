#!/usr/bin/env node
// Takes the README screenshots from a running VCRX in headless Chrome and writes
// them to docs/screenshots as WebP (no metadata, see scripts/check-private.sh).
// Point it at scripts/screenshots/demo.sh, never at a server with a real library.
//
//   node scripts/screenshots/capture.mjs [base-url]    (default http://127.0.0.1:3790)
//
// CHROME may name the browser binary. Talks to Chrome over --remote-debugging-pipe,
// like scripts/web-smoke.mjs, so it needs nothing beyond Node 20 and Chrome.

import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const base = (process.argv[2] || "http://127.0.0.1:3790").replace(/\/$/, "");
const out = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "docs", "screenshots");
const chrome = process.env.CHROME || [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser",
].find((p) => existsSync(p));
if (!chrome) {
  console.error("capture: no Chrome found; set CHROME");
  process.exit(2);
}
const [W, H] = [1440, 900];
const profile = mkdtempSync(join(tmpdir(), "vcrx-shots-"));
const proc = spawn(chrome, ["--headless=new", "--remote-debugging-pipe", `--user-data-dir=${profile}`, "--no-first-run",
  "--hide-scrollbars", `--window-size=${W},${H}`, "--mute-audio", "about:blank"], { stdio: ["ignore", "ignore", "pipe", "pipe", "pipe"] });
proc.stderr.on("data", () => {});

let nextId = 0;
let buf = "";
const pending = new Map();
proc.stdio[4].on("data", (chunk) => {
  buf += chunk.toString("utf8");
  let i;
  while ((i = buf.indexOf("\0")) >= 0) {
    const msg = JSON.parse(buf.slice(0, i));
    buf = buf.slice(i + 1);
    if (msg.id && pending.has(msg.id)) {
      pending.get(msg.id)(msg);
      pending.delete(msg.id);
    }
  }
});
const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
  const id = ++nextId;
  pending.set(id, (m) => (m.error ? reject(new Error(`${method}: ${m.error.message}`)) : resolve(m.result)));
  proc.stdio[3].write(JSON.stringify({ id, method, params, sessionId }) + "\0");
});
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => send(m, p, sessionId);
  await s("Page.enable");
  await s("Emulation.setDeviceMetricsOverride", { width: W, height: H, deviceScaleFactor: 1, mobile: false });
  const evaluate = async (expr) => {
    const r = await s("Runtime.evaluate", { expression: `(async () => { ${expr} })()`, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  };

  await s("Page.navigate", { url: `${base}/#/` });
  await sleep(3000);
  const ids = await evaluate(`
    const j = (p) => fetch("/api/v1" + p).then((r) => r.json());
    const lib = (await j("/libraries")).libraries.find((l) => l.kind === "movie");
    const items = (await j("/libraries/" + lib.id + "/items?limit=100")).items;
    const sintel = items.find((i) => i.title === "Sintel");
    return { lib: lib.id, sintel: sintel && sintel.id };`);
  if (!ids.sintel) throw new Error("no Sintel: is this the demo library from scripts/screenshots/demo.sh?");

  const shots = [["home", "#/"], ["library", `#/library/${ids.lib}`], ["details", `#/item/${ids.sintel}`], ["users", "#/settings/users"]];
  mkdirSync(out, { recursive: true });
  for (const [name, route] of shots) {
    await s("Page.navigate", { url: `${base}/${route}` });
    await sleep(3000);
    await evaluate(`await Promise.all([...document.images].map((i) => i.complete ? 0
      : new Promise((r) => { i.onload = i.onerror = r; setTimeout(r, 5000); })));`);
    await sleep(800);
    const { data } = await s("Page.captureScreenshot", { format: "webp", quality: 82 });
    writeFileSync(join(out, `${name}.webp`), Buffer.from(data, "base64"));
    console.log(`capture: ${name}.webp (${route})`);
  }
}

let code = 0;
try {
  await main();
} catch (e) {
  console.error(`capture: ${e.message}`);
  code = 1;
}
// Chrome keeps writing its profile for a moment after the signal.
await new Promise((r) => { proc.once("exit", r); proc.kill(); setTimeout(r, 3000); });
try {
  rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
} catch {
  // A leftover profile in the temp dir is harmless.
}
process.exit(code);

#!/usr/bin/env node
// Drives first-run setup in headless Chrome against a server with no account,
// as scripts/setup-smoke.sh starts one: a new browser lands on Set up, a
// mismatched password is caught before anything is sent, a good form makes the
// admin and lands on Settings > Libraries signed in, and once set up a fresh
// browser gets the ordinary sign-in form, never Set up again.
//
//   scripts/setup-smoke.sh                 (builds and starts the server too)
//   node scripts/setup-smoke.mjs <base-url>
//
// CHROME may name the browser binary; VCRX_SMOKE_SHOT=<file.png> saves the setup screen. Talks to Chrome over --remote-debugging-pipe.

import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const base = (process.argv[2] || "http://127.0.0.1:3753").replace(/\/$/, "");
const chrome = process.env.CHROME || [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser",
  "/run/current-system/sw/bin/google-chrome",
].find((p) => existsSync(p));
if (!chrome) {
  console.error("setup-smoke: no Chrome found; set CHROME");
  process.exit(2);
}

const profile = mkdtempSync(join(tmpdir(), "vcrx-setup-smoke-"));
const args = ["--headless=new", "--remote-debugging-pipe", `--user-data-dir=${profile}`, "--no-first-run", "--window-size=1280,800", "about:blank"];
if (process.env.CI) args.unshift("--no-sandbox");
const proc = spawn(chrome, args, { stdio: ["ignore", "ignore", "pipe", "pipe", "pipe"] });
proc.stderr.on("data", () => {});

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
const fail = (what) => {
  failures.push(what);
  console.log(`  FAIL ${what}`);
};

async function main() {
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => send(m, p, sessionId);
  const exceptions = [];
  listeners.push((m) => {
    if (m.sessionId === sessionId && m.method === "Runtime.exceptionThrown") {
      const d = m.params.exceptionDetails;
      exceptions.push((d.exception && d.exception.description) || d.text);
    }
  });
  await s("Runtime.enable");
  await s("Page.enable");
  await s("Network.enable");
  const evaluate = async (expr) => {
    const r = await s("Runtime.evaluate", { expression: `(async () => { ${expr} })()`, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  };
  const waitHash = async (prefix, ms = 8000) => {
    for (let t = 0; t < ms; t += 150) {
      const h = await evaluate("return location.hash;");
      if (h.startsWith(prefix)) return h;
      await sleep(150);
    }
    return evaluate("return location.hash;");
  };
  const fill = (fields) => evaluate(`
    const f = document.querySelector(".login form");
    for (const [k, v] of Object.entries(${JSON.stringify(fields)})) f.elements[k].value = v;
    f.requestSubmit();
    return true;`);

  console.log("setup: a new server opens on Set up");
  await s("Page.navigate", { url: `${base}/#/` });
  let hash = await waitHash("#/setup");
  if (!hash.startsWith("#/setup")) fail(`a new browser landed on ${hash}, not #/setup`);
  if (process.env.VCRX_SMOKE_SHOT) {
    await sleep(500);
    const { data } = await s("Page.captureScreenshot", { format: "png" });
    writeFileSync(process.env.VCRX_SMOKE_SHOT, Buffer.from(data, "base64"));
  }
  const fields = await evaluate(`return [...document.querySelectorAll(".login form input")].map((i) => i.name);`);
  if (fields.join(",") !== "display_name,username,password,confirm") fail(`setup form fields: ${fields}`);

  console.log("setup: mismatched passwords are caught in the page");
  await fill({ display_name: "Owner", username: "owner", password: "ownerpass1", confirm: "ownerpass2" });
  await sleep(300);
  const msg = await evaluate(`return document.querySelector(".login .form-error").textContent;`);
  if (!/do not match/.test(msg)) fail(`mismatch message: ${JSON.stringify(msg)}`);
  const stillOpen = await evaluate(`return (await (await fetch("/api/v1/server")).json()).setup_needed;`);
  if (stillOpen !== true) fail("a mismatched form reached the server and closed setup");

  console.log("setup: a good form makes the admin and lands on Libraries");
  await fill({ display_name: "Owner", username: "owner", password: "ownerpass1", confirm: "ownerpass1" });
  hash = await waitHash("#/settings/libraries");
  if (!hash.startsWith("#/settings/libraries")) fail(`after setup the browser is on ${hash}`);
  const me = await evaluate(`const r = await fetch("/api/v1/me"); return r.ok ? r.json() : { status: r.status };`);
  if (!me || me.username !== "owner" || !me.is_admin) fail(`after setup /me is ${JSON.stringify(me)}`);

  console.log("setup: once set up, a new browser gets sign-in, not Set up");
  await s("Network.clearBrowserCookies");
  await s("Page.navigate", { url: `${base}/#/setup` });
  hash = await waitHash("#/login");
  if (!hash.startsWith("#/login")) fail(`a signed-out browser after setup landed on ${hash}`);
  const after = await evaluate(`return (await (await fetch("/api/v1/server")).json()).setup_needed;`);
  if (after !== false) fail(`setup_needed after setup is ${after}`);

  if (exceptions.length) fail(`page exceptions: ${exceptions.join(" | ")}`);
}

try {
  await main();
} catch (err) {
  fail(String(err && err.stack || err));
} finally {
  // Chrome keeps writing its profile for a moment after the signal, so wait
  // for it to exit before removing the folder.
  const exited = new Promise((r) => proc.once("exit", r));
  await Promise.race([send("Browser.close").catch(() => {}), sleep(2000)]);
  proc.kill();
  await Promise.race([exited, sleep(5000)]);
  try {
    rmSync(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  } catch (err) {
    console.log(`setup-smoke: could not remove ${profile}: ${err.code || err}`);
  }
}
if (failures.length) {
  console.log(`setup-smoke: ${failures.length} failure(s)`);
  process.exit(1);
}
console.log("setup-smoke: PASS");

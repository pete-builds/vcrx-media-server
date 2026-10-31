// The REST client. The session lives in an HttpOnly cookie, so script never
// holds the token; every request names the client, which the server requires
// for cookie-authenticated changes.

const CLIENT_HEADER = { "X-VCRX-Client": "web" };
export const CLIENT_VERSION = "1.0.0";

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message || `Request failed (${status})`);
    this.status = status;
    this.code = code || "";
  }
}

let onSignedOut = () => {};
export function whenSignedOut(fn) { onSignedOut = fn; }

// A lent tape's browser, once the loan is over, is told "Be kind, rewind."
// (401 loan_ended) rather than signed out.
let onRewind = () => {};
export function whenRewound(fn) { onRewind = fn; }

export async function api(path, { method = "GET", body, signal, keepalive } = {}) {
  const opts = { method, headers: { ...CLIENT_HEADER }, credentials: "same-origin", signal, keepalive };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch("/api/v1" + path, opts);
  if (r.status === 204) return null;
  const isJSON = (r.headers.get("content-type") || "").includes("json");
  const data = isJSON ? await r.json().catch(() => null) : null;
  if (!r.ok) {
    if (r.status === 401 && data && data.error === "loan_ended") onRewind(data);
    else if (r.status === 401 && !path.startsWith("/auth/")) onSignedOut();
    throw new ApiError(r.status, data && data.error, data && data.message);
  }
  return data;
}

export function artUrl(itemId, kind, width) {
  return `/api/v1/items/${encodeURIComponent(itemId)}/artwork/${kind}?width=${width}`;
}

// A stable id for this browser, so Activity can tell devices apart.
export function device() {
  let id = localStorage.getItem("vcrx.device");
  if (!id) {
    id = "web-" + crypto.getRandomValues(new Uint32Array(4)).join("-");
    localStorage.setItem("vcrx.device", id);
  }
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
  return { id, name: os ? `${browser} on ${os}` : browser, model: browser, platform: "web", client: "VCRX Web", client_version: CLIENT_VERSION };
}

export async function autoSignIn() {
  return api("/auth/auto", { method: "POST", body: { device: device(), cookie: true } });
}

export async function signIn(username, password) {
  return api("/auth/login", { method: "POST", body: { username, password, device: device(), cookie: true } });
}

export async function signOut() {
  await api("/auth/logout", { method: "POST" });
}

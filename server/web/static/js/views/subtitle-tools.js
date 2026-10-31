import { api } from "../api.js";
import { h, toast } from "../dom.js";

// Player add-ons for subtitles, kept out of player.js: the timing control and
// the "Search for subtitles" panel (OpenSubtitles, through the server).

const STEP_MS = 250;

function timingLabel(ms) {
  if (!ms) return "0 ms";
  const sign = ms > 0 ? "+" : "−";
  const abs = Math.abs(ms);
  return `${sign}${abs >= 1000 ? `${(abs / 1000).toFixed(2).replace(/0$/, "")} s` : `${abs} ms`}`;
}

// subtitleTiming is the "Subtitle timing" control: − and + move subtitles
// 250 ms earlier or later, Reset puts them back. The delay is saved on the
// server for this user and file, and the server shifts the cues it serves;
// onChange runs once the server has it, so the player can fetch them again.
export function subtitleTiming({ versionId, initialMs = 0, onChange }) {
  let ms = initialMs || 0;
  let saved = ms;
  let timer = null;
  const value = h("span", { class: "sub-timing-value", "aria-live": "polite" }, timingLabel(ms));
  const save = async () => {
    const want = ms;
    try {
      const r = await api(`/versions/${versionId}/subtitle-offset`, { method: "PUT", body: { offset_ms: want } });
      saved = r.offset_ms;
      if (want === ms) onChange(saved);
    } catch (e) {
      toast(e.message || "The subtitle timing could not be saved.");
      ms = saved;
      draw();
    }
  };
  const set = (next) => {
    ms = Math.max(-600000, Math.min(600000, next));
    draw();
    clearTimeout(timer);
    // A few quick presses make one request.
    timer = setTimeout(save, 300);
  };
  const minus = h("button", { class: "pc-btn sub-timing-minus", type: "button", title: "Show subtitles earlier", "aria-label": "Subtitles earlier" }, "−");
  const plus = h("button", { class: "pc-btn sub-timing-plus", type: "button", title: "Show subtitles later", "aria-label": "Subtitles later" }, "+");
  const reset = h("button", { class: "pc-btn sub-timing-reset", type: "button", title: "Back to the file's own timing" }, "Reset");
  minus.addEventListener("click", () => set(ms - STEP_MS));
  plus.addEventListener("click", () => set(ms + STEP_MS));
  reset.addEventListener("click", () => set(0));
  function draw() {
    value.textContent = timingLabel(ms);
    reset.disabled = ms === 0;
  }
  draw();
  const el = h("div", { class: "sub-timing", role: "group", "aria-label": "Subtitle timing" },
    h("span", { class: "sub-timing-label" }, "Subtitle timing"), minus, value, plus, reset);
  return { el, get value() { return ms; } };
}

function languageName(code) {
  try {
    return new Intl.DisplayNames([navigator.language || "en"], { type: "language" }).of(code) || code;
  } catch {
    return code;
  }
}

// The browser's languages as two-letter codes, which the server accepts.
function browserLanguages() {
  const out = [];
  for (const l of navigator.languages || [navigator.language || "en"]) {
    const c = String(l).split("-")[0].toLowerCase();
    if (c && !out.includes(c)) out.push(c);
  }
  return out.slice(0, 3).join(",");
}

// subtitleSearch opens a panel over the player that searches for this file's
// subtitles and, with one click, downloads one and hands it to onPicked as a
// webvtt_subtitles entry. Users who may not download can still use a file
// already downloaded for this version.
export function subtitleSearch({ versionId, canDownload, onPicked }) {
  const results = h("div", { class: "sub-results" });
  const langs = h("input", { name: "languages", value: browserLanguages(), "aria-label": "Languages", placeholder: "en,es" });
  const hi = h("input", { type: "checkbox", name: "hearing_impaired" });
  const quota = h("p", { class: "faint sub-quota", hidden: true });
  const form = h("form", { class: "sub-form" }, langs,
    h("label", { class: "check" }, hi, "Hearing impaired first"),
    h("button", { class: "btn btn-small btn-primary", type: "submit" }, "Search"));
  const close = h("button", { class: "btn btn-small", type: "button", "aria-label": "Close" }, "Close");
  const panel = h("div", { class: "sub-search panel", role: "dialog", "aria-label": "Search for subtitles" },
    h("div", { class: "sub-head" }, h("h3", {}, "Search for subtitles"), close),
    h("p", { class: "muted" }, "From OpenSubtitles. A match for this exact file is listed first."),
    form, quota, results);

  const showQuota = (q) => {
    if (!q || !q.known) return;
    quota.textContent = `OpenSubtitles downloads left today: ${q.remaining}${q.allowed ? ` of ${q.allowed}` : ""}`;
    quota.hidden = false;
  };

  const row = (r) => {
    const badges = [h("span", { class: "pill" }, r.language ? languageName(r.language) : (r.language_code || "Unknown").toUpperCase())];
    if (r.hash_match) badges.push(h("span", { class: "pill sub-badge-match" }, "Matches this file"));
    if (r.hearing_impaired) badges.push(h("span", { class: "pill" }, "SDH"));
    if (r.trusted) badges.push(h("span", { class: "pill" }, "Trusted"));
    const have = r.stream_id !== null && r.stream_id !== undefined;
    const use = h("button", { class: "btn btn-small" + (have || canDownload ? " btn-primary" : ""), type: "button",
      disabled: !have && !canDownload, title: !have && !canDownload ? "Your account cannot download subtitles" : null }, have ? "Use" : "Download");
    use.addEventListener("click", async () => {
      use.disabled = true;
      try {
        let track;
        if (have) {
          track = { track_id: r.stream_id, url: `/api/v1/subtitles/${r.stream_id}?format=vtt`, codec: "webvtt", mime_type: "text/vtt",
            language: r.language, title: null, is_forced: false, embedded: false, downloaded: true };
        } else {
          const out = await api(`/versions/${versionId}/subtitles/download`, { method: "POST", body: { file_id: r.file_id } });
          track = out.webvtt;
          showQuota(out.quota);
        }
        hide();
        onPicked(track);
      } catch (e) {
        toast(e.message || "The subtitle could not be downloaded.");
        use.disabled = false;
      }
    });
    return h("div", { class: "sub-result" },
      h("div", { class: "sub-result-main" },
        h("div", { class: "sub-release" }, r.release || r.file_name || "Untitled"),
        h("div", { class: "sub-badges" }, badges, r.download_count ? h("span", { class: "faint" }, `${r.download_count.toLocaleString()} downloads`) : null)),
      use);
  };

  const search = async () => {
    results.replaceChildren(h("p", { class: "muted" }, "Searching"));
    try {
      const q = new URLSearchParams();
      if (langs.value.trim()) q.set("languages", langs.value.trim());
      if (hi.checked) q.set("hearing_impaired", "true");
      const data = await api(`/versions/${versionId}/subtitles/search?${q}`);
      showQuota(data.quota);
      if (!data.results.length) {
        results.replaceChildren(h("p", { class: "muted" }, "Nothing found. Try another language."));
        return;
      }
      if (!data.can_download && !canDownload) {
        results.replaceChildren(h("p", { class: "faint" }, "Your account cannot download new subtitles; ask the server's admin."));
      } else {
        results.replaceChildren();
      }
      results.append(...data.results.slice(0, 30).map(row));
    } catch (e) {
      results.replaceChildren(h("p", { class: "muted" }, e.message || "The search failed."));
    }
  };

  form.addEventListener("submit", (e) => { e.preventDefault(); search(); });
  // Keys typed in the panel are the panel's, not the player's shortcuts.
  panel.addEventListener("keydown", (e) => {
    e.stopPropagation();
    if (e.key === "Escape") hide();
  });
  close.addEventListener("click", () => hide());
  panel.hidden = true;
  function hide() { panel.hidden = true; }
  return {
    el: panel,
    open() {
      panel.hidden = false;
      if (!results.childNodes.length) search();
      langs.focus();
    },
  };
}

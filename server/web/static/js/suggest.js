// Search as you type: the top bar's search box shows up to eight titles once two
// characters are in, as an ARIA combobox. Arrow keys move through them, Enter
// opens the highlighted one (or, with none highlighted, the full results page,
// which the form's own submit handles), Escape closes the list.

import { api, artUrl } from "./api.js";
import { h, episodeCode } from "./dom.js";
import { go } from "./app.js";

const LIMIT = 8;
const DELAY_MS = 200;
const KINDS = { movie: "Movie", show: "Show", episode: "Episode" };

export function searchSuggest(form) {
  const input = form.querySelector("input");
  const list = h("ul", { class: "suggest", id: "search-suggest", role: "listbox", "aria-label": "Matching titles", hidden: true });
  form.append(list);
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-controls", list.id);
  input.setAttribute("aria-expanded", "false");

  let timer = 0;
  let ctrl = null;
  let items = [];
  let active = -1;

  const cancel = () => {
    clearTimeout(timer);
    timer = 0;
    if (ctrl) ctrl.abort();
    ctrl = null;
  };
  const close = () => {
    cancel();
    items = [];
    active = -1;
    list.hidden = true;
    list.replaceChildren();
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  };
  const highlight = (i) => {
    active = i;
    list.querySelectorAll("[role=option]").forEach((li, n) => {
      li.setAttribute("aria-selected", n === i ? "true" : "false");
      li.classList.toggle("active", n === i);
      if (n === i) li.scrollIntoView({ block: "nearest" });
    });
    if (i >= 0) input.setAttribute("aria-activedescendant", `search-suggest-${i}`);
    else input.removeAttribute("aria-activedescendant");
  };
  const pick = (item) => {
    close();
    input.blur();
    go(`/item/${item.id}`);
  };

  const show = (found) => {
    items = found.slice(0, LIMIT);
    active = -1;
    input.removeAttribute("aria-activedescendant");
    const rows = items.length
      ? items.map((item, i) => option(item, i, () => pick(item)))
      : [h("li", { class: "suggest-empty", role: "presentation" }, "No titles match")];
    list.replaceChildren(...rows.filter(Boolean));
    list.hidden = false;
    input.setAttribute("aria-expanded", "true");
  };

  const lookup = async (q) => {
    if (ctrl) ctrl.abort();
    const mine = new AbortController();
    ctrl = mine;
    try {
      const data = await api(`/search?limit=${LIMIT}&q=${encodeURIComponent(q)}`, { signal: mine.signal });
      // A late answer for text the box no longer holds is dropped.
      if (ctrl !== mine || input.value.trim() !== q || document.activeElement !== input) return;
      show((data && data.items) || []);
    } catch (e) {
      if (e.name !== "AbortError" && ctrl === mine) close();
    } finally {
      if (ctrl === mine) ctrl = null;
    }
  };
  const schedule = () => {
    cancel();
    const q = input.value.trim();
    if (q.length < 2) {
      close();
      return;
    }
    timer = setTimeout(() => { timer = 0; lookup(q); }, DELAY_MS);
  };

  input.addEventListener("input", schedule);
  input.addEventListener("focus", () => { if (list.hidden) schedule(); });
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (!list.hidden || timer || ctrl) {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
      return;
    }
    if (list.hidden || !items.length) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      highlight(active + 1 >= items.length ? 0 : active + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      highlight(active <= 0 ? items.length - 1 : active - 1);
    } else if (e.key === "Enter" && active >= 0) {
      e.preventDefault();
      pick(items[active]);
    }
  });
  // The full results page takes over: stop any lookup so it cannot reopen the list.
  form.addEventListener("submit", close);
  form.addEventListener("focusout", (e) => { if (!form.contains(e.relatedTarget)) close(); });
  window.addEventListener("hashchange", close);
}

function option(item, i, onPick) {
  const thumb = h("div", { class: "suggest-art" + (item.kind === "episode" ? " wide" : "") });
  const kind = item.kind === "episode" && item.has_thumb ? "thumb" : item.has_poster ? "poster" : null;
  if (kind) {
    const img = h("img", { src: artUrl(item.id, kind, kind === "thumb" ? 160 : 92), alt: "", loading: "lazy", decoding: "async" });
    img.addEventListener("error", () => img.remove(), { once: true });
    thumb.append(img);
  }
  const detail = item.kind === "episode"
    ? [item.show_title, episodeCode(item)]
    : [item.year != null ? String(item.year) : ""];
  const meta = [...detail, KINDS[item.kind] || ""].filter(Boolean).join(" · ");
  const li = h("li", { id: `search-suggest-${i}`, role: "option", "aria-selected": "false", class: "suggest-item" },
    thumb,
    h("div", { class: "suggest-text" }, h("div", { class: "suggest-title" }, item.title), meta ? h("div", { class: "suggest-meta" }, meta) : null));
  // mousedown would blur the input and close the list before the click lands.
  li.addEventListener("mousedown", (e) => e.preventDefault());
  li.addEventListener("click", onPick);
  return li;
}

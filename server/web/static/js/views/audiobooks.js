// Audiobook pages: a library's books with author, series and progress
// filters, and a book with its chapters. Playing a book hands it to the
// player in music/player.js, whose bar becomes a book player and keeps going
// while these pages come and go.

import { api } from "../api.js";
import { h, clock, runtime, fadeImg, toast } from "../dom.js";
import { state, go } from "../app.js";
import { micon } from "../music/icons.js";
import * as player from "../music/player.js";

const PAGE = 120;

export const bookCoverUrl = (id, w) => `/api/v1/audiobooks/books/${id}/cover?width=${w}`;

// bookArt is a cover, or the title on a dark gradient when there is none.
function bookArt(b, w) {
  const art = h("div", { class: "art music-art book-art" });
  const placeholder = () => art.prepend(h("div", { class: "placeholder" }, h("span", {}, b.title || "")));
  if (b.has_cover) art.append(fadeImg(bookCoverUrl(b.id, w), { onFail: placeholder }));
  else placeholder();
  const p = b.progress;
  if (p && !p.finished && p.position_s > 0 && b.duration_s > 0) {
    art.append(h("div", { class: "progress" }, h("i", { style: { width: `${Math.min(100, (100 * p.position_s) / b.duration_s)}%` } })));
  }
  if (p && p.finished) art.append(h("span", { class: "badge book-done", title: "Finished" }, micon("check")));
  return art;
}

// seriesLine is "Series, book 2", or just the series.
function seriesLine(b) {
  if (!b.series) return "";
  return b.series_index != null ? `${b.series}, book ${b.series_index}` : b.series;
}

export function bookCard(b) {
  return h("a", { class: "card music-card book-card", href: `#/audiobooks/book/${b.id}`, title: b.title },
    bookArt(b, 320), h("div", { class: "title" }, b.title), h("div", { class: "sub" }, b.author));
}

const playing = (fn) => async () => {
  try {
    await fn();
  } catch (e) {
    toast(e.message || "Could not start the book.");
  }
};

// audiobookLibraryView is a library's books, filtered by author, series or
// progress, in the order asked for. The choices live in the address.
export async function audiobookLibraryView({ match, params }) {
  const id = Number(match[1]);
  const lib = state.libraries.find((l) => l.id === id);
  const sort = params.get("sort") || "title";
  const author = params.get("author") || "";
  const series = params.get("series") || "";
  const st = params.get("state") || "";
  const query = (offset) => {
    const q = new URLSearchParams({ sort, limit: String(PAGE), offset: String(offset) });
    if (author) q.set("author", author);
    if (series) q.set("series", series);
    if (st) q.set("state", st);
    return `/audiobooks/libraries/${id}/books?${q}`;
  };
  const href = (change) => {
    const q = new URLSearchParams({ sort, author, series, state: st, ...change });
    for (const [k, v] of [...q]) if (!v || (k === "sort" && v === "title")) q.delete(k);
    const s = q.toString();
    return `/audiobooks/${id}${s ? "?" + s : ""}`;
  };
  const first = await api(query(0));
  const grid = h("div", { class: "grid music-grid book-grid" }, first.books.map(bookCard));
  let loaded = first.books.length;
  const moreBtn = h("button", { class: "btn", type: "button" }, "Show more");
  const more = h("div", { class: "more" }, moreBtn);
  more.hidden = loaded >= first.total;
  moreBtn.addEventListener("click", async () => {
    moreBtn.disabled = true;
    try {
      const page = await api(query(loaded));
      grid.append(...page.books.map(bookCard));
      loaded += page.books.length;
      more.hidden = loaded >= page.total || page.books.length === 0;
    } finally {
      moreBtn.disabled = false;
    }
  });
  const select = (label, value, options, key) => h("select", { "aria-label": label, onchange: (e) => go(href({ [key]: e.target.value })) },
    options.map(([v, text]) => h("option", { value: v, selected: v === value }, text)));
  const filters = h("div", { class: "filters book-filters" },
    select("Sort", sort, [["title", "Title"], ["author", "Author"], ["series", "Series"], ["added", "Recently added"], ["recent", "Recently listened"]], "sort"),
    first.authors.length > 1 || author ? select("Author", author, [["", "All authors"], ...first.authors.map((a) => [a.name, `${a.name} (${a.count})`])], "author") : null,
    first.series.length || series ? select("Series", series, [["", "All series"], ...first.series.map((s) => [s.name, `${s.name} (${s.count})`])], "series") : null,
    select("Progress", st, [["", "All books"], ["in_progress", "In progress"], ["unplayed", "Not started"], ["finished", "Finished"]], "state"));
  const filtered = author || series || st;
  const el = h("div", { class: "music-page books-page" },
    h("div", { class: "toolbar" }, h("h1", {}, lib ? lib.name : "Audiobooks"),
      h("span", { class: "count" }, `${first.total} ${first.total === 1 ? "book" : "books"}`)),
    filters,
    first.total ? grid : h("div", { class: "empty" }, filtered ? "No books match these filters." : "No books here yet. Scan the library from Settings."),
    more);
  return { el, title: lib ? lib.name : "Audiobooks" };
}

// audiobookView is a book: cover, author, narrator, series, description,
// chapters, and Resume or Play.
export async function audiobookView({ match }) {
  const id = Number(match[1]);
  let b = await api(`/audiobooks/books/${id}`);
  const libHref = (q) => `#/audiobooks/${b.library_id}?${new URLSearchParams(q)}`;

  const meta = [];
  if (b.duration_s) meta.push(b.duration_s < 60 ? `${Math.round(b.duration_s)} s` : runtime(b.duration_s * 1000));
  meta.push(`${b.chapters.length} ${b.chapters.length === 1 ? "chapter" : "chapters"}`);
  if (b.year) meta.push(String(b.year));
  if (b.genre) meta.push(b.genre);
  const facts = [];
  const fact = (k, v) => { if (v) facts.push(h("dt", {}, k), h("dd", {}, v)); };
  fact("Read by", b.narrator);
  if (b.series) facts.push(h("dt", {}, "Series"), h("dd", {}, h("a", { href: libHref({ series: b.series, sort: "series" }) }, seriesLine(b))));
  fact("Publisher", b.publisher);

  const rows = b.chapters.map((c) => h("li", { class: "track", "data-chapter": String(c.index) },
    h("button", { type: "button", class: "track-btn", onclick: playing(() => player.playBook(id, { atS: c.start_s })), "aria-label": `Play ${c.title}` },
      h("span", { class: "track-no" }, String(c.index + 1)),
      h("span", { class: "track-title" }, h("span", {}, c.title)),
      h("span", { class: "track-time" }, clock((c.end_s - c.start_s) * 1000)))));
  const list = h("ol", { class: "tracklist chapters" }, rows);

  const actions = h("div", { class: "actions" });
  const status = h("p", { class: "muted book-status" });
  const drawActions = () => {
    const p = b.progress;
    const started = p && !p.finished && p.position_s > 0;
    status.textContent = p && p.finished ? "Finished." : started ? `${clock(p.position_s * 1000)} of ${clock(b.duration_s * 1000)} listened.` : "";
    status.hidden = !status.textContent;
    const mark = async (finished) => {
      try {
        b.progress = await api(`/audiobooks/books/${id}/finished`, { method: finished ? "POST" : "DELETE" });
        drawActions();
      } catch (e) {
        toast(e.message || "Could not save that.");
      }
    };
    actions.replaceChildren(...[
      started
        ? h("button", { class: "btn btn-primary", type: "button", onclick: playing(() => player.playBook(id)) }, micon("play"), "Resume")
        : h("button", { class: "btn btn-primary", type: "button", onclick: playing(() => player.playBook(id, { fromStart: true })) }, micon("play"), "Play"),
      started ? h("button", { class: "btn", type: "button", onclick: playing(() => player.playBook(id, { fromStart: true })) }, "Play from the start") : null,
      p && p.finished
        ? h("button", { class: "btn", type: "button", onclick: () => mark(false) }, "Mark as not finished")
        : h("button", { class: "btn", type: "button", onclick: () => mark(true) }, "Mark as finished"),
    ].filter(Boolean));
  };
  drawActions();

  // The playing chapter is lit, and the progress line follows the player.
  const mark = () => {
    const playingHere = player.currentBook() && player.currentBook().id === id;
    const i = playingHere ? player.chapterAt(player.bookPos()) : -1;
    for (const li of list.querySelectorAll("li.track")) {
      const on = li.dataset.chapter === String(i);
      li.classList.toggle("is-current", on);
      li.classList.toggle("is-playing", on && player.isPlaying());
    }
  };
  mark();
  const stop = player.onChange(mark);
  // Coming back to the page after listening shows where the book is now.
  let lastSaves = player.debug.saves.length;
  const refresh = player.onChange(async () => {
    if (player.debug.saves.length === lastSaves) return;
    lastSaves = player.debug.saves.length;
    const s = player.debug.saves[lastSaves - 1];
    if (s && s.book === id) {
      b = { ...b, progress: { position_s: s.position_s, finished: s.finished, updated_at: Date.now() } };
      drawActions();
    }
  });

  const el = h("div", { class: "music-page book-page" },
    h("header", { class: "music-head" },
      bookArt(b, 640),
      h("div", { class: "music-info" },
        h("div", { class: "kicker" }, "Audiobook"),
        h("h1", {}, b.title),
        b.subtitle ? h("p", { class: "book-subtitle" }, b.subtitle) : null,
        h("p", { class: "music-by" }, h("a", { href: libHref({ author: b.author, sort: "author" }) }, b.author)),
        h("p", { class: "muted" }, meta.join(" · ")),
        facts.length ? h("dl", { class: "music-facts" }, facts) : null,
        status,
        actions)),
    b.description ? h("p", { class: "book-description" }, b.description) : null,
    h("h2", { class: "book-chapters-head" }, "Chapters"),
    list);
  return { el, title: b.title, cleanup: () => { stop(); refresh(); } };
}

// listeningRow is Home's "Continue listening": books in progress, or nothing
// when there are none or the server has no audiobooks.
export async function listeningRow() {
  let r;
  try {
    r = await api("/audiobooks/continue?limit=20");
  } catch {
    return null;
  }
  if (!r.books || !r.books.length) return null;
  return r.books;
}

// Lend a tape (milestone 10, item 1): #/lend/<item id>, for admins. The owner
// says who it is for and for how long, and prints a card: the poster on a tape
// label, the due date, and a QR code that is an invite to stream this one
// movie. The invite is shown once, like any invite, so the card is printed
// from this page or not at all. Also the "Be kind, rewind." page a borrower's
// browser lands on when the loan is over.

import { api, artUrl } from "../api.js";
import { h, toast, errorBox, fadeImg, placeholderArt } from "../dom.js";
import { state, go } from "../app.js";

const LENGTHS = [[24, "1 day"], [48, "2 days"], [72, "3 days"], [120, "5 days"], [168, "1 week"]];

// dueDate is how a due date reads on the card and the shelf.
export function dueDate(ms) {
  return new Date(ms).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

export async function lendView({ match }) {
  const id = Number(match[1]);
  if (!state.me || !state.me.is_admin) {
    return { el: errorBox("Admins only", "Only an admin, on the local network, can lend a tape."), title: "Lend a tape" };
  }
  const item = await api(`/items/${id}`);
  const root = h("div", { class: "settings lend-page" });
  const back = h("a", { class: "link", href: `#/item/${id}` }, `Back to ${item.title}`);
  if (item.kind !== "movie") {
    root.replaceChildren(h("h1", {}, "Lend a tape"), errorBox("Only movies", "A tape is one movie. Shows and episodes cannot be lent."), back);
    return { el: root, title: "Lend a tape" };
  }
  if (item.loan) {
    root.replaceChildren(h("h1", {}, "Lend a tape"),
      h("div", { class: "panel" }, h("p", {}, `${item.title} is out on loan${item.loan.borrower ? " to " + item.loan.borrower : ""}, due back ${dueDate(item.loan.due_at)}.`),
        h("div", { class: "actions" }, returnButton(item.loan, item.title, () => go(`/lend/${id}`)))),
      back);
    return { el: root, title: "Lend a tape" };
  }
  const err = h("p", { class: "form-error", role: "alert" });
  const length = h("select", { "aria-label": "How long" }, LENGTHS.map(([v, t]) => h("option", { value: String(v), selected: v === 72 }, t)));
  const allow4k = h("input", { type: "checkbox" });
  const conv = h("input", { type: "checkbox" });
  const button = h("button", { class: "btn btn-primary", type: "submit" }, "Lend it and make the card");
  const form = h("form", { class: "scope-form lend-form" },
    h("label", { class: "field" }, "Lend to", h("input", { name: "name", maxlength: "64", autocomplete: "off", required: true, placeholder: "Mark" })),
    h("div", {}, h("div", { class: "k" }, "For"), h("div", { class: "checks" }, length)),
    h("div", {}, h("div", { class: "k" }, "Limits"), h("div", { class: "checks" },
      h("label", { class: "check" }, allow4k, "Allow 4K"),
      h("label", { class: "check" }, conv, "May convert files their device cannot play"))),
    err,
    h("div", { class: "actions" }, button));
  const card = h("div", {});
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    err.textContent = "";
    button.disabled = true;
    try {
      const out = await api("/admin/loans", { method: "POST", body: { item_id: id, name: form.elements.name.value.trim(),
        hours: Number(length.value), allow_4k: allow4k.checked, allow_transcode: conv.checked } });
      if (out.streams_stopped) toast(`Stopped ${out.streams_stopped} stream${out.streams_stopped === 1 ? "" : "s"} of ${item.title}`);
      root.replaceChildren(h("h1", { class: "no-print" }, "Your tape is out"),
        h("p", { class: "muted no-print" }, "Print the card now: its code is shown once and VCRX keeps no copy. ",
          "Whoever scans it streams this movie and nothing else until it is due back. Nobody else can play your copy until then, you included."),
        tapeCard(item, out),
        h("div", { class: "actions no-print" },
          h("button", { class: "btn btn-primary", type: "button", onclick: () => window.print() }, "Print the card"),
          h("a", { class: "btn", href: `#/item/${id}` }, "Done")));
      window.scrollTo(0, 0);
    } catch (e2) {
      err.textContent = e2.message;
      button.disabled = false;
    }
  });
  root.replaceChildren(
    h("h1", {}, "Lend a tape"),
    h("p", { class: "muted" }, `Lend ${item.title} to a friend for a few days. You print a card with a QR code; `,
      "it streams this one movie on whatever scans it, never a download, and comes back on its own when it is due. ",
      "While it is out, nobody else can play your copy."),
    h("div", { class: "panel lend-item" },
      h("div", { class: "lend-poster" }, item.has_poster ? fadeImg(artUrl(item.id, "poster", 240) + "&overlays=0", { lazy: false }) : placeholderArt(item.title)),
      h("div", {}, h("strong", {}, item.title), item.year ? h("span", { class: "muted" }, ` ${item.year}`) : null)),
    h("div", { class: "panel" }, form),
    card, back);
  return { el: root, title: `Lend ${item.title}` };
}

// spaced groups an invite in fours, for typing on a TV's remote.
function spaced(secret) {
  return (secret.match(/.{1,4}/g) || []).join(" ");
}

// tapeCard is the printable card: a tape label in VCRX green and black. It
// fills 6 by 4 inches on paper and the page's width on screen; every size in
// it is relative to the card, so the two look the same.
function tapeCard(item, out) {
  const loan = out.loan;
  const poster = h("div", { class: "tape-poster" });
  if (item.has_poster) {
    poster.append(fadeImg(artUrl(item.id, "poster", 480) + "&overlays=0", { lazy: false, alt: "", onFail: () => poster.append(placeholderArt(item.title)) }));
  } else {
    poster.append(placeholderArt(item.title));
  }
  const no = String(loan.id).padStart(4, "0");
  return h("article", { class: "tape-card", "aria-label": `Loan card for ${item.title}` },
    h("div", { class: "tape-inner" },
      h("header", { class: "tape-head" },
        h("span", { class: "tape-brand" }, "VCRX"),
        h("span", { class: "tape-kind" }, "Tape on loan"),
        h("span", { class: "tape-no" }, `No. ${no}`)),
      poster,
      h("div", { class: "tape-body" },
        h("h2", { class: "tape-title" }, item.title),
        h("p", { class: "tape-meta" }, [item.year ? String(item.year) : "", `Lent to ${loan.borrower}`].filter(Boolean).join(" · ")),
        h("div", { class: "tape-due" }, h("span", {}, "Due back"), h("strong", {}, dueDate(loan.due_at))),
        h("p", { class: "tape-how" }, "Scan to watch. Streams on your phone or TV; on a TV, choose Pair with a phone and type the code.")),
      h("div", { class: "tape-qr" },
        h("img", { class: "qr", src: `data:image/png;base64,${out.qr_png}`, alt: `QR code: the invite to watch ${item.title}`, width: "220", height: "220" }),
        h("code", { class: "tape-code" }, spaced(out.invite))),
      h("footer", { class: "tape-foot" },
        h("span", { class: "tape-stripes", "aria-hidden": "true" }),
        h("span", { class: "tape-rewind" }, "Be kind, rewind."))));
}

// returnButton brings a lent tape back early.
export function returnButton(loan, title, after) {
  const b = h("button", { class: "btn btn-danger", type: "button" }, "Return early");
  b.addEventListener("click", async () => {
    if (!confirm(`Return ${title} now? Whoever has it stops watching at once.`)) return;
    b.disabled = true;
    try {
      const r = await api(`/admin/loans/${loan.id}/return`, { method: "POST" });
      toast(r.streams_stopped ? `${title} is back, and its stream stopped` : `${title} is back on the shelf`);
      after();
    } catch (e) {
      toast(e.message);
      b.disabled = false;
    }
  });
  return b;
}

// #/rewind: a borrower's browser after the loan is over. The server said so
// (loan_ended); there is nothing left to sign in to.
export async function rewindView() {
  const r = state.rewind || {};
  return {
    el: h("div", { class: "rewind-page" },
      h("div", { class: "rewind-tape", "aria-hidden": "true" }, h("span", {}), h("span", {})),
      h("h1", {}, "Be kind, rewind."),
      h("p", { class: "muted" }, r.title ? `${r.title} is back on its owner's shelf. Thanks for watching.` : "This tape is back on its owner's shelf. Thanks for watching."),
      h("p", { class: "faint" }, "Ask for another card to watch again.")),
    title: "Be kind, rewind",
  };
}

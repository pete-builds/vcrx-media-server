// The keyboard shortcuts list: ? anywhere outside a text field opens it, Escape
// (or ? again, or the Close button) shuts it. It lists only keys that exist:
// the player's come from views/player.js, the search box's from suggest.js.

import { h } from "./dom.js";

const GROUPS = [
  ["Anywhere", [
    [["?"], "Show this list"],
    [["/"], "Go to the search box"],
  ]],
  ["Search box", [
    [["↓", "↑"], "Move through matching titles"],
    [["Enter"], "Open the highlighted title, or all results"],
    [["Esc"], "Close the list of titles"],
  ]],
  ["Music", [
    [["Space"], "Play or pause the music, on any page"],
  ]],
  ["Player", [
    [["Space"], "Play or pause"],
    [["K"], "Play or pause"],
    [["←", "→"], "Back or forward 10 seconds (30 on a converted stream)"],
    [["F"], "Full screen"],
    [["M"], "Mute or unmute"],
    [["C"], "Subtitles on or off"],
    [["Esc"], "Leave the player"],
  ]],
];

function typing(el) {
  return !!el && (el.isContentEditable || el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT");
}

export function installShortcuts() {
  const closeBtn = h("button", { class: "btn btn-small", type: "button" }, "Close");
  const dialog = h("div", { class: "shortcuts", role: "dialog", "aria-modal": "true", "aria-labelledby": "shortcuts-title" },
    h("h2", { id: "shortcuts-title" }, "Keyboard shortcuts"),
    GROUPS.map(([name, keys]) => h("section", {},
      h("h3", {}, name),
      h("dl", {}, keys.flatMap(([combo, what]) => [
        h("dt", {}, combo.flatMap((k, i) => (i ? [" ", h("kbd", {}, k)] : [h("kbd", {}, k)]))),
        h("dd", {}, what),
      ])))),
    h("div", { class: "shortcuts-foot" }, closeBtn));
  const backdrop = h("div", { class: "shortcuts-backdrop", id: "shortcuts", hidden: true }, dialog);
  document.body.append(backdrop);

  let returnTo = null;
  const isOpen = () => !backdrop.hidden;
  const open = () => {
    returnTo = document.activeElement;
    backdrop.hidden = false;
    closeBtn.focus();
  };
  const close = () => {
    if (!isOpen()) return;
    backdrop.hidden = true;
    if (returnTo && returnTo.isConnected && returnTo.focus) returnTo.focus();
    returnTo = null;
  };
  closeBtn.addEventListener("click", close);
  backdrop.addEventListener("click", (e) => { if (e.target === backdrop) close(); });
  window.addEventListener("hashchange", close);

  // Capture phase, so while the list is open the player never sees the keys
  // (Escape here must close the list, not leave the player).
  window.addEventListener("keydown", (e) => {
    if (isOpen()) {
      if (e.key === "Escape" || e.key === "?") {
        e.preventDefault();
        close();
      } else if (e.key === "Tab") {
        e.preventDefault();
        closeBtn.focus();
      }
      // Enter and Space still press the Close button: stopping propagation
      // does not cancel a default action.
      e.stopPropagation();
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target)) return;
    if (e.key === "?") {
      e.preventDefault();
      e.stopPropagation();
      open();
    } else if (e.key === "/") {
      const input = document.querySelector("#search input");
      if (!input || document.getElementById("topbar").hidden) return;
      e.preventDefault();
      input.focus();
      input.select();
    }
  }, true);
}

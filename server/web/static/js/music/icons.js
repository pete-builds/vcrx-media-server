// Icons for the music player and pages, in the same 24-unit style as dom.js.

import { s } from "../dom.js";

const PATHS = {
  play: "M8 5v14l11-7z",
  pause: "M6 5h4v14H6zm8 0h4v14h-4z",
  prevtrack: "M6 6h2v12H6zm3.5 6 8.5 6V6z",
  nexttrack: "M16 6h2v12h-2zM6 18l8.5-6L6 6z",
  shuffle: "M10.6 9.2 5.4 4 4 5.4l5.2 5.2zM14.5 4l2 2L4 18.6 5.4 20 18 7.5l2 2V4zm.3 9.4-1.4 1.4 3.1 3.1-2 2.1H20v-5.5l-2 2z",
  repeat: "M7 7h10v3l4-4-4-4v3H5v6h2zm10 10H7v-3l-4 4 4 4v-3h12v-6h-2z",
  repeatone: "M7 7h10v3l4-4-4-4v3H5v6h2zm10 10H7v-3l-4 4 4 4v-3h12v-6h-2zm-4-2V9h-1l-2 1v1h1.5v4z",
  queue: "M3 6h12v2H3zm0 4h12v2H3zm0 4h8v2H3zm14-4v6.2a3 3 0 1 0 2 2.8V12h3v-2z",
  close: "M19 6.4 17.6 5 12 10.6 6.4 5 5 6.4 10.6 12 5 17.6 6.4 19 12 13.4 17.6 19 19 17.6 13.4 12z",
  // A book's skips: a circling arrow back and forward.
  back15: "M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z",
  fwd30: "M12 5V1l5 5-5 5V7a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z",
  check: "M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z",
  book: "M18 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V4a2 2 0 0 0-2-2zM6 4h5v8l-2.5-1.5L6 12z",
  disc: "M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm0 14.5a4.5 4.5 0 1 1 0-9 4.5 4.5 0 0 1 0 9zm0-5.5a1 1 0 1 0 0 2 1 1 0 0 0 0-2z",
};

export function micon(name) {
  return s("svg", { viewBox: "0 0 24 24", "aria-hidden": "true" }, s("path", { d: PATHS[name] }));
}

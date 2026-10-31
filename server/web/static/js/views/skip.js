import { h, toast } from "../dom.js";

// Skip Intro and Skip Credits for the player. The session carries the
// episode's markers and the viewer's choice (skip_markers: button, auto or
// off). While the playhead is inside a marker a button shows; pressing it
// seeks to the marker's end through the player's own seek, so converted
// streams reopen there. Credits that run to the end of the file become "Next
// Episode", which seeks to the end so the player's Up Next takes over.
// Credits followed by a scene end before it, so Skip Credits lands on the
// scene. Only intros the server marked auto_skip are skipped without a press.
//
// skipButtons({ shell, video, session, pos, seekTo, durationMs, hasNext })
//   pos() and durationMs() are file times in ms; seekTo(ms) seeks the file.
export function skipButtons({ shell, video, session, pos, seekTo, durationMs, hasNext }) {
  const markers = (session.markers || []).filter((m) => m.end_ms > m.start_ms);
  if (!markers.length || session.skip_markers === "off") return;
  const btn = h("button", { class: "btn skip-btn", type: "button", hidden: true });
  let current = null;
  const autoDone = new Set();

  // The marker the playhead is in, leaving the last half second alone so the
  // button does not flash as the marker ends by itself.
  const inside = (at) => markers.find((m) => at >= m.start_ms && at < m.end_ms - 500) || null;
  const toEnd = (m) => {
    const d = durationMs();
    return m.kind === "credits" && d && m.end_ms >= d - 2000;
  };
  const skip = (m) => {
    btn.hidden = true;
    current = null;
    // The very end: the player sees the episode finish and shows Up Next.
    seekTo(toEnd(m) ? durationMs() : m.end_ms);
  };

  const update = () => {
    const m = inside(pos());
    if (m && m.auto_skip && !autoDone.has(m.kind)) {
      autoDone.add(m.kind);
      toast(m.kind === "intro" ? "Skipped the intro" : "Skipped the credits");
      skip(m);
      return;
    }
    if (m === current) return;
    current = m;
    if (!m) {
      btn.hidden = true;
      return;
    }
    btn.textContent = m.kind === "intro" ? "Skip Intro" : toEnd(m) && hasNext ? "Next Episode" : "Skip Credits";
    btn.hidden = false;
  };
  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    if (current) skip(current);
  });
  video.addEventListener("timeupdate", update);
  shell.append(btn);
}

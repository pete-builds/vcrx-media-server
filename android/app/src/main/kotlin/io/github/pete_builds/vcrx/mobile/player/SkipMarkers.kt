package io.github.pete_builds.vcrx.mobile.player

import io.github.pete_builds.vcrx.mobile.net.Marker
import io.github.pete_builds.vcrx.mobile.net.PlaybackSettings

/**
 * What the Skip button would do now: its label, where it seeks, whether it goes
 * straight to Up Next instead, and whether to do it without a press.
 */
data class SkipAction(
    val kind: String,
    val label: String,
    val targetMs: Long,
    val playsNext: Boolean,
    val auto: Boolean,
)

/**
 * Skip Intro and Skip Credits. Pure, so the rules are unit tested:
 * - The button shows while the playhead is inside a marker, except its last half second.
 * - Intro: seek to its end.
 * - Credits that run to the end of the file, with an episode after this one: Next
 *   Episode, straight into Up Next. Credits followed by something (a post-credits
 *   scene) seek to their end, which the server puts before the scene.
 * - skip_markers off: nothing. Only markers the server flagged auto_skip (confident
 *   intros, with "skip automatically" chosen) are skipped without a press.
 */
object SkipMarkers {
    const val INTRO = "intro"
    const val CREDITS = "credits"
    private const val TAIL_MS = 500L
    private const val TO_END_MS = 2_000L

    fun actionAt(markers: List<Marker>, positionMs: Long, durationMs: Long, hasNext: Boolean, mode: String): SkipAction? {
        if (mode == PlaybackSettings.OFF) return null
        val m = markers.firstOrNull { it.endMs > it.startMs && positionMs >= it.startMs && positionMs < it.endMs - TAIL_MS } ?: return null
        val toEnd = m.kind == CREDITS && durationMs > 0 && m.endMs >= durationMs - TO_END_MS
        return when {
            m.kind == INTRO -> SkipAction(INTRO, "Skip Intro", m.endMs, playsNext = false, auto = m.autoSkip && mode == PlaybackSettings.AUTO)
            toEnd && hasNext -> SkipAction(CREDITS, "Next Episode", durationMs, playsNext = true, auto = false)
            else -> SkipAction(CREDITS, "Skip Credits", m.endMs, playsNext = false, auto = false)
        }
    }
}

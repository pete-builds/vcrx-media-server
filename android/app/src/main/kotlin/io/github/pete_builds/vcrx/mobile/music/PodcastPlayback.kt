package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode

/**
 * The rules of playing a podcast episode, kept apart from Media3 so they are
 * plain Kotlin with unit tests: where it starts, how far the skip buttons
 * go, which speeds there are, and when the place is saved on the server.
 */
object PodcastPlayback {
    const val SKIP_BACK_MS = 15_000L
    const val SKIP_FORWARD_MS = 30_000L

    /** How often the place is saved while it plays, as the web player does. */
    const val SAVE_EVERY_MS = 15_000L

    /** The speeds offered, slowest first. */
    val SPEEDS: List<Float> = listOf(0.8f, 1.0f, 1.2f, 1.5f, 1.75f, 2.0f, 2.5f)
    const val MIN_SPEED = 0.8f
    const val MAX_SPEED = 2.5f

    /** Any speed within the offered range. */
    fun clampSpeed(speed: Float): Float = if (speed.isNaN()) 1.0f else speed.coerceIn(MIN_SPEED, MAX_SPEED)

    /** The speed after [current] in [SPEEDS], back to the slowest after the fastest. */
    fun nextSpeed(current: Float): Float = SPEEDS.firstOrNull { it > current + 0.001f } ?: SPEEDS.first()

    /** "1.5×", "1×". */
    fun speedLabel(speed: Float): String {
        val s = if (speed % 1f == 0f) speed.toInt().toString() else speed.toString().trimEnd('0')
        return "$s×"
    }

    /**
     * Where a skip of [deltaMs] lands: never before the start, and never past
     * half a second before the end (when the length is known), so a skip
     * forward near the end does not finish the episode by accident.
     */
    fun skipTarget(positionMs: Long, deltaMs: Long, durationMs: Long): Long {
        val end = if (durationMs > 0) (durationMs - 500).coerceAtLeast(0L) else Long.MAX_VALUE
        return (positionMs + deltaMs).coerceIn(0L, end)
    }

    /** Where an episode starts: its saved place, or the top once it was played. */
    fun startPositionMs(episode: PodcastEpisode): Long =
        if (episode.played || episode.positionSeconds <= 0.0) 0L else (episode.positionSeconds * 1000).toLong()

    /** "3:41 left", or the length when it has not been started. */
    fun remaining(episode: PodcastEpisode): String? {
        val total = episode.durationSeconds ?: return null
        if (episode.played) return "Played"
        val left = (total - episode.positionSeconds.toLong()).coerceAtLeast(0)
        val minutes = (left + 59) / 60
        val text = if (minutes >= 60) "${minutes / 60} h ${minutes % 60} min" else "$minutes min"
        return if (episode.positionSeconds > 0) "$text left" else text
    }
}

/**
 * When to save the place: every [intervalMs] while playing, and at once on a
 * pause, a stop or the end (the caller asks with [force]). It never sends the
 * same place twice in a row, so a paused episode does not keep saving.
 */
class ProgressClock(private val intervalMs: Long = PodcastPlayback.SAVE_EVERY_MS) {
    private var lastSentAt: Long? = null
    private var lastPositionMs: Long? = null

    fun shouldSend(nowMs: Long, positionMs: Long, playing: Boolean, force: Boolean = false): Boolean {
        if (positionMs == lastPositionMs) return false
        val due = force || (playing && (lastSentAt?.let { nowMs - it >= intervalMs } ?: true))
        if (due) {
            lastSentAt = nowMs
            lastPositionMs = positionMs
        }
        return due
    }

    fun reset() {
        lastSentAt = null
        lastPositionMs = null
    }
}

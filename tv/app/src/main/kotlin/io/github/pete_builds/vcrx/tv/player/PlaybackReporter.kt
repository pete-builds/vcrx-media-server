package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.PlaybackEvent

/**
 * Turns player observations into the contract's playback events, each sent once
 * and in a sensible order: start once playing, then pause and resume pairs,
 * buffering once per stall, seeks with their origin, progress only while playing,
 * and exactly one stop, after which nothing else is sent. Pure, so it is unit tested.
 */
class PlaybackReporter(private val emit: (PlaybackEvent) -> Unit) {
    private var started = false
    private var paused = false
    private var buffering = false

    var stopped = false
        private set

    /** The player started or restarted rendering. */
    fun onPlaying(positionMs: Long, durationMs: Long?) {
        if (stopped) return
        buffering = false
        when {
            !started -> {
                started = true
                paused = false
                send("start", positionMs, durationMs)
            }
            paused -> {
                paused = false
                send("resume", positionMs, durationMs)
            }
        }
    }

    /** The user paused (play-when-ready went false). */
    fun onPaused(positionMs: Long, durationMs: Long?) {
        if (stopped || !started || paused) return
        paused = true
        send("pause", positionMs, durationMs)
    }

    /** Playback stalled waiting for data while it should be playing. */
    fun onBuffering(positionMs: Long, durationMs: Long?) {
        if (stopped || !started || buffering || paused) return
        buffering = true
        send("buffering", positionMs, durationMs)
    }

    fun onSeek(fromMs: Long, toMs: Long, durationMs: Long?) {
        if (stopped) return
        emit(PlaybackEvent(type = "seek", positionMs = toMs, fromPositionMs = fromMs, durationMs = durationMs))
    }

    /** Called every heartbeat interval; reports only while actually playing. */
    fun onHeartbeat(positionMs: Long, durationMs: Long?, isPlaying: Boolean) {
        if (stopped || !started || !isPlaying) return
        send("progress", positionMs, durationMs)
    }

    fun onError(message: String, positionMs: Long, durationMs: Long?) {
        if (stopped) return
        emit(PlaybackEvent(type = "error", positionMs = positionMs, durationMs = durationMs, message = message))
    }

    /** The final position. Safe to call from every exit path; only the first call counts. */
    fun stop(positionMs: Long, durationMs: Long?) {
        if (stopped) return
        stopped = true
        send("stop", positionMs, durationMs)
    }

    private fun send(type: String, positionMs: Long, durationMs: Long?) {
        emit(PlaybackEvent(type = type, positionMs = positionMs.coerceAtLeast(0), durationMs = durationMs))
    }
}

/** How far one D-pad press moves. Holding the key accelerates through longer steps. */
object SeekSteps {
    fun stepMs(repeatCount: Int): Long = when {
        repeatCount < 20 -> 10_000
        repeatCount < 60 -> 30_000
        else -> 60_000
    }

    /** Clamps a seek target inside the media, keeping clear of the very end. */
    fun clamp(targetMs: Long, durationMs: Long?): Long {
        val max = durationMs?.takeIf { it > 0 }?.let { (it - 1_000).coerceAtLeast(0) } ?: Long.MAX_VALUE
        return targetMs.coerceIn(0, max)
    }
}

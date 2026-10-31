package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.BUSY_MESSAGE
import io.github.pete_builds.vcrx.tv.net.PlaybackSession
import io.github.pete_builds.vcrx.tv.net.httpError
import io.github.pete_builds.vcrx.tv.net.isBusy
import io.github.pete_builds.vcrx.tv.net.streamRefusal
import kotlinx.serialization.json.Json

/**
 * The two ways a session's stream reaches the player. Pure, so it is unit tested.
 *
 * Direct: the file itself (or HLS), which Media3 seeks by byte range.
 * Converted (protocol fmp4): a fragmented MP4 piped from ffmpeg on the server,
 * with the video copied or re-encoded and the audio copied or converted. It cannot
 * be seeked by range: the seek endpoint says where a stream from a position starts
 * (the keyframe at or before it) and the player opens a new stream there.
 */
object StreamKinds {
    const val FMP4 = "fmp4"

    fun isConverted(session: PlaybackSession): Boolean = session.stream.protocol == FMP4

    /** What the viewer is told about a converted stream, as the web player labels it; null for direct play. */
    fun label(session: PlaybackSession): String? = if (!isConverted(session)) {
        null
    } else {
        when (session.decision) {
            "transcode" -> "Video converted"
            "audio_transcode" -> "Audio converted"
            else -> "Repackaged"
        }
    }

    /** The seek endpoint: the session's seek_url, or the documented route for servers that omit it. */
    fun seekPath(session: PlaybackSession): String =
        session.stream.seekUrl?.takeIf { it.isNotBlank() } ?: "/api/v1/playback/sessions/${session.sessionId}/seek"
}

/**
 * Where the player is in the file. Direct play: the player's own position and
 * duration, untouched. Converted: each stream starts at the start_ms the seek
 * endpoint answered, so the file position is that start plus the player's own
 * time, and the duration is the version's, since a stream piped from ffmpeg
 * carries none. Every position the app shows or reports comes from here.
 */
class StreamTimeline(
    val converted: Boolean,
    private val fileDurationMs: Long?,
    private val playerPositionMs: () -> Long,
    private val playerDurationMs: () -> Long?,
) {
    /** Where the current converted stream starts in the file; always 0 for direct play. */
    var startMs: Long = 0
        private set

    private var ticket = 0

    fun positionMs(): Long {
        val player = playerPositionMs()
        return if (converted) startMs + player.coerceAtLeast(0) else player
    }

    fun durationMs(): Long? {
        if (!converted) return playerDurationMs()
        return fileDurationMs?.takeIf { it > 0 } ?: playerDurationMs()?.let { startMs + it }
    }

    /** The position to ask the seek endpoint for: the target, kept inside the file. */
    fun seekTarget(targetMs: Long): Long = SeekSteps.clamp(targetMs, durationMs())

    /** Starts a seek; only the newest one may land, so answers that arrive late are dropped. */
    fun beginSeek(): Int = ++ticket

    fun isCurrent(seek: Int): Boolean = seek == ticket

    /** Drops any seek in flight, for when playback ends. */
    fun cancelSeeks() {
        ticket++
    }

    /**
     * The seek endpoint answered [answerStartMs] for [seek]. Returns false, changing
     * nothing, when a newer seek has started since. Call it just before the new stream
     * is set on the player, whose own time then restarts at zero.
     */
    fun land(seek: Int, answerStartMs: Long): Boolean {
        if (!converted || !isCurrent(seek)) return false
        startMs = answerStartMs.coerceAtLeast(0)
        return true
    }

    /**
     * A converted stream that ends well before the file does was cut off (the server
     * stopped or the connection closed cleanly), not watched to the end. Reporting a
     * stop at the full duration would mark it played and lose the resume point.
     */
    fun endedEarly(): Boolean {
        if (!converted) return false
        val duration = fileDurationMs?.takeIf { it > 0 } ?: return false
        return positionMs() < duration - END_TOLERANCE_MS
    }

    private companion object {
        /** ffmpeg's streams can end a few seconds short of the container's duration. */
        const val END_TOLERANCE_MS = 15_000L
    }
}

/** The screen for a failure to start playback, or to find where a converted stream starts. */
fun phaseFor(error: ApiException): Phase = when {
    error is ApiException.Http && (error.status == 403 || error.status == 422) -> Phase.Refused(error.message.orEmpty())
    isBusy(error) -> Phase.Failed(BUSY_MESSAGE, unreachable = false, busy = true)
    error is ApiException.Http && error.code == "session_terminated" ->
        Phase.Failed("Playback was stopped on the server: ${error.message.orEmpty()}", unreachable = false)
    error is ApiException.Unreachable -> Phase.Failed(error.message.orEmpty(), unreachable = true)
    else -> Phase.Failed(error.message.orEmpty(), unreachable = false)
}

/** The screen for an HTTP error on the stream itself, mid-play. The message is [streamRefusal]'s. */
fun streamFailure(status: Int, body: ByteArray?, json: Json): Phase.Failed = Phase.Failed(
    streamRefusal(status, body, json),
    unreachable = false,
    busy = isBusy(httpError(status, body?.toString(Charsets.UTF_8), json)),
)

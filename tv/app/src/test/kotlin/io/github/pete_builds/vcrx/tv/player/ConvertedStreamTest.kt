package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.PlaybackEvent
import io.github.pete_builds.vcrx.tv.net.PlaybackSession
import io.github.pete_builds.vcrx.tv.net.StreamInfo
import io.github.pete_builds.vcrx.tv.net.Version
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Direct versus converted playback, and the position math a converted stream needs. */
class ConvertedStreamTest {
    private fun session(decision: String, protocol: String, seekUrl: String? = null, durationMs: Long? = 7_200_000) = PlaybackSession(
        sessionId = "s1",
        decision = decision,
        itemId = 5,
        version = Version(id = 9, durationMs = durationMs),
        stream = StreamInfo(url = "/stream", protocol = protocol, seekUrl = seekUrl),
    )

    /** A stand-in for the player: its own time restarts at zero with every stream. */
    private var playerMs = 0L
    private var playerDurationMs: Long? = null

    private fun converted(fileDurationMs: Long? = 7_200_000) =
        StreamTimeline(converted = true, fileDurationMs = fileDurationMs, playerPositionMs = { playerMs }, playerDurationMs = { playerDurationMs })

    private fun direct() =
        StreamTimeline(converted = false, fileDurationMs = 7_200_000, playerPositionMs = { playerMs }, playerDurationMs = { playerDurationMs })

    @Test
    fun onlyAnFmp4StreamIsConverted() {
        assertFalse(StreamKinds.isConverted(session("direct_play", "progressive")))
        assertFalse(StreamKinds.isConverted(session("direct_play", "hls")))
        assertTrue(StreamKinds.isConverted(session("audio_transcode", "fmp4")))
        assertTrue(StreamKinds.isConverted(session("remux", "fmp4")))
    }

    @Test
    fun onlyTheFileOrAnFmp4StreamIsPlayable() {
        assertTrue(StreamKinds.isPlayable(session("direct_play", "progressive")))
        assertTrue(StreamKinds.isPlayable(session("remux", "fmp4")))
        // HLS, or a protocol from a newer server, is never opened as if it were the file.
        assertFalse(StreamKinds.isPlayable(session("remux", "hls")))
        assertFalse(StreamKinds.isPlayable(session("remux", "dash")))
    }

    @Test
    fun labelsMatchTheWebPlayer() {
        assertNull(StreamKinds.label(session("direct_play", "progressive")))
        assertEquals("Audio converted", StreamKinds.label(session("audio_transcode", "fmp4")))
        assertEquals("Repackaged", StreamKinds.label(session("remux", "fmp4")))
        assertEquals("Video converted", StreamKinds.label(session("transcode", "fmp4")))
    }

    @Test
    fun seekPathPrefersTheSessionsSeekUrl() {
        assertEquals("/api/v1/playback/sessions/s1/seek", StreamKinds.seekPath(session("remux", "fmp4", seekUrl = "/api/v1/playback/sessions/s1/seek")))
        assertEquals("/custom/seek", StreamKinds.seekPath(session("remux", "fmp4", seekUrl = "/custom/seek")))
        // A server that omits seek_url still has the documented route.
        assertEquals("/api/v1/playback/sessions/s1/seek", StreamKinds.seekPath(session("remux", "fmp4")))
    }

    @Test
    fun directPlayUsesThePlayersOwnPositionAndDuration() {
        val timeline = direct()
        playerMs = 61_000
        playerDurationMs = 100_000
        assertEquals(61_000L, timeline.positionMs())
        assertEquals(100_000L, timeline.durationMs())
        // A converted answer never moves a direct stream.
        assertFalse(timeline.land(timeline.beginSeek(), 600_000))
        assertEquals(61_000L, timeline.positionMs())
        assertFalse(timeline.endedEarly())
    }

    @Test
    fun convertedPositionIsStreamStartPlusPlayerTime() {
        val timeline = converted()
        playerDurationMs = null // ffmpeg's piped MP4 carries no duration
        assertTrue(timeline.land(timeline.beginSeek(), 1_195_000))
        playerMs = 0
        assertEquals(1_195_000L, timeline.positionMs())
        playerMs = 42_500
        assertEquals(1_237_500L, timeline.positionMs())
        assertEquals(7_200_000L, timeline.durationMs())
        // Without the version's duration, the stream's own runs from its start.
        val unknown = converted(fileDurationMs = null)
        unknown.land(unknown.beginSeek(), 300_000)
        playerDurationMs = 900_000
        assertEquals(1_200_000L, unknown.durationMs())
    }

    @Test
    fun seekTargetsStayInsideTheFile() {
        val timeline = converted()
        assertEquals(0L, timeline.seekTarget(-10_000))
        assertEquals(1_250_000L, timeline.seekTarget(1_250_000))
        assertEquals(7_199_000L, timeline.seekTarget(9_000_000))
    }

    @Test
    fun aLateSeekAnswerIsDropped() {
        val timeline = converted()
        val first = timeline.beginSeek()
        val second = timeline.beginSeek()
        assertFalse(timeline.land(first, 100_000))
        assertEquals(0L, timeline.startMs)
        assertTrue(timeline.land(second, 200_000))
        assertEquals(200_000L, timeline.startMs)
        val pending = timeline.beginSeek()
        timeline.cancelSeeks()
        assertFalse(timeline.isCurrent(pending))
        assertFalse(timeline.land(pending, 300_000))
        assertEquals(200_000L, timeline.startMs)
    }

    @Test
    fun aStreamCutOffBeforeTheEndIsNotAFinish() {
        val timeline = converted(fileDurationMs = 3_600_000)
        timeline.land(timeline.beginSeek(), 1_800_000)
        playerMs = 600_000
        assertTrue(timeline.endedEarly())
        playerMs = 1_795_000 // within ffmpeg's few seconds of the container's end
        assertFalse(timeline.endedEarly())
        // Unknown duration: nothing to compare with, so the end is trusted.
        assertFalse(converted(fileDurationMs = null).endedEarly())
    }

    @Test
    fun resumeReportingUsesFileTime() {
        val events = mutableListOf<PlaybackEvent>()
        val reporter = PlaybackReporter { events += it }
        val timeline = converted()

        // Resume at 20:00; the server's keyframe is at 19:58.
        timeline.land(timeline.beginSeek(), 1_198_000)
        playerMs = 0
        reporter.onPlaying(timeline.positionMs(), timeline.durationMs())
        playerMs = 10_000
        reporter.onHeartbeat(timeline.positionMs(), timeline.durationMs(), isPlaying = true)

        // Skip forward: the seek event goes from where the viewer was to the new stream's start.
        val from = timeline.positionMs()
        val seek = timeline.beginSeek()
        timeline.land(seek, 1_236_000)
        reporter.onSeek(from, timeline.startMs, timeline.durationMs())
        playerMs = 4_000
        reporter.stop(timeline.positionMs(), timeline.durationMs())

        assertEquals(listOf("start", "progress", "seek", "stop"), events.map { it.type })
        assertEquals(1_198_000L, events[0].positionMs)
        assertEquals(1_208_000L, events[1].positionMs)
        assertEquals(1_208_000L, events[2].fromPositionMs)
        assertEquals(1_236_000L, events[2].positionMs)
        assertEquals(1_240_000L, events[3].positionMs)
        assertEquals(7_200_000L, events[3].durationMs)
    }
}

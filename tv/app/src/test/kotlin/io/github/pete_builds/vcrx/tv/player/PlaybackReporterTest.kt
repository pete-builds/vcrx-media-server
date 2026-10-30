package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.PlaybackEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PlaybackReporterTest {
    private val events = mutableListOf<PlaybackEvent>()
    private val reporter = PlaybackReporter { events += it }
    private fun types() = events.map { it.type }

    @Test
    fun fullSessionInOrder() {
        reporter.onHeartbeat(0, 100_000, isPlaying = false)
        reporter.onPlaying(61_000, 100_000)
        reporter.onHeartbeat(71_000, 100_000, isPlaying = true)
        reporter.onPaused(72_000, 100_000)
        reporter.onPaused(72_000, 100_000)
        reporter.onHeartbeat(72_000, 100_000, isPlaying = false)
        reporter.onPlaying(72_000, 100_000)
        reporter.onSeek(72_000, 82_000, 100_000)
        reporter.onBuffering(82_000, 100_000)
        reporter.onBuffering(82_000, 100_000)
        reporter.onPlaying(82_000, 100_000)
        reporter.stop(90_000, 100_000)
        assertEquals(listOf("start", "progress", "pause", "resume", "seek", "buffering", "stop"), types())
        assertEquals(61_000L, events.first().positionMs)
        val seek = events.single { it.type == "seek" }
        assertEquals(72_000L, seek.fromPositionMs)
        assertEquals(82_000L, seek.positionMs)
        assertEquals(90_000L, events.last().positionMs)
        assertEquals(100_000L, events.last().durationMs)
    }

    @Test
    fun stopIsSentOnceAndSilencesEverythingAfter() {
        reporter.onPlaying(0, null)
        reporter.stop(5_000, null)
        reporter.stop(6_000, null)
        reporter.onPlaying(6_000, null)
        reporter.onError("late", 6_000, null)
        assertEquals(listOf("start", "stop"), types())
        assertTrue(reporter.stopped)
    }

    @Test
    fun nothingButStopAndErrorsBeforeStart() {
        reporter.onPaused(0, null)
        reporter.onBuffering(0, null)
        reporter.onError("decoder failed", 0, null)
        reporter.stop(0, null)
        assertEquals(listOf("error", "stop"), types())
        assertEquals("decoder failed", events.first().message)
    }

    @Test
    fun seekStepsAccelerateAndClamp() {
        assertEquals(10_000L, SeekSteps.stepMs(0))
        assertEquals(10_000L, SeekSteps.stepMs(19))
        assertEquals(30_000L, SeekSteps.stepMs(20))
        assertEquals(60_000L, SeekSteps.stepMs(60))
        assertEquals(0L, SeekSteps.clamp(-5_000, 100_000))
        assertEquals(99_000L, SeekSteps.clamp(500_000, 100_000))
        assertEquals(500_000L, SeekSteps.clamp(500_000, null))
    }
}

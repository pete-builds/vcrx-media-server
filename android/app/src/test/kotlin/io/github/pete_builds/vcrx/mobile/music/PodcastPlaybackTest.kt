package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PodcastPlaybackTest {
    @Test
    fun skipsStayInsideTheEpisode() {
        assertEquals(0L, PodcastPlayback.skipTarget(10_000, -PodcastPlayback.SKIP_BACK_MS, 600_000))
        assertEquals(40_000L, PodcastPlayback.skipTarget(10_000, PodcastPlayback.SKIP_FORWARD_MS, 600_000))
        // Near the end, forward stops short of finishing it.
        assertEquals(599_500L, PodcastPlayback.skipTarget(590_000, PodcastPlayback.SKIP_FORWARD_MS, 600_000))
        // An unknown length does not cap a skip forward.
        assertEquals(40_000L, PodcastPlayback.skipTarget(10_000, 30_000, 0))
    }

    @Test
    fun speedsStayBetweenPointEightAndTwoAndAHalf() {
        assertEquals(0.8f, PodcastPlayback.clampSpeed(0.1f), 0f)
        assertEquals(2.5f, PodcastPlayback.clampSpeed(9f), 0f)
        assertEquals(1.0f, PodcastPlayback.clampSpeed(Float.NaN), 0f)
        assertEquals(1.2f, PodcastPlayback.nextSpeed(1.0f), 0f)
        assertEquals(0.8f, PodcastPlayback.nextSpeed(2.5f), 0f)
        assertTrue(PodcastPlayback.SPEEDS.all { it in PodcastPlayback.MIN_SPEED..PodcastPlayback.MAX_SPEED })
        assertEquals("1.5×", PodcastPlayback.speedLabel(1.5f))
        assertEquals("1×", PodcastPlayback.speedLabel(1.0f))
    }

    @Test
    fun startsFromTheSavedPlaceUnlessPlayed() {
        val e = PodcastEpisode(id = 1, positionSeconds = 61.5)
        assertEquals(61_500L, PodcastPlayback.startPositionMs(e))
        assertEquals(0L, PodcastPlayback.startPositionMs(e.copy(played = true)))
        assertEquals(0L, PodcastPlayback.startPositionMs(e.copy(positionSeconds = 0.0)))
    }

    @Test
    fun remainingTime() {
        assertEquals("10 min", PodcastPlayback.remaining(PodcastEpisode(id = 1, durationSeconds = 600)))
        assertEquals("9 min left", PodcastPlayback.remaining(PodcastEpisode(id = 1, durationSeconds = 600, positionSeconds = 60.0)))
        assertEquals("1 h 30 min", PodcastPlayback.remaining(PodcastEpisode(id = 1, durationSeconds = 5400)))
        assertEquals("Played", PodcastPlayback.remaining(PodcastEpisode(id = 1, durationSeconds = 600, played = true)))
        assertEquals(null, PodcastPlayback.remaining(PodcastEpisode(id = 1)))
    }

    @Test
    fun savesEveryFifteenSecondsWhilePlayingAndAtOnceOnPause() {
        val clock = ProgressClock(intervalMs = 15_000)
        assertTrue("the first tick while playing saves", clock.shouldSend(0, 1_000, playing = true))
        assertFalse(clock.shouldSend(5_000, 6_000, playing = true))
        assertFalse(clock.shouldSend(14_999, 15_999, playing = true))
        assertTrue(clock.shouldSend(15_000, 16_000, playing = true))
        // Paused: no ticking saves, but the pause itself does.
        assertFalse(clock.shouldSend(40_000, 20_000, playing = false))
        assertTrue(clock.shouldSend(40_000, 20_000, playing = false, force = true))
        // The same place is never sent twice.
        assertFalse(clock.shouldSend(41_000, 20_000, playing = false, force = true))
        clock.reset()
        assertTrue(clock.shouldSend(42_000, 20_000, playing = false, force = true))
    }
}

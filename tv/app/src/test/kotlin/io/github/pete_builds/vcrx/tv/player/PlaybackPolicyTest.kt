package io.github.pete_builds.vcrx.tv.player

import androidx.media3.common.Player
import io.github.pete_builds.vcrx.tv.net.Track
import io.github.pete_builds.vcrx.tv.settings.AudioPrefs
import io.github.pete_builds.vcrx.tv.settings.PlaybackPrefs
import io.github.pete_builds.vcrx.tv.settings.ResumeMode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The Playback and Audio settings as the player applies them. */
class PlaybackPolicyTest {
    private val start = PlaybackPolicy.Start.Beginning
    private val resume = PlaybackPolicy.Start.Resume
    private val ask = PlaybackPolicy.Start.Ask

    @Test
    fun resumeFollowsTheSettingForAPlainPlay() {
        assertEquals(resume, PlaybackPolicy.start(StartRequest.Default, ResumeMode.Resume, 600_000))
        assertEquals(start, PlaybackPolicy.start(StartRequest.Default, ResumeMode.StartOver, 600_000))
        assertEquals(ask, PlaybackPolicy.start(StartRequest.Default, ResumeMode.Ask, 600_000))
    }

    @Test
    fun theExplicitButtonsWinOverTheSetting() {
        for (mode in ResumeMode.entries) {
            assertEquals(mode.name, resume, PlaybackPolicy.start(StartRequest.Resume, mode, 600_000))
            assertEquals(mode.name, start, PlaybackPolicy.start(StartRequest.Beginning, mode, 600_000))
        }
    }

    @Test
    fun nothingIsAskedWithoutASavedPosition() {
        for (mode in ResumeMode.entries) {
            assertEquals(mode.name, resume, PlaybackPolicy.start(StartRequest.Default, mode, null))
            assertEquals(mode.name, resume, PlaybackPolicy.start(StartRequest.Default, mode, 0))
        }
    }

    @Test
    fun theCountdownIsTheSettingOrNoneWhenAutoPlayIsOff() {
        assertEquals(10, PlaybackPolicy.countdownSeconds(PlaybackPrefs()))
        assertEquals(30, PlaybackPolicy.countdownSeconds(PlaybackPrefs(countdownSeconds = 30)))
        assertNull(PlaybackPolicy.countdownSeconds(PlaybackPrefs(autoPlayNext = false, countdownSeconds = 30)))
        // A stored value from outside the offered range is kept sensible.
        assertEquals(3, PlaybackPolicy.countdownSeconds(PlaybackPrefs(countdownSeconds = 0)))
    }

    @Test
    fun skipSizesComeFromSettings() {
        val prefs = PlaybackPrefs(skipBackSeconds = 15, skipForwardSeconds = 30)
        assertEquals(15_000L, PlaybackPolicy.skipBackMs(prefs))
        assertEquals(30_000L, PlaybackPolicy.skipForwardMs(prefs))
        assertEquals(10_000L, PlaybackPolicy.skipBackMs(PlaybackPrefs()))
        assertEquals(5_000L, PlaybackPolicy.skipBackMs(PlaybackPrefs(skipBackSeconds = -4)))
    }

    @Test
    fun aDpadTapMovesBySkipSizeAndHoldingStillAccelerates() {
        assertEquals(30_000L, SeekSteps.stepMs(0, 30_000))
        assertEquals(5_000L, SeekSteps.stepMs(19, 5_000))
        assertEquals(30_000L, SeekSteps.stepMs(20, 5_000))
        assertEquals(60_000L, SeekSteps.stepMs(60, 5_000))
        // Holding never moves by less than one tap.
        assertEquals(60_000L, SeekSteps.stepMs(20, 60_000))
    }

    private val tracks = listOf(
        Track(id = 1, kind = "audio", index = 1, language = "jpn", isDefault = true),
        Track(id = 2, kind = "audio", index = 2, language = "eng"),
        Track(id = 3, kind = "audio", index = 3, language = "eng", title = "Commentary"),
    )

    @Test
    fun aConvertedStreamAsksForTheAudioLanguageUnlessTheOriginalIsPreferred() {
        assertNull(PlaybackPolicy.convertedAudioIndex(tracks, AudioPrefs(), "en"))
        assertEquals(2, PlaybackPolicy.convertedAudioIndex(tracks, AudioPrefs(preferOriginal = false, language = "en"), "fr"))
        // Empty language: the TV's own.
        assertEquals(2, PlaybackPolicy.convertedAudioIndex(tracks, AudioPrefs(preferOriginal = false), "en"))
        assertNull(PlaybackPolicy.convertedAudioIndex(tracks, AudioPrefs(preferOriginal = false, language = "de"), "en"))
    }

    @Test
    fun aConvertedStreamsAudioLanguageIsTheRequestedTrackOrTheDefault() {
        assertEquals("eng", PlaybackPolicy.convertedAudioLanguage(tracks, 2))
        assertEquals("jpn", PlaybackPolicy.convertedAudioLanguage(tracks, null))
        assertNull(PlaybackPolicy.convertedAudioLanguage(emptyList(), null))
    }

    @Test
    fun theAudioTrackIsAddedToTheStreamUrl() {
        assertEquals("/s/stream.mp4?start_ms=5&audio=2", StreamKinds.withAudio("/s/stream.mp4?start_ms=5", 2))
        assertEquals("/s/stream.mp4?audio=0", StreamKinds.withAudio("/s/stream.mp4", 0))
        assertEquals("/s/stream.mp4?start_ms=5", StreamKinds.withAudio("/s/stream.mp4?start_ms=5", null))
    }

    @Test
    fun subtitlesBeingPreparedAreAskedForAgainAtTheServersPace() {
        assertEquals(5, PlaybackPolicy.subtitleRetrySeconds(null))
        assertEquals(7, PlaybackPolicy.subtitleRetrySeconds(7))
        assertEquals(2, PlaybackPolicy.subtitleRetrySeconds(0))
        assertEquals(15, PlaybackPolicy.subtitleRetrySeconds(600))
    }

    @Test
    fun theScreenSleepsOnlyWhenThePlayerIsNotMeantToPlay() {
        assertTrue(PlaybackPolicy.keepScreenOn(true, Player.STATE_READY))
        assertTrue("buffering toward playing", PlaybackPolicy.keepScreenOn(true, Player.STATE_BUFFERING))
        assertFalse("paused", PlaybackPolicy.keepScreenOn(false, Player.STATE_READY))
        assertFalse("paused while buffering", PlaybackPolicy.keepScreenOn(false, Player.STATE_BUFFERING))
        assertFalse("ended", PlaybackPolicy.keepScreenOn(true, Player.STATE_ENDED))
        assertFalse("stopped or failed", PlaybackPolicy.keepScreenOn(true, Player.STATE_IDLE))
    }
}

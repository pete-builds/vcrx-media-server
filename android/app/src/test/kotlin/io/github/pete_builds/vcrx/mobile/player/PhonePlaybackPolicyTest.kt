package io.github.pete_builds.vcrx.mobile.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PhonePlaybackPolicyTest {
    @Test
    fun aDownloadAlwaysWins() {
        assertEquals(PlaySource.Local, PhonePlaybackPolicy.source(downloaded = true, online = true))
        assertEquals(PlaySource.Local, PhonePlaybackPolicy.source(downloaded = true, online = false))
        assertEquals(PlaySource.Stream, PhonePlaybackPolicy.source(downloaded = false, online = true))
        assertEquals(PlaySource.Unavailable, PhonePlaybackPolicy.source(downloaded = false, online = false))
    }

    @Test
    fun startPosition() {
        assertEquals(0L, PhonePlaybackPolicy.startMs(fromStart = true, savedMs = 60_000, durationMs = 600_000))
        assertEquals(60_000L, PhonePlaybackPolicy.startMs(fromStart = false, savedMs = 60_000, durationMs = 600_000))
        assertEquals(60_000L, PhonePlaybackPolicy.startMs(fromStart = false, savedMs = 60_000, durationMs = null))
        // Saved in the last half minute: start over rather than land on the credits' last frame.
        assertEquals(0L, PhonePlaybackPolicy.startMs(fromStart = false, savedMs = 590_000, durationMs = 600_000))
        assertEquals(0L, PhonePlaybackPolicy.startMs(fromStart = false, savedMs = null, durationMs = 600_000))
    }

    @Test
    fun pipOnlyForPlayingVideo() {
        assertTrue(PhonePlaybackPolicy.enterPip(isPlaying = true, isVideo = true))
        assertFalse(PhonePlaybackPolicy.enterPip(isPlaying = false, isVideo = true))
        assertFalse(PhonePlaybackPolicy.enterPip(isPlaying = true, isVideo = false))
    }

    @Test
    fun webVttCuesAndTags() {
        val vtt = """
            WEBVTT

            1
            00:00:01.000 --> 00:00:03.500
            <i>Hello</i> &amp; welcome

            00:01:02.250 --> 00:01:04.000 align:start
            Second
            line

            bad --> line
        """.trimIndent()
        val cues = WebVtt.parse(vtt)
        assertEquals(2, cues.size)
        assertEquals(Cue(1_000, 3_500, "Hello & welcome"), cues[0])
        assertEquals(62_250L, cues[1].startMs)
        assertEquals("Hello & welcome", WebVtt.at(cues, 2_000))
        assertEquals("Second\nline", WebVtt.at(cues, 63_000))
        assertNull(WebVtt.at(cues, 10_000))
    }
}

package io.github.pete_builds.vcrx.tv.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Subtitle cues held in file time, and the converted-stream maths that keeps them
 * in sync: each stream restarts the player's clock at zero, so the cue shown must
 * follow start_ms plus player time through every seek.
 */
class CueTimelineTest {
    private fun cue(start: Long, end: Long, text: String) = CueTimeline.TimedCue(start, end, text)

    private val cues = CueTimeline(
        listOf(
            cue(1_000, 3_000, "one"),
            cue(5_000, 8_000, "two"),
            // Overlaps "two": both show between 7 and 8 seconds.
            cue(7_000, 9_000, "sign"),
            cue(3_600_000, 3_602_000, "an hour in"),
        ),
    )

    @Test
    fun showsTheCuesActiveAtAPosition() {
        assertEquals(emptyList<String>(), cues.at(0))
        assertEquals(emptyList<String>(), cues.at(999))
        assertEquals(listOf("one"), cues.at(1_000))
        assertEquals(listOf("one"), cues.at(2_999))
        // The end is exclusive, as in WebVTT.
        assertEquals(emptyList<String>(), cues.at(3_000))
        assertEquals(listOf("two"), cues.at(6_000))
        assertEquals(listOf("two", "sign"), cues.at(7_500))
        assertEquals(listOf("sign"), cues.at(8_000))
        assertEquals(emptyList<String>(), cues.at(9_000))
        assertEquals(listOf("an hour in"), cues.at(3_601_000))
        assertEquals(emptyList<String>(), cues.at(Long.MAX_VALUE))
    }

    @Test
    fun knowsWhenWhatShowsNextChanges() {
        assertEquals(1_000L, cues.nextChangeAfter(0))
        assertEquals(3_000L, cues.nextChangeAfter(1_500))
        assertEquals(7_000L, cues.nextChangeAfter(6_000))
        assertEquals(8_000L, cues.nextChangeAfter(7_000))
        assertNull(cues.nextChangeAfter(3_602_000))
    }

    @Test
    fun waitsUntilTheNextChangeButNeverTooLongOrTooShort() {
        assertEquals(100L, CueTimeline.waitMs(900, 1_000))
        assertEquals(250L, CueTimeline.waitMs(0, 60_000))
        assertEquals(250L, CueTimeline.waitMs(0, null))
        assertEquals(20L, CueTimeline.waitMs(1_000, 1_000))
    }

    @Test
    fun dropsEmptyAndBackwardCues() {
        val t = CueTimeline(listOf(cue(5, 5, "empty"), cue(9, 2, "backward")))
        assertTrue(t.isEmpty)
        assertEquals(emptyList<String>(), t.at(5))
    }

    @Test
    fun aLongCueSpanningManyShortOnesStaysUp() {
        val t = CueTimeline(listOf(cue(0, 10_000, "song"), cue(1_000, 2_000, "a"), cue(3_000, 4_000, "b")))
        assertEquals(listOf("song", "a"), t.at(1_500))
        assertEquals(listOf("song"), t.at(2_500))
        assertEquals(listOf("song", "b"), t.at(3_500))
        assertEquals(listOf("song"), t.at(9_999))
    }

    /** A stand-in for the player: its own time restarts at zero with every converted stream. */
    private var playerMs = 0L

    @Test
    fun convertedSubtitlesStayInSyncThroughSeveralSeeks() {
        val timeline = StreamTimeline(converted = true, fileDurationMs = 7_200_000, playerPositionMs = { playerMs }, playerDurationMs = { null })
        fun shown() = cues.at(timeline.positionMs())

        // First stream from the resume point: the keyframe the server chose is 4 s into the file.
        timeline.land(timeline.beginSeek(), 4_000)
        playerMs = 0
        assertEquals(emptyList<String>(), shown())
        playerMs = 1_500 // file 5.5 s
        assertEquals(listOf("two"), shown())
        playerMs = 3_200 // file 7.2 s
        assertEquals(listOf("two", "sign"), shown())

        // Seek back to the start: a new stream at 0; player time restarts.
        timeline.land(timeline.beginSeek(), 0)
        playerMs = 1_200
        assertEquals(listOf("one"), shown())

        // Seek an hour in: the keyframe is 3 599 s; the player is 1.5 s into the new stream.
        timeline.land(timeline.beginSeek(), 3_599_000)
        playerMs = 1_500
        assertEquals(listOf("an hour in"), shown())

        // A late answer for an older seek must not move the subtitles.
        val stale = timeline.beginSeek()
        val current = timeline.beginSeek()
        timeline.land(current, 6_000)
        playerMs = 0
        assertEquals(listOf("two"), shown())
        timeline.land(stale, 0)
        assertEquals(listOf("two"), shown())

        // Without the stream's start the same player time shows the wrong line (the bug this fixes).
        assertEquals(emptyList<String>(), cues.at(playerMs))
    }

    @Test
    fun directPlayUsesThePlayersOwnTime() {
        val timeline = StreamTimeline(converted = false, fileDurationMs = null, playerPositionMs = { playerMs }, playerDurationMs = { null })
        timeline.land(timeline.beginSeek(), 60_000)
        playerMs = 2_000
        assertEquals(listOf("one"), cues.at(timeline.positionMs()))
    }
}

package io.github.pete_builds.vcrx.tv.music

import kotlin.random.Random
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The play queue's rules, without a player: next, previous, shuffle and repeat. */
class MusicQueueTest {
    private val album = listOf("Low Tide", "Breakwater", "Undertow", "Riptide", "Slack Water")

    @Test
    fun nextAndPreviousWalkTheAlbumInOrder() {
        val q = MusicQueue(album)
        assertEquals("Low Tide", q.current)
        assertTrue(q.next())
        assertEquals("Breakwater", q.current)
        // A few seconds in, Previous restarts the track rather than going back.
        assertEquals(MusicQueue.Back.Restart, q.previous(positionMs = 45_000))
        assertEquals("Breakwater", q.current)
        assertEquals(MusicQueue.Back.Previous, q.previous(positionMs = 1_000))
        assertEquals("Low Tide", q.current)
        // On the first track Previous always restarts.
        assertEquals(MusicQueue.Back.Restart, q.previous(positionMs = 0))
        assertEquals(0, q.position)
    }

    @Test
    fun theEndOfTheQueueStopsUnlessItRepeats() {
        val q = MusicQueue(album, startIndex = 4)
        assertNull(q.nextPosition())
        assertFalse(q.next())
        assertNull(q.ended())
        q.repeat = Repeat.All
        assertEquals(0, q.ended())
        assertEquals("Low Tide", q.current)
    }

    @Test
    fun repeatOnePlaysTheTrackAgainButNextStillMovesOn() {
        val q = MusicQueue(album, startIndex = 1)
        q.repeat = Repeat.One
        assertEquals(1, q.ended())
        assertEquals("Breakwater", q.current)
        assertTrue(q.next())
        assertEquals("Undertow", q.current)
    }

    @Test
    fun repeatCyclesOffAllOne() {
        val q = MusicQueue(album)
        assertEquals(Repeat.All, q.cycleRepeat())
        assertEquals(Repeat.One, q.cycleRepeat())
        assertEquals(Repeat.Off, q.cycleRepeat())
    }

    @Test
    fun shuffleKeepsTheCurrentTrackFirstAndEveryTrackOnce() {
        val q = MusicQueue(album, startIndex = 2, random = Random(7))
        q.setShuffle(true)
        assertTrue(q.shuffle)
        assertEquals("Undertow", q.current)
        assertEquals(0, q.position)
        assertEquals(album.sorted(), q.playOrder.sorted())
        assertNotEquals(album, q.playOrder)
        // Off again: the album's order, still on the same track.
        q.next()
        val playing = q.current
        q.setShuffle(false)
        assertEquals(album, q.playOrder)
        assertEquals(playing, q.current)
        assertEquals(album.indexOf(playing), q.position)
    }

    @Test
    fun shuffledFromTheStartBeginsWithTheChosenTrack() {
        val q = MusicQueue(album, startIndex = 3, shuffle = true, random = Random(1))
        assertEquals("Riptide", q.current)
        assertEquals(5, q.playOrder.toSet().size)
    }

    @Test
    fun jumpToStaysInsideTheQueue() {
        val q = MusicQueue(album)
        assertTrue(q.jumpTo(3))
        assertEquals("Riptide", q.current)
        assertFalse(q.jumpTo(5))
        assertFalse(q.jumpTo(-1))
        assertEquals("Riptide", q.current)
    }

    @Test
    fun anEmptyQueueHasNothingToPlay() {
        val q = MusicQueue(emptyList<String>())
        assertNull(q.current)
        assertFalse(q.next())
        q.setShuffle(true)
        assertNull(q.current)
    }
}

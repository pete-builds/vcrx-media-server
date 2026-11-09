package io.github.pete_builds.vcrx.tv.music

import io.github.pete_builds.vcrx.tv.net.AudiobookChapter
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class BookPositionTest {
    private val starts = listOf(0.0, 600.0, 1500.0)
    private val durations = listOf(600.0, 900.0, 300.0)

    @Test
    fun aBookPositionMapsOntoOneFileAndBack() {
        for ((pos, want) in listOf(0.0 to (0 to 0.0), 599.5 to (0 to 599.5), 600.0 to (1 to 0.0), 1000.0 to (1 to 400.0),
            1799.0 to (2 to 299.0), 5000.0 to (2 to 300.0), -3.0 to (0 to 0.0))) {
            val got = BookPosition.locate(starts, durations, pos)
            assertEquals("position $pos", want, got)
            if (pos in 0.0..1800.0) assertEquals("position $pos maps back", pos, starts[got.first] + got.second, 1e-9)
        }
        assertEquals(0 to 0.0, BookPosition.locate(emptyList(), emptyList(), 10.0))
    }

    private val chapters = listOf(
        AudiobookChapter(0, "Opening", 0.0, 8.0),
        AudiobookChapter(1, "The Middle", 8.0, 16.0),
        AudiobookChapter(2, "Ending", 16.0, 24.0),
    )

    @Test
    fun chaptersForwardAndBack() {
        assertEquals(1, BookPosition.chapterAt(chapters, 9.0))
        assertEquals(8.0, BookPosition.nextChapter(chapters, 2.0)!!, 0.0)
        assertNull(BookPosition.nextChapter(chapters, 20.0))
        // A few seconds in, back restarts the chapter; at its start, it goes to the one before.
        assertEquals(8.0, BookPosition.previousChapter(chapters, 12.0), 0.0)
        assertEquals(0.0, BookPosition.previousChapter(chapters, 9.0), 0.0)
        assertEquals(0.0, BookPosition.previousChapter(chapters, 1.0), 0.0)
    }

    @Test
    fun speedsStepUpAndWrap() {
        assertEquals(1.2f, BookPosition.nextSpeed(1f), 0f)
        assertEquals(0.8f, BookPosition.nextSpeed(2.5f), 0f)
        assertEquals(1f, BookPosition.nextSpeed(0.8f), 0f)
    }
}

package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.AudiobookChapter
import org.junit.Assert.assertEquals
import org.junit.Test

class BookTimelineTest {
    // Three files of 100 s; the middle one is converted (no byte ranges).
    private val files = listOf(BookSpan(11, 0.0, 100.0), BookSpan(12, 100.0, 100.0, seekable = false), BookSpan(13, 200.0, 100.0))
    private val chapters = listOf(
        AudiobookChapter(0, "One", 0.0, 150.0, 11),
        AudiobookChapter(1, "Two", 150.0, 250.0, 12),
        AudiobookChapter(2, "Three", 250.0, 300.0, 13),
    )
    private val book = BookTimeline(7, 300.0, files, chapters)

    @Test
    fun startsInTheRightFile() {
        assertEquals(BookStart(0, 42_000, 0), book.start(42.0))
        assertEquals(BookStart(2, 50_000, 0), book.start(250.0))
        // A converted file started part way asks its stream to begin there.
        assertEquals(BookStart(1, 30_000, 30_000), book.start(130.0))
        // At its very start, nothing to skip.
        assertEquals(BookStart(1, 0, 0), book.start(100.0))
        // Left within a second of the end: start over.
        assertEquals(BookStart(0, 0, 0), book.start(299.5))
        assertEquals(BookStart(0, 0, 0), book.start(-5.0))
    }

    @Test
    fun aPlaceIsSecondsAcrossTheBook() {
        assertEquals(212.5, book.positionS(2, 0, 12_500), 1e-9)
        // A converted stream that began 30 s in: the player counts from there.
        assertEquals(135.0, book.positionS(1, 30_000, 5_000), 1e-9)
        assertEquals(0.0, book.positionS(9, 0, 1_000), 1e-9)
        assertEquals(212.5, BookPosition.inBook(doubleArrayOf(0.0, 100.0, 200.0), 2, 0, 12_500), 1e-9)
    }

    @Test
    fun seeksInPlaceUnlessTheFileIsConverted() {
        assertEquals(BookSeek.InPlace(2, 10_000), book.seek(210.0))
        assertEquals(BookSeek.InPlace(0, 5_000), book.seek(5.0))
        assertEquals(BookSeek.Reload(150.0), book.seek(150.0))
        // Clamped to the book, half a second short of the end so a seek does not finish it.
        assertEquals(BookSeek.InPlace(2, 99_500), book.seek(1_000.0))
        assertEquals(BookSeek.InPlace(0, 0), book.seek(-3.0))
    }

    @Test
    fun skipsAndChapters() {
        assertEquals(0.0, book.skipTarget(10.0, -15.0), 0.0)
        assertEquals(299.5, book.skipTarget(290.0, 30.0), 0.0)
        assertEquals(150.0, book.nextChapter(20.0)!!, 0.0)
        assertEquals(null, book.nextChapter(260.0))
        // Past the first few seconds of a chapter, Previous restarts it.
        assertEquals(150.0, book.previousChapter(170.0), 0.0)
        assertEquals(0.0, book.previousChapter(151.0), 0.0)
        // The sleep timer's end of chapter.
        assertEquals(150.0, book.chapterEnd(20.0), 0.0)
        assertEquals(300.0, book.chapterEnd(260.0), 0.0)
        assertEquals(1, BookPosition.chapterIndex(doubleArrayOf(0.0, 150.0, 250.0), 151.0))
        assertEquals(-1, BookPosition.chapterIndex(DoubleArray(0), 151.0))
    }

    @Test
    fun aBookWithoutChaptersEndsAtItsEnd() {
        val plain = BookTimeline(1, 90.0, listOf(BookSpan(1, 0.0, 90.0)), emptyList())
        assertEquals(90.0, plain.chapterEnd(10.0), 0.0)
        assertEquals(null, plain.nextChapter(10.0))
    }

    @Test
    fun sleepTimerLabels() {
        assertEquals("End of chapter", SleepTimer.label(SleepTimer.END_OF_CHAPTER))
        assertEquals("15 min", SleepTimer.label(15))
        assertEquals("Off", SleepTimer.label(SleepTimer.OFF))
        assertEquals("12 min left", SleepTimer.remaining(1_000 + 12 * 60_000 + 500, 1_000))
        assertEquals("Under a minute left", SleepTimer.remaining(30_000, 0))
    }
}

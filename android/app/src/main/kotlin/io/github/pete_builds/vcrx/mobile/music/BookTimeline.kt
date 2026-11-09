package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.AudiobookChapter

/** One file of a book as the player holds it: where it starts in the book, its length, and whether it seeks in place. */
data class BookSpan(val fileId: Long, val startS: Double, val durationS: Double, val seekable: Boolean = true)

/** How to get to a place in a book. */
sealed interface BookSeek {
    /** Seek the playlist: file [index], [offsetMs] into it. */
    data class InPlace(val index: Int, val offsetMs: Long) : BookSeek

    /** Load the book again from [positionS]: a converted file starts a new stream there (start_ms). */
    data class Reload(val positionS: Double) : BookSeek
}

/** Where a book starts: the file, the offset into it, and for a converted file the start_ms its stream begins at. */
data class BookStart(val index: Int, val offsetMs: Long, val convertedStartMs: Long)

/**
 * A book in the player: its files as one Media3 playlist, and its chapters.
 * A place is seconds from the start of the whole book ([BookPosition]); this
 * maps it to a file and back, and decides how a skip or a chapter jump gets
 * there, as the TV's AudiobookPlayer does. Pure, so it is unit tested.
 */
class BookTimeline(val bookId: Long, val durationS: Double, val files: List<BookSpan>, val chapters: List<AudiobookChapter>) {
    private val starts = files.map { it.startS }
    private val durations = files.map { it.durationS }

    /** Where to start for [positionS], resuming near the end starting over. */
    fun start(positionS: Double): BookStart {
        val pos = if (positionS >= durationS - 1) 0.0 else positionS.coerceAtLeast(0.0)
        val (index, offsetS) = BookPosition.locate(starts, durations, pos)
        val offsetMs = (offsetS * 1000).toLong()
        val converted = files.getOrNull(index)?.seekable == false && offsetMs > 0
        return BookStart(index, offsetMs, if (converted) offsetMs else 0L)
    }

    /** Seconds into the book: file [index], whose stream began [convertedStartMs] in, at the player's [positionMs]. */
    fun positionS(index: Int, convertedStartMs: Long, positionMs: Long): Double {
        val f = files.getOrNull(index) ?: return 0.0
        return f.startS + (convertedStartMs + positionMs.coerceAtLeast(0)) / 1000.0
    }

    /**
     * How to reach [targetS]: in place in a file Media3 can seek, else by loading
     * the book again from there, which starts a converted file's stream at the
     * place (the TV's rule). A converted file never seeks in place: its stream
     * answers no byte ranges.
     */
    fun seek(targetS: Double): BookSeek {
        val target = targetS.coerceIn(0.0, (durationS - 0.5).coerceAtLeast(0.0))
        val (index, offsetS) = BookPosition.locate(starts, durations, target)
        return if (files.getOrNull(index)?.seekable == true) BookSeek.InPlace(index, (offsetS * 1000).toLong()) else BookSeek.Reload(target)
    }

    /** Where a skip of [deltaS] from [positionS] lands. */
    fun skipTarget(positionS: Double, deltaS: Double): Double = (positionS + deltaS).coerceIn(0.0, (durationS - 0.5).coerceAtLeast(0.0))

    fun nextChapter(positionS: Double): Double? = BookPosition.nextChapter(chapters, positionS)

    fun previousChapter(positionS: Double): Double = BookPosition.previousChapter(chapters, positionS)

    fun chapterAt(positionS: Double): Int = BookPosition.chapterAt(chapters, positionS)

    /** Where the chapter playing at [positionS] ends: the next chapter's start, else the end of the book. */
    fun chapterEnd(positionS: Double): Double {
        val i = chapterAt(positionS)
        val c = chapters.getOrNull(i) ?: return durationS
        return c.endS.takeIf { it > c.startS } ?: chapters.getOrNull(i + 1)?.startS ?: durationS
    }
}

/** The sleep timer's choices: minutes, or [END_OF_CHAPTER]. */
object SleepTimer {
    const val OFF = 0
    const val END_OF_CHAPTER = -1
    val MINUTES = listOf(5, 10, 15, 30, 45, 60)

    fun label(choice: Int): String = when (choice) {
        OFF -> "Off"
        END_OF_CHAPTER -> "End of chapter"
        else -> "$choice min"
    }

    /** "12 min left", "Under a minute left", for a timer that ends at [atMs]. */
    fun remaining(atMs: Long, nowMs: Long): String {
        val left = (atMs - nowMs).coerceAtLeast(0) / 60_000
        return if (left < 1) "Under a minute left" else "$left min left"
    }
}

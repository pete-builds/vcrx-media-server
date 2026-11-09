package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.AudiobookChapter

/**
 * A book's position math, kept apart from the player so it is plain Kotlin
 * with unit tests. A position is seconds from the start of the whole book;
 * each file starts at its startS, so a position maps onto one file and an
 * offset into it, and back.
 */
object BookPosition {
    /** Where [positionS] falls: the index of the file it is in and the offset into it, both clamped to the book. */
    fun locate(starts: List<Double>, durations: List<Double>, positionS: Double): Pair<Int, Double> {
        if (starts.isEmpty()) return 0 to 0.0
        val pos = positionS.coerceAtLeast(0.0)
        val file = starts.indexOfLast { pos >= it }.coerceAtLeast(0)
        return file to (pos - starts[file]).coerceIn(0.0, durations[file])
    }

    /** The chapter [positionS] is in, or -1 before the first. */
    fun chapterAt(chapters: List<AudiobookChapter>, positionS: Double): Int = chapters.indexOfLast { positionS >= it.startS - 0.05 }

    /** Seconds into the whole book: file [index]'s start, where its stream began, and the player's place in it. */
    fun inBook(fileStarts: DoubleArray, index: Int, convertedStartMs: Long, positionMs: Long): Double =
        (fileStarts.getOrNull(index) ?: 0.0) + (convertedStartMs + positionMs.coerceAtLeast(0)) / 1000.0

    /** [chapterAt] from the chapters' starts alone, as the phone's screens get them from the player. */
    fun chapterIndex(starts: DoubleArray, positionS: Double): Int = starts.indexOfLast { positionS >= it - 0.05 }

    /**
     * Where Previous chapter goes: the start of this chapter once it has played
     * a few seconds, else the start of the one before.
     */
    fun previousChapter(chapters: List<AudiobookChapter>, positionS: Double): Double {
        val i = chapterAt(chapters, positionS)
        return when {
            i >= 0 && positionS - chapters[i].startS > RESTART_AFTER_S -> chapters[i].startS
            i > 0 -> chapters[i - 1].startS
            else -> 0.0
        }
    }

    /** Where Next chapter goes, or null in the last chapter. */
    fun nextChapter(chapters: List<AudiobookChapter>, positionS: Double): Double? =
        chapters.getOrNull(chapterAt(chapters, positionS) + 1)?.startS

    /** The speeds a listener can choose. */
    val SPEEDS = listOf(0.8f, 1f, 1.2f, 1.5f, 1.75f, 2f, 2.5f)

    /** The next speed up, wrapping round to the slowest. */
    fun nextSpeed(current: Float): Float = SPEEDS.getOrElse(SPEEDS.indexOfFirst { it > current + 0.001f }) { SPEEDS.first() }

    private const val RESTART_AFTER_S = 3.0
}

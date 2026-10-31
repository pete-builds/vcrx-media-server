package io.github.pete_builds.vcrx.tv.player

/**
 * Subtitle cues in file time, for converted streams. A converted stream restarts
 * its own clock at zero after every seek, so cues handed to the player would be
 * out by the stream's start; instead the app keeps the cues itself and shows
 * those active at the file position ([StreamTimeline.positionMs]), which is right
 * after any number of seeks. Pure, so the timing is unit tested.
 *
 * Cues may overlap. The timeline is cut at every start and end into segments
 * whose active set does not change, so a lookup is one binary search.
 */
class CueTimeline<T>(cues: List<TimedCue<T>>) {
    data class TimedCue<T>(val startMs: Long, val endMs: Long, val value: T)

    private val points: LongArray
    private val active: List<List<T>>

    init {
        val valid = cues.filter { it.endMs > it.startMs }.sortedBy { it.startMs }
        points = valid.flatMap { listOf(it.startMs, it.endMs) }.distinct().sorted().toLongArray()
        // active[i] holds the cues showing from points[i] until points[i + 1]. One sweep:
        // a cue joins at its start point and leaves at its end point.
        var next = 0
        val showing = mutableListOf<TimedCue<T>>()
        active = points.map { at ->
            showing.removeAll { it.endMs <= at }
            while (next < valid.size && valid[next].startMs <= at) showing += valid[next++]
            showing.map { it.value }
        }
    }

    val isEmpty: Boolean get() = points.isEmpty()

    /** The cues showing at [positionMs], in start order. */
    fun at(positionMs: Long): List<T> {
        val i = segment(positionMs)
        return if (i < 0) emptyList() else active[i]
    }

    /** The next time after [positionMs] at which what shows changes; null when nothing more changes. */
    fun nextChangeAfter(positionMs: Long): Long? {
        val i = segment(positionMs)
        return points.getOrNull(i + 1)
    }

    /** The index of the last point at or before [positionMs], or -1 before the first. */
    private fun segment(positionMs: Long): Int {
        var lo = 0
        var hi = points.size - 1
        var found = -1
        while (lo <= hi) {
            val mid = (lo + hi) ushr 1
            if (points[mid] <= positionMs) {
                found = mid
                lo = mid + 1
            } else {
                hi = mid - 1
            }
        }
        return found
    }

    companion object {
        /** How long to wait before looking again: until the next change, but at least [minMs] and at most [maxMs]. */
        fun waitMs(positionMs: Long, nextChangeMs: Long?, minMs: Long = 20, maxMs: Long = 250): Long =
            ((nextChangeMs ?: Long.MAX_VALUE) - positionMs).coerceIn(minMs, maxMs)
    }
}

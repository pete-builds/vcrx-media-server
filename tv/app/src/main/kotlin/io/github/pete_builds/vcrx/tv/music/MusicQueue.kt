package io.github.pete_builds.vcrx.tv.music

import kotlin.random.Random

/** Off, the whole queue again from the top, or the one track over and over. */
enum class Repeat { Off, All, One }

/**
 * The play queue's order and position, kept apart from the player so the rules
 * are plain Kotlin with unit tests: what Next and Previous do at either end,
 * what shuffle does to the tracks still to come, and what repeat does when a
 * track ends. [tracks] stay in the order the server sent them (an album's
 * order); [order] is the play order, a permutation of their indexes.
 */
class MusicQueue<T>(
    val tracks: List<T>,
    startIndex: Int = 0,
    shuffle: Boolean = false,
    private val random: Random = Random.Default,
) {
    var order: List<Int> = tracks.indices.toList()
        private set

    /** Where in [order] playback is; -1 for an empty queue. */
    var position: Int = if (tracks.isEmpty()) -1 else startIndex.coerceIn(0, tracks.size - 1)
        private set

    var shuffle: Boolean = false
        private set

    var repeat: Repeat = Repeat.Off

    init {
        if (shuffle) setShuffle(true)
    }

    val current: T? get() = order.getOrNull(position)?.let { tracks[it] }

    /** The tracks in play order. */
    val playOrder: List<T> get() = order.map { tracks[it] }

    /** Where Next goes: the following track, the first when the queue repeats, or null at the end. */
    fun nextPosition(): Int? = when {
        position + 1 < order.size -> position + 1
        repeat == Repeat.All && order.isNotEmpty() -> 0
        else -> null
    }

    /** Moves to the next track; false at the end of a queue that does not repeat. */
    fun next(): Boolean {
        val p = nextPosition() ?: return false
        position = p
        return true
    }

    /** What Previous means [positionMs] into a track. */
    sealed interface Back {
        /** Start the current track again. */
        data object Restart : Back

        /** Go to the track before. */
        data object Previous : Back
    }

    /**
     * Previous restarts the track once it has played a few seconds, as players
     * do, and on the first track; otherwise it goes back one.
     */
    fun previous(positionMs: Long): Back {
        if (positionMs > RESTART_AFTER_MS || position <= 0) return Back.Restart
        position -= 1
        return Back.Previous
    }

    /**
     * A track finished on its own. Repeat one plays it again; otherwise the
     * queue moves on. Returns the position to play, or null when it is over.
     */
    fun ended(): Int? = when (repeat) {
        Repeat.One -> position
        else -> if (next()) position else null
    }

    /** Plays the track at [orderPosition] in play order. */
    fun jumpTo(orderPosition: Int): Boolean {
        if (orderPosition !in order.indices) return false
        position = orderPosition
        return true
    }

    /**
     * Shuffle on reorders the other tracks at random and puts the current one
     * first, so it keeps playing. Shuffle off restores the server's order with
     * the current track where it belongs.
     */
    fun setShuffle(on: Boolean) {
        val playing = order.getOrNull(position)
        if (on) {
            val rest = order.filter { it != playing }.shuffled(random)
            order = if (playing == null) rest else listOf(playing) + rest
            position = if (playing == null) -1 else 0
        } else {
            order = tracks.indices.toList()
            position = playing ?: -1
        }
        shuffle = on
    }

    fun cycleRepeat(): Repeat {
        repeat = when (repeat) {
            Repeat.Off -> Repeat.All
            Repeat.All -> Repeat.One
            Repeat.One -> Repeat.Off
        }
        return repeat
    }

    companion object {
        const val RESTART_AFTER_MS = 3_000L
    }
}

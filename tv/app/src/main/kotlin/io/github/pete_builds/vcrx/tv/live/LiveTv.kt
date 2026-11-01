// What Live TV shows and how the remote moves through channels. Pure, so the
// rules are unit tested without a TV.
package io.github.pete_builds.vcrx.tv.live

import android.view.KeyEvent
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.LiveChannel
import io.github.pete_builds.vcrx.tv.net.LiveProgram

object LiveTv {
    /** What a remote key does in the live player. */
    enum class Key { ChannelUp, ChannelDown, Info, Other }

    /**
     * Up and the remote's channel-up key go up the list, Down and channel-down
     * go down, as on cable; OK shows what is on.
     */
    fun key(code: Int): Key = when (code) {
        KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_CHANNEL_UP, KeyEvent.KEYCODE_PAGE_UP -> Key.ChannelUp
        KeyEvent.KEYCODE_DPAD_DOWN, KeyEvent.KEYCODE_CHANNEL_DOWN, KeyEvent.KEYCODE_PAGE_DOWN -> Key.ChannelDown
        KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_INFO -> Key.Info
        else -> Key.Other
    }

    /**
     * The channel [delta] places from [current] in the guide's order, wrapping
     * round at either end; null for an empty list. A current channel no longer
     * listed starts from the top.
     */
    fun step(channels: List<LiveChannel>, current: Long, delta: Int): LiveChannel? {
        if (channels.isEmpty()) return null
        val at = channels.indexOfFirst { it.id == current }.coerceAtLeast(0)
        return channels[Math.floorMod(at + delta, channels.size)]
    }

    /** How far into [p] the time [nowMs] is, 0 to 1. */
    fun progress(p: LiveProgram, nowMs: Long): Float {
        val span = p.stop - p.start
        if (span <= 0) return 0f
        return ((nowMs - p.start).toFloat() / span).coerceIn(0f, 1f)
    }

    /** "4.1  Example News", or the name alone with no number. */
    fun label(c: LiveChannel): String = listOfNotNull(c.number?.takeIf { it.isNotBlank() }, c.name).joinToString("  ")

    /** What is on now, with its episode; "No guide" for a channel without one. */
    fun nowLine(c: LiveChannel): String =
        c.now?.let { p -> listOfNotNull(p.title, p.episode?.takeIf { it.isNotBlank() }).joinToString("  ") } ?: "No guide"

    /** "Next  8:00 PM  Late News", or empty. [time] formats a Unix millisecond. */
    fun nextLine(c: LiveChannel, time: (Long) -> String): String =
        c.next?.let { "Next  ${time(it.start)}  ${it.title}" } ?: ""

    /** What the player says when a channel will not start. */
    fun failure(e: Throwable): String = when {
        e is ApiException.Http && e.code == "streams_busy" -> "Every live stream is in use. Try again when someone stops watching a channel."
        e is ApiException.Http && e.status == 404 -> "This channel is no longer available."
        e is ApiException.Unreachable -> "Cannot reach the server."
        else -> e.message ?: "This channel will not play."
    }
}

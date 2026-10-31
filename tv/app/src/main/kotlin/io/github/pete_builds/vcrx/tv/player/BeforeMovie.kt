// What plays before a movie and what the screen says while it does. Pure, so
// the rules are unit tested without a TV.
package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.BeforeClip

object BeforeMovie {
    /**
     * Only a playback that starts from the beginning asks: resuming, and a
     * trailer or extra played on its own, go straight to the player. The server
     * answers with nothing for anything but a movie.
     */
    fun asks(start: StartRequest, extraId: Long?): Boolean = start == StartRequest.Beginning && extraId == null

    /** The line at the top while [clip] plays. */
    fun label(clip: BeforeClip): String = if (clip.kind == "trailer") "Trailer  ·  ${clip.title}" else "Your movie is next"

    /** The Skip button: the last clip skips to the movie itself. */
    fun skipLabel(index: Int, count: Int): String = if (index < count - 1) "Skip" else "Skip to the movie"
}

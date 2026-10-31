// What the Trailer button offers and how a YouTube trailer is opened. Pure, so
// every rule here is unit tested; the composables in ui/details/DetailsExtras.kt
// only wire it to the screen and to Android.
package io.github.pete_builds.vcrx.tv.trailers

import io.github.pete_builds.vcrx.tv.net.Trailer
import io.github.pete_builds.vcrx.tv.settings.Preferences
import io.github.pete_builds.vcrx.tv.settings.TrailerMode

/** Where trailers may come from: the viewer's "Trailers" choice in Settings. */
enum class TrailerSource { Off, LocalOnly, LocalAndYouTube }

object TrailerSettings {
    /** The viewer's Trailers choice in Settings (default: local and YouTube). */
    fun chosen(prefs: Preferences): TrailerSource = when (prefs.trailers) {
        TrailerMode.Off -> TrailerSource.Off
        TrailerMode.LocalOnly -> TrailerSource.LocalOnly
        TrailerMode.LocalAndYouTube -> TrailerSource.LocalAndYouTube
    }
}

/** The viewer's choice, else "local and YouTube" when the server offers YouTube trailers, else local only. */
fun trailerSource(chosen: TrailerSource?, serverOffersYouTube: Boolean): TrailerSource =
    chosen ?: if (serverOffersYouTube) TrailerSource.LocalAndYouTube else TrailerSource.LocalOnly

/** The trailer the Trailer button plays under [source], or null for no button. */
fun offeredTrailer(trailer: Trailer?, source: TrailerSource): Trailer? = when {
    trailer == null || source == TrailerSource.Off -> null
    trailer.source == "local" && trailer.extraId != null -> trailer
    trailer.source == "youtube" && source == TrailerSource.LocalAndYouTube && isYouTubeKey(trailer.youtubeKey) -> trailer
    else -> null
}

private val youTubeKey = Regex("^[A-Za-z0-9_-]{11}$")

/** A YouTube video id. The server sends nothing else; checked again before it goes into an intent. */
fun isYouTubeKey(key: String?): Boolean = key != null && youTubeKey.matches(key)

/**
 * The URIs an ACTION_VIEW intent tries, in order: the YouTube app's own scheme,
 * which YouTube for Google TV answers, then the web address, which the YouTube
 * app or a browser may claim. Empty for anything that is not a YouTube id.
 */
fun youTubeUris(key: String?): List<String> =
    if (!isYouTubeKey(key)) emptyList() else listOf("vnd.youtube:$key", "https://www.youtube.com/watch?v=$key")

sealed interface LaunchResult {
    data class Opened(val uri: String) : LaunchResult
    data class Failed(val message: String) : LaunchResult
}

const val NO_YOUTUBE = "This TV has no app that opens YouTube. Install YouTube from the Play Store to watch this trailer."
const val NOT_YOUTUBE = "This trailer is not a YouTube video this app can open."

/**
 * Opens a YouTube trailer with the first URI something on the TV handles.
 * [open] starts an ACTION_VIEW for one URI and returns false when nothing
 * resolves it (ActivityNotFoundException).
 */
fun launchYouTube(key: String?, open: (String) -> Boolean): LaunchResult {
    val uris = youTubeUris(key)
    if (uris.isEmpty()) return LaunchResult.Failed(NOT_YOUTUBE)
    for (uri in uris) {
        if (open(uri)) return LaunchResult.Opened(uri)
    }
    return LaunchResult.Failed(NO_YOUTUBE)
}

package io.github.pete_builds.vcrx.mobile.offline

/**
 * Download ids and cache keys. A download is stored under a key that names the
 * content, not the URL, so a changed server address still finds the copy, and
 * playback asks the cache for the same key. Pure, so it is unit tested.
 */
object DownloadKeys {
    private const val VERSION = "version:"
    private const val TRACK = "track:"

    fun version(versionId: Long): String = "$VERSION$versionId"

    fun track(trackId: Long): String = "$TRACK$trackId"

    fun versionPath(versionId: Long): String = "/api/v1/versions/$versionId/file"

    fun trackPath(trackId: Long): String = "/api/v1/music/tracks/$trackId/file"

    /** The server path a key downloads from, or null for a key this app did not make. */
    fun pathFor(key: String): String? = when {
        key.startsWith(VERSION) -> key.removePrefix(VERSION).toLongOrNull()?.let(::versionPath)
        key.startsWith(TRACK) -> key.removePrefix(TRACK).toLongOrNull()?.let(::trackPath)
        else -> null
    }

    fun isAudio(key: String): Boolean = key.startsWith(TRACK)
}

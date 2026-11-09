package io.github.pete_builds.vcrx.mobile.offline

/**
 * Download ids and cache keys. A download is stored under a key that names the
 * content, not the URL, so a changed server address still finds the copy, and
 * playback asks the cache for the same key. Pure, so it is unit tested.
 */
object DownloadKeys {
    private const val VERSION = "version:"
    private const val TRACK = "track:"
    private const val BOOK_FILE = "bookfile:"
    private const val EPISODE = "episode:"

    fun version(versionId: Long): String = "$VERSION$versionId"

    fun track(trackId: Long): String = "$TRACK$trackId"

    /** One file of an audiobook; a book is downloaded as every one of its files. */
    fun bookFile(fileId: Long): String = "$BOOK_FILE$fileId"

    fun episode(episodeId: Long): String = "$EPISODE$episodeId"

    fun versionPath(versionId: Long): String = "/api/v1/versions/$versionId/file"

    fun trackPath(trackId: Long): String = "/api/v1/music/tracks/$trackId/file"

    /** The book file whole, as the server keeps it (Range-capable, so it plays and seeks offline). */
    fun bookFilePath(fileId: Long): String = "/api/v1/audiobooks/files/$fileId/file"

    /** A full GET of the episode's stream is the whole file, fetched by the server from the podcast's host. */
    fun episodePath(episodeId: Long): String = "/api/v1/podcasts/episodes/$episodeId/stream"

    /** The server path a key downloads from, or null for a key this app did not make. */
    fun pathFor(key: String): String? = when {
        key.startsWith(VERSION) -> key.removePrefix(VERSION).toLongOrNull()?.let(::versionPath)
        key.startsWith(TRACK) -> key.removePrefix(TRACK).toLongOrNull()?.let(::trackPath)
        key.startsWith(BOOK_FILE) -> key.removePrefix(BOOK_FILE).toLongOrNull()?.let(::bookFilePath)
        key.startsWith(EPISODE) -> key.removePrefix(EPISODE).toLongOrNull()?.let(::episodePath)
        else -> null
    }

    fun isAudio(key: String): Boolean = key.startsWith(TRACK) || key.startsWith(BOOK_FILE) || key.startsWith(EPISODE)
}

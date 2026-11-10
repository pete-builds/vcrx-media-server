// Wire types for the audiobook routes in server/api/openapi.yaml
// (/api/v1/audiobooks/...). Times are seconds from the start of the whole
// book, across its files. As in Models.kt, optional fields default so a
// newer server never breaks decoding.
package io.github.pete_builds.vcrx.mobile.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Where the listener is in a book. */
@Serializable
data class AudiobookProgress(
    @SerialName("position_s") val positionS: Double = 0.0,
    val finished: Boolean = false,
    @SerialName("updated_at") val updatedAt: Long = 0,
)

@Serializable
data class AudiobookBook(
    val id: Long,
    val title: String,
    @SerialName("library_id") val libraryId: Long = 0,
    val subtitle: String? = null,
    val author: String = "",
    val narrator: String? = null,
    val series: String? = null,
    @SerialName("series_index") val seriesIndex: Double? = null,
    val year: Int? = null,
    val genre: String? = null,
    @SerialName("duration_s") val durationS: Double = 0.0,
    @SerialName("chapter_count") val chapterCount: Int = 0,
    @SerialName("has_cover") val hasCover: Boolean = false,
    val progress: AudiobookProgress? = null,
)

@Serializable
data class AudiobookFile(
    val id: Long,
    val index: Int = 0,
    val title: String = "",
    @SerialName("start_s") val startS: Double = 0.0,
    @SerialName("duration_s") val durationS: Double = 0.0,
    /** Set in a play queue: how to play it, or why it cannot. */
    val stream: MusicStream? = null,
    val refusal: MusicRefusal? = null,
)

@Serializable
data class AudiobookChapter(
    val index: Int = 0,
    val title: String = "",
    @SerialName("start_s") val startS: Double = 0.0,
    @SerialName("end_s") val endS: Double = 0.0,
    @SerialName("file_id") val fileId: Long = 0,
)

@Serializable
data class AudiobookDetail(
    val id: Long,
    val title: String,
    @SerialName("library_id") val libraryId: Long = 0,
    val subtitle: String? = null,
    val author: String = "",
    val narrator: String? = null,
    val series: String? = null,
    @SerialName("series_index") val seriesIndex: Double? = null,
    val year: Int? = null,
    val genre: String? = null,
    val description: String? = null,
    @SerialName("duration_s") val durationS: Double = 0.0,
    @SerialName("has_cover") val hasCover: Boolean = false,
    val progress: AudiobookProgress? = null,
    val files: List<AudiobookFile> = emptyList(),
    val chapters: List<AudiobookChapter> = emptyList(),
)

@Serializable
data class AudiobookPage(val books: List<AudiobookBook> = emptyList(), val total: Int = 0)

@Serializable
data class AudiobookContinue(val books: List<AudiobookBook> = emptyList())

/** Everything the player needs: files with streams, chapters, and where the listener left off. */
@Serializable
data class AudiobookQueue(
    @SerialName("book_id") val bookId: Long,
    val title: String = "",
    val author: String = "",
    @SerialName("has_cover") val hasCover: Boolean = false,
    @SerialName("duration_s") val durationS: Double = 0.0,
    val files: List<AudiobookFile> = emptyList(),
    val chapters: List<AudiobookChapter> = emptyList(),
    val progress: AudiobookProgress = AudiobookProgress(),
)

@Serializable
data class AudiobookQueueRequest(val capabilities: Capabilities)

@Serializable
data class AudiobookListenRequest(val capabilities: Capabilities)

/** A listen to one file, in Activity; events go to the playback events route. */
@Serializable
data class AudiobookListen(
    @SerialName("session_id") val sessionId: String,
    @SerialName("file_id") val fileId: Long = 0,
    @SerialName("book_id") val bookId: Long = 0,
    val decision: String = "",
    @SerialName("heartbeat_interval_ms") val heartbeatIntervalMs: Long = 10_000,
)

@Serializable
data class AudiobookProgressRequest(
    @SerialName("position_s") val positionS: Double,
    val finished: Boolean? = null,
)

/** "Sample Series, book 2", or the series alone. */
fun AudiobookDetail.seriesLine(): String? = series?.let { s -> seriesIndex?.let { "$s, book ${formatIndex(it)}" } ?: s }

private fun formatIndex(n: Double): String = if (n == Math.floor(n)) n.toLong().toString() else n.toString()

/** True once started and not finished. */
val AudiobookProgress?.inProgress: Boolean get() = this != null && !finished && positionS > 0

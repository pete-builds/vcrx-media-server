package io.github.pete_builds.vcrx.mobile.offline

/** A downloaded book: its files' downloads, shown and deleted as one. */
data class BookDownload(val bookId: Long, val title: String, val author: String, val files: List<DownloadEntry>) {
    val done: Boolean get() = files.isNotEmpty() && files.all { it.done }
    val filesDone: Int get() = files.count { it.done }
    val bytes: Long get() = files.sumOf { it.bytes.coerceAtLeast(0) }

    /** Progress over the whole book, 0 to 100, weighting each file by its length. */
    val percent: Float
        get() {
            val total = files.sumOf { (it.meta.durationMs ?: 0L).coerceAtLeast(1L) }.toFloat()
            return files.sumOf { ((it.meta.durationMs ?: 0L).coerceAtLeast(1L) * (if (it.done) 100f else it.percent)).toDouble() }.toFloat() / total
        }
}

/**
 * How the Downloads screen and Android Auto group what is kept: a book's files
 * become one book, in the order they were first downloaded. Pure, so it is unit
 * tested.
 */
object DownloadGroups {
    fun books(entries: List<DownloadEntry>): List<BookDownload> = entries
        .filter { it.meta.kind == DownloadMeta.BOOK_FILE }
        .groupBy { it.meta.bookId }
        .map { (id, files) -> BookDownload(id, files.first().meta.title, files.first().meta.subtitle, files) }

    fun episodes(entries: List<DownloadEntry>): List<DownloadEntry> = entries.filter { it.meta.kind == DownloadMeta.EPISODE }

    /** Whether every file of [bookId] is on the phone, so the book plays with no network. */
    fun bookDownloaded(entries: List<DownloadEntry>, bookId: Long): Boolean = books(entries).firstOrNull { it.bookId == bookId }?.done == true
}

package io.github.pete_builds.vcrx.mobile.offline

import androidx.media3.exoplayer.offline.Download
import io.github.pete_builds.vcrx.mobile.net.User
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Who may keep a book or an episode, the keys they are kept under, and how a book's files group. */
class AudioDownloadsTest {
    private fun user(kind: String, podcasts: Boolean = true) = User(id = 1, username = "a", kind = kind, podcasts = podcasts)

    @Test
    fun accountsMayKeepBooksAndEpisodes() {
        assertTrue(DownloadEligibility.canDownloadBook(3, user("user"), invitedDevice = false))
        assertTrue(DownloadEligibility.canDownloadBook(1, user("profile"), invitedDevice = false))
        assertTrue(DownloadEligibility.canDownloadEpisode("/api/v1/podcasts/episodes/4/stream", user("user"), invitedDevice = false))
        // A book with no files has nothing to fetch; an episode with no audio neither.
        assertFalse(DownloadEligibility.canDownloadBook(0, user("user"), invitedDevice = false))
        assertFalse(DownloadEligibility.canDownloadEpisode("", user("user"), invitedDevice = false))
    }

    @Test
    fun anInvitedDeviceKeepsNothing() {
        // The server refuses it the whole-file routes; it plays inside its own listen.
        assertFalse(DownloadEligibility.canDownloadBook(3, user("token"), invitedDevice = false))
        assertFalse(DownloadEligibility.canDownloadBook(3, user("user"), invitedDevice = true))
        assertFalse(DownloadEligibility.canDownloadEpisode("/s", user("token"), invitedDevice = false))
        assertFalse(DownloadEligibility.canDownloadEpisode("/s", user("user"), invitedDevice = true))
        // Podcasts off: no episode downloads either.
        assertFalse(DownloadEligibility.canDownloadEpisode("/s", user("user", podcasts = false), invitedDevice = false))
        assertFalse(DownloadEligibility.canDownloadBook(3, null, invitedDevice = false))
    }

    @Test
    fun keysNameTheContentAndMapBackToTheirRoute() {
        assertEquals("bookfile:9", DownloadKeys.bookFile(9))
        assertEquals("/api/v1/audiobooks/files/9/file", DownloadKeys.pathFor(DownloadKeys.bookFile(9)))
        assertEquals("episode:4", DownloadKeys.episode(4))
        // A full GET of the stream is the whole episode, fetched through the server.
        assertEquals("/api/v1/podcasts/episodes/4/stream", DownloadKeys.pathFor(DownloadKeys.episode(4)))
        assertTrue(DownloadKeys.isAudio(DownloadKeys.bookFile(9)))
        assertTrue(DownloadKeys.isAudio(DownloadKeys.episode(4)))
        assertEquals(null, DownloadKeys.pathFor("bookfile:x"))
    }

    private fun file(bookId: Long, fileId: Long, state: Int, percent: Float = 0f, durationMs: Long = 1_000) = DownloadEntry(
        id = DownloadKeys.bookFile(fileId),
        meta = DownloadMeta(kind = DownloadMeta.BOOK_FILE, bookId = bookId, fileId = fileId, title = "Book $bookId", subtitle = "A. Author", durationMs = durationMs),
        state = state,
        percent = percent,
        bytes = 100,
        total = 100,
        failure = 0,
    )

    @Test
    fun aBooksFilesAreOneDownload() {
        val ep = DownloadEntry("episode:4", DownloadMeta(kind = DownloadMeta.EPISODE, episodeId = 4, title = "E"), Download.STATE_COMPLETED, 100f, 1, 1, 0)
        val entries = listOf(
            file(1, 10, Download.STATE_COMPLETED),
            ep,
            file(2, 20, Download.STATE_COMPLETED),
            file(1, 11, Download.STATE_DOWNLOADING, percent = 50f, durationMs = 3_000),
        )
        val books = DownloadGroups.books(entries)
        assertEquals(listOf(1L, 2L), books.map { it.bookId })
        val one = books[0]
        assertFalse(one.done)
        assertEquals(1, one.filesDone)
        assertEquals("A. Author", one.author)
        // Weighted by length: a 1 s file done and half of a 3 s file is 62.5%.
        assertEquals(62.5f, one.percent, 0.01f)
        assertTrue(books[1].done)
        assertFalse(DownloadGroups.bookDownloaded(entries, 1))
        assertTrue(DownloadGroups.bookDownloaded(entries, 2))
        assertFalse(DownloadGroups.bookDownloaded(entries, 3))
        assertEquals(listOf(ep), DownloadGroups.episodes(entries))
    }
}

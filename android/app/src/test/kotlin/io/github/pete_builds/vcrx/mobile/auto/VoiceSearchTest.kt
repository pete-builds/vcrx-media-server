package io.github.pete_builds.vcrx.mobile.auto

import io.github.pete_builds.vcrx.mobile.net.AudiobookBook
import io.github.pete_builds.vcrx.mobile.net.Library
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.MusicArtist
import io.github.pete_builds.vcrx.mobile.net.Podcast
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import io.github.pete_builds.vcrx.mobile.net.User
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceSearchTest {
    private val all = Features(music = true, audiobooks = true, podcasts = true)
    private val albums = listOf(MusicAlbum(id = 1, title = "Night Drive", artistName = "Neon"), MusicAlbum(id = 2, title = "Harbor Lights", artistName = "Cedar Static"))
    private val artists = listOf(MusicArtist(id = 7, name = "Neon Lights"))
    private val books = listOf(
        AudiobookBook(id = 10, title = "The Long Harbor", author = "Mara Quill", series = "Tides"),
        AudiobookBook(id = 11, title = "Night Drive", author = "B. Writer"),
        AudiobookBook(id = 12, title = "Second Tide: A Novel", author = "Mara Quill", series = "Tides"),
    )
    private val shows = listOf(Podcast(id = 3, title = "Harbor Talk", author = "C. Host"))
    private val episodes = listOf(PodcastEpisode(id = 30, podcastId = 3, podcastTitle = "Harbor Talk", title = "Lighthouse keepers"))

    private fun music(q: String) = BrowseTree.search(q, albums, artists)

    private fun rank(q: String, f: Features = all) = VoiceSearch.rank(q, f, music(q), books, shows, episodes).map { it.id }

    @Test
    fun musicAloneIsUnchanged() {
        val musicOnly = Features(music = true)
        for (q in listOf("neon", "night drive", "harbor", "lights", "nothing here")) {
            assertEquals(q, music(q).map { it.id }, rank(q, musicOnly))
        }
    }

    @Test
    fun anAlbumThatMatchesAsWellStillWins() {
        // The album and a book share the title: music keeps the voice request.
        assertEquals("album:1", rank("night drive").first())
        assertTrue("book:11" in rank("night drive"))
    }

    @Test
    fun aBookOrShowThatMatchesBetterGoesFirst() {
        // "harbor talk" is a show's whole title; no album or artist has both words.
        assertEquals("show:3", rank("harbor talk").first())
        assertEquals("book:10", rank("the long harbor").first())
        // Punctuation is not spoken.
        assertEquals("book:12", rank("second tide a novel").first())
        // A title's start beats every-word matches.
        assertEquals("book:12", rank("second tide").first())
    }

    @Test
    fun booksMatchByAuthorAndSeriesToo() {
        val byAuthor = rank("mara quill")
        assertEquals(listOf("book:10", "book:12"), byAuthor.filter { it.startsWith("book:") })
        assertEquals(listOf("book:10", "book:12"), rank("tides").filter { it.startsWith("book:") })
        assertEquals("episode:30", rank("lighthouse").first())
    }

    @Test
    fun onlyWhatThisSessionMaySee() {
        val noPods = rank("harbor talk", all.copy(podcasts = false))
        assertTrue(noPods.none { it.startsWith("show:") || it.startsWith("episode:") })
        val noBooks = rank("the long harbor", all.copy(audiobooks = false))
        assertTrue(noBooks.none { it.startsWith("book:") })
        // An invited device: podcasts are never on, whatever /me says.
        val invited = Features.of(listOf(Library(1, "B", "audiobook")), User(id = 1, username = "d", kind = "token", podcasts = true), invitedDevice = true)
        assertTrue(rank("harbor talk", invited).none { it.startsWith("show:") || it.startsWith("episode:") })
        assertTrue("book:10" in rank("the long harbor", invited))
    }

    @Test
    fun matching() {
        assertEquals(VoiceSearch.TITLE, VoiceSearch.tier("Harbor Talk!", "harbor talk"))
        assertEquals(VoiceSearch.TITLE_START, VoiceSearch.tier("harbor", "Harbor Talk"))
        assertEquals(VoiceSearch.ALL_WORDS, VoiceSearch.tier("quill harbor", "The Long Harbor", "Mara Quill"))
        assertNull(VoiceSearch.tier("quill harbor", "The Long Harbor"))
        assertNull(VoiceSearch.tier("   ", "Anything"))
        assertTrue(VoiceSearch.rank("  ", all, emptyList(), books, shows, episodes).isEmpty())
    }

    @Test
    fun aShowPlaysItsNewestUnplayedEpisode() {
        val eps = listOf(
            PodcastEpisode(id = 1, title = "Old", publishedAt = 100),
            PodcastEpisode(id = 2, title = "Newest", publishedAt = 300, played = true),
            PodcastEpisode(id = 3, title = "Newer", publishedAt = 200),
        )
        assertEquals(3L, BrowseTree.showEpisodeToPlay(eps)?.id)
        assertEquals(2L, BrowseTree.showEpisodeToPlay(eps.map { it.copy(played = true) })?.id)
        assertNull(BrowseTree.showEpisodeToPlay(emptyList()))
        // A show node is browsable in the car and reads back as a show.
        assertEquals(BrowseId.Show(3), BrowseId.parse(BrowseTree.show(shows[0]).id))
    }
}

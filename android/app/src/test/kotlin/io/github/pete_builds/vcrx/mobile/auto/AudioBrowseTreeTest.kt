package io.github.pete_builds.vcrx.mobile.auto

import io.github.pete_builds.vcrx.mobile.net.AudiobookBook
import io.github.pete_builds.vcrx.mobile.net.AudiobookChapter
import io.github.pete_builds.vcrx.mobile.net.AudiobookProgress
import io.github.pete_builds.vcrx.mobile.net.Library
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.Podcast
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import io.github.pete_builds.vcrx.mobile.net.User
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Audiobooks and podcasts: which sessions see them, and the nodes Android Auto browses. */
class AudioBrowseTreeTest {
    private val music = Library(id = 1, name = "Music", kind = "music")
    private val books = Library(id = 2, name = "Books", kind = "audiobook")
    private val movies = Library(id = 3, name = "Movies", kind = "movie")
    private fun user(kind: String, podcasts: Boolean = true) = User(id = 1, username = "a", kind = kind, podcasts = podcasts)

    @Test
    fun featuresComeFromWhatTheServerReports() {
        val f = Features.of(listOf(music, books), user("user"), invitedDevice = false)
        assertTrue(f.music && f.audiobooks && f.podcasts)
        // A household profile is an account too.
        assertTrue(Features.of(listOf(books), user("profile"), invitedDevice = false).podcasts)
        // No audiobook library: no Audiobooks.
        assertFalse(Features.of(listOf(music, movies), user("user"), invitedDevice = false).audiobooks)
        // Podcasts off on the server.
        assertFalse(Features.of(listOf(music), user("user", podcasts = false), invitedDevice = false).podcasts)
    }

    @Test
    fun anOlderServerShowsNeither() {
        // No audiobook kind in /libraries and no podcasts in /me: both stay hidden.
        val old = Features.of(listOf(music, movies), User(id = 1, username = "a", kind = "user"), invitedDevice = false)
        assertFalse(old.audiobooks)
        assertFalse(old.podcasts)
        assertEquals(listOf(BrowseTree.CONTINUE, BrowseTree.MUSIC, BrowseTree.DOWNLOADS), BrowseTree.root(old).map { it.id })
    }

    @Test
    fun anInvitedDeviceNeverGetsPodcasts() {
        // Podcasts answer accounts only (403 accounts_only); hide them even if /me said yes.
        assertFalse(Features.of(listOf(books), user("token"), invitedDevice = false).podcasts)
        assertFalse(Features.of(listOf(books), user("user"), invitedDevice = true).podcasts)
        // Audiobooks still show: the device plays them inside its own listen.
        assertTrue(Features.of(listOf(books), user("token"), invitedDevice = true).audiobooks)
    }

    @Test
    fun fiveTabsFoldDownloadsIntoContinueListening() {
        val all = Features(music = true, audiobooks = true, podcasts = true, hasDownloads = true)
        val root = BrowseTree.root(all)
        assertEquals(listOf(BrowseTree.CONTINUE, BrowseTree.MUSIC, BrowseTree.AUDIOBOOKS, BrowseTree.PODCASTS), root.map { it.id })
        assertTrue(root.size <= BrowseTree.MAX_TABS)
        assertTrue(BrowseTree.downloadsInContinue(all))
        val cont = BrowseTree.continueListening(all, emptyList(), emptyList(), emptyList())
        assertEquals(listOf(BrowseTree.DOWNLOADS), cont.map { it.id })
        // With room, Downloads keeps its own tab.
        val two = Features(music = false, audiobooks = true, podcasts = true)
        assertEquals(listOf(BrowseTree.CONTINUE, BrowseTree.AUDIOBOOKS, BrowseTree.PODCASTS, BrowseTree.DOWNLOADS), BrowseTree.root(two).map { it.id })
        assertFalse(BrowseTree.downloadsInContinue(two))
    }

    @Test
    fun continueListeningPutsBooksThenEpisodesThenAlbums() {
        val f = Features(music = true, audiobooks = true, podcasts = false)
        val started = book(1, AudiobookProgress(positionS = 30.0))
        val finished = book(2, AudiobookProgress(positionS = 100.0, finished = true))
        val untouched = book(3, null)
        val ep = episode(7, position = 12.0)
        val nodes = BrowseTree.continueListening(f, listOf(started, finished, untouched), listOf(ep), listOf(MusicAlbum(id = 9, title = "LP")))
        // Podcasts are off for this session, so the episode is left out; with three tabs Downloads keeps its own.
        assertEquals(listOf("book:1", "album:9"), nodes.map { it.id })
        val withPods = BrowseTree.continueListening(f.copy(podcasts = true, music = false), listOf(started), listOf(ep, episode(8, played = true), episode(9)), emptyList())
        assertEquals(listOf("book:1", "episode:7"), withPods.map { it.id })
    }

    @Test
    fun booksStartedComeFirstAndPlayFromTheirPlace() {
        val a = book(1, null)
        val b = book(2, AudiobookProgress(positionS = 5.0))
        val nodes = BrowseTree.books(listOf(b), listOf(a, b))
        assertEquals(listOf("book:2", "book:1"), nodes.map { it.id })
        val n = nodes.first()
        assertTrue(n.playable && n.browsable)
        assertEquals(ArtRef(ArtRef.BOOK, 2), n.art)
        assertEquals(BrowseNode.TYPE_BOOK, n.type)
        assertNull(BrowseTree.book(a.copy(hasCover = false)).art)
    }

    @Test
    fun chaptersPlayTheBookFromTheirStart() {
        val chapters = listOf(AudiobookChapter(0, "Opening", 0.0, 60.0, 5), AudiobookChapter(1, "", 60.5, 120.0, 5))
        val nodes = BrowseTree.chapters(4, chapters, hasCover = true)
        assertEquals(listOf("book:4@0", "book:4@60500"), nodes.map { it.id })
        assertEquals("Chapter 2", nodes[1].title)
        assertEquals(BrowseId.Book(4, 60_500), BrowseId.parse(nodes[1].id))
        assertTrue(nodes.all { it.playable && !it.browsable })
    }

    @Test
    fun podcastsListNewEpisodesThenShows() {
        val nodes = BrowseTree.podcasts(listOf(Podcast(id = 3, title = "Harbor Talk", newCount = 2, hasArtwork = true), Podcast(id = 4, title = "Quiet", author = "M. Quill")))
        assertEquals(listOf(BrowseTree.NEW_EPISODES, "show:3", "show:4"), nodes.map { it.id })
        assertEquals("2 new", nodes[1].subtitle)
        assertEquals("M. Quill", nodes[2].subtitle)
        assertNull(nodes[2].art)
        assertTrue(nodes[1].browsable && !nodes[1].playable)
        // Played episodes go last, so a car still lists them for a replay.
        val eps = BrowseTree.episodes(listOf(episode(1, played = true), episode(2)), withShow = false)
        assertEquals(listOf("episode:2", "episode:1"), eps.map { it.id })
        assertNull(eps[0].subtitle)
        assertEquals(ArtRef(ArtRef.EPISODE, 2), eps[0].art)
    }

    @Test
    fun downloadsListBooksEpisodesAndTracks() {
        val nodes = BrowseTree.downloads(
            listOf(DownloadedBook(1, "A Book", "A. Author")),
            listOf(DownloadedEpisode(2, "Two", "Harbor Talk")),
            listOf(DownloadedTrack(3, "T", "", 9)),
        )
        assertEquals(listOf("download:book:1", "download:episode:2", "download:track:3"), nodes.map { it.id })
        assertTrue(nodes.all { it.playable })
    }

    @Test
    fun everyNewIdReadsBackAndHasItsKind() {
        val ids = mapOf(
            BrowseId.book(1) to BrowseId.Book(1),
            BrowseId.bookAt(1, 9000) to BrowseId.Book(1, 9000),
            BrowseId.bookFile(1, 22) to BrowseId.BookFile(1, 22),
            BrowseId.show(3) to BrowseId.Show(3),
            BrowseId.episode(4) to BrowseId.Episode(4),
            BrowseId.downloadedBook(5) to BrowseId.DownloadedBook(5),
            BrowseId.downloadedEpisode(6) to BrowseId.DownloadedEpisode(6),
            BrowseTree.NEW_EPISODES to BrowseId.Folder(BrowseTree.NEW_EPISODES),
        )
        ids.forEach { (id, parsed) -> assertEquals(id, parsed, BrowseId.parse(id)) }
        assertNull(BrowseId.parse("book:x"))
        assertNull(BrowseId.parse("book:1@"))
        assertEquals(AudioKind.Book, BrowseId.audioKind(BrowseId.bookFile(1, 2)))
        assertEquals(AudioKind.Book, BrowseId.audioKind(BrowseId.downloadedBook(1)))
        assertEquals(AudioKind.Episode, BrowseId.audioKind(BrowseId.episode(1)))
        assertEquals(AudioKind.Episode, BrowseId.audioKind(BrowseId.downloadedEpisode(1)))
        assertEquals(AudioKind.Music, BrowseId.audioKind(BrowseId.albumTrack(1, 2)))
        assertEquals(AudioKind.Music, BrowseId.audioKind("nonsense"))
        // Every node the audio tree makes can be read back.
        val all = Features(music = true, audiobooks = true, podcasts = true)
        val nodes = BrowseTree.root(all) + BrowseTree.podcasts(listOf(Podcast(id = 1))) + BrowseTree.books(emptyList(), listOf(book(1, null))) +
            BrowseTree.episodes(listOf(episode(2))) + BrowseTree.continueListening(all, emptyList(), emptyList(), emptyList())
        nodes.forEach { assertTrue(it.id, BrowseId.parse(it.id) != null) }
    }

    private fun book(id: Long, progress: AudiobookProgress?) =
        AudiobookBook(id = id, title = "Book $id", author = "A. Author", durationS = 100.0, hasCover = true, progress = progress)

    private fun episode(id: Long, position: Double = 0.0, played: Boolean = false) =
        PodcastEpisode(id = id, podcastId = 3, podcastTitle = "Harbor Talk", title = "Episode $id", positionSeconds = position, played = played)
}

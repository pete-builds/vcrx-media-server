package io.github.pete_builds.vcrx.mobile.auto

import io.github.pete_builds.vcrx.mobile.music.ListenSessions
import io.github.pete_builds.vcrx.mobile.net.AudiobookBook
import io.github.pete_builds.vcrx.mobile.net.AudiobookChapter
import io.github.pete_builds.vcrx.mobile.net.Library
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.MusicArtist
import io.github.pete_builds.vcrx.mobile.net.MusicTrack
import io.github.pete_builds.vcrx.mobile.net.Podcast
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import io.github.pete_builds.vcrx.mobile.net.User
import io.github.pete_builds.vcrx.mobile.net.inProgress
import io.github.pete_builds.vcrx.mobile.net.line

/** How Android Auto draws a list: rows, or a grid of square art. */
enum class Style { List, Grid }

/** What a node's artwork is; the service turns it into a content URI Auto can load. */
data class ArtRef(val kind: String, val id: Long) {
    companion object {
        const val ALBUM = "album"
        const val ARTIST = "artist"
        const val BOOK = "book"
        const val PODCAST = "podcast"
        const val EPISODE = "episode"
    }
}

/** One entry in the tree Android Auto browses. Pure data, so the tree is unit tested. */
data class BrowseNode(
    val id: String,
    val title: String,
    val subtitle: String? = null,
    val browsable: Boolean = false,
    val playable: Boolean = false,
    val art: ArtRef? = null,
    /** How this node's own children are drawn, for a browsable node. */
    val childStyle: Style = Style.List,
    /** "album", "artist", "track", "folder": the Media3 media type the service sets. */
    val type: String = TYPE_FOLDER,
) {
    companion object {
        const val TYPE_FOLDER = "folder"
        const val TYPE_ALBUM = "album"
        const val TYPE_ARTIST = "artist"
        const val TYPE_TRACK = "track"
        const val TYPE_BOOK = "book"
        const val TYPE_CHAPTER = "chapter"
        const val TYPE_PODCAST = "podcast"
        const val TYPE_EPISODE = "episode"
    }
}

/** What the server offers this session, which decides the root's tabs and the app's. */
data class Features(
    val music: Boolean,
    val audiobooks: Boolean = false,
    val podcasts: Boolean = false,
    val hasDownloads: Boolean = false,
) {
    companion object {
        const val KIND_MUSIC = "music"
        const val KIND_AUDIOBOOK = "audiobook"

        /**
         * Read from what the server says: a music or audiobook library in GET
         * /libraries, and podcasts from GET /me. An older server sends neither the
         * audiobook kind nor the podcasts flag, so both stay hidden there. Podcasts
         * are accounts only: an invited device never gets them, whatever /me says.
         */
        fun of(libraries: List<Library>, me: User?, invitedDevice: Boolean, hasDownloads: Boolean = true): Features = Features(
            music = libraries.any { it.kind == KIND_MUSIC },
            audiobooks = libraries.any { it.kind == KIND_AUDIOBOOK },
            podcasts = me?.podcasts == true && !ListenSessions.streamOnly(me, invitedDevice),
            hasDownloads = hasDownloads,
        )
    }
}

/** A downloaded track, as the Downloads node lists it. */
data class DownloadedTrack(val trackId: Long, val title: String, val artist: String, val albumId: Long)

/** A downloaded audiobook (every file of it), as the Downloads node lists it. */
data class DownloadedBook(val bookId: Long, val title: String, val author: String)

/** A downloaded podcast episode. */
data class DownloadedEpisode(val episodeId: Long, val title: String, val podcastTitle: String)

/**
 * The tree Android Auto browses. Audio only: Auto never plays video. Ids are
 * stable strings that [BrowseId] reads back when Auto asks for children or plays
 * an item. Pure: the service fetches the data and this decides the shape.
 */
object BrowseTree {
    const val ROOT = "root"
    const val CONTINUE = "continue"
    const val MUSIC = "music"
    const val RECENT_ALBUMS = "music/recent"
    const val ARTISTS = "music/artists"
    const val ALBUMS = "music/albums"
    const val DOWNLOADS = "downloads"
    const val AUDIOBOOKS = "audiobooks"
    const val PODCASTS = "podcasts"
    const val NEW_EPISODES = "podcasts/new"

    /** Auto draws at most this many root tabs; more land in an overflow it handles badly. */
    const val MAX_TABS = 4

    private val downloadsNode = BrowseNode(DOWNLOADS, "Downloads", browsable = true, childStyle = Style.List)

    /**
     * The root's tabs, in order: Continue listening, Music, Audiobooks, Podcasts,
     * Downloads. Downloads always shows, so a car with no signal still has
     * something to play; with all five Auto would overflow, so then Downloads
     * moves to the end of Continue listening ([continueListening]).
     */
    fun root(f: Features): List<BrowseNode> {
        val tabs = buildList {
            if (f.music || f.audiobooks || f.podcasts) add(BrowseNode(CONTINUE, "Continue listening", browsable = true, childStyle = Style.Grid))
            if (f.music) add(BrowseNode(MUSIC, "Music", browsable = true, childStyle = Style.List))
            if (f.audiobooks) add(BrowseNode(AUDIOBOOKS, "Audiobooks", browsable = true, childStyle = Style.Grid))
            if (f.podcasts) add(BrowseNode(PODCASTS, "Podcasts", browsable = true, childStyle = Style.Grid))
        }
        return if (tabs.size < MAX_TABS) tabs + downloadsNode else tabs
    }

    /** Whether Downloads is inside Continue listening rather than a tab of its own. */
    fun downloadsInContinue(f: Features): Boolean = root(f).none { it.id == DOWNLOADS }

    /**
     * Continue listening: books in progress first, then podcast episodes left part
     * way, then albums played lately; and Downloads at the end when the root has
     * no room for it.
     */
    fun continueListening(
        f: Features,
        books: List<AudiobookBook>,
        episodes: List<PodcastEpisode>,
        albums: List<MusicAlbum>,
    ): List<BrowseNode> = buildList {
        if (f.audiobooks) addAll(books.filter { it.progress.inProgress }.map { book(it) })
        if (f.podcasts) addAll(episodes.filter { !it.played && it.positionSeconds > 0 }.map { episode(it) })
        if (f.music) addAll(albums(albums))
        if (downloadsInContinue(f)) add(downloadsNode)
    }

    fun music(): List<BrowseNode> = listOf(
        BrowseNode(RECENT_ALBUMS, "Recently added", browsable = true, childStyle = Style.Grid),
        BrowseNode(ARTISTS, "Artists", browsable = true, childStyle = Style.List),
        BrowseNode(ALBUMS, "Albums", browsable = true, childStyle = Style.Grid),
    )

    fun album(a: MusicAlbum, withArtist: Boolean = true): BrowseNode = BrowseNode(
        id = BrowseId.album(a.id),
        title = a.title,
        subtitle = a.line(withArtist).ifEmpty { null },
        browsable = true,
        playable = true,
        art = if (a.hasCover) ArtRef(ArtRef.ALBUM, a.id) else null,
        childStyle = Style.List,
        type = BrowseNode.TYPE_ALBUM,
    )

    fun albums(list: List<MusicAlbum>, withArtist: Boolean = true): List<BrowseNode> = list.map { album(it, withArtist) }

    fun artists(list: List<MusicArtist>): List<BrowseNode> = list.map { a ->
        BrowseNode(
            id = BrowseId.artist(a.id),
            title = a.name,
            subtitle = if (a.albumCount == 1) "1 album" else "${a.albumCount} albums",
            browsable = true,
            art = if (a.hasImage) ArtRef(ArtRef.ARTIST, a.id) else null,
            childStyle = Style.Grid,
            type = BrowseNode.TYPE_ARTIST,
        )
    }

    /** An album's tracks; each plays the album from that track. */
    fun tracks(albumId: Long, list: List<MusicTrack>): List<BrowseNode> = list.map { t ->
        BrowseNode(
            id = BrowseId.albumTrack(albumId, t.id),
            title = t.title,
            subtitle = t.artistName.ifEmpty { null },
            playable = true,
            art = if (t.hasCover) ArtRef(ArtRef.ALBUM, t.albumId) else null,
            type = BrowseNode.TYPE_TRACK,
        )
    }

    fun downloads(list: List<DownloadedTrack>): List<BrowseNode> = list.map { t ->
        BrowseNode(
            id = BrowseId.downloadedTrack(t.trackId),
            title = t.title,
            subtitle = t.artist.ifEmpty { null },
            playable = true,
            art = ArtRef(ArtRef.ALBUM, t.albumId),
            type = BrowseNode.TYPE_TRACK,
        )
    }

    /** A book plays from where the listener left off; opened, it lists its chapters. */
    fun book(b: AudiobookBook): BrowseNode = BrowseNode(
        id = BrowseId.book(b.id),
        title = b.title,
        subtitle = b.author.ifEmpty { null },
        browsable = true,
        playable = true,
        art = if (b.hasCover) ArtRef(ArtRef.BOOK, b.id) else null,
        childStyle = Style.List,
        type = BrowseNode.TYPE_BOOK,
    )

    /** The Audiobooks tab: books in progress first, then every other book. */
    fun books(inProgress: List<AudiobookBook>, all: List<AudiobookBook>): List<BrowseNode> {
        val started = inProgress.filter { it.progress.inProgress }
        val ids = started.map { it.id }.toSet()
        return (started + all.filter { it.id !in ids }).map { book(it) }
    }

    /** A book's chapters; each plays the book from its start. */
    fun chapters(bookId: Long, list: List<AudiobookChapter>, hasCover: Boolean): List<BrowseNode> = list.map { c ->
        BrowseNode(
            id = BrowseId.bookAt(bookId, (c.startS * 1000).toLong()),
            title = c.title.ifEmpty { "Chapter ${c.index + 1}" },
            playable = true,
            art = if (hasCover) ArtRef(ArtRef.BOOK, bookId) else null,
            type = BrowseNode.TYPE_CHAPTER,
        )
    }

    /** The Podcasts tab: New episodes, then every show. */
    fun podcasts(shows: List<Podcast>): List<BrowseNode> = listOf(BrowseNode(NEW_EPISODES, "New episodes", browsable = true, childStyle = Style.List)) +
        shows.map { p ->
            BrowseNode(
                id = BrowseId.show(p.id),
                title = p.title,
                subtitle = when {
                    p.newCount > 0 -> "${p.newCount} new"
                    p.author.isNotEmpty() -> p.author
                    else -> null
                },
                browsable = true,
                art = if (p.hasArtwork) ArtRef(ArtRef.PODCAST, p.id) else null,
                childStyle = Style.List,
                type = BrowseNode.TYPE_PODCAST,
            )
        }

    fun episode(e: PodcastEpisode, withShow: Boolean = true): BrowseNode = BrowseNode(
        id = BrowseId.episode(e.id),
        title = e.title,
        subtitle = (if (withShow) e.podcastTitle else null)?.ifEmpty { null },
        playable = true,
        art = ArtRef(ArtRef.EPISODE, e.id),
        type = BrowseNode.TYPE_EPISODE,
    )

    /** A show's episodes, or New episodes; played ones stay, last, so a car can replay one. */
    fun episodes(list: List<PodcastEpisode>, withShow: Boolean = true): List<BrowseNode> =
        list.sortedBy { it.played }.map { episode(it, withShow) }

    /** Everything kept on the phone that plays in a car: books, then episodes, then tracks. */
    fun downloads(books: List<DownloadedBook>, episodes: List<DownloadedEpisode>, tracks: List<DownloadedTrack>): List<BrowseNode> =
        books.map { b ->
            BrowseNode(BrowseId.downloadedBook(b.bookId), b.title, b.author.ifEmpty { null }, playable = true, art = ArtRef(ArtRef.BOOK, b.bookId), type = BrowseNode.TYPE_BOOK)
        } + episodes.map { e ->
            BrowseNode(BrowseId.downloadedEpisode(e.episodeId), e.title, e.podcastTitle.ifEmpty { null }, playable = true, art = ArtRef(ArtRef.EPISODE, e.episodeId), type = BrowseNode.TYPE_EPISODE)
        } + downloads(tracks)

    /** Search answers: albums and artists whose names hold every word of [query], albums first. */
    fun search(query: String, albums: List<MusicAlbum>, artists: List<MusicArtist>, limit: Int = 30): List<BrowseNode> {
        val words = query.lowercase().split(' ').filter { it.isNotBlank() }
        if (words.isEmpty()) return emptyList()
        fun hit(text: String) = text.lowercase().let { t -> words.all { it in t } }
        val a = albums.filter { hit(it.title + " " + it.artistName) }.map { album(it) }
        val r = artists(artists.filter { hit(it.name) })
        return (a + r).take(limit)
    }
}

/** Reads back the ids [BrowseTree] makes. */
sealed interface BrowseId {
    data class Album(val albumId: Long) : BrowseId
    data class Artist(val artistId: Long) : BrowseId
    data class AlbumTrack(val albumId: Long, val trackId: Long) : BrowseId
    data class DownloadedTrack(val trackId: Long) : BrowseId

    /** A book, from where the listener left off, or from [atMs] into the whole book when set. */
    data class Book(val bookId: Long, val atMs: Long? = null) : BrowseId

    /** One file of a book in a play queue; asked to play again, the book plays from that file's start. */
    data class BookFile(val bookId: Long, val fileId: Long) : BrowseId
    data class Show(val podcastId: Long) : BrowseId
    data class Episode(val episodeId: Long) : BrowseId
    data class DownloadedBook(val bookId: Long) : BrowseId
    data class DownloadedEpisode(val episodeId: Long) : BrowseId
    data class Folder(val id: String) : BrowseId

    companion object {
        fun album(id: Long) = "album:$id"
        fun artist(id: Long) = "artist:$id"
        fun albumTrack(albumId: Long, trackId: Long) = "album:$albumId/track:$trackId"
        fun downloadedTrack(trackId: Long) = "download:track:$trackId"
        fun book(id: Long) = "book:$id"
        fun bookAt(id: Long, atMs: Long) = "book:$id@${atMs.coerceAtLeast(0)}"
        fun bookFile(bookId: Long, fileId: Long) = "book:$bookId/file:$fileId"
        fun show(id: Long) = "show:$id"
        fun episode(id: Long) = "episode:$id"
        fun downloadedBook(id: Long) = "download:book:$id"
        fun downloadedEpisode(id: Long) = "download:episode:$id"

        fun parse(id: String): BrowseId? {
            val track = Regex("^album:(\\d+)/track:(\\d+)$").find(id)
            if (track != null) return AlbumTrack(track.groupValues[1].toLong(), track.groupValues[2].toLong())
            Regex("^album:(\\d+)$").find(id)?.let { return Album(it.groupValues[1].toLong()) }
            Regex("^artist:(\\d+)$").find(id)?.let { return Artist(it.groupValues[1].toLong()) }
            Regex("^download:track:(\\d+)$").find(id)?.let { return DownloadedTrack(it.groupValues[1].toLong()) }
            Regex("^book:(\\d+)(?:@(\\d+))?$").find(id)?.let { return Book(it.groupValues[1].toLong(), it.groupValues[2].toLongOrNull()) }
            Regex("^book:(\\d+)/file:(\\d+)$").find(id)?.let { return BookFile(it.groupValues[1].toLong(), it.groupValues[2].toLong()) }
            Regex("^show:(\\d+)$").find(id)?.let { return Show(it.groupValues[1].toLong()) }
            Regex("^episode:(\\d+)$").find(id)?.let { return Episode(it.groupValues[1].toLong()) }
            Regex("^download:book:(\\d+)$").find(id)?.let { return DownloadedBook(it.groupValues[1].toLong()) }
            Regex("^download:episode:(\\d+)$").find(id)?.let { return DownloadedEpisode(it.groupValues[1].toLong()) }
            return if (id in FOLDERS) Folder(id) else null
        }

        /** What kind of audio a playable id is: music, a book or an episode. Decides speed, skips and saving the place. */
        fun audioKind(id: String): AudioKind = when (parse(id)) {
            is Book, is BookFile, is DownloadedBook -> AudioKind.Book
            is Episode, is DownloadedEpisode -> AudioKind.Episode
            else -> AudioKind.Music
        }

        private val FOLDERS = setOf(
            BrowseTree.ROOT, BrowseTree.CONTINUE, BrowseTree.MUSIC, BrowseTree.RECENT_ALBUMS, BrowseTree.ARTISTS,
            BrowseTree.ALBUMS, BrowseTree.DOWNLOADS, BrowseTree.AUDIOBOOKS, BrowseTree.PODCASTS, BrowseTree.NEW_EPISODES,
        )
    }
}

/** The three kinds of audio the one player plays. Music plays at 1x; books and episodes have their own speed and save their place. */
enum class AudioKind { Music, Book, Episode }

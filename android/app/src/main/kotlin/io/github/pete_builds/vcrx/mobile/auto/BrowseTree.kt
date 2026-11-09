package io.github.pete_builds.vcrx.mobile.auto

import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.MusicArtist
import io.github.pete_builds.vcrx.mobile.net.MusicTrack
import io.github.pete_builds.vcrx.mobile.net.line

/** How Android Auto draws a list: rows, or a grid of square art. */
enum class Style { List, Grid }

/** What a node's artwork is; the service turns it into a content URI Auto can load. */
data class ArtRef(val kind: String, val id: Long) {
    companion object {
        const val ALBUM = "album"
        const val ARTIST = "artist"
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
    }
}

/** What the server offers this session, which decides the root's tabs. */
data class Features(
    val music: Boolean,
    val audiobooks: Boolean = false,
    val podcasts: Boolean = false,
    val hasDownloads: Boolean = false,
)

/** A downloaded track, as the Downloads node lists it. */
data class DownloadedTrack(val trackId: Long, val title: String, val artist: String, val albumId: Long)

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

    /** Auto shows at most four tabs; extra ones land in an overflow it handles badly, so order matters. */
    fun root(f: Features): List<BrowseNode> = buildList {
        if (f.music) add(BrowseNode(CONTINUE, "Continue listening", browsable = true, childStyle = Style.Grid))
        if (f.music) add(BrowseNode(MUSIC, "Music", browsable = true, childStyle = Style.List))
        if (f.audiobooks) add(BrowseNode(AUDIOBOOKS, "Audiobooks", browsable = true, childStyle = Style.Grid))
        if (f.podcasts) add(BrowseNode(PODCASTS, "Podcasts", browsable = true, childStyle = Style.Grid))
        // Downloads always shows, so a car with no signal still has something to play.
        add(BrowseNode(DOWNLOADS, "Downloads", browsable = true, childStyle = Style.List))
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
    data class Folder(val id: String) : BrowseId

    companion object {
        fun album(id: Long) = "album:$id"
        fun artist(id: Long) = "artist:$id"
        fun albumTrack(albumId: Long, trackId: Long) = "album:$albumId/track:$trackId"
        fun downloadedTrack(trackId: Long) = "download:track:$trackId"

        fun parse(id: String): BrowseId? {
            val track = Regex("^album:(\\d+)/track:(\\d+)$").find(id)
            if (track != null) return AlbumTrack(track.groupValues[1].toLong(), track.groupValues[2].toLong())
            Regex("^album:(\\d+)$").find(id)?.let { return Album(it.groupValues[1].toLong()) }
            Regex("^artist:(\\d+)$").find(id)?.let { return Artist(it.groupValues[1].toLong()) }
            Regex("^download:track:(\\d+)$").find(id)?.let { return DownloadedTrack(it.groupValues[1].toLong()) }
            return if (id in FOLDERS) Folder(id) else null
        }

        private val FOLDERS = setOf(
            BrowseTree.ROOT, BrowseTree.CONTINUE, BrowseTree.MUSIC, BrowseTree.RECENT_ALBUMS, BrowseTree.ARTISTS,
            BrowseTree.ALBUMS, BrowseTree.DOWNLOADS, BrowseTree.AUDIOBOOKS, BrowseTree.PODCASTS,
        )
    }
}

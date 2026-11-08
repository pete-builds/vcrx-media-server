package io.github.pete_builds.vcrx.mobile.auto

import android.app.PendingIntent
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.LibraryResult
import androidx.media3.session.MediaLibraryService
import androidx.media3.session.MediaSession
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import io.github.pete_builds.vcrx.mobile.AppGraph
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.MusicQueueRequest
import io.github.pete_builds.vcrx.mobile.net.MusicTrack
import io.github.pete_builds.vcrx.mobile.net.PlaybackEvent
import io.github.pete_builds.vcrx.mobile.net.musicAlbum
import io.github.pete_builds.vcrx.mobile.net.musicAlbums
import io.github.pete_builds.vcrx.mobile.net.musicArtist
import io.github.pete_builds.vcrx.mobile.net.musicArtists
import io.github.pete_builds.vcrx.mobile.net.musicCapabilities
import io.github.pete_builds.vcrx.mobile.net.musicListen
import io.github.pete_builds.vcrx.mobile.net.musicQueue
import io.github.pete_builds.vcrx.mobile.offline.DownloadKeys
import io.github.pete_builds.vcrx.mobile.offline.DownloadMeta
import io.github.pete_builds.vcrx.mobile.ui.MainActivity
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.cancel
import kotlinx.coroutines.guava.future
import kotlinx.coroutines.launch

/**
 * The one audio player: music on the phone with notification and lock screen
 * controls, and the media app Android Auto browses. Auto sees only audio. Every
 * playable id is resolved here ([BrowseId]), so the phone's own screens and Auto
 * start playback the same way: by media id, through a MediaController.
 */
@OptIn(UnstableApi::class)
class PlaybackService : MediaLibraryService() {
    private val scope = MainScope()
    private var session: MediaLibrarySession? = null
    private lateinit var app: AppGraph
    private val searches = HashMap<String, List<BrowseNode>>()
    private var listen: Pair<String, Long>? = null

    override fun onCreate() {
        super.onCreate()
        app = graph
        val player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(app.downloads.dataSourceFactory))
            .setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(), true)
            .setHandleAudioBecomingNoisy(true)
            .setWakeMode(C.WAKE_MODE_NETWORK)
            .build()
        player.addListener(listens)
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        session = MediaLibrarySession.Builder(this, player, Callback()).setSessionActivity(open).build()
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaLibrarySession? = session

    override fun onTaskRemoved(rootIntent: Intent?) {
        val p = session?.player
        if (p == null || !p.playWhenReady || p.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        endListen()
        session?.run {
            player.release()
            release()
        }
        session = null
        scope.cancel()
        super.onDestroy()
    }

    // Each track that starts opens a listen in Activity, as the web and TV players do.
    private val listens = object : Player.Listener {
        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            endListen()
            val trackId = mediaItem?.mediaMetadata?.extras?.getLong(EXTRA_TRACK_ID) ?: return
            if (trackId == 0L || !app.online.value) return
            scope.launch {
                runCatching { app.api.musicListen(trackId, musicCapabilities(app.capabilities)) }.getOrNull()?.let {
                    listen = it.sessionId to trackId
                    app.events.send(it.sessionId, PlaybackEvent("start", 0))
                }
            }
        }
    }

    private fun endListen() {
        val (sessionId, _) = listen ?: return
        listen = null
        val position = session?.player?.currentPosition ?: 0
        app.events.send(sessionId, PlaybackEvent("stop", position.coerceAtLeast(0)))
    }

    private inner class Callback : MediaLibrarySession.Callback {
        override fun onGetLibraryRoot(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<MediaItem>> {
            val extras = Bundle().apply {
                putBoolean(AutoExtras.CONTENT_STYLE_SUPPORTED, true)
                putBoolean(AutoExtras.SEARCH_SUPPORTED, true)
                putInt(AutoExtras.BROWSABLE_HINT, AutoExtras.LIST)
                putInt(AutoExtras.PLAYABLE_HINT, AutoExtras.LIST)
            }
            val root = folder(BrowseTree.ROOT, "VCRX")
            return Futures.immediateFuture(LibraryResult.ofItem(root, LibraryParams.Builder().setExtras(extras).build()))
        }

        override fun onGetChildren(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            parentId: String,
            page: Int,
            pageSize: Int,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> = scope.future {
            val nodes = try {
                children(parentId)
            } catch (e: ApiException) {
                // Offline: the music nodes are empty and Downloads still plays.
                emptyList()
            }
            val all = nodes.map { toItem(it, browser.packageName) }
            val from = (page * pageSize).coerceAtMost(all.size)
            val to = if (pageSize <= 0) all.size else (from + pageSize).coerceAtMost(all.size)
            LibraryResult.ofItemList(ImmutableList.copyOf(all.subList(from, to)), params)
        }

        override fun onGetItem(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            mediaId: String,
        ): ListenableFuture<LibraryResult<MediaItem>> =
            Futures.immediateFuture(LibraryResult.ofItem(MediaItem.Builder().setMediaId(mediaId).build(), null))

        override fun onSearch(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            query: String,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<Void>> {
            scope.launch {
                val hits = runCatching { search(query) }.getOrDefault(emptyList())
                searches[query] = hits
                session.notifySearchResultChanged(browser, query, hits.size, params)
            }
            return Futures.immediateFuture(LibraryResult.ofVoid())
        }

        override fun onGetSearchResult(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            query: String,
            page: Int,
            pageSize: Int,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> {
            val hits = searches[query].orEmpty().map { toItem(it, browser.packageName) }
            return Futures.immediateFuture(LibraryResult.ofItemList(ImmutableList.copyOf(hits), params))
        }

        override fun onSetMediaItems(
            mediaSession: MediaSession,
            controller: MediaSession.ControllerInfo,
            mediaItems: MutableList<MediaItem>,
            startIndex: Int,
            startPositionMs: Long,
        ): ListenableFuture<MediaSession.MediaItemsWithStartPosition> = scope.future {
            val (items, start) = resolve(idsFor(mediaItems), startIndex)
            MediaSession.MediaItemsWithStartPosition(items, start.coerceAtLeast(0), if (start == startIndex) startPositionMs else C.TIME_UNSET)
        }

        override fun onAddMediaItems(
            mediaSession: MediaSession,
            controller: MediaSession.ControllerInfo,
            mediaItems: MutableList<MediaItem>,
        ): ListenableFuture<MutableList<MediaItem>> = scope.future {
            resolve(idsFor(mediaItems), 0).first.toMutableList()
        }
    }

    /**
     * The ids to play. A voice request ("play ... on VCRX") comes with no id and a
     * search query: the best album or artist match plays, and with no query (just
     * "play VCRX") the album played last does.
     */
    private suspend fun idsFor(items: List<MediaItem>): List<String> = items.mapNotNull { item ->
        if (item.mediaId.isNotEmpty()) return@mapNotNull item.mediaId
        val query = item.requestMetadata.searchQuery?.trim().orEmpty()
        if (query.isEmpty()) {
            app.prefs.recentAlbums().firstOrNull()?.let { BrowseId.album(it.id) }
        } else {
            runCatching { search(query) }.getOrDefault(emptyList()).firstOrNull()?.id
        }
    }

    private fun musicLibrary(): Long? = app.libraries.value.firstOrNull { it.kind == "music" }?.id

    private suspend fun children(parentId: String): List<BrowseNode> = when (val id = BrowseId.parse(parentId)) {
        is BrowseId.Folder -> when (id.id) {
            BrowseTree.ROOT -> BrowseTree.root(app.features())
            BrowseTree.CONTINUE -> BrowseTree.albums(app.prefs.recentAlbums())
            BrowseTree.MUSIC -> BrowseTree.music()
            BrowseTree.RECENT_ALBUMS -> musicLibrary()?.let { BrowseTree.albums(app.api.musicAlbums(it, "added", null, limit = 50).albums) }.orEmpty()
            BrowseTree.ALBUMS -> musicLibrary()?.let { BrowseTree.albums(app.api.musicAlbums(it, "title", null, limit = 500).albums) }.orEmpty()
            BrowseTree.ARTISTS -> musicLibrary()?.let { BrowseTree.artists(app.api.musicArtists(it, limit = 500).artists) }.orEmpty()
            BrowseTree.DOWNLOADS -> BrowseTree.downloads(downloadedTracks())
            // Audiobooks and podcasts land with their server routes; the nodes show once the server has the libraries.
            else -> emptyList()
        }
        is BrowseId.Album -> app.api.musicAlbum(id.albumId).let { BrowseTree.tracks(it.id, it.tracks) }
        is BrowseId.Artist -> app.api.musicArtist(id.artistId).let { BrowseTree.albums(it.albums + it.live, withArtist = false) }
        else -> emptyList()
    }

    private suspend fun search(query: String): List<BrowseNode> {
        val lib = musicLibrary() ?: return emptyList()
        return BrowseTree.search(query, app.api.musicAlbums(lib, "title", null, limit = 500).albums, app.api.musicArtists(lib, limit = 500).artists)
    }

    private fun downloadedTracks(): List<DownloadedTrack> = app.downloads.entries.value
        .filter { it.done && it.meta.kind == DownloadMeta.TRACK }
        .map { DownloadedTrack(it.meta.trackId, it.meta.title, it.meta.subtitle.substringBefore(" · "), it.meta.albumId) }

    /**
     * Turns the ids a controller asked to play into playable items: an album
     * (from a track), an artist (shuffled), or downloaded tracks, which play with
     * no network. Returns the items and where to start.
     */
    private suspend fun resolve(ids: List<String>, startIndex: Int): Pair<List<MediaItem>, Int> {
        val parsed = ids.mapNotNull { BrowseId.parse(it) }
        val first = parsed.getOrNull(startIndex) ?: parsed.firstOrNull() ?: return emptyList<MediaItem>() to 0
        return try {
            when {
                parsed.size > 1 && parsed.all { it is BrowseId.AlbumTrack } -> {
                    val trackIds = parsed.map { (it as BrowseId.AlbumTrack).trackId }
                    val tracks = queue(MusicQueueRequest(trackIds = trackIds, capabilities = musicCapabilities(app.capabilities)))
                    playable(tracks) to startIndex
                }
                first is BrowseId.Album || first is BrowseId.AlbumTrack -> {
                    val albumId = (first as? BrowseId.Album)?.albumId ?: (first as BrowseId.AlbumTrack).albumId
                    val tracks = queue(MusicQueueRequest(albumId = albumId, capabilities = musicCapabilities(app.capabilities)))
                    rememberAlbum(albumId)
                    val items = playable(tracks)
                    val at = (first as? BrowseId.AlbumTrack)?.let { t -> items.indexOfFirst { it.mediaMetadata.extras?.getLong(EXTRA_TRACK_ID) == t.trackId } } ?: 0
                    items to at.coerceAtLeast(0)
                }
                first is BrowseId.Artist -> {
                    playable(queue(MusicQueueRequest(artistId = first.artistId, shuffle = true, capabilities = musicCapabilities(app.capabilities)))) to 0
                }
                first is BrowseId.DownloadedTrack -> offlineQueue(first.trackId)
                else -> emptyList<MediaItem>() to 0
            }
        } catch (e: ApiException) {
            // No server: whatever of it is downloaded still plays.
            val trackId = when (first) {
                is BrowseId.AlbumTrack -> first.trackId
                is BrowseId.DownloadedTrack -> first.trackId
                else -> null
            }
            offlineQueue(trackId)
        }
    }

    private suspend fun queue(request: MusicQueueRequest): List<MusicTrack> = app.api.musicQueue(request)

    private suspend fun rememberAlbum(albumId: Long) {
        runCatching { app.api.musicAlbum(albumId) }.getOrNull()?.let {
            app.prefs.notePlayed(MusicAlbum(id = it.id, title = it.title, artistId = it.artistId, artistName = it.artistName, year = it.year, hasCover = it.hasCover))
        }
    }

    /** Every downloaded track, starting at [trackId]. */
    private fun offlineQueue(trackId: Long?): Pair<List<MediaItem>, Int> {
        val tracks = downloadedTracks()
        val items = tracks.map { t ->
            item(t.trackId, t.title, t.artist, "", t.albumId, DownloadKeys.trackPath(t.trackId), cacheKey = DownloadKeys.track(t.trackId))
        }
        return items to tracks.indexOfFirst { it.trackId == trackId }.coerceAtLeast(0)
    }

    private fun playable(tracks: List<MusicTrack>): List<MediaItem> = tracks.mapNotNull { t ->
        val key = DownloadKeys.track(t.id)
        when {
            app.downloads.isDownloaded(key) -> item(t.id, t.title, t.artistName, t.albumTitle, t.albumId, DownloadKeys.trackPath(t.id), cacheKey = key)
            t.stream != null -> item(t.id, t.title, t.artistName, t.albumTitle, t.albumId, t.stream.url, cacheKey = null)
            else -> null
        }
    }

    private fun item(trackId: Long, title: String, artist: String, album: String, albumId: Long, path: String, cacheKey: String?): MediaItem {
        // Offline with no server address left there is no URL to name; the cache key alone finds the file.
        val url = app.api.absoluteUrl(path) ?: "http://offline.invalid$path"
        val art = ArtworkProvider.uri(this, ArtRef(ArtRef.ALBUM, albumId))
        return MediaItem.Builder()
            .setMediaId(BrowseId.albumTrack(albumId, trackId))
            .setUri(Uri.parse(url))
            .apply { if (cacheKey != null) setCustomCacheKey(cacheKey) }
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(title)
                    .setArtist(artist)
                    .setAlbumTitle(album)
                    .setArtworkUri(art)
                    .setIsBrowsable(false)
                    .setIsPlayable(true)
                    .setMediaType(MediaMetadata.MEDIA_TYPE_MUSIC)
                    .setExtras(Bundle().apply { putLong(EXTRA_TRACK_ID, trackId) })
                    .build(),
            )
            .build()
    }

    private fun folder(id: String, title: String): MediaItem = MediaItem.Builder()
        .setMediaId(id)
        .setMediaMetadata(
            MediaMetadata.Builder().setTitle(title).setIsBrowsable(true).setIsPlayable(false)
                .setMediaType(MediaMetadata.MEDIA_TYPE_FOLDER_MIXED).build(),
        )
        .build()

    private fun toItem(node: BrowseNode, browserPackage: String): MediaItem {
        val art = node.art?.let { ArtworkProvider.uri(this, it) }
        if (art != null) runCatching { grantUriPermission(browserPackage, art, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
        val style = if (node.childStyle == Style.Grid) AutoExtras.GRID else AutoExtras.LIST
        val type = when (node.type) {
            BrowseNode.TYPE_ALBUM -> MediaMetadata.MEDIA_TYPE_ALBUM
            BrowseNode.TYPE_ARTIST -> MediaMetadata.MEDIA_TYPE_ARTIST
            BrowseNode.TYPE_TRACK -> MediaMetadata.MEDIA_TYPE_MUSIC
            else -> MediaMetadata.MEDIA_TYPE_FOLDER_MIXED
        }
        return MediaItem.Builder()
            .setMediaId(node.id)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(node.title)
                    .setSubtitle(node.subtitle)
                    .setArtist(node.subtitle)
                    .setIsBrowsable(node.browsable)
                    .setIsPlayable(node.playable)
                    .setMediaType(type)
                    .setArtworkUri(art)
                    .setExtras(
                        Bundle().apply {
                            putInt(AutoExtras.BROWSABLE_HINT, style)
                            putInt(AutoExtras.PLAYABLE_HINT, style)
                        },
                    )
                    .build(),
            )
            .build()
    }

    companion object {
        const val EXTRA_TRACK_ID = "vcrx.track_id"
    }
}

/** Android Auto's content style keys, as its media documentation names them. */
object AutoExtras {
    const val CONTENT_STYLE_SUPPORTED = "android.media.browse.CONTENT_STYLE_SUPPORTED"
    const val SEARCH_SUPPORTED = "android.media.browse.SEARCH_SUPPORTED"
    const val BROWSABLE_HINT = "android.media.browse.CONTENT_STYLE_BROWSABLE_HINT"
    const val PLAYABLE_HINT = "android.media.browse.CONTENT_STYLE_PLAYABLE_HINT"
    const val LIST = 1
    const val GRID = 2
}

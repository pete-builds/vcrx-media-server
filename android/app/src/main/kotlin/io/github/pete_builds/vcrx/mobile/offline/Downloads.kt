package io.github.pete_builds.vcrx.mobile.offline

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.database.StandaloneDatabaseProvider
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.cache.CacheDataSource
import androidx.media3.datasource.cache.NoOpCacheEvictor
import androidx.media3.datasource.cache.SimpleCache
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.offline.Download
import androidx.media3.exoplayer.offline.DownloadManager
import androidx.media3.exoplayer.offline.DownloadRequest
import androidx.media3.exoplayer.offline.DownloadService
import androidx.media3.exoplayer.scheduler.Requirements
import io.github.pete_builds.vcrx.mobile.AppGraph
import io.github.pete_builds.vcrx.mobile.net.ApiJson
import io.github.pete_builds.vcrx.mobile.net.Item
import io.github.pete_builds.vcrx.mobile.net.MusicAlbumDetail
import java.io.File
import java.util.concurrent.Executors
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** What a download is, kept in the download request's data so the Downloads screen works offline. */
@Serializable
data class DownloadMeta(
    /** "video" or "track". */
    val kind: String,
    @SerialName("item_id") val itemId: Long = 0,
    @SerialName("version_id") val versionId: Long = 0,
    @SerialName("track_id") val trackId: Long = 0,
    @SerialName("album_id") val albumId: Long = 0,
    val title: String,
    val subtitle: String = "",
    @SerialName("duration_ms") val durationMs: Long? = null,
    @SerialName("resume_ms") val resumeMs: Long? = null,
) {
    companion object {
        const val VIDEO = "video"
        const val TRACK = "track"
    }
}

/** One row of the Downloads screen. */
@OptIn(UnstableApi::class)
data class DownloadEntry(
    val id: String,
    val meta: DownloadMeta,
    val state: Int,
    val percent: Float,
    val bytes: Long,
    val total: Long,
    val failure: Int,
) {
    val done: Boolean get() = state == Download.STATE_COMPLETED
}

/**
 * Whole files kept on the phone, through Media3's DownloadManager into a cache
 * in the app's private storage. Each download is keyed by what it is
 * ([DownloadKeys]), and every player reads through [dataSourceFactory], so a
 * downloaded title plays from the phone with no network at all.
 */
@OptIn(UnstableApi::class)
class Downloads(private val context: Context, private val graph: AppGraph) {
    private val database = StandaloneDatabaseProvider(context)
    val cache = SimpleCache(File(context.filesDir, "downloads"), NoOpCacheEvictor(), database)
    private val upstream = OkHttpDataSource.Factory(graph.httpClient)

    val manager: DownloadManager = DownloadManager(context, database, cache, upstream, Executors.newFixedThreadPool(2)).apply {
        maxParallelDownloads = 2
        requirements = requirementsFor(graph.prefs.wifiOnly)
    }

    /**
     * For every player: a downloaded key is read from the phone, anything else
     * from the server. Streams are never written into the download cache.
     */
    val dataSourceFactory: DataSource.Factory = readingFactory { it }

    /**
     * [dataSourceFactory] with [network] wrapped around the server side only, so a
     * downloaded file is read from the phone without it. The audio player uses this
     * to ask for tracks inside their listen on an invited device.
     */
    fun readingFactory(network: (DataSource.Factory) -> DataSource.Factory): DataSource.Factory = CacheDataSource.Factory()
        .setCache(cache)
        .setUpstreamDataSourceFactory(network(DefaultDataSource.Factory(context, upstream)))
        .setCacheWriteDataSinkFactory(null)

    private val state = MutableStateFlow<List<DownloadEntry>>(emptyList())
    val entries: StateFlow<List<DownloadEntry>> = state.asStateFlow()

    private val handler = Handler(Looper.getMainLooper())

    // The index only changes on a state change, so while anything downloads the
    // entries are re-read every second for live progress.
    private val ticker = object : Runnable {
        override fun run() {
            refresh()
            if (manager.currentDownloads.any { it.state == Download.STATE_DOWNLOADING }) handler.postDelayed(this, 1_000)
        }
    }

    init {
        manager.addListener(
            object : DownloadManager.Listener {
                override fun onInitialized(downloadManager: DownloadManager) = refresh()

                override fun onDownloadChanged(downloadManager: DownloadManager, download: Download, finalException: Exception?) {
                    refresh()
                    handler.removeCallbacks(ticker)
                    if (download.state == Download.STATE_DOWNLOADING) handler.postDelayed(ticker, 1_000)
                }

                override fun onDownloadRemoved(downloadManager: DownloadManager, download: Download) = refresh()
            },
        )
        refresh()
    }

    /** Re-reads the index, with live progress for those running. The screen calls it while it shows. */
    fun refresh() {
        val live = manager.currentDownloads.associateBy { it.request.id }
        val out = mutableListOf<DownloadEntry>()
        runCatching {
            manager.downloadIndex.getDownloads().use { cursor ->
                while (cursor.moveToNext()) {
                    val d = live[cursor.download.request.id] ?: cursor.download
                    entry(d)?.let(out::add)
                }
            }
        }
        state.value = out
    }

    private fun entry(d: Download): DownloadEntry? {
        val meta = runCatching { ApiJson.decodeFromString(DownloadMeta.serializer(), String(d.request.data, Charsets.UTF_8)) }.getOrNull() ?: return null
        return DownloadEntry(
            id = d.request.id,
            meta = meta,
            state = d.state,
            percent = d.percentDownloaded.coerceAtLeast(0f),
            bytes = d.bytesDownloaded,
            total = d.contentLength,
            failure = d.failureReason,
        )
    }

    fun find(key: String): DownloadEntry? = entries.value.firstOrNull { it.id == key }

    fun isDownloaded(key: String): Boolean = find(key)?.done == true

    /** Keeps a movie or an episode: the first version's whole file, and its poster for the offline list. */
    fun addVideo(item: Item) {
        val versionId = DownloadEligibility.versionToKeep(item) ?: return
        val subtitle = listOfNotNull(
            item.showTitle,
            if (item.seasonNumber != null && item.episodeNumber != null) "S${item.seasonNumber} E${item.episodeNumber}" else item.year?.toString(),
        ).joinToString(" · ")
        val meta = DownloadMeta(
            kind = DownloadMeta.VIDEO,
            itemId = item.id,
            versionId = versionId,
            title = item.title,
            subtitle = subtitle,
            durationMs = item.versions.first().durationMs ?: item.runtimeMs,
            resumeMs = item.watch?.positionMs,
        )
        add(DownloadKeys.version(versionId), DownloadKeys.versionPath(versionId), meta)
        saveArt(ArtStore.ITEM, item.id, graph.api.artworkUrl(item.id, if (item.kind == "episode") "thumb" else "poster", 480))
    }

    /** Keeps every track of an album, and its cover. */
    fun addAlbum(album: MusicAlbumDetail) {
        album.tracks.forEach { t ->
            val meta = DownloadMeta(
                kind = DownloadMeta.TRACK,
                trackId = t.id,
                albumId = album.id,
                title = t.title,
                subtitle = listOf(t.artistName.ifEmpty { album.artistName }, album.title).filter { it.isNotEmpty() }.joinToString(" · "),
                durationMs = t.durationMs,
            )
            add(DownloadKeys.track(t.id), DownloadKeys.trackPath(t.id), meta)
        }
        saveArt(ArtStore.ALBUM, album.id, graph.api.absoluteUrl("/api/v1/music/albums/${album.id}/cover?width=480"))
    }

    private fun add(key: String, path: String, meta: DownloadMeta) {
        val url = graph.api.absoluteUrl(path) ?: return
        val request = DownloadRequest.Builder(key, Uri.parse(url))
            .setCustomCacheKey(key)
            .setData(ApiJson.encodeToString(DownloadMeta.serializer(), meta).toByteArray(Charsets.UTF_8))
            .build()
        DownloadService.sendAddDownload(context, VcrxDownloadService::class.java, request, false)
    }

    fun remove(key: String) {
        DownloadService.sendRemoveDownload(context, VcrxDownloadService::class.java, key, false)
    }

    fun setWifiOnly(on: Boolean) {
        graph.prefs.wifiOnly = on
        DownloadService.sendSetRequirements(context, VcrxDownloadService::class.java, requirementsFor(on), false)
    }

    /** Bytes the downloads take on the phone. */
    fun usedBytes(): Long = runCatching { cache.cacheSpace }.getOrDefault(0L)

    fun freeBytes(): Long = context.filesDir.usableSpace

    private fun saveArt(kind: String, id: Long, url: String?) {
        url ?: return
        graph.appScope.launch(Dispatchers.IO) { ArtStore.fetch(context, graph.httpClient, kind, id, url, keep = true) }
    }

    companion object {
        fun requirementsFor(wifiOnly: Boolean) = Requirements(if (wifiOnly) Requirements.NETWORK_UNMETERED else Requirements.NETWORK)
    }
}

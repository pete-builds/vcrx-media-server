package io.github.pete_builds.vcrx.tv.music

import android.annotation.SuppressLint
import android.content.Context
import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.extractor.DefaultExtractorsFactory
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.caps.CapabilityProbe
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.MusicQueueRequest
import io.github.pete_builds.vcrx.tv.net.MusicTrack
import io.github.pete_builds.vcrx.tv.net.PlaybackEvent
import io.github.pete_builds.vcrx.tv.net.albumCoverUrl
import io.github.pete_builds.vcrx.tv.net.musicCapabilities
import io.github.pete_builds.vcrx.tv.net.musicListen
import io.github.pete_builds.vcrx.tv.net.musicQueue
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** What the Now Playing screen and the music pages draw. */
data class MusicState(
    /** The queue in play order. */
    val queue: List<MusicTrack> = emptyList(),
    val position: Int = -1,
    val playing: Boolean = false,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    val shuffle: Boolean = false,
    val repeat: Repeat = Repeat.Off,
    /** A problem to show once, such as tracks this device cannot play. */
    val message: String? = null,
) {
    val current: MusicTrack? get() = queue.getOrNull(position)
}

/**
 * The app's one music player. It outlives the screens, so music keeps playing
 * between the album, the artist and Now Playing; it does not play in the
 * background once the app leaves the screen (a TV does not need that).
 *
 * The queue's rules live in [MusicQueue]; Media3 holds the same queue as a
 * playlist in play order, which gives gapless playback between tracks for
 * free (it prepares the next item while the current one plays). Each track
 * that starts opens a listen in Activity, as the web player does.
 */
@OptIn(UnstableApi::class)
class MusicPlayer private constructor(private val context: Context, private val graph: AppGraph) {
    private val scope = MainScope()
    private val _state = MutableStateFlow(MusicState())
    val state: StateFlow<MusicState> = _state.asStateFlow()

    private var queue: MusicQueue<MusicTrack>? = null
    private var player: ExoPlayer? = null
    private var caps: Capabilities? = null
    private var listenSession: String? = null
    private var listenJob: Job? = null
    private var ticker: Job? = null

    suspend fun playAlbum(albumId: Long, startTrackId: Long? = null, shuffle: Boolean = false) {
        val tracks = fetch(MusicQueueRequest(albumId = albumId, capabilities = capabilities()))
        start(tracks, tracks.indexOfFirst { it.id == startTrackId }.coerceAtLeast(0), shuffle)
    }

    suspend fun playArtist(artistId: Long) {
        start(fetch(MusicQueueRequest(artistId = artistId, shuffle = true, capabilities = capabilities())), 0, false)
    }

    private suspend fun capabilities(): Capabilities =
        caps ?: withContext(Dispatchers.Default) { musicCapabilities(CapabilityProbe.probe(context)) }.also { caps = it }

    private suspend fun fetch(request: MusicQueueRequest): List<MusicTrack> = graph.api.musicQueue(request)

    private fun start(all: List<MusicTrack>, startIndex: Int, shuffle: Boolean) {
        // Tracks this device cannot play, and the server will not convert for
        // this account, stay out of the queue; the screen says how many.
        val playable = all.filter { it.stream != null }
        val skipped = all.size - playable.size
        if (playable.isEmpty()) {
            publish(message = all.firstOrNull()?.refusal?.message ?: "Nothing here can play on this TV.")
            return
        }
        val wanted = all.getOrNull(startIndex)
        val start = playable.indexOf(wanted).coerceAtLeast(0)
        val q = MusicQueue(playable, start, shuffle).also { it.repeat = queue?.repeat ?: Repeat.Off }
        queue = q
        load(0L)
        publish(message = if (skipped > 0) "$skipped tracks cannot play on this TV and were left out." else null)
    }

    private fun ensurePlayer(): ExoPlayer = player ?: run {
        val http = OkHttpDataSource.Factory(graph.httpClient)
        val renderers = DefaultRenderersFactory(context)
            .setExtensionRendererMode(DefaultRenderersFactory.EXTENSION_RENDERER_MODE_ON)
            .setEnableDecoderFallback(true)
        ExoPlayer.Builder(context, renderers)
            .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(context, http), DefaultExtractorsFactory()))
            .setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(), true)
            .setHandleAudioBecomingNoisy(true)
            .build()
            .also {
                it.addListener(listener)
                player = it
            }
    }

    /** Hands the queue to Media3 in play order, at the queue's position. */
    private fun load(positionMs: Long) {
        val q = queue ?: return
        val p = ensurePlayer()
        val items = q.playOrder.map { t ->
            MediaItem.Builder()
                .setMediaId(t.id.toString())
                .setUri(graph.api.absoluteUrl(t.stream?.url.orEmpty()).orEmpty())
                .setMediaMetadata(
                    MediaMetadata.Builder()
                        .setTitle(t.title)
                        .setArtist(t.artistName)
                        .setAlbumTitle(t.albumTitle)
                        .apply { if (t.hasCover) graph.api.albumCoverUrl(t.albumId, 480)?.let { setArtworkUri(Uri.parse(it)) } }
                        .build(),
                )
                .build()
        }
        p.setMediaItems(items, q.position, positionMs)
        p.repeatMode = repeatMode(q.repeat)
        p.prepare()
        p.play()
        // The listen opens in onMediaItemTransition, which this playlist change fires.
        startTicker()
    }

    private fun repeatMode(r: Repeat): Int = when (r) {
        Repeat.Off -> Player.REPEAT_MODE_OFF
        Repeat.All -> Player.REPEAT_MODE_ALL
        Repeat.One -> Player.REPEAT_MODE_ONE
    }

    fun toggle() {
        val p = player ?: return
        if (p.isPlaying) p.pause() else p.play()
    }

    fun next() {
        val q = queue ?: return
        if (q.next()) player?.seekTo(q.position, 0)
    }

    fun previous() {
        val q = queue ?: return
        val p = player ?: return
        when (q.previous(p.currentPosition)) {
            MusicQueue.Back.Restart -> p.seekTo(0)
            MusicQueue.Back.Previous -> p.seekTo(q.position, 0)
        }
    }

    fun jumpTo(orderPosition: Int) {
        val q = queue ?: return
        if (q.jumpTo(orderPosition)) player?.seekTo(q.position, 0)
    }

    /** Seeks within the track; converted streams cannot. */
    fun seekBy(deltaMs: Long) {
        val p = player ?: return
        if (_state.value.current?.stream?.seekable != true) return
        val end = p.duration.takeIf { it > 0 }?.let { (it - 500).coerceAtLeast(0L) } ?: Long.MAX_VALUE
        p.seekTo((p.currentPosition + deltaMs).coerceIn(0L, end))
        report("seek")
    }

    fun setShuffle(on: Boolean) {
        val q = queue ?: return
        q.setShuffle(on)
        // Media3's playlist follows the new order; the current track carries on.
        load(player?.currentPosition ?: 0L)
        publish()
    }

    fun cycleRepeat() {
        val q = queue ?: return
        player?.repeatMode = repeatMode(q.cycleRepeat())
        publish()
    }

    /** Stops the music and empties the queue. */
    fun stop() {
        endListen()
        ticker?.cancel()
        player?.run {
            removeListener(listener)
            release()
        }
        player = null
        queue = null
        _state.value = MusicState()
    }

    fun clearMessage() {
        _state.value = _state.value.copy(message = null)
    }

    private val listener = object : Player.Listener {
        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            val q = queue ?: return
            val p = player ?: return
            // Media3 moved on by itself (the end of a track, or repeat): the
            // queue follows its index, and the next track is a new listen.
            if (p.currentMediaItemIndex != q.position) q.jumpTo(p.currentMediaItemIndex)
            beginListen()
            publish()
        }

        override fun onIsPlayingChanged(isPlaying: Boolean) {
            report(if (isPlaying) "resume" else "pause")
            publish()
        }

        override fun onPlaybackStateChanged(playbackState: Int) {
            if (playbackState == Player.STATE_ENDED) endListen()
            publish()
        }

        override fun onPlayerError(error: PlaybackException) {
            publish(message = "This track could not play: ${error.errorCodeName}.")
            val q = queue ?: return
            if (q.next()) {
                player?.seekTo(q.position, 0)
                player?.prepare()
            }
        }
    }

    private fun beginListen() {
        endListen()
        val track = queue?.current ?: return
        listenJob = scope.launch {
            val session = try {
                graph.api.musicListen(track.id, capabilities())
            } catch (e: ApiException) {
                publish(message = e.message)
                return@launch
            }
            listenSession = session.sessionId
            report("start")
            val beat = session.heartbeatIntervalMs.coerceAtLeast(5_000)
            while (isActive) {
                delay(beat)
                if (player?.isPlaying == true) report("progress")
            }
        }
    }

    private fun endListen() {
        listenJob?.cancel()
        listenJob = null
        val id = listenSession ?: return
        listenSession = null
        graph.playbackEvents.send(id, PlaybackEvent(type = "stop", positionMs = player?.currentPosition ?: 0, durationMs = duration()))
    }

    /** Reports an event; a 410 means an admin stopped this listen from Activity. */
    private fun report(type: String) {
        val id = listenSession ?: return
        val event = PlaybackEvent(type = type, positionMs = player?.currentPosition ?: 0, durationMs = duration())
        scope.launch {
            try {
                graph.api.postEvent(id, event)
            } catch (e: ApiException) {
                if (e is ApiException.Http && e.code == "session_terminated" && listenSession == id) {
                    listenSession = null
                    stop()
                    publish(message = "The music was stopped on the server: ${e.message}")
                }
            }
        }
    }

    private fun duration(): Long? = player?.duration?.takeIf { it > 0 } ?: queue?.current?.durationMs

    private fun startTicker() {
        if (ticker?.isActive == true) return
        ticker = scope.launch {
            while (isActive) {
                publish()
                delay(500)
            }
        }
    }

    private fun publish(message: String? = _state.value.message) {
        val q = queue
        val p = player
        _state.value = MusicState(
            queue = q?.playOrder.orEmpty(),
            position = q?.position ?: -1,
            playing = p?.isPlaying == true,
            positionMs = p?.currentPosition ?: 0,
            durationMs = duration() ?: 0,
            shuffle = q?.shuffle == true,
            repeat = q?.repeat ?: Repeat.Off,
            message = message,
        )
    }

    companion object {
        // Holds the application context only, never an activity's.
        @SuppressLint("StaticFieldLeak")
        @Volatile
        private var instance: MusicPlayer? = null

        fun get(graph: AppGraph, context: Context): MusicPlayer =
            instance ?: synchronized(this) {
                instance ?: MusicPlayer(context.applicationContext, graph).also { instance = it }
            }
    }
}

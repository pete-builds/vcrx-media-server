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
import androidx.media3.common.PlaybackParameters
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
import io.github.pete_builds.vcrx.tv.net.AudiobookFile
import io.github.pete_builds.vcrx.tv.net.AudiobookQueue
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.PlaybackEvent
import io.github.pete_builds.vcrx.tv.net.audiobookCoverUrl
import io.github.pete_builds.vcrx.tv.net.audiobookListen
import io.github.pete_builds.vcrx.tv.net.audiobookQueue
import io.github.pete_builds.vcrx.tv.net.musicCapabilities
import io.github.pete_builds.vcrx.tv.net.saveAudiobookProgress
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

/** What the book player screen draws. */
data class AudiobookState(
    val book: AudiobookQueue? = null,
    val playing: Boolean = false,
    /** Seconds from the start of the whole book. */
    val positionS: Double = 0.0,
    val chapter: Int = -1,
    val speed: Float = 1f,
    /** A problem to show once, such as a file this TV cannot play. */
    val message: String? = null,
) {
    val chapterTitle: String? get() = book?.chapters?.getOrNull(chapter)?.title
}

/**
 * The app's one book player, built like [MusicPlayer]: it outlives the
 * screens, stops when the app leaves the screen or the session changes, and
 * holds the book's files as one Media3 playlist so a file runs into the next.
 * A position is seconds across the whole book ([BookPosition]); it is saved
 * to the server every [SAVE_MS] while playing, on every pause, and on stop.
 * Starting a book stops the music, and starting music stops the book.
 */
@OptIn(UnstableApi::class)
class AudiobookPlayer private constructor(private val context: Context, private val graph: AppGraph) {
    private val scope = MainScope()
    private val _state = MutableStateFlow(AudiobookState())
    val state: StateFlow<AudiobookState> = _state.asStateFlow()

    private var book: AudiobookQueue? = null
    private var files: List<AudiobookFile> = emptyList()
    private var player: ExoPlayer? = null
    private var caps: Capabilities? = null
    private var speed = 1f
    // A converted file started part way through: its stream begins at this
    // offset, which currentPosition does not include.
    private var convertedOffsetMs = 0L
    private var listenSession: String? = null
    private var listenFile: Long? = null
    private var listenJob: Job? = null
    private val listens = ListenSessions(Regex("/api/v1/audiobooks/files/(\\d+)/")) { id ->
        graph.api.audiobookListen(id, capabilities()).let { OpenListen(it.sessionId, it.heartbeatIntervalMs) }
    }
    private var ticker: Job? = null
    private var lastSave = 0L

    /** Plays a book from where the listener left off, from [atS] when given, or from the start. */
    suspend fun play(bookId: Long, atS: Double? = null, fromStart: Boolean = false) {
        MusicPlayer.stopIfRunning()
        listens.enabled = streamOnly(graph)
        val q = graph.api.audiobookQueue(bookId, capabilities())
        val playable = q.files.filter { it.stream != null }
        if (playable.size != q.files.size || playable.isEmpty()) {
            publish(message = q.files.firstOrNull { it.refusal != null }?.refusal?.message ?: "This book cannot play on this TV.")
            return
        }
        saveNow()
        endListen()
        closeUnplayed(listens) { id -> graph.playbackEvents.send(id, PlaybackEvent(type = "stop", positionMs = 0)) }
        book = q
        files = q.files
        var pos = atS ?: if (fromStart || q.progress.finished) 0.0 else q.progress.positionS
        if (pos >= q.durationS - 1) pos = 0.0
        load(pos)
    }

    private suspend fun capabilities(): Capabilities =
        caps ?: withContext(Dispatchers.Default) { musicCapabilities(CapabilityProbe.probe(context)) }.also { caps = it }

    private fun ensurePlayer(): ExoPlayer = player ?: run {
        val http = OkHttpDataSource.Factory(graph.httpClient)
        val renderers = DefaultRenderersFactory(context)
            .setExtensionRendererMode(DefaultRenderersFactory.EXTENSION_RENDERER_MODE_ON)
            .setEnableDecoderFallback(true)
        ExoPlayer.Builder(context, renderers)
            .setMediaSourceFactory(DefaultMediaSourceFactory(listens.dataSource(DefaultDataSource.Factory(context, http)), DefaultExtractorsFactory()))
            .setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_SPEECH).build(), true)
            .setHandleAudioBecomingNoisy(true)
            .build()
            .also {
                it.addListener(listener)
                player = it
            }
    }

    /** Hands the files to Media3 and starts at [positionS] in the book. */
    private fun load(positionS: Double) {
        val q = book ?: return
        val p = ensurePlayer()
        val (index, offsetS) = BookPosition.locate(files.map { it.startS }, files.map { it.durationS }, positionS)
        val offsetMs = (offsetS * 1000).toLong()
        convertedOffsetMs = 0L
        val items = files.mapIndexed { i, f ->
            val stream = f.stream!!
            var url = stream.url
            if (i == index && !stream.seekable && offsetMs > 0) {
                url += (if ("?" in url) "&" else "?") + "start_ms=$offsetMs"
                convertedOffsetMs = offsetMs
            }
            MediaItem.Builder()
                .setMediaId(f.id.toString())
                .setUri(graph.api.absoluteUrl(url).orEmpty())
                .setMediaMetadata(
                    MediaMetadata.Builder()
                        .setTitle(q.title)
                        .setArtist(q.author)
                        .apply { if (q.hasCover) graph.api.audiobookCoverUrl(q.bookId, 480)?.let { setArtworkUri(Uri.parse(it)) } }
                        .build(),
                )
                .build()
        }
        p.setMediaItems(items, index, if (convertedOffsetMs > 0) 0L else offsetMs)
        p.playbackParameters = PlaybackParameters(speed)
        p.prepare()
        p.play()
        startTicker()
        publish()
    }

    /** Seconds from the start of the book. */
    private fun positionS(): Double {
        val p = player ?: return 0.0
        val f = files.getOrNull(p.currentMediaItemIndex) ?: return 0.0
        return f.startS + (convertedOffsetMs + p.currentPosition) / 1000.0
    }

    private fun currentFile(): AudiobookFile? = player?.let { files.getOrNull(it.currentMediaItemIndex) }

    fun toggle() {
        val p = player ?: return
        if (p.isPlaying) p.pause() else p.play()
    }

    /** Moves to [positionS] in the book: in place within a file Media3 can seek, else by loading from there. */
    fun seekTo(positionS: Double) {
        val q = book ?: return
        val p = player ?: return
        val target = positionS.coerceIn(0.0, (q.durationS - 0.5).coerceAtLeast(0.0))
        val (index, offsetS) = BookPosition.locate(files.map { it.startS }, files.map { it.durationS }, target)
        val seekable = files[index].stream?.seekable == true
        if (seekable && (index != p.currentMediaItemIndex || convertedOffsetMs == 0L)) {
            convertedOffsetMs = 0L
            p.seekTo(index, (offsetS * 1000).toLong())
        } else {
            load(target)
        }
        report("seek")
        saveNow()
        publish()
    }

    fun skip(deltaS: Double) = seekTo(positionS() + deltaS)

    fun nextChapter() {
        val chapters = book?.chapters ?: return
        BookPosition.nextChapter(chapters, positionS())?.let(::seekTo)
    }

    fun previousChapter() {
        val chapters = book?.chapters ?: return
        seekTo(BookPosition.previousChapter(chapters, positionS()))
    }

    /** Steps through the speeds, kept for the next book. */
    fun cycleSpeed() {
        speed = BookPosition.nextSpeed(speed)
        player?.playbackParameters = PlaybackParameters(speed)
        publish()
    }

    /** Stops the book, saving where it got to. */
    fun stop() {
        saveNow()
        endListen()
        closeUnplayed(listens) { id -> graph.playbackEvents.send(id, PlaybackEvent(type = "stop", positionMs = 0)) }
        ticker?.cancel()
        player?.run {
            removeListener(listener)
            release()
        }
        player = null
        book = null
        files = emptyList()
        _state.value = AudiobookState(speed = speed)
    }

    fun clearMessage() {
        _state.value = _state.value.copy(message = null)
    }

    private val listener = object : Player.Listener {
        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            if (reason == Player.MEDIA_ITEM_TRANSITION_REASON_AUTO) convertedOffsetMs = 0L
            beginListen()
            publish()
        }

        override fun onIsPlayingChanged(isPlaying: Boolean) {
            report(if (isPlaying) "resume" else "pause")
            if (!isPlaying) saveNow()
            publish()
        }

        override fun onPlaybackStateChanged(playbackState: Int) {
            if (playbackState == Player.STATE_ENDED) {
                val q = book
                if (q != null) save(q.durationS, finished = true)
                endListen()
            }
            publish()
        }

        override fun onPlayerError(error: PlaybackException) {
            publish(message = "This book could not play: ${error.errorCodeName}.")
        }
    }

    private fun saveNow() {
        if (book == null || player == null) return
        save(positionS(), finished = null)
    }

    private fun save(positionS: Double, finished: Boolean?) {
        val q = book ?: return
        lastSave = System.currentTimeMillis()
        scope.launch {
            try {
                graph.api.saveAudiobookProgress(q.bookId, positionS, finished)
            } catch (_: ApiException) {
                // The next save carries the position anyway.
            }
        }
    }

    private fun beginListen() {
        endListen()
        val file = currentFile() ?: return
        listenJob = scope.launch {
            val session = try {
                if (listens.enabled) {
                    listens.get(file.id)
                } else {
                    graph.api.audiobookListen(file.id, capabilities()).let { OpenListen(it.sessionId, it.heartbeatIntervalMs) }
                }
            } catch (e: ApiException) {
                publish(message = e.message)
                return@launch
            }
            listenSession = session.sessionId
            listenFile = file.id
            report("start")
            val beat = session.heartbeatMs.coerceAtLeast(5_000)
            while (isActive) {
                delay(beat)
                if (player?.isPlaying == true) report("progress")
            }
        }
    }

    private fun endListen() {
        listenJob?.cancel()
        listenJob = null
        listenFile?.let { listens.forget(it) }
        listenFile = null
        val id = listenSession ?: return
        listenSession = null
        graph.playbackEvents.send(id, PlaybackEvent(type = "stop", positionMs = fileMs(), durationMs = fileDuration()))
    }

    private fun fileMs(): Long = (player?.currentPosition ?: 0L) + convertedOffsetMs

    private fun fileDuration(): Long? = currentFile()?.let { (it.durationS * 1000).toLong() }

    /** Reports an event; a 410 means an admin stopped this listen from Activity. */
    private fun report(type: String) {
        val id = listenSession ?: return
        val event = PlaybackEvent(type = type, positionMs = fileMs(), durationMs = fileDuration())
        scope.launch {
            try {
                graph.api.postEvent(id, event)
            } catch (e: ApiException) {
                if (e is ApiException.Http && e.code == "session_terminated" && listenSession == id) {
                    listenSession = null
                    stop()
                    publish(message = "The book was stopped on the server: ${e.message}")
                }
            }
        }
    }

    private fun startTicker() {
        if (ticker?.isActive == true) return
        ticker = scope.launch {
            while (isActive) {
                publish()
                if (player?.isPlaying == true && System.currentTimeMillis() - lastSave >= SAVE_MS) saveNow()
                delay(500)
            }
        }
    }

    private fun publish(message: String? = _state.value.message) {
        val q = book
        val pos = positionS()
        _state.value = AudiobookState(
            book = q,
            playing = player?.isPlaying == true,
            positionS = pos,
            chapter = q?.let { BookPosition.chapterAt(it.chapters, pos) } ?: -1,
            speed = speed,
            message = message,
        )
    }

    companion object {
        /** How often a playing book saves its place. */
        const val SAVE_MS = 15_000L

        // Holds the application context only, never an activity's.
        @SuppressLint("StaticFieldLeak")
        @Volatile
        private var instance: AudiobookPlayer? = null

        fun get(graph: AppGraph, context: Context): AudiobookPlayer =
            instance ?: synchronized(this) {
                instance ?: AudiobookPlayer(context.applicationContext, graph).also { instance = it }
            }

        /** Stops the book, saving its place, if the player exists; never creates one. Safe from any thread. */
        fun stopIfRunning() {
            instance?.let { p -> p.scope.launch { p.stop() } }
        }
    }
}

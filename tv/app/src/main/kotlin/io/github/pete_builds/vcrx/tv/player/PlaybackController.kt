package io.github.pete_builds.vcrx.tv.player

import android.net.Uri
import android.view.KeyEvent
import androidx.activity.ComponentActivity
import androidx.annotation.OptIn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.extractor.DefaultExtractorsFactory
import io.github.pete_builds.vcrx.tv.caps.CapabilityProbe
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Marker
import io.github.pete_builds.vcrx.tv.net.PlaybackRequest
import io.github.pete_builds.vcrx.tv.net.PlaybackSession
import io.github.pete_builds.vcrx.tv.net.PlaybackSettings
import io.github.pete_builds.vcrx.tv.net.streamRefusal
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

sealed interface Phase {
    data object Starting : Phase
    data object Playing : Phase
    /** 403 or 422 from the server: policy or a file this device cannot play yet. */
    data class Refused(val message: String) : Phase
    data class Failed(val message: String, val unreachable: Boolean) : Phase
    /** An episode ended and the next one starts when [secondsLeft] reaches zero. */
    data class UpNext(val next: Item, val secondsLeft: Int) : Phase
}

enum class Overlay { Hidden, Info, Controls }

enum class TrackKind { Audio, Subtitles }

data class TrackOption(val label: String, val selected: Boolean, val supported: Boolean, val apply: () -> Unit)

/**
 * One playback: asks the server for a session with this device's capabilities,
 * plays the stream through the shared OkHttp client (so it carries the token),
 * reports events, and turns remote keys into player actions.
 */
@OptIn(UnstableApi::class)
class PlaybackController(
    private val activity: ComponentActivity,
    private val itemId: Long,
    private val versionId: Long?,
    private val fromStart: Boolean,
    val title: String,
) {
    private val graph = activity.graph

    var phase by mutableStateOf<Phase>(Phase.Starting)
        private set
    var overlay by mutableStateOf(Overlay.Hidden)
        private set
    var dialog by mutableStateOf<TrackKind?>(null)
    var positionMs by mutableStateOf(0L)
        private set
    var durationMs by mutableStateOf(0L)
        private set
    var isPlaying by mutableStateOf(false)
        private set

    /** This item's intro and credits, and the user's choice for them (player/SkipMarkers.kt). */
    var markers by mutableStateOf<List<Marker>>(emptyList())
        private set

    /**
     * button, auto or off: from the session, which carries the server-side setting. A
     * settings screen that changes it mid-playback may set it here too.
     */
    var skipMode by mutableStateOf(PlaybackSettings.BUTTON)

    /** What the Skip button does now; null when it is hidden. */
    val skipAction: SkipAction?
        get() = SkipMarkers.actionAt(markers, positionMs, durationMs, nextEpisode != null, skipMode)

    /** Where held left or right keys have moved the playhead to; committed on release. */
    var scrubTargetMs by mutableStateOf<Long?>(null)
        private set

    val player: ExoPlayer
    private var session: PlaybackSession? = null
    private var reporter: PlaybackReporter? = null
    private var released = false
    private var heartbeat: Job? = null
    private var ticker: Job? = null
    private var autoHide: Job? = null
    private var countdown: Job? = null

    /** For an episode, the one after it, from the item's details; null for a movie or the last episode. */
    private var nextEpisode: Item? = null

    init {
        val http = OkHttpDataSource.Factory(graph.httpClient)
        val extractors = DefaultExtractorsFactory().setConstantBitrateSeekingEnabled(true)
        val renderers = DefaultRenderersFactory(activity)
            // Platform decoders and passthrough win; the bundled FFmpeg decodes what they cannot.
            .setExtensionRendererMode(DefaultRenderersFactory.EXTENSION_RENDERER_MODE_ON)
            .setEnableDecoderFallback(true)
        player = ExoPlayer.Builder(activity, renderers)
            .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(activity, http), extractors))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
                true,
            )
            .setHandleAudioBecomingNoisy(true)
            .build()
        player.addListener(listener())
    }

    /** Opens a session and starts playing. [startAtMs] overrides the saved position, for retries. */
    fun start(startAtMs: Long? = null) {
        if (released) return
        phase = Phase.Starting
        activity.lifecycleScope.launch {
            try {
                // Details are only for Up Next, so a failure here never blocks playback.
                launch {
                    nextEpisode = runCatching { graph.api.item(itemId) }.getOrNull()
                        ?.takeIf { it.kind == "episode" }?.nextEpisode
                }
                val capabilities = withContext(Dispatchers.Default) { CapabilityProbe.probe(activity) }
                val start = startAtMs ?: if (fromStart) 0L else null
                val created = graph.api.startPlayback(PlaybackRequest(itemId, versionId, start, capabilities))
                if (released) return@launch
                markers = created.markers
                skipMode = created.skipMarkers
                play(created)
            } catch (e: ApiException.Http) {
                phase = if (e.status == 403 || e.status == 422) Phase.Refused(e.message.orEmpty()) else Phase.Failed(e.message.orEmpty(), false)
            } catch (e: ApiException.Unreachable) {
                phase = Phase.Failed(e.message.orEmpty(), true)
            } catch (e: ApiException) {
                phase = Phase.Failed(e.message.orEmpty(), false)
            }
        }
    }

    fun retry() {
        val at = if (session != null) player.currentPosition else null
        reporter?.stop(player.currentPosition, duration())
        reporter = null
        session = null
        player.stop()
        start(at)
    }

    private fun play(created: PlaybackSession) {
        val uri = graph.api.absoluteUrl(created.stream.url)
        if (uri == null) {
            phase = Phase.Failed("The server sent a stream address this app cannot use.", false)
            return
        }
        val subtitles = created.subtitles.mapNotNull { sub ->
            val url = graph.api.absoluteUrl(sub.url) ?: return@mapNotNull null
            MediaItem.SubtitleConfiguration.Builder(Uri.parse(url))
                .setId("sidecar:${sub.trackId}")
                .setMimeType(sub.mimeType)
                .setLanguage(sub.language)
                .setLabel(sub.title ?: sub.language?.let { TrackLabels.language(it) })
                .setSelectionFlags(if (sub.isForced) C.SELECTION_FLAG_FORCED else 0)
                .build()
        }
        val item = MediaItem.Builder()
            .setUri(uri)
            .setMediaId(created.sessionId)
            .setSubtitleConfigurations(subtitles)
            .apply { if (created.stream.protocol == "hls") setMimeType(MimeTypes.APPLICATION_M3U8) }
            .build()

        session = created
        reporter = PlaybackReporter { event -> graph.playbackEvents.send(created.sessionId, event) }
        player.setMediaItem(item, created.resumePositionMs)
        player.prepare()
        player.playWhenReady = true
        phase = Phase.Playing

        heartbeat?.cancel()
        heartbeat = activity.lifecycleScope.launch {
            val interval = created.heartbeatIntervalMs.coerceAtLeast(2_000)
            while (isActive) {
                delay(interval)
                reporter?.onHeartbeat(player.currentPosition, duration(), player.isPlaying)
            }
        }
        ticker?.cancel()
        ticker = activity.lifecycleScope.launch {
            while (isActive) {
                positionMs = player.currentPosition
                durationMs = duration() ?: 0L
                delay(500)
            }
        }
    }

    /** Sends the final stop and frees the decoder. Every exit path calls this; only the first does anything. */
    fun finishPlayback() {
        if (released) return
        released = true
        reporter?.stop(player.currentPosition, duration())
        heartbeat?.cancel()
        ticker?.cancel()
        autoHide?.cancel()
        countdown?.cancel()
        player.release()
    }

    private fun duration(): Long? = player.duration.takeIf { it != C.TIME_UNSET && it > 0 }

    private fun listener() = object : Player.Listener {
        override fun onIsPlayingChanged(playing: Boolean) {
            isPlaying = playing
            if (playing) {
                reporter?.onPlaying(player.currentPosition, duration())
                if (overlay == Overlay.Info) scheduleHide(INFO_MS)
            } else if (!player.playWhenReady) {
                reporter?.onPaused(player.currentPosition, duration())
            }
        }

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            if (!playWhenReady) reporter?.onPaused(player.currentPosition, duration())
        }

        override fun onPlaybackStateChanged(state: Int) {
            when (state) {
                Player.STATE_BUFFERING -> if (player.playWhenReady) reporter?.onBuffering(player.currentPosition, duration())
                Player.STATE_ENDED -> {
                    reporter?.stop(duration() ?: player.currentPosition, duration())
                    val next = nextEpisode
                    if (next != null) upNext(next) else activity.finish()
                }
                else -> Unit
            }
        }

        override fun onPositionDiscontinuity(old: Player.PositionInfo, new: Player.PositionInfo, reason: Int) {
            if (reason == Player.DISCONTINUITY_REASON_SEEK) reporter?.onSeek(old.positionMs, new.positionMs, duration())
        }

        override fun onPlayerError(error: PlaybackException) {
            val message = describe(error)
            reporter?.onError(message, player.currentPosition, duration())
            phase = Phase.Failed(message, false)
        }
    }

    private fun describe(error: PlaybackException): String {
        val http = generateSequence(error.cause) { it.cause }.filterIsInstance<HttpDataSource.InvalidResponseCodeException>().firstOrNull()
        return when {
            http != null -> streamRefusal(http.responseCode, http.responseBody, graph.json)
            error.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED ||
                error.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT ->
                "Lost the connection to the server."
            else -> "Playback failed: ${error.errorCodeName}. ${error.message.orEmpty()}".trim()
        }
    }

    private fun upNext(next: Item) {
        countdown?.cancel()
        countdown = activity.lifecycleScope.launch {
            for (left in UP_NEXT_SECONDS downTo 1) {
                phase = Phase.UpNext(next, left)
                delay(1_000)
            }
            playNext()
        }
    }

    /** Plays the Up Next episode now, in a fresh player so its session starts clean. */
    fun playNext() {
        val next = (phase as? Phase.UpNext)?.next ?: return
        countdown?.cancel()
        val title = listOfNotNull(next.showTitle, episodeCode(next), next.title).joinToString("  ")
        activity.startActivity(PlayerActivity.intent(activity, next.id, null, false, title))
        activity.finish()
    }

    /**
     * Skip Intro or Skip Credits. A seek goes through [skip], the controller's own seek
     * path, so a converted stream reopens at the target. The delta is from [positionMs],
     * at most half a second old, so the landing is that much past the target at worst.
     * Next Episode ends this one (reported as finished) and starts the Up Next countdown.
     */
    fun skipMarker(action: SkipAction) {
        val next = nextEpisode
        if (action.playsNext && next != null) {
            player.playWhenReady = false
            reporter?.stop(durationMs, durationMs.takeIf { it > 0 })
            upNext(next)
            return
        }
        skip(action.targetMs - positionMs)
    }

    fun togglePlayPause() {
        if (player.playbackState == Player.STATE_ENDED) player.seekTo(0)
        player.playWhenReady = !player.playWhenReady
        showInfo(persist = !player.playWhenReady)
    }

    fun skip(deltaMs: Long) {
        player.seekTo(SeekSteps.clamp(player.currentPosition + deltaMs, duration()))
        showInfo()
    }

    private fun scrub(direction: Int, repeatCount: Int) {
        val from = scrubTargetMs ?: player.currentPosition
        scrubTargetMs = SeekSteps.clamp(from + direction * SeekSteps.stepMs(repeatCount), duration())
        positionMs = player.currentPosition
        if (overlay == Overlay.Hidden) overlay = Overlay.Info
        autoHide?.cancel()
    }

    private fun commitScrub() {
        val target = scrubTargetMs ?: return
        scrubTargetMs = null
        player.seekTo(target)
        showInfo()
    }

    fun showControls() {
        overlay = Overlay.Controls
        scheduleHide(CONTROLS_MS)
    }

    fun hideControls() {
        overlay = if (!player.playWhenReady) Overlay.Info else Overlay.Hidden
        autoHide?.cancel()
    }

    /** Any key while the controls are up keeps them up a while longer. */
    fun touch() {
        if (overlay == Overlay.Controls) scheduleHide(CONTROLS_MS)
    }

    private fun showInfo(persist: Boolean = false) {
        if (overlay != Overlay.Controls) overlay = Overlay.Info
        if (persist) autoHide?.cancel() else scheduleHide(if (overlay == Overlay.Controls) CONTROLS_MS else INFO_MS)
    }

    private fun scheduleHide(afterMs: Long) {
        autoHide?.cancel()
        autoHide = activity.lifecycleScope.launch {
            delay(afterMs)
            // Paused: keep the position on screen; playing: clear the picture.
            if (dialog == null && scrubTargetMs == null) overlay = if (player.playWhenReady) Overlay.Hidden else Overlay.Info
        }
    }

    /**
     * Remote keys before Compose sees them. With the controls hidden: center plays or
     * pauses, left and right seek (hold to go faster), up and down show the controls,
     * Back leaves. With the controls up, Compose moves focus and Back hides them.
     */
    fun handleKey(event: KeyEvent, exit: () -> Unit): Boolean {
        val down = event.action == KeyEvent.ACTION_DOWN
        val up = event.action == KeyEvent.ACTION_UP
        when (event.keyCode) {
            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> { if (down && event.repeatCount == 0) togglePlayPause(); return true }
            KeyEvent.KEYCODE_MEDIA_PLAY -> { if (down) { player.playWhenReady = true; showInfo() }; return true }
            KeyEvent.KEYCODE_MEDIA_PAUSE -> { if (down) { player.playWhenReady = false; showInfo(persist = true) }; return true }
            KeyEvent.KEYCODE_MEDIA_STOP -> { if (up) exit(); return true }
        }
        if (phase != Phase.Playing) return false
        when (event.keyCode) {
            KeyEvent.KEYCODE_MEDIA_FAST_FORWARD, KeyEvent.KEYCODE_MEDIA_REWIND -> {
                val direction = if (event.keyCode == KeyEvent.KEYCODE_MEDIA_FAST_FORWARD) 1 else -1
                if (down) scrub(direction, event.repeatCount) else if (up) commitScrub()
                return true
            }
        }
        if (overlay == Overlay.Controls) {
            if (event.keyCode == KeyEvent.KEYCODE_BACK) {
                if (up) hideControls()
                return true
            }
            touch()
            return false
        }
        // The Skip button has focus while it shows: OK presses it.
        skipAction?.let { action ->
            if (SkipMarkers.isSelect(event.keyCode)) {
                if (up) skipMarker(action)
                return true
            }
        }
        return when (event.keyCode) {
            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> {
                if (up) togglePlayPause()
                true
            }
            KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_DPAD_RIGHT -> {
                val direction = if (event.keyCode == KeyEvent.KEYCODE_DPAD_RIGHT) 1 else -1
                if (down) scrub(direction, event.repeatCount) else if (up) commitScrub()
                true
            }
            KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN -> {
                if (down) showControls()
                true
            }
            KeyEvent.KEYCODE_BACK -> {
                if (up) {
                    if (scrubTargetMs != null) {
                        scrubTargetMs = null
                        showInfo()
                    } else {
                        exit()
                    }
                }
                true
            }
            else -> false
        }
    }

    fun trackOptions(kind: TrackKind): List<TrackOption> {
        val type = if (kind == TrackKind.Audio) C.TRACK_TYPE_AUDIO else C.TRACK_TYPE_TEXT
        val groups = player.currentTracks.groups.filter { it.type == type }
        val options = groups.flatMap { group ->
            (0 until group.length).map { index ->
                val format = group.getTrackFormat(index)
                TrackOption(
                    label = TrackLabels.label(format, index),
                    selected = group.isTrackSelected(index),
                    supported = group.isTrackSupported(index),
                    apply = { select(group, index, type) },
                )
            }
        }
        if (kind == TrackKind.Audio) return options
        val off = TrackOption("Off", selected = options.none { it.selected }, supported = true) {
            player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
                .clearOverridesOfType(C.TRACK_TYPE_TEXT)
                .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
                .build()
        }
        return listOf(off) + options
    }

    private fun select(group: Tracks.Group, index: Int, type: Int) {
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
            .setTrackTypeDisabled(type, false)
            .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, index))
            .build()
    }

    private companion object {
        const val UP_NEXT_SECONDS = 10
        const val INFO_MS = 3_000L
        const val CONTROLS_MS = 6_000L
    }
}

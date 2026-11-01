package io.github.pete_builds.vcrx.tv.player

import android.content.Context
import android.net.Uri
import android.view.KeyEvent
import android.view.WindowManager
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
import androidx.media3.common.text.Cue
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.AudioTrackAudioOutputProvider
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.extractor.DefaultExtractorsFactory
import io.github.pete_builds.vcrx.tv.caps.CapabilityPolicy
import io.github.pete_builds.vcrx.tv.caps.CapabilityProbe
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Marker
import io.github.pete_builds.vcrx.tv.net.PlaybackRequest
import io.github.pete_builds.vcrx.tv.net.PlaybackSession
import io.github.pete_builds.vcrx.tv.net.PlaybackSettings
import io.github.pete_builds.vcrx.tv.net.SubtitleSource
import io.github.pete_builds.vcrx.tv.settings.Languages
import io.github.pete_builds.vcrx.tv.settings.Preferences
import io.github.pete_builds.vcrx.tv.settings.SubtitleStyle
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

sealed interface Phase {
    data object Starting : Phase
    data object Playing : Phase
    /** 403 or 422 from the server: policy or a file this device cannot play yet. */
    data class Refused(val message: String) : Phase
    /** [busy]: the server is converting as many streams as it allows (503 busy); Retry may work shortly. */
    data class Failed(val message: String, val unreachable: Boolean, val busy: Boolean = false) : Phase
    /** An episode ended; the next one starts when [secondsLeft] reaches zero, or waits for Play when it is null. */
    data class UpNext(val next: Item, val secondsLeft: Int?) : Phase
    /** Settings say ask: resume from [positionMs] or start over. */
    data class AskResume(val positionMs: Long) : Phase
}

enum class Overlay { Hidden, Info, Controls }

enum class TrackKind { Audio, Subtitles }

/** Which button has focus when the controls open. */
enum class ControlsFocus {
    Play,
    Subtitles,
    ;

    companion object {
        /**
         * Down opens the controls on the Subtitles button, so captions are two
         * presses (Down, OK) on a remote with no captions key; Up opens on Play.
         */
        fun forKey(keyCode: Int): ControlsFocus =
            if (keyCode == android.view.KeyEvent.KEYCODE_DPAD_DOWN) Subtitles else Play
    }
}

data class TrackOption(val label: String, val selected: Boolean, val supported: Boolean, val apply: () -> Unit)

/**
 * One playback: asks the server for a session with this device's capabilities,
 * plays the stream through the shared OkHttp client (so it carries the token),
 * reports events, and turns remote keys into player actions.
 *
 * A converted session (see [StreamKinds]) plays as a progressive fragmented MP4
 * and seeks by asking the server where a new stream starts; [timeline] maps the
 * player's own time to the file's for everything shown and reported.
 */
@OptIn(UnstableApi::class)
class PlaybackController(
    private val activity: ComponentActivity,
    private val itemId: Long,
    private val versionId: Long?,
    private val request: StartRequest,
    val title: String,
    /** Set when playing one of the item's extras (a trailer) rather than the item. */
    private val extraId: Long? = null,
) {
    private val graph = activity.graph

    /** Settings as they were when this playback opened; a change applies from the next title. */
    private val prefs: Preferences = graph.preferences.current
    private val deviceLanguage: String = Locale.getDefault().language

    /** Settings > Subtitles > Style, for the views that draw the subtitles. */
    val captionStyle: SubtitleStyle = prefs.subtitles.style

    val skipBackMs: Long = PlaybackPolicy.skipBackMs(prefs.playback)
    val skipForwardMs: Long = PlaybackPolicy.skipForwardMs(prefs.playback)

    var phase by mutableStateOf<Phase>(Phase.Starting)
        private set
    var overlay by mutableStateOf(Overlay.Hidden)
        private set
    var controlsFocus by mutableStateOf(ControlsFocus.Play)
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

    /** For a converted stream, what kind ("Audio converted"); null for direct play. */
    var streamLabel by mutableStateOf<String?>(null)
        private set

    /** A converted stream is reopening at a new position. */
    var reloading by mutableStateOf(false)
        private set

    /** The player is waiting for data: the loading bar shows (never a spinner). */
    var buffering by mutableStateOf(false)
        private set

    /** A subtitle track is showing. */
    var subtitlesOn by mutableStateOf(false)
        private set

    /** For a converted stream, the cues to draw now; direct play draws through the player's own view. */
    var subtitleCues by mutableStateOf<List<Cue>>(emptyList())
        private set

    /** A short line on screen: what a subtitle press did, or that subtitles are being prepared. */
    var notice by mutableStateOf<String?>(null)
        private set

    /** True for a converted stream whose file has picture subtitles, which only direct play can show. */
    var pictureSubtitlesUnavailable by mutableStateOf(false)
        private set

    val player: ExoPlayer
    private var session: PlaybackSession? = null
    private var reporter: PlaybackReporter? = null
    private var released = false
    private var heartbeat: Job? = null
    private var ticker: Job? = null
    private var autoHide: Job? = null
    private var countdown: Job? = null
    private var seekJob: Job? = null
    private var noticeJob: Job? = null
    private var subtitleJob: Job? = null
    private var cueTicker: Job? = null

    /** The item's details, for its show (subtitle choices carry across a show) and Up Next. */
    private var details: Item? = null

    /** Capabilities waiting while the viewer answers the resume question. */
    private var pendingCapabilities: Capabilities? = null

    /** Direct play: the opening subtitle choice has been made for the current file. */
    private var subtitlesDecided = false

    /** Converted: the WebVTT tracks, the one chosen, and its cues once loaded. */
    private var converted: ConvertedSubtitles? = null
    private var convertedChoice: SubtitleSource? = null
    private var convertedCues: CueTimeline<Cue>? = null

    /** Converted: the audio track asked for by stream index, from the audio language setting; null lets the server choose. */
    private var convertedAudioIndex: Int? = null

    /** Direct play until a converted session replaces it in [play]. */
    private var timeline = directTimeline()

    /** Where a converted seek is headed while the server answers, so the bar does not jump back. */
    private var pendingSeekMs: Long? = null
    private val progressive: ProgressiveMediaSource.Factory

    /** For an episode, the one after it, from the item's details; null for a movie or the last episode. */
    private var nextEpisode: Item? = null

    init {
        val http = OkHttpDataSource.Factory(graph.httpClient)
        val extractors = DefaultExtractorsFactory().setConstantBitrateSeekingEnabled(true)
        progressive = ProgressiveMediaSource.Factory(DefaultDataSource.Factory(activity, http), extractors)
        val passthrough = prefs.audio.passthrough
        val renderers = object : DefaultRenderersFactory(activity) {
            override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioTrackPlaybackParams: Boolean): AudioSink? =
                if (passthrough) {
                    super.buildAudioSink(context, enableFloatOutput, enableAudioTrackPlaybackParams)
                } else {
                    // Settings > Audio > Passthrough off: an output with no context reports only
                    // PCM, so every format is decoded here instead of sent on to a receiver.
                    DefaultAudioSink.Builder(context).setAudioOutputProvider(AudioTrackAudioOutputProvider.Builder(null).build()).build()
                }
        }
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
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon().apply {
            // Subtitles start off and the app turns on the one Settings call for once the
            // file's tracks are known, so no forced or default track flashes up first.
            setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            if (!prefs.audio.preferOriginal) {
                Languages.preferred(listOf(prefs.audio.language), deviceLanguage).firstOrNull()?.let { setPreferredAudioLanguages(it) }
            }
        }.build()
    }

    /** Opens a session and starts playing. [startAtMs] overrides the saved position, for retries. */
    fun start(startAtMs: Long? = null) {
        if (released) return
        phase = Phase.Starting
        activity.lifecycleScope.launch {
            try {
                // Details give the show (for subtitle choices), Up Next and the saved position.
                // A failure or a slow answer never blocks playback; the server still resumes.
                val item = async { withTimeoutOrNull(DETAILS_TIMEOUT_MS) { runCatching { graph.api.item(itemId) }.getOrNull() } }
                val probed = async(Dispatchers.Default) { CapabilityProbe.probe(activity) }
                details = item.await()
                nextEpisode = details?.takeIf { it.kind == "episode" }?.nextEpisode
                val capabilities = CapabilityPolicy.apply(probed.await(), prefs)
                if (released) return@launch
                val saved = details?.watch?.takeIf { !it.played }?.positionMs?.takeIf { it > 0 }
                val start = startAtMs ?: when (PlaybackPolicy.start(request, prefs.playback.resume, saved)) {
                    PlaybackPolicy.Start.Resume -> null
                    PlaybackPolicy.Start.Beginning -> 0L
                    PlaybackPolicy.Start.Ask -> {
                        pendingCapabilities = capabilities
                        phase = Phase.AskResume(saved ?: 0L)
                        return@launch
                    }
                }
                open(capabilities, start)
            } catch (e: ApiException) {
                phase = phaseFor(e)
            }
        }
    }

    /** The viewer answered the resume question. */
    fun answerResume(resume: Boolean) {
        val ask = phase as? Phase.AskResume ?: return
        val capabilities = pendingCapabilities ?: return
        pendingCapabilities = null
        phase = Phase.Starting
        activity.lifecycleScope.launch {
            try {
                open(capabilities, if (resume) ask.positionMs else 0L)
            } catch (e: ApiException) {
                phase = phaseFor(e)
            }
        }
    }

    private suspend fun open(capabilities: Capabilities, startMs: Long?) {
        val created = graph.api.startPlayback(PlaybackRequest(itemId, versionId, startMs, capabilities, extraId))
        if (released) return
        markers = created.markers
        skipMode = created.skipMarkers
        play(created)
    }

    fun retry() {
        val at = if (session != null) shownPosition() else null
        reporter?.stop(shownPosition(), duration())
        reporter = null
        session = null
        seekJob?.cancel()
        timeline.cancelSeeks()
        pendingSeekMs = null
        reloading = false
        player.stop()
        start(at)
    }

    /** Clears subtitle state for a new file; the opening choice is made again from Settings and remembered choices. */
    private fun resetSubtitles() {
        subtitlesDecided = false
        subtitlesOn = false
        subtitleJob?.cancel()
        cueTicker?.cancel()
        converted = null
        convertedChoice = null
        convertedCues = null
        subtitleCues = emptyList()
        pictureSubtitlesUnavailable = false
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
            .clearOverridesOfType(C.TRACK_TYPE_TEXT)
            .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            .build()
    }

    private fun play(created: PlaybackSession) {
        resetSubtitles()
        if (StreamKinds.isConverted(created)) {
            playConverted(created)
            return
        }
        timeline = directTimeline()
        streamLabel = null
        val uri = graph.api.absoluteUrl(created.stream.url)
        if (uri == null) {
            phase = Phase.Failed("The server sent a stream address this app cannot use.", false)
            return
        }
        // TODO(#41): once #41 lands, the session carries subtitle_offset_ms (the viewer's
        // subtitle delay). Apply it to these side-loaded tracks and to embedded ones (for
        // example by shifting the cues the player outputs), but never to the WebVTT URLs
        // used for converted streams in ConvertedSubtitles: the server already shifts those.
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
        startReporting(created)
    }

    /**
     * A converted session: the first stream opens at the resume position the server
     * keyframe-aligned. The phase stays Starting until the first stream is set on the
     * player. Subtitles are not side-loaded (their times are the file's, while each
     * stream restarts at zero after a seek); the app draws them itself in file time
     * ([ConvertedSubtitles]). Picture subtitles cannot be drawn that way and need
     * direct play.
     */
    private fun playConverted(created: PlaybackSession) {
        converted = ConvertedSubtitles(graph.api, created.webVttSubtitles)
        pictureSubtitlesUnavailable = created.version.subtitleTracks.any { it.isImageBased }
        convertedAudioIndex = PlaybackPolicy.convertedAudioIndex(created.version.audioTracks, prefs.audio, deviceLanguage)
        timeline = StreamTimeline(
            converted = true,
            fileDurationMs = created.version.durationMs,
            playerPositionMs = { player.currentPosition },
            playerDurationMs = ::playerDuration,
        )
        streamLabel = StreamKinds.label(created)
        session = created
        reporter = PlaybackReporter { event -> graph.playbackEvents.send(created.sessionId, event) }
        player.playWhenReady = true
        openConverted(created, created.resumePositionMs, seekFromMs = null)
        startReporting(created)
        val audioLanguage = PlaybackPolicy.convertedAudioLanguage(created.version.audioTracks, convertedAudioIndex)
        val chosen = SubtitlePicker.choose(prefs.subtitles, converted?.candidates.orEmpty(), audioLanguage, sticky(), deviceLanguage)
        selectConverted(chosen?.let { converted?.source(it) })
    }

    /**
     * Asks the seek endpoint where a stream from [targetMs] starts, then plays that
     * stream. [seekFromMs] is the position the viewer left, reported with the seek
     * event once the new start is known; null for the first stream.
     */
    private fun openConverted(created: PlaybackSession, targetMs: Long, seekFromMs: Long?) {
        val seek = timeline.beginSeek()
        val target = timeline.seekTarget(targetMs)
        pendingSeekMs = target
        positionMs = target
        reloading = true
        seekJob?.cancel()
        seekJob = activity.lifecycleScope.launch {
            try {
                val answer = graph.api.convertedStart(StreamKinds.seekPath(created), target)
                if (released || !timeline.isCurrent(seek)) return@launch
                val uri = graph.api.absoluteUrl(StreamKinds.withAudio(answer.url, convertedAudioIndex))
                if (uri == null) {
                    convertedFailed(Phase.Failed("The server sent a stream address this app cannot use.", false))
                    return@launch
                }
                val item = MediaItem.Builder()
                    .setUri(uri)
                    .setMediaId(created.sessionId)
                    .setMimeType(MimeTypes.VIDEO_MP4)
                    .build()
                timeline.land(seek, answer.startMs)
                pendingSeekMs = null
                reloading = false
                // Before the new stream, so the seek is reported ahead of its buffering, as in direct play.
                if (seekFromMs != null) reporter?.onSeek(seekFromMs, answer.startMs, duration())
                player.setMediaSource(progressive.createMediaSource(item))
                player.prepare()
                positionMs = position()
                if (phase == Phase.Starting) phase = Phase.Playing
            } catch (e: ApiException) {
                if (!released && timeline.isCurrent(seek)) convertedFailed(phaseFor(e))
            }
        }
    }

    /**
     * A converted stream could not be opened or reopened: stop the old one and say why.
     * A pending seek target is kept, so Retry and the final stop use where the viewer was going.
     */
    private fun convertedFailed(failed: Phase) {
        reloading = false
        player.stop()
        if (failed is Phase.Failed) reporter?.onError(failed.message, shownPosition(), duration())
        phase = failed
    }

    private fun startReporting(created: PlaybackSession) {
        heartbeat?.cancel()
        heartbeat = activity.lifecycleScope.launch {
            val interval = created.heartbeatIntervalMs.coerceAtLeast(2_000)
            while (isActive) {
                delay(interval)
                reporter?.onHeartbeat(position(), duration(), player.isPlaying)
            }
        }
        ticker?.cancel()
        ticker = activity.lifecycleScope.launch {
            while (isActive) {
                positionMs = shownPosition()
                durationMs = duration() ?: 0L
                delay(500)
            }
        }
    }

    /** Sends the final stop and frees the decoder. Every exit path calls this; only the first does anything. */
    fun finishPlayback() {
        if (released) return
        released = true
        reporter?.stop(shownPosition(), duration())
        timeline.cancelSeeks()
        seekJob?.cancel()
        heartbeat?.cancel()
        ticker?.cancel()
        autoHide?.cancel()
        countdown?.cancel()
        noticeJob?.cancel()
        subtitleJob?.cancel()
        cueTicker?.cancel()
        player.release()
    }

    private fun playerDuration(): Long? = player.duration.takeIf { it != C.TIME_UNSET && it > 0 }

    private fun directTimeline() = StreamTimeline(
        converted = false,
        fileDurationMs = null,
        playerPositionMs = { player.currentPosition },
        playerDurationMs = ::playerDuration,
    )

    /** The position in the file: the player's own for direct play, start plus player time for a converted stream. */
    private fun position(): Long = timeline.positionMs()

    private fun duration(): Long? = timeline.durationMs()

    /**
     * What the bar shows and where a stop or retry lands: a converted stream's target
     * until it opens (the resume point for the first one). Always [position] for direct play.
     */
    private fun shownPosition(): Long = pendingSeekMs ?: position()

    /** Seeks the file: by range for direct play, by opening a new stream for a converted one. */
    private fun seekTo(targetMs: Long) {
        val current = session
        if (timeline.converted && current != null) {
            openConverted(current, targetMs, seekFromMs = position())
        } else {
            player.seekTo(targetMs)
        }
    }

    /** Lets the screen sleep while paused; see [PlaybackPolicy.keepScreenOn]. */
    private fun updateKeepScreenOn() {
        val flag = WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON
        if (PlaybackPolicy.keepScreenOn(player.playWhenReady, player.playbackState)) {
            activity.window.addFlags(flag)
        } else {
            activity.window.clearFlags(flag)
        }
    }

    private fun listener() = object : Player.Listener {
        override fun onIsPlayingChanged(playing: Boolean) {
            isPlaying = playing
            if (playing) {
                reporter?.onPlaying(position(), duration())
                if (overlay == Overlay.Info) scheduleHide(INFO_MS)
            } else if (!player.playWhenReady) {
                reporter?.onPaused(position(), duration())
            }
        }

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            updateKeepScreenOn()
            if (!playWhenReady) reporter?.onPaused(position(), duration())
        }

        override fun onPlaybackStateChanged(state: Int) {
            updateKeepScreenOn()
            buffering = state == Player.STATE_BUFFERING
            when (state) {
                Player.STATE_BUFFERING -> if (player.playWhenReady) reporter?.onBuffering(position(), duration())
                Player.STATE_ENDED -> {
                    if (timeline.endedEarly()) {
                        // Cut off, not finished: keep the resume point and offer Retry from here.
                        convertedFailed(Phase.Failed("The converted stream stopped before the end.", false))
                        return
                    }
                    reporter?.stop(duration() ?: position(), duration())
                    val next = nextEpisode
                    if (next != null) upNext(next) else activity.finish()
                }
                else -> Unit
            }
        }

        override fun onTracksChanged(tracks: Tracks) {
            if (timeline.converted || tracks.groups.isEmpty()) return
            if (!subtitlesDecided) {
                subtitlesDecided = true
                val chosen = SubtitlePicker.choose(prefs.subtitles, directCandidates(tracks), directAudioLanguage(tracks), sticky(), deviceLanguage)
                selectDirect(chosen)
            }
            subtitlesOn = tracks.groups.any { it.type == C.TRACK_TYPE_TEXT && it.isSelected }
        }

        override fun onPositionDiscontinuity(old: Player.PositionInfo, new: Player.PositionInfo, reason: Int) {
            // A converted stream reports its seeks when the new stream opens, in file time.
            if (reason == Player.DISCONTINUITY_REASON_SEEK && !timeline.converted) reporter?.onSeek(old.positionMs, new.positionMs, duration())
        }

        override fun onPlayerError(error: PlaybackException) {
            val failed = describe(error)
            reporter?.onError(failed.message, position(), duration())
            phase = failed
        }
    }

    private fun describe(error: PlaybackException): Phase.Failed {
        val http = generateSequence(error.cause) { it.cause }.filterIsInstance<HttpDataSource.InvalidResponseCodeException>().firstOrNull()
        return when {
            http != null -> streamFailure(http.responseCode, http.responseBody, graph.json)
            error.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED ||
                error.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT ->
                Phase.Failed("Lost the connection to the server.", false)
            else -> Phase.Failed("Playback failed: ${error.errorCodeName}. ${error.message.orEmpty()}".trim(), false)
        }
    }

    private fun upNext(next: Item) {
        countdown?.cancel()
        val seconds = PlaybackPolicy.countdownSeconds(prefs.playback)
        if (seconds == null) {
            // Auto-play off: the next episode waits for Play now.
            phase = Phase.UpNext(next, null)
            return
        }
        countdown = activity.lifecycleScope.launch {
            for (left in seconds downTo 1) {
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
        activity.startActivity(PlayerActivity.intent(activity, next.id, null, StartRequest.Default, title))
        activity.finish()
    }

    /**
     * Skip Intro or Skip Credits. The seek goes through [seekTo], the controller's own
     * seek path, so a converted stream reopens at the target through the seek endpoint
     * and direct play seeks by range. Next Episode ends this one (reported as finished)
     * and starts the Up Next countdown.
     */
    fun skipMarker(action: SkipAction) {
        val next = nextEpisode
        if (action.playsNext && next != null) {
            player.playWhenReady = false
            reporter?.stop(durationMs, durationMs.takeIf { it > 0 })
            upNext(next)
            return
        }
        seekTo(SeekSteps.clamp(action.targetMs, duration()))
        showInfo()
    }

    fun togglePlayPause() {
        if (player.playbackState == Player.STATE_ENDED) seekTo(0)
        player.playWhenReady = !player.playWhenReady
        showInfo(persist = !player.playWhenReady)
    }

    fun skip(deltaMs: Long) {
        seekTo(SeekSteps.clamp(shownPosition() + deltaMs, duration()))
        showInfo()
    }

    private fun scrub(direction: Int, repeatCount: Int) {
        val from = scrubTargetMs ?: shownPosition()
        val first = if (direction > 0) skipForwardMs else skipBackMs
        scrubTargetMs = SeekSteps.clamp(from + direction * SeekSteps.stepMs(repeatCount, first), duration())
        positionMs = shownPosition()
        if (overlay == Overlay.Hidden) overlay = Overlay.Info
        autoHide?.cancel()
    }

    private fun commitScrub() {
        val target = scrubTargetMs ?: return
        scrubTargetMs = null
        seekTo(target)
        showInfo()
    }

    fun showControls(focus: ControlsFocus = ControlsFocus.Play) {
        controlsFocus = focus
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
     * pauses, left and right seek (hold to go faster), up shows the controls on Play and
     * down on Subtitles (so captions are Down, OK), the captions or menu key toggles subtitles,
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
            // The remote's captions or menu key: one press turns subtitles on or off.
            KeyEvent.KEYCODE_CAPTIONS, KeyEvent.KEYCODE_MENU -> {
                if (down && event.repeatCount == 0 && dialog == null) toggleSubtitles()
                return true
            }
        }
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
                if (down) showControls(ControlsFocus.forKey(event.keyCode))
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
        if (kind == TrackKind.Subtitles) return subtitleOptions()
        val groups = player.currentTracks.groups.filter { it.type == C.TRACK_TYPE_AUDIO }
        return groups.flatMap { group ->
            (0 until group.length).map { index ->
                val format = group.getTrackFormat(index)
                TrackOption(
                    label = TrackLabels.label(format, index),
                    selected = group.isTrackSelected(index),
                    supported = group.isTrackSupported(index),
                    apply = { selectAudio(group, index) },
                )
            }
        }
    }

    /** Off, then every subtitle track; picking one is remembered for this title and its show. */
    private fun subtitleOptions(): List<TrackOption> {
        val subs = converted
        val options = if (subs != null) {
            subs.sources.map { source ->
                val candidate = subs.candidates.first { it.id == source.trackId.toString() }
                TrackOption(subs.label(source), selected = convertedChoice?.trackId == source.trackId, supported = true) {
                    chooseSubtitle(candidate)
                }
            }
        } else {
            val tracks = player.currentTracks
            directCandidates(tracks).zip(directTracks(tracks)).map { (candidate, track) ->
                TrackOption(
                    label = TrackLabels.label(track.group.getTrackFormat(track.index), track.index),
                    selected = track.group.isTrackSelected(track.index),
                    supported = candidate.supported,
                ) { chooseSubtitle(candidate) }
            }
        }
        val off = TrackOption("Off", selected = !subtitlesOn, supported = true) { chooseSubtitle(null) }
        return listOf(off) + options
    }

    /**
     * One press of the subtitle toggle: off when a track shows, otherwise the best
     * track for the viewer's languages. Remembered like a pick from the list.
     */
    fun toggleSubtitles() {
        if (subtitlesOn || convertedChoice != null) {
            chooseSubtitle(null)
            say("Subtitles off")
            return
        }
        val candidates = converted?.candidates ?: directCandidates(player.currentTracks)
        val chosen = SubtitlePicker.forToggleOn(prefs.subtitles, candidates, deviceLanguage)
        if (chosen == null) {
            say(if (pictureSubtitlesUnavailable) PICTURE_SUBTITLES_NOTE else "This title has no subtitles")
            return
        }
        chooseSubtitle(chosen)
        say("Subtitles on: ${subtitleLabel(chosen)}")
    }

    /** The viewer chose [candidate] (null: off). Applies it and remembers it for this title and its show. */
    private fun chooseSubtitle(candidate: SubtitleCandidate?) {
        graph.preferences.rememberSubtitle(itemId, details?.showId, SubtitlePicker.sticky(candidate))
        val subs = converted
        if (subs != null) selectConverted(candidate?.let { subs.source(it) }) else selectDirect(candidate)
    }

    private fun sticky(): StickyChoice? = graph.preferences.stickySubtitle(itemId, details?.showId)

    private fun subtitleLabel(candidate: SubtitleCandidate): String {
        converted?.let { subs -> subs.source(candidate)?.let { return subs.label(it) } }
        val track = directTracks(player.currentTracks).getOrNull(directCandidates(player.currentTracks).indexOf(candidate))
        return track?.let { TrackLabels.label(it.group.getTrackFormat(it.index), it.index) } ?: "on"
    }

    private class DirectTrack(val group: Tracks.Group, val index: Int)

    private fun directTracks(tracks: Tracks): List<DirectTrack> =
        tracks.groups.filter { it.type == C.TRACK_TYPE_TEXT }.flatMap { group -> (0 until group.length).map { DirectTrack(group, it) } }

    /** The file's text and picture subtitle tracks as the picker sees them, in the same order as [directTracks]. */
    private fun directCandidates(tracks: Tracks): List<SubtitleCandidate> =
        directTracks(tracks).mapIndexed { i, track ->
            val format = track.group.getTrackFormat(track.index)
            SubtitleCandidate(
                id = "direct:$i",
                language = format.language,
                forced = (format.selectionFlags and C.SELECTION_FLAG_FORCED) != 0,
                isDefault = (format.selectionFlags and C.SELECTION_FLAG_DEFAULT) != 0,
                label = format.label,
                supported = track.group.isTrackSupported(track.index),
            )
        }

    /** The language of the audio playing, or of the first audio track while none is selected yet. */
    private fun directAudioLanguage(tracks: Tracks): String? {
        val audio = tracks.groups.filter { it.type == C.TRACK_TYPE_AUDIO }
        audio.forEach { group -> (0 until group.length).firstOrNull { group.isTrackSelected(it) }?.let { return group.getTrackFormat(it).language } }
        return audio.firstOrNull()?.takeIf { it.length > 0 }?.getTrackFormat(0)?.language
    }

    private fun selectDirect(candidate: SubtitleCandidate?) {
        val tracks = player.currentTracks
        val track = candidate?.let { directTracks(tracks).getOrNull(directCandidates(tracks).indexOfFirst { c -> c.id == it.id }) }
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon().apply {
            if (track == null) {
                clearOverridesOfType(C.TRACK_TYPE_TEXT)
                setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            } else {
                setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
                setOverrideForType(TrackSelectionOverride(track.group.mediaTrackGroup, track.index))
            }
        }.build()
        subtitlesOn = track != null
    }

    /** Converted: loads [source]'s cues (or clears them) and draws them in file time. */
    private fun selectConverted(source: SubtitleSource?) {
        val subs = converted ?: return
        subtitleJob?.cancel()
        cueTicker?.cancel()
        convertedChoice = source
        convertedCues = null
        subtitleCues = emptyList()
        subtitlesOn = source != null
        if (source == null) return
        subtitleJob = activity.lifecycleScope.launch {
            try {
                val cues = subs.load(source) { status -> showNotice(status, NOTICE_MS * 4) }
                if (convertedChoice != source) return@launch
                convertedCues = cues
                if (notice?.startsWith("Preparing subtitles") == true) notice = null
                startCueTicker(cues)
            } catch (e: ApiException) {
                if (convertedChoice != source) return@launch
                convertedChoice = null
                subtitlesOn = false
                say("Subtitles could not be loaded: ${e.message.orEmpty()}")
            }
        }
    }

    /**
     * Draws the cues showing at the file position: start plus player time, so they
     * are right after every seek. Wakes at the next cue change, at most every 250 ms
     * so a seek or pause is picked up promptly.
     */
    private fun startCueTicker(cues: CueTimeline<Cue>) {
        cueTicker?.cancel()
        cueTicker = activity.lifecycleScope.launch {
            while (isActive) {
                if (reloading || phase != Phase.Playing) {
                    if (subtitleCues.isNotEmpty()) subtitleCues = emptyList()
                    delay(100)
                    continue
                }
                val at = position()
                val showing = cues.at(at)
                if (showing != subtitleCues) subtitleCues = showing
                delay(CueTimeline.waitMs(at, cues.nextChangeAfter(at)))
            }
        }
    }

    private fun say(message: String) = showNotice(message, NOTICE_MS)

    private fun showNotice(message: String, forMs: Long) {
        notice = message
        noticeJob?.cancel()
        noticeJob = activity.lifecycleScope.launch {
            delay(forMs)
            notice = null
        }
    }

    private fun selectAudio(group: Tracks.Group, index: Int) {
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
            .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false)
            .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, index))
            .build()
    }

    private companion object {
        const val DETAILS_TIMEOUT_MS = 4_000L
        const val NOTICE_MS = 3_000L
        const val PICTURE_SUBTITLES_NOTE = "Picture subtitles (PGS, VobSub) show only when the file plays directly"
        const val INFO_MS = 3_000L
        const val CONTROLS_MS = 6_000L
    }
}

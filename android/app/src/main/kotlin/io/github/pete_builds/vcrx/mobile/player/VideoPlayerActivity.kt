package io.github.pete_builds.vcrx.mobile.player

import android.app.PictureInPictureParams
import android.content.Context
import android.content.Intent
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Rational
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import io.github.pete_builds.vcrx.mobile.AppGraph
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.PlaybackRequest
import io.github.pete_builds.vcrx.mobile.net.PlaybackSession
import io.github.pete_builds.vcrx.mobile.net.SubtitleAnswer
import io.github.pete_builds.vcrx.mobile.net.SubtitleSource
import io.github.pete_builds.vcrx.mobile.offline.DownloadKeys
import io.github.pete_builds.vcrx.mobile.offline.OfflineProgress
import io.github.pete_builds.vcrx.mobile.offline.OfflineProgressQueue
import io.github.pete_builds.vcrx.mobile.ui.Brand
import io.github.pete_builds.vcrx.mobile.ui.BrandButton
import io.github.pete_builds.vcrx.mobile.ui.QuietButton
import io.github.pete_builds.vcrx.mobile.ui.VcrxTheme
import io.github.pete_builds.vcrx.mobile.ui.formatTime
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Full screen video: a title streamed through a playback session (the file
 * itself when this phone decodes it, else the server's converted stream), a
 * downloaded title from the phone, or an IPTV channel. Picture in picture when
 * the viewer leaves while it plays. Progress goes to the server as playback
 * events; a downloaded title's progress goes through the offline queue.
 */
@OptIn(UnstableApi::class)
class VideoPlayerActivity : ComponentActivity() {
    private lateinit var app: AppGraph
    private lateinit var player: ExoPlayer

    private var mode = MODE_ITEM
    private var session: PlaybackSession? = null
    private var timeline: StreamTimeline? = null
    private var reporter: PlaybackReporter? = null
    private var liveRun: Pair<Long, String>? = null
    private var offlineKey: String? = null
    private var ticker: Job? = null
    private val autoSkipped = mutableSetOf<String>()

    private var title by mutableStateOf("")
    private var error by mutableStateOf<String?>(null)
    private var loading by mutableStateOf(true)
    private var playing by mutableStateOf(false)
    private var positionMs by mutableStateOf(0L)
    private var durationMs by mutableStateOf(0L)
    private var controls by mutableStateOf(true)
    private var pip by mutableStateOf(false)
    private var skip by mutableStateOf<SkipAction?>(null)
    private var label by mutableStateOf<String?>(null)
    private var cue by mutableStateOf<String?>(null)
    private var cues: List<Cue> = emptyList()
    private var subtitleChoice by mutableStateOf<SubtitleSource?>(null)
    private var pickingSubtitles by mutableStateOf(false)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        app = graph
        requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        WindowCompat.getInsetsController(window, window.decorView).apply {
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            hide(WindowInsetsCompat.Type.systemBars())
        }
        player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(app.downloads.dataSourceFactory))
            .setHandleAudioBecomingNoisy(true)
            // Takes audio focus, so music playing in the background pauses for the film.
            .setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(), true)
            .build()
        // Subtitles are the server's WebVTT, drawn by this screen in file time, so the
        // file's own text tracks stay off and nothing shows twice.
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true).build()
        player.addListener(listener)
        title = intent.getStringExtra(EXTRA_TITLE).orEmpty()
        mode = intent.getStringExtra(EXTRA_MODE) ?: MODE_ITEM
        setContent { VcrxTheme { Screen() } }
        start()
    }

    private fun start() {
        lifecycleScope.launch {
            try {
                when (mode) {
                    MODE_LIVE -> startLive(intent.getLongExtra(EXTRA_CHANNEL, 0))
                    MODE_DOWNLOAD -> startDownload(intent.getStringExtra(EXTRA_KEY).orEmpty())
                    else -> startItem(
                        intent.getLongExtra(EXTRA_ITEM, 0),
                        intent.getLongExtra(EXTRA_VERSION, 0).takeIf { it > 0 },
                        intent.getBooleanExtra(EXTRA_FROM_START, false),
                    )
                }
            } catch (e: ApiException) {
                error = when {
                    e is ApiException.Http && (e.status == 403 || e.status == 422) -> e.message
                    else -> e.message
                }
            } finally {
                loading = false
            }
        }
        ticker = lifecycleScope.launch {
            var beat = 0L
            while (isActive) {
                delay(TICK_MS)
                tick()
                beat += TICK_MS
                val every = session?.heartbeatIntervalMs ?: 10_000
                if (beat >= every) {
                    beat = 0
                    reporter?.onHeartbeat(pos(), dur(), player.isPlaying)
                    if (mode == MODE_DOWNLOAD) recordOffline(ended = false)
                }
            }
        }
    }

    private suspend fun startItem(itemId: Long, versionId: Long?, fromStart: Boolean) {
        // A downloaded copy wins: it starts at once and costs no bandwidth.
        val key = versionId?.let { DownloadKeys.version(it) }
        if (key != null && PhonePlaybackPolicy.source(app.downloads.isDownloaded(key), app.online.value) == PlaySource.Local) {
            return startDownload(key, fromStart)
        }
        val s = app.api.startPlayback(
            PlaybackRequest(itemId = itemId, versionId = versionId, startPositionMs = if (fromStart) 0 else null, capabilities = app.capabilities),
        )
        if (!StreamKinds.isPlayable(s)) {
            error = "This server sent a stream this app cannot play."
            return
        }
        session = s
        label = StreamKinds.label(s)
        val converted = StreamKinds.isConverted(s)
        val t = StreamTimeline(converted, s.version.durationMs, { player.currentPosition }, { player.duration.takeIf { it != C.TIME_UNSET } })
        timeline = t
        reporter = PlaybackReporter { e -> app.events.send(s.sessionId, e) }
        val startAt = PhonePlaybackPolicy.startMs(fromStart, s.resumePositionMs, s.version.durationMs)
        if (converted) {
            val seek = t.beginSeek()
            val answer = app.api.convertedStart(StreamKinds.seekPath(s), startAt)
            t.land(seek, answer.startMs)
            setStream(answer.url, 0)
        } else {
            setStream(s.stream.url, startAt)
        }
        // A default or forced subtitle in the viewer's language starts on, as on the TV.
        s.webVttSubtitles.firstOrNull { it.isForced }?.let { chooseSubtitle(it) }
    }

    private fun setStream(path: String, startMs: Long) {
        val url = app.api.absoluteUrl(path) ?: throw ApiException.BadResponse("No server is configured.")
        player.setMediaItem(MediaItem.fromUri(url), startMs)
        player.prepare()
        player.play()
    }

    private suspend fun startDownload(key: String, fromStart: Boolean = false) {
        val entry = app.downloads.find(key)?.takeIf { it.done } ?: run {
            error = "This download is not on this device."
            return
        }
        offlineKey = key
        mode = MODE_DOWNLOAD
        title = entry.meta.title
        label = "Downloaded"
        val path = DownloadKeys.pathFor(key) ?: return
        val url = app.api.absoluteUrl(path) ?: "http://offline.invalid$path"
        val start = PhonePlaybackPolicy.startMs(fromStart, app.offline.resumeFor(entry.meta.itemId, entry.meta.resumeMs), entry.meta.durationMs)
        timeline = StreamTimeline(false, entry.meta.durationMs, { player.currentPosition }, { player.duration.takeIf { it != C.TIME_UNSET } })
        player.setMediaItem(MediaItem.Builder().setUri(Uri.parse(url)).setCustomCacheKey(key).build(), start)
        player.prepare()
        player.play()
    }

    private suspend fun startLive(channelId: Long) {
        val watch = app.api.liveWatch(channelId)
        liveRun = channelId to watch.run
        label = "IPTV"
        val url = app.api.absoluteUrl(watch.playlist) ?: return
        player.setMediaItem(MediaItem.Builder().setUri(url).setMimeType(MimeTypes.APPLICATION_M3U8).build())
        player.prepare()
        player.play()
    }

    private fun pos(): Long = timeline?.positionMs() ?: player.currentPosition.coerceAtLeast(0)

    private fun dur(): Long? = timeline?.durationMs() ?: player.duration.takeIf { it != C.TIME_UNSET && it > 0 }

    private fun tick() {
        positionMs = pos()
        durationMs = dur() ?: 0
        playing = player.isPlaying
        cue = if (cues.isEmpty()) null else WebVtt.at(cues, positionMs)
        val s = session
        skip = if (s == null) null else SkipMarkers.actionAt(s.markers, positionMs, durationMs, hasNext = false, mode = s.skipMarkers)
        skip?.let { a -> if (a.auto && autoSkipped.add(a.kind)) seekTo(a.targetMs) }
    }

    private fun seekTo(targetMs: Long) {
        val from = pos()
        val t = timeline
        val s = session
        if (t != null && t.converted && s != null) {
            val seek = t.beginSeek()
            val target = t.seekTarget(targetMs)
            lifecycleScope.launch {
                try {
                    val answer = app.api.convertedStart(StreamKinds.seekPath(s), target)
                    if (t.land(seek, answer.startMs)) setStream(answer.url, 0)
                } catch (e: ApiException) {
                    error = e.message
                }
            }
        } else {
            player.seekTo(SeekSteps.clamp(targetMs, dur()))
        }
        reporter?.onSeek(from, targetMs, dur())
    }

    private fun chooseSubtitle(source: SubtitleSource?) {
        subtitleChoice = source
        cues = emptyList()
        source ?: return
        lifecycleScope.launch {
            val started = System.currentTimeMillis()
            while (isActive && subtitleChoice == source) {
                val answer = try {
                    app.api.subtitle(source.url)
                } catch (e: ApiException) {
                    error = "Subtitles: ${e.message}"
                    return@launch
                }
                when (answer) {
                    is SubtitleAnswer.Ready -> {
                        cues = WebVtt.parse(String(answer.bytes, Charsets.UTF_8))
                        return@launch
                    }
                    is SubtitleAnswer.Preparing -> {
                        if (System.currentTimeMillis() - started > SUBTITLE_GIVE_UP_MS) return@launch
                        delay((answer.retryAfterSeconds ?: 5).coerceIn(2, 15) * 1000L)
                    }
                }
            }
        }
    }

    private val listener = object : Player.Listener {
        override fun onIsPlayingChanged(isPlaying: Boolean) {
            playing = isPlaying
            if (isPlaying) reporter?.onPlaying(pos(), dur()) else if (!player.playWhenReady) reporter?.onPaused(pos(), dur())
            if (!isPlaying && mode == MODE_DOWNLOAD) recordOffline(ended = false)
            updatePipParams()
        }

        override fun onPlaybackStateChanged(state: Int) {
            when (state) {
                Player.STATE_BUFFERING -> reporter?.onBuffering(pos(), dur())
                Player.STATE_ENDED -> {
                    if (mode == MODE_DOWNLOAD) recordOffline(ended = true)
                    val t = timeline
                    if (t == null || !t.endedEarly()) {
                        reporter?.stop(dur() ?: pos(), dur())
                        finish()
                    }
                }
                else -> Unit
            }
        }

        override fun onPlayerError(e: PlaybackException) {
            reporter?.onError(e.errorCodeName, pos(), dur())
            error = "Playback failed (${e.errorCodeName})."
        }
    }

    private fun recordOffline(ended: Boolean) {
        val key = offlineKey ?: return
        val meta = app.downloads.find(key)?.meta ?: return
        val p = pos()
        val d = dur() ?: meta.durationMs
        app.offline.record(
            OfflineProgress(
                itemId = meta.itemId,
                versionId = meta.versionId,
                positionMs = p,
                durationMs = d,
                played = ended || OfflineProgressQueue.isPlayed(p, d),
                recordedAt = System.currentTimeMillis(),
            ),
        )
    }

    private fun pipParams(): PictureInPictureParams = PictureInPictureParams.Builder()
        .setAspectRatio(Rational(16, 9))
        .apply { if (Build.VERSION.SDK_INT >= 31) setAutoEnterEnabled(PhonePlaybackPolicy.enterPip(player.isPlaying, isVideo = true)) }
        .build()

    private fun updatePipParams() {
        runCatching { setPictureInPictureParams(pipParams()) }
    }

    private fun enterPip() {
        if (PhonePlaybackPolicy.enterPip(player.isPlaying, isVideo = true)) runCatching { enterPictureInPictureMode(pipParams()) }
    }

    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        if (Build.VERSION.SDK_INT < 31) enterPip()
    }

    override fun onPictureInPictureModeChanged(isInPictureInPictureMode: Boolean, newConfig: Configuration) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        pip = isInPictureInPictureMode
    }

    override fun onStop() {
        super.onStop()
        // Closing the picture in picture window, or leaving without it, ends playback.
        if (!isInPictureInPictureMode) finish()
    }

    override fun onDestroy() {
        ticker?.cancel()
        if (mode == MODE_DOWNLOAD) {
            recordOffline(ended = false)
            if (app.online.value) app.appScope.launch { app.offline.sync(app.api, app.capabilities) }
        }
        reporter?.stop(pos(), dur())
        liveRun?.let { (channel, run) -> app.appScope.launch { runCatching { app.api.liveLeave(channel, run) } } }
        timeline?.cancelSeeks()
        player.removeListener(listener)
        player.release()
        super.onDestroy()
    }

    @Composable
    private fun Screen() {
        val interaction = remember { MutableInteractionSource() }
        Box(Modifier.fillMaxSize().background(Color.Black).clickable(interactionSource = interaction, indication = null) { controls = !controls }) {
            AndroidView(
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        useController = false
                        player = this@VideoPlayerActivity.player
                        setShutterBackgroundColor(android.graphics.Color.BLACK)
                    }
                },
                modifier = Modifier.fillMaxSize(),
            )
            cue?.let {
                Text(
                    it,
                    color = Color.White,
                    textAlign = TextAlign.Center,
                    style = if (pip) MaterialTheme.typography.bodySmall else MaterialTheme.typography.titleMedium,
                    modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = if (controls && !pip) 96.dp else 24.dp, start = 32.dp, end = 32.dp)
                        .background(Color(0xAA000000)).padding(horizontal = 8.dp, vertical = 2.dp),
                )
            }
            if (loading) CircularProgressIndicator(color = Brand.Green, modifier = Modifier.align(Alignment.Center))
            if (!pip && controls) Controls()
            if (!pip) {
                skip?.let { a ->
                    BrandButton(a.label, onClick = { seekTo(a.targetMs) }, modifier = Modifier.align(Alignment.BottomEnd).safeDrawingPadding().padding(end = 24.dp, bottom = 96.dp))
                }
            }
            error?.let { message ->
                AlertDialog(
                    onDismissRequest = { finish() },
                    title = { Text("Cannot play") },
                    text = { Text(message) },
                    confirmButton = { BrandButton("Close", onClick = { finish() }) },
                )
            }
            if (pickingSubtitles) SubtitlePicker()
        }
    }

    @Composable
    private fun Controls() {
        val live = mode == MODE_LIVE
        Box(Modifier.fillMaxSize().background(Color(0x66000000)).safeDrawingPadding()) {
            Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { finish() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back", tint = Color.White) }
                Column(Modifier.weight(1f)) {
                    Text(title, style = MaterialTheme.typography.titleMedium, maxLines = 1)
                    label?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Brand.Muted) }
                }
                if (session?.webVttSubtitles?.isNotEmpty() == true) {
                    IconButton(onClick = { pickingSubtitles = true }) { Icon(painterResource(R.drawable.ic_subtitles), "Subtitles", tint = Color.White) }
                }
                IconButton(onClick = { enterPip() }) { Icon(painterResource(R.drawable.ic_pip), "Picture in picture", tint = Color.White) }
            }
            Row(Modifier.align(Alignment.Center), horizontalArrangement = Arrangement.spacedBy(32.dp), verticalAlignment = Alignment.CenterVertically) {
                if (!live) IconButton(onClick = { seekTo(pos() - 10_000) }) { Icon(painterResource(R.drawable.ic_replay), "Back 10 seconds", tint = Color.White, modifier = Modifier.size(36.dp)) }
                IconButton(
                    onClick = { if (player.isPlaying) player.pause() else player.play() },
                    modifier = Modifier.size(72.dp).clip(CircleShape).background(Brand.Tint),
                ) {
                    Icon(painterResource(if (playing) R.drawable.ic_pause else R.drawable.ic_play), if (playing) "Pause" else "Play", tint = Color.White, modifier = Modifier.size(40.dp))
                }
                if (!live) IconButton(onClick = { seekTo(pos() + 30_000) }) { Icon(painterResource(R.drawable.ic_forward), "Forward 30 seconds", tint = Color.White, modifier = Modifier.size(36.dp)) }
            }
            if (!live) {
                var dragging by remember { mutableStateOf(false) }
                var dragValue by remember { mutableFloatStateOf(0f) }
                Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
                    val fraction = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
                    Slider(
                        value = if (dragging) dragValue else fraction,
                        onValueChange = {
                            dragging = true
                            dragValue = it
                        },
                        onValueChangeFinished = {
                            dragging = false
                            seekTo((dragValue * durationMs).toLong())
                        },
                        colors = SliderDefaults.colors(thumbColor = Brand.Green, activeTrackColor = Brand.Green),
                    )
                    Row {
                        Text(formatTime(if (dragging) (dragValue * durationMs).toLong() else positionMs), color = Color.White)
                        Spacer(Modifier.weight(1f))
                        Text(formatTime(durationMs), color = Color.White)
                    }
                }
            }
        }
    }

    @Composable
    private fun SubtitlePicker() {
        val options = session?.webVttSubtitles.orEmpty()
        AlertDialog(
            onDismissRequest = { pickingSubtitles = false },
            title = { Text("Subtitles") },
            text = {
                LazyColumn {
                    item { QuietButton(if (subtitleChoice == null) "Off (on now)" else "Off", onClick = { chooseSubtitle(null); pickingSubtitles = false }) }
                    items(options) { s ->
                        val name = listOfNotNull(s.title, s.language, if (s.isForced) "forced" else null).joinToString(", ").ifEmpty { "Track ${s.trackId}" }
                        QuietButton(if (s == subtitleChoice) "$name (on now)" else name, onClick = { chooseSubtitle(s); pickingSubtitles = false })
                    }
                }
            },
            confirmButton = { QuietButton("Close", onClick = { pickingSubtitles = false }) },
        )
    }

    companion object {
        private const val EXTRA_MODE = "mode"
        private const val EXTRA_ITEM = "item_id"
        private const val EXTRA_VERSION = "version_id"
        private const val EXTRA_FROM_START = "from_start"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_CHANNEL = "channel_id"
        private const val EXTRA_KEY = "download_key"
        private const val MODE_ITEM = "item"
        private const val MODE_DOWNLOAD = "download"
        private const val MODE_LIVE = "live"
        private const val TICK_MS = 500L
        private const val SUBTITLE_GIVE_UP_MS = 20 * 60 * 1000L

        fun playItem(context: Context, itemId: Long, versionId: Long?, fromStart: Boolean, title: String) {
            context.startActivity(
                Intent(context, VideoPlayerActivity::class.java)
                    .putExtra(EXTRA_MODE, MODE_ITEM)
                    .putExtra(EXTRA_ITEM, itemId)
                    .putExtra(EXTRA_VERSION, versionId ?: 0L)
                    .putExtra(EXTRA_FROM_START, fromStart)
                    .putExtra(EXTRA_TITLE, title),
            )
        }

        fun playDownload(context: Context, key: String) {
            context.startActivity(Intent(context, VideoPlayerActivity::class.java).putExtra(EXTRA_MODE, MODE_DOWNLOAD).putExtra(EXTRA_KEY, key))
        }

        fun playChannel(context: Context, channelId: Long, name: String) {
            context.startActivity(
                Intent(context, VideoPlayerActivity::class.java).putExtra(EXTRA_MODE, MODE_LIVE).putExtra(EXTRA_CHANNEL, channelId).putExtra(EXTRA_TITLE, name),
            )
        }
    }
}
